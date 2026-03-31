"""Integration tests for the rclone sync backend."""

from __future__ import annotations

import shutil
import subprocess

import pytest

from swarf.daemon.backends.rclone import RcloneBackend


@pytest.fixture()
def rclone_drawer(tmp_path):
    """Swarf dir with real git repo and local target for rclone testing."""
    swarf = tmp_path / ".swarf"
    swarf.mkdir()
    # Real git repo for local commits
    subprocess.run(["git", "init"], cwd=swarf, capture_output=True, check=True)
    subprocess.run(
        ["git", "config", "user.email", "test@test.com"],
        cwd=swarf,
        capture_output=True,
        check=True,
    )
    subprocess.run(
        ["git", "config", "user.name", "Test"],
        cwd=swarf,
        capture_output=True,
        check=True,
    )
    # Initial commit
    (swarf / "open-questions.md").write_text("# Open Questions\n")
    subprocess.run(["git", "add", "-A"], cwd=swarf, capture_output=True, check=True)
    subprocess.run(["git", "commit", "-m", "init"], cwd=swarf, capture_output=True, check=True)

    target = tmp_path / "target"
    target.mkdir()
    return swarf, target


@pytest.mark.skipif(shutil.which("rclone") is None, reason="rclone not installed")
class TestRcloneBackend:
    def test_syncs_files(self, rclone_drawer):
        swarf, target = rclone_drawer
        (swarf / "notes.md").write_text("# Notes\n")
        backend = RcloneBackend(remote=str(target))
        result = backend.sync(swarf)
        assert result.success
        assert (target / "notes.md").exists()

    def test_excludes_git_dir(self, rclone_drawer):
        swarf, target = rclone_drawer
        (swarf / "notes.md").write_text("# Notes\n")
        backend = RcloneBackend(remote=str(target))
        backend.sync(swarf)
        assert not (target / ".git").exists()

    def test_has_changes_always_true(self, rclone_drawer):
        swarf, target = rclone_drawer
        backend = RcloneBackend(remote=str(target))
        assert backend.has_changes(swarf)

    def test_local_git_commit(self, rclone_drawer):
        """Verify rclone backend also creates local git commits."""
        swarf, target = rclone_drawer
        (swarf / "notes.md").write_text("# Notes\n")
        backend = RcloneBackend(remote=str(target))
        backend.sync(swarf)
        r = subprocess.run(
            ["git", "log", "--oneline", "-1"],
            cwd=swarf,
            capture_output=True,
            text=True,
        )
        assert "auto: sync" in r.stdout


class TestRcloneNotInstalled:
    def test_graceful_error(self, tmp_path, monkeypatch):
        monkeypatch.setattr(shutil, "which", lambda _name: None)
        swarf = tmp_path / ".swarf"
        swarf.mkdir()
        # Need a git repo for the commit step
        subprocess.run(["git", "init"], cwd=swarf, capture_output=True, check=True)
        subprocess.run(
            ["git", "config", "user.email", "test@test.com"],
            cwd=swarf,
            capture_output=True,
            check=True,
        )
        subprocess.run(
            ["git", "config", "user.name", "Test"],
            cwd=swarf,
            capture_output=True,
            check=True,
        )
        backend = RcloneBackend(remote="gdrive:swarf")
        result = backend.sync(swarf)
        assert not result.success
        assert "not installed" in result.message
