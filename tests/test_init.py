"""Tests for swarf init command."""

from __future__ import annotations

import subprocess

import pytest
from click.testing import CliRunner

from swarf.cli import main
from swarf.config import GlobalConfig, write_global_config


class TestInit:
    def test_init_creates_structure(self, git_repo):
        runner = CliRunner()
        result = runner.invoke(main, ["init"])
        assert result.exit_code == 0, result.output
        assert (git_repo / ".swarf").is_dir()
        assert (git_repo / ".swarf" / "docs" / "research").is_dir()
        assert (git_repo / ".swarf" / "docs" / "design").is_dir()
        assert (git_repo / ".swarf" / "links").is_dir()
        assert (git_repo / ".swarf" / "open-questions.md").exists()

    def test_init_creates_git_repo(self, git_repo):
        runner = CliRunner()
        runner.invoke(main, ["init"])
        assert (git_repo / ".swarf" / ".git").is_dir()

    def test_init_writes_config_from_global(self, git_repo):
        runner = CliRunner()
        runner.invoke(main, ["init"])
        config = (git_repo / ".swarf" / "config.toml").read_text()
        assert 'backend = "git"' in config
        assert 'remote = "origin"' in config

    def test_init_with_git_remote(self, git_repo, bare_remote):
        write_global_config(GlobalConfig(backend="git", remote=str(bare_remote)))
        runner = CliRunner()
        result = runner.invoke(main, ["init"])
        assert result.exit_code == 0, result.output
        r = subprocess.run(
            ["git", "remote", "-v"],
            cwd=git_repo / ".swarf",
            capture_output=True,
            text=True,
        )
        assert str(bare_remote) in r.stdout

    def test_init_creates_mise_local(self, git_repo):
        runner = CliRunner()
        runner.invoke(main, ["init"])
        mise = git_repo / ".mise.local.toml"
        assert mise.exists()
        content = mise.read_text()
        assert "swarf enter" in content
        assert "[hooks]" in content

    @pytest.mark.usefixtures("git_repo")
    def test_init_registers_drawer(self):
        import swarf.config as cfg

        runner = CliRunner()
        runner.invoke(main, ["init"])

        drawers = cfg.read_drawers()
        assert len(drawers) == 1
        assert drawers[0].backend == "git"

    @pytest.mark.usefixtures("git_repo")
    def test_init_refuses_if_already_initialized(self):
        runner = CliRunner()
        runner.invoke(main, ["init"])
        result = runner.invoke(main, ["init"])
        assert result.exit_code != 0
        assert "already initialized" in result.output

    def test_init_updates_git_info_exclude(self, git_repo):
        runner = CliRunner()
        result = runner.invoke(main, ["init"])
        assert result.exit_code == 0, result.output
        exclude = git_repo / ".git" / "info" / "exclude"
        content = exclude.read_text()
        assert "/.swarf/" in content
        assert "/.mise.local.toml" in content
        assert "swarf managed" in content

    def test_init_rclone_backend(self, git_repo):
        write_global_config(GlobalConfig(backend="rclone", remote="gdrive:swarf"))
        runner = CliRunner()
        runner.invoke(main, ["init"])
        config = (git_repo / ".swarf" / "config.toml").read_text()
        assert 'backend = "rclone"' in config

    def test_init_not_in_git_repo(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("GIT_CEILING_DIRECTORIES", str(tmp_path))
        runner = CliRunner()
        result = runner.invoke(main, ["init"])
        assert result.exit_code != 0
        assert "Not inside a git repository" in result.output

    def test_init_existing_mise_local_warns(self, git_repo):
        (git_repo / ".mise.local.toml").write_text("[tools]\npython = '3.13'\n")
        runner = CliRunner()
        result = runner.invoke(main, ["init"])
        assert result.exit_code == 0, result.output
        assert "already exists" in result.output
        content = (git_repo / ".mise.local.toml").read_text()
        assert "python" in content

    def test_init_initial_commit(self, git_repo):
        runner = CliRunner()
        runner.invoke(main, ["init"])
        r = subprocess.run(
            ["git", "log", "--oneline", "-1"],
            cwd=git_repo / ".swarf",
            capture_output=True,
            text=True,
        )
        assert "init: swarf drawer" in r.stdout

    @pytest.mark.usefixtures("git_repo")
    def test_init_no_backend_remote_flags(self):
        """--backend and --remote flags should not exist."""
        runner = CliRunner()
        result = runner.invoke(main, ["init", "--backend", "git"])
        assert result.exit_code != 0
        assert "No such option" in result.output
