"""Tests for swarf doctor command."""

import subprocess

import pytest
from click.testing import CliRunner

from swarf.cli import main
from swarf.doctor import (
    check_daemon_running,
    check_links_healthy,
    check_mise_local,
    check_swarf_is_git_repo,
)


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

    def test_swarf_gitignored(self, initialized_swarf):
        root = initialized_swarf
        (root / ".gitignore").write_text(".swarf/\n.mise.local.toml\n")
        (root / ".mise.local.toml").write_text('[hooks]\nenter = "swarf link --quiet"\n')
        # Add a remote so that check passes
        bare = root / "remote.git"
        bare.mkdir()
        subprocess.run(["git", "init", "--bare"], cwd=bare, capture_output=True, check=True)
        subprocess.run(
            ["git", "remote", "add", "origin", str(bare)],
            cwd=root / ".swarf",
            capture_output=True,
            check=True,
        )
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
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
        # This will still fail due to other checks (mise.local, git repo, etc.)
        assert "AGENTS.md is gitignored" in result.output

    def test_not_in_git_repo(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("GIT_CEILING_DIRECTORIES", str(tmp_path))
        runner = CliRunner()
        result = runner.invoke(main, ["doctor"])
        assert result.exit_code != 0
        assert "Not inside a git repository" in result.output


class TestDoctorMiseLocal:
    def test_check_mise_local_missing(self, git_repo):
        _, ok, msg = check_mise_local(git_repo)
        assert not ok
        assert "not found" in msg

    def test_check_mise_local_present(self, git_repo):
        (git_repo / ".mise.local.toml").write_text('[hooks]\nenter = "swarf link --quiet"\n')
        _, ok, msg = check_mise_local(git_repo)
        assert ok
        assert "enter hook" in msg

    def test_check_mise_local_missing_hook(self, git_repo):
        (git_repo / ".mise.local.toml").write_text("[tools]\npython = '3.13'\n")
        _, ok, msg = check_mise_local(git_repo)
        assert not ok
        assert "missing" in msg


class TestDoctorGitRepo:
    def test_check_swarf_is_git_repo(self, initialized_swarf):
        _, ok, _ = check_swarf_is_git_repo(initialized_swarf)
        assert ok

    def test_check_swarf_not_git_repo(self, git_repo):
        (git_repo / ".swarf").mkdir()
        _, ok, _ = check_swarf_is_git_repo(git_repo)
        assert not ok

    def test_check_swarf_missing(self, git_repo):
        _, ok, _ = check_swarf_is_git_repo(git_repo)
        assert not ok


class TestDoctorDaemon:
    def test_daemon_not_running(self, monkeypatch, tmp_path):
        import swarf.doctor as doc

        monkeypatch.setattr(doc, "PID_FILE", tmp_path / "nonexistent.pid")
        _, ok, msg = check_daemon_running()
        assert not ok
        assert "not running" in msg

    def test_daemon_stale_pid(self, monkeypatch, tmp_path):
        import swarf.doctor as doc

        pid_file = tmp_path / "daemon.pid"
        pid_file.write_text("999999999")  # very unlikely to be running
        monkeypatch.setattr(doc, "PID_FILE", pid_file)
        _, ok, msg = check_daemon_running()
        assert not ok
        assert "not running" in msg


class TestDoctorLinks:
    def test_links_healthy(self, initialized_swarf):
        root = initialized_swarf
        source = root / ".swarf" / "links" / "AGENTS.md"
        source.write_text("# Agents\n")
        target = root / "AGENTS.md"
        target.symlink_to(source)
        _, ok, _ = check_links_healthy(root)
        assert ok

    def test_broken_symlink(self, initialized_swarf):
        root = initialized_swarf
        source = root / ".swarf" / "links" / "AGENTS.md"
        source.write_text("# Agents\n")
        target = root / "AGENTS.md"
        target.symlink_to(root / "nonexistent")
        _, ok, msg = check_links_healthy(root)
        assert not ok
        assert "Broken" in msg

    def test_no_links_dir(self, git_repo):
        _, ok, _ = check_links_healthy(git_repo)
        assert ok
