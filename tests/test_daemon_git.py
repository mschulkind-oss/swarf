"""Integration tests for the git sync backend."""

from __future__ import annotations

import subprocess

import pytest

from swarf.daemon.backends.git import GitBackend


@pytest.fixture()
def synced_drawer(tmp_path, monkeypatch):
    """Swarf dir with git repo and bare remote."""
    swarf = tmp_path / ".swarf"
    swarf.mkdir()

    # Bare remote
    remote = tmp_path / "remote.git"
    remote.mkdir()
    subprocess.run(["git", "init", "--bare"], cwd=remote, capture_output=True, check=True)

    # Init swarf as git repo
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
    subprocess.run(
        ["git", "remote", "add", "origin", str(remote)],
        cwd=swarf,
        capture_output=True,
        check=True,
    )

    # Initial commit so we have a branch
    (swarf / "config.toml").write_text('[sync]\nbackend = "git"\n')
    subprocess.run(["git", "add", "-A"], cwd=swarf, capture_output=True, check=True)
    subprocess.run(
        ["git", "commit", "-m", "init"],
        cwd=swarf,
        capture_output=True,
        check=True,
    )
    # Push to create remote branch
    subprocess.run(
        ["git", "push", "-u", "origin", "master"],
        cwd=swarf,
        capture_output=True,
        check=True,
    )

    monkeypatch.setenv("HOME", str(tmp_path))
    return swarf, remote


class TestGitBackend:
    def test_sync_commits(self, synced_drawer):
        swarf, _ = synced_drawer
        (swarf / "notes.md").write_text("# Notes\n")
        backend = GitBackend()
        result = backend.sync(swarf)
        assert result.success
        assert result.files_changed == 1
        # Verify commit exists
        r = subprocess.run(
            ["git", "log", "--oneline", "-1"],
            cwd=swarf,
            capture_output=True,
            text=True,
        )
        assert "auto: sync 1 file" in r.stdout

    def test_sync_pushes(self, synced_drawer):
        swarf, remote = synced_drawer
        (swarf / "notes.md").write_text("# Notes\n")
        backend = GitBackend()
        backend.sync(swarf)
        # Verify remote has the commit
        r = subprocess.run(
            ["git", "log", "--oneline", "-1"],
            cwd=remote,
            capture_output=True,
            text=True,
        )
        assert "auto: sync" in r.stdout

    def test_no_changes_noop(self, synced_drawer):
        swarf, _ = synced_drawer
        backend = GitBackend()
        result = backend.sync(swarf)
        assert result.success
        assert result.files_changed == 0

    def test_push_failure_handled(self, synced_drawer):
        swarf, _ = synced_drawer
        # Remove the remote
        subprocess.run(
            ["git", "remote", "remove", "origin"],
            cwd=swarf,
            capture_output=True,
            check=True,
        )
        # Add a bogus remote
        subprocess.run(
            ["git", "remote", "add", "origin", "/nonexistent"],
            cwd=swarf,
            capture_output=True,
            check=True,
        )
        (swarf / "notes.md").write_text("# Notes\n")
        backend = GitBackend()
        result = backend.sync(swarf)
        # Commit succeeds even though push fails
        assert result.success
        assert result.files_changed == 1

    def test_has_changes(self, synced_drawer):
        swarf, _ = synced_drawer
        assert not GitBackend().has_changes(swarf)
        (swarf / "new.md").write_text("new")
        assert GitBackend().has_changes(swarf)


class TestGitBackendFilterGitDir:
    @pytest.mark.usefixtures("synced_drawer")
    def test_git_dir_changes_not_counted(self, synced_drawer):
        """Changes inside .git/ should not appear as user changes."""
        swarf, _ = synced_drawer
        # Only .git internal changes (no user changes)
        assert not GitBackend().has_changes(swarf)
