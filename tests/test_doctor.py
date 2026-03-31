"""Tests for swarf doctor command."""

import subprocess

import pytest
from click.testing import CliRunner

from swarf.cli import main


@pytest.fixture()
def git_repo(tmp_path, monkeypatch):
    """Create a temporary git repo with no global gitignore, and cd into it."""
    monkeypatch.chdir(tmp_path)
    # Isolate from the real global gitignore by pointing XDG_CONFIG_HOME to an empty dir
    fake_config = tmp_path / ".config"
    fake_config.mkdir()
    monkeypatch.setenv("XDG_CONFIG_HOME", str(fake_config))
    monkeypatch.setenv("GIT_CONFIG_GLOBAL", str(tmp_path / ".gitconfig-empty"))
    (tmp_path / ".gitconfig-empty").write_text("")
    monkeypatch.setenv("HOME", str(tmp_path))
    subprocess.run(["git", "init"], capture_output=True, check=True, cwd=tmp_path)
    subprocess.run(
        ["git", "config", "user.email", "test@test.com"],
        capture_output=True,
        check=True,
        cwd=tmp_path,
    )
    subprocess.run(
        ["git", "config", "user.name", "Test"],
        capture_output=True,
        check=True,
        cwd=tmp_path,
    )
    return tmp_path


class TestDoctorGitignore:
    @pytest.mark.usefixtures("git_repo")
    def test_no_swarf_dir(self):
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
        assert result.exit_code != 0
        assert ".swarf/ directory not found" in result.output

    def test_swarf_not_gitignored(self, git_repo):
        (git_repo / ".swarf").mkdir()
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
        assert result.exit_code != 0
        assert ".swarf/ is NOT gitignored" in result.output

    def test_swarf_gitignored(self, git_repo):
        (git_repo / ".swarf").mkdir()
        (git_repo / ".gitignore").write_text(".swarf/\n.mise.local.toml\n")
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
        assert result.exit_code == 0
        assert ".swarf/ is gitignored" in result.output
        assert ".mise.local.toml is gitignored" in result.output

    def test_mise_local_not_gitignored(self, git_repo):
        (git_repo / ".swarf").mkdir()
        (git_repo / ".gitignore").write_text(".swarf/\n")
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
        assert result.exit_code != 0
        assert ".mise.local.toml is NOT gitignored" in result.output

    def test_linked_file_not_gitignored(self, git_repo):
        (git_repo / ".swarf" / "links").mkdir(parents=True)
        (git_repo / ".swarf" / "links" / "AGENTS.md").write_text("# agents\n")
        (git_repo / ".gitignore").write_text(".swarf/\n.mise.local.toml\n")
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
        assert result.exit_code != 0
        assert "AGENTS.md is NOT gitignored" in result.output

    def test_linked_file_gitignored(self, git_repo):
        (git_repo / ".swarf" / "links").mkdir(parents=True)
        (git_repo / ".swarf" / "links" / "AGENTS.md").write_text("# agents\n")
        (git_repo / ".gitignore").write_text(".swarf/\n.mise.local.toml\nAGENTS.md\n")
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
        assert result.exit_code == 0
        assert "AGENTS.md is gitignored" in result.output

    def test_not_in_git_repo(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        # Ensure we're not inside any git repo
        monkeypatch.setenv("GIT_CEILING_DIRECTORIES", str(tmp_path))
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
        assert result.exit_code != 0
        assert "Not inside a git repository" in result.output
