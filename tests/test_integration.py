"""End-to-end integration test for the swarf lifecycle."""

from __future__ import annotations

import subprocess

from helpers import invoke

import swarf.paths as paths
from swarf.config import GlobalConfig, write_global_config
from swarf.daemon.backends.git import GitBackend


def test_full_lifecycle(git_repo):
    """Test the complete swarf lifecycle: init -> link -> sync -> status -> doctor."""
    # 1. Create a bare remote for pushing
    bare = git_repo / "remote.git"
    bare.mkdir()
    subprocess.run(["git", "init", "--bare"], cwd=bare, capture_output=True, check=True)

    # 2. Set global config to use bare remote, then init
    write_global_config(GlobalConfig(backend="git", remote=str(bare)))
    result = invoke(["init"])
    assert result.exit_code == 0, result.output
    assert "Initialized swarf" in result.output

    # 3. .swarf should be a symlink into the store
    assert (git_repo / ".swarf").is_symlink()

    # 4. Create a link source
    links = git_repo / ".swarf" / "links"
    (links / "AGENTS.md").write_text("# Agent Instructions\n")

    # 5. swarf link
    result = invoke(["link"])
    assert result.exit_code == 0
    assert (git_repo / "AGENTS.md").is_symlink()
    assert (git_repo / "AGENTS.md").read_text() == "# Agent Instructions\n"

    # 6. Push the initial commit to establish the remote branch
    store = paths.STORE_DIR
    subprocess.run(
        ["git", "push", "-u", "origin", "master"],
        cwd=store,
        capture_output=True,
        check=True,
    )

    # 7. Modify a file in the store
    (links / "AGENTS.md").write_text("# Updated Agent Instructions\n")

    # 8. Manually trigger GitBackend.sync() on the store
    backend = GitBackend()
    result_sync = backend.sync(store)
    assert result_sync.success
    assert result_sync.files_changed > 0

    # 9. Verify commit in store git log
    r = subprocess.run(
        ["git", "log", "--oneline"],
        cwd=store,
        capture_output=True,
        text=True,
    )
    assert "auto: sync" in r.stdout

    # 10. Verify push to bare remote
    r = subprocess.run(
        ["git", "log", "--oneline"],
        cwd=bare,
        capture_output=True,
        text=True,
    )
    assert "auto: sync" in r.stdout

    # 11. swarf status
    result = invoke(["status"])
    assert result.exit_code == 0
    assert "git" in result.output

    # 12. swarf doctor
    result = invoke(["doctor"])
    assert ".swarf/" in result.output
