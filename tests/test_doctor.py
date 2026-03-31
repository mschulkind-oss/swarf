"""Tests for swarf doctor command."""

import pytest
from helpers import invoke

from swarf.doctor import (
    check_daemon_running,
    check_links_healthy,
    check_mise_local,
    check_store_exists,
)


class TestDoctorGitignore:
    @pytest.mark.usefixtures("git_repo")
    def test_no_swarf_dir(self):
        result = invoke(["doctor"])
        assert result.exit_code != 0
        assert ".swarf/ directory not found" in result.output

    def test_swarf_not_gitignored(self, git_repo):
        (git_repo / ".swarf").mkdir()
        result = invoke(["doctor"])
        assert result.exit_code != 0
        assert ".swarf/ is NOT gitignored" in result.output

    def test_swarf_gitignored(self, initialized_swarf):
        root = initialized_swarf
        (root / ".gitignore").write_text(".swarf/\n.mise.local.toml\n")
        (root / ".mise.local.toml").write_text('[hooks]\nenter = "swarf enter"\n')
        result = invoke(["doctor"])
        # Other checks may fail (remote, etc), but gitignore checks should pass
        assert ".swarf/ is gitignored" in result.output or ".swarf/ linked to" in result.output
        assert ".mise.local.toml is gitignored" in result.output

    def test_mise_local_not_gitignored(self, git_repo):
        (git_repo / ".swarf").mkdir()
        (git_repo / ".gitignore").write_text(".swarf/\n")
        result = invoke(["doctor"])
        assert result.exit_code != 0
        assert ".mise.local.toml is NOT gitignored" in result.output

    def test_linked_file_not_gitignored(self, initialized_swarf):
        root = initialized_swarf
        (root / ".swarf" / "links" / "AGENTS.md").write_text("# agents\n")
        (root / ".gitignore").write_text(".swarf/\n.mise.local.toml\n")
        result = invoke(["doctor"])
        assert result.exit_code != 0
        assert "AGENTS.md is NOT gitignored" in result.output

    def test_linked_file_gitignored(self, initialized_swarf):
        root = initialized_swarf
        (root / ".swarf" / "links" / "AGENTS.md").write_text("# agents\n")
        (root / ".gitignore").write_text(".swarf/\n.mise.local.toml\nAGENTS.md\n")
        result = invoke(["doctor"])
        assert "AGENTS.md is gitignored" in result.output

    def test_not_in_git_repo(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("GIT_CEILING_DIRECTORIES", str(tmp_path))
        result = invoke(["doctor"])
        assert result.exit_code != 0
        assert "Not inside a git repository" in result.output


class TestDoctorMiseLocal:
    def test_check_mise_local_missing(self, git_repo):
        _, ok, msg = check_mise_local(git_repo)
        assert not ok
        assert "not found" in msg

    def test_check_mise_local_present(self, git_repo):
        (git_repo / ".mise.local.toml").write_text('[hooks]\nenter = "swarf enter"\n')
        _, ok, msg = check_mise_local(git_repo)
        assert ok
        assert "enter hook" in msg

    def test_check_mise_local_missing_hook(self, git_repo):
        (git_repo / ".mise.local.toml").write_text("[tools]\npython = '3.13'\n")
        _, ok, msg = check_mise_local(git_repo)
        assert not ok
        assert "missing" in msg


class TestDoctorStore:
    @pytest.mark.usefixtures("initialized_swarf")
    def test_check_store_exists(self):
        _, ok, _ = check_store_exists()
        assert ok

    @pytest.mark.usefixtures("git_repo")
    def test_check_store_missing(self):
        _, ok, _ = check_store_exists()
        assert not ok


class TestDoctorDaemon:
    def test_daemon_not_running(self, monkeypatch, tmp_path):
        import swarf.paths as p

        monkeypatch.setattr(p, "PID_FILE", tmp_path / "nonexistent.pid")
        _, ok, msg = check_daemon_running()
        assert not ok
        assert "not running" in msg

    def test_daemon_stale_pid(self, monkeypatch, tmp_path):
        import swarf.paths as p

        pid_file = tmp_path / "daemon.pid"
        pid_file.write_text("999999999")
        monkeypatch.setattr(p, "PID_FILE", pid_file)
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
