"""Tests for swarf init command."""

from __future__ import annotations

import subprocess

import pytest
from helpers import invoke

import swarf.paths as paths
from swarf.config import GlobalConfig, write_global_config


class TestInit:
    def test_init_creates_symlink(self, git_repo):
        result = invoke(["init"])
        assert result.exit_code == 0, result.output
        sd = git_repo / ".swarf"
        assert sd.is_symlink()
        assert sd.resolve().parent == paths.STORE_DIR

    def test_init_creates_links_dir(self, git_repo):
        invoke(["init"])
        assert (git_repo / ".swarf" / "links").is_dir()

    def test_init_no_skeleton_files(self, git_repo):
        invoke(["init"])
        sd = git_repo / ".swarf"
        # Should only have links/ — no docs/, no open-questions.md
        children = {p.name for p in sd.iterdir()}
        assert "links" in children
        assert "docs" not in children
        assert "open-questions.md" not in children

    @pytest.mark.usefixtures("git_repo")
    def test_init_creates_store_git_repo(self):
        invoke(["init"])
        assert (paths.STORE_DIR / ".git").is_dir()

    @pytest.mark.usefixtures("git_repo")
    def test_init_with_git_remote(self, bare_remote):
        write_global_config(GlobalConfig(backend="git", remote=str(bare_remote)))
        result = invoke(["init"])
        assert result.exit_code == 0, result.output
        r = subprocess.run(
            ["git", "remote", "-v"],
            cwd=paths.STORE_DIR,
            capture_output=True,
            text=True,
        )
        assert str(bare_remote) in r.stdout

    def test_init_creates_mise_local(self, git_repo):
        invoke(["init"])
        mise = git_repo / ".mise.local.toml"
        assert mise.exists()
        content = mise.read_text()
        assert "swarf enter" in content
        assert "[hooks]" in content

    @pytest.mark.usefixtures("git_repo")
    def test_init_registers_drawer(self):
        import swarf.config as cfg

        invoke(["init"])

        drawers = cfg.read_drawers()
        assert len(drawers) == 1

    @pytest.mark.usefixtures("git_repo")
    def test_init_refuses_if_already_initialized(self):
        invoke(["init"])
        result = invoke(["init"])
        assert result.exit_code != 0
        assert "already initialized" in result.output

    def test_init_updates_git_info_exclude(self, git_repo):
        result = invoke(["init"])
        assert result.exit_code == 0, result.output
        exclude = git_repo / ".git" / "info" / "exclude"
        content = exclude.read_text()
        assert "/.swarf/" in content
        assert "/.mise.local.toml" in content
        assert "swarf managed" in content

    @pytest.mark.usefixtures("git_repo")
    def test_init_rclone_backend(self):
        write_global_config(GlobalConfig(backend="rclone", remote="gdrive:swarf"))
        result = invoke(["init"])
        assert result.exit_code == 0, result.output
        import swarf.config as cfg

        drawers = cfg.read_drawers()
        assert len(drawers) == 1

    def test_init_not_in_git_repo(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("GIT_CEILING_DIRECTORIES", str(tmp_path))
        result = invoke(["init"])
        assert result.exit_code != 0
        assert "Not inside a git repository" in result.output

    def test_init_existing_mise_local_warns(self, git_repo):
        (git_repo / ".mise.local.toml").write_text("[tools]\npython = '3.13'\n")
        result = invoke(["init"])
        assert result.exit_code == 0, result.output
        assert "already exists" in result.output
        content = (git_repo / ".mise.local.toml").read_text()
        assert "python" in content

    def test_init_store_commit(self, git_repo):
        invoke(["init"])
        r = subprocess.run(
            ["git", "log", "--oneline", "-1"],
            cwd=paths.STORE_DIR,
            capture_output=True,
            text=True,
        )
        slug = paths.project_slug(git_repo)
        assert f"init: {slug}" in r.stdout

    @pytest.mark.usefixtures("git_repo")
    def test_init_no_backend_remote_flags(self):
        """--backend and --remote flags should not exist."""
        result = invoke(["init", "--backend", "git"])
        assert result.exit_code != 0

    @pytest.mark.usefixtures("git_repo")
    def test_init_reuses_existing_store(self, tmp_path):
        """Second init in a different project reuses the same store."""
        invoke(["init"])

        # Create a second project
        proj2 = tmp_path / "proj2"
        proj2.mkdir()
        subprocess.run(["git", "init"], capture_output=True, check=True, cwd=proj2)
        subprocess.run(
            ["git", "config", "user.email", "test@test.com"],
            capture_output=True,
            check=True,
            cwd=proj2,
        )
        subprocess.run(
            ["git", "config", "user.name", "Test"],
            capture_output=True,
            check=True,
            cwd=proj2,
        )

        import os

        os.chdir(proj2)
        result = invoke(["init"])
        assert result.exit_code == 0, result.output
        assert (proj2 / ".swarf").is_symlink()

    def test_init_existing_project_in_store(self, git_repo):
        """If project already exists in store (from clone), just links."""
        slug = paths.project_slug(git_repo)
        proj_dir = paths.STORE_DIR / slug

        # Pre-create store and project
        paths.STORE_DIR.mkdir(parents=True)
        subprocess.run(["git", "init"], capture_output=True, check=True, cwd=paths.STORE_DIR)
        subprocess.run(
            ["git", "config", "user.email", "test@test.com"],
            capture_output=True,
            check=True,
            cwd=paths.STORE_DIR,
        )
        subprocess.run(
            ["git", "config", "user.name", "Test"],
            capture_output=True,
            check=True,
            cwd=paths.STORE_DIR,
        )
        proj_dir.mkdir()
        (proj_dir / "links").mkdir()
        (proj_dir / "links" / "AGENTS.md").write_text("# Agents\n")

        result = invoke(["init"])
        assert result.exit_code == 0, result.output
        assert "existing project" in result.output.lower()
        assert (git_repo / ".swarf").is_symlink()
