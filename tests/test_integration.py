"""End-to-end integration test for the swarf lifecycle."""

from __future__ import annotations

import subprocess

from click.testing import CliRunner

from swarf.cli import main
from swarf.config import GlobalConfig, write_global_config
from swarf.daemon.backends.git import GitBackend


def test_full_lifecycle(git_repo):
    """Test the complete swarf lifecycle: init -> link -> sync -> status -> doctor."""
    runner = CliRunner()

    # 1. Create a bare remote for pushing
    bare = git_repo / "remote.git"
    bare.mkdir()
    subprocess.run(["git", "init", "--bare"], cwd=bare, capture_output=True, check=True)

    # 2. Set global config to use bare remote, then init
    write_global_config(GlobalConfig(backend="git", remote=str(bare)))
    result = runner.invoke(main, ["init"])
    assert result.exit_code == 0, result.output
    assert "Initialized swarf" in result.output

    # 3. Create a link source
    links = git_repo / ".swarf" / "links"
    (links / "AGENTS.md").write_text("# Agent Instructions\n")

    # 4. swarf link
    result = runner.invoke(main, ["link"])
    assert result.exit_code == 0
    assert (git_repo / "AGENTS.md").is_symlink()
    assert (git_repo / "AGENTS.md").read_text() == "# Agent Instructions\n"

    # 4b. Push the initial commit to establish the remote branch
    subprocess.run(
        ["git", "push", "-u", "origin", "master"],
        cwd=git_repo / ".swarf",
        capture_output=True,
        check=True,
    )

    # 5. Create some research notes
    docs = git_repo / ".swarf" / "docs" / "research"
    (docs / "notes.md").write_text("# Research Notes\nImportant finding.\n")

    # 6. Manually trigger GitBackend.sync()
    swarf = git_repo / ".swarf"
    backend = GitBackend()
    result_sync = backend.sync(swarf)
    assert result_sync.success
    assert result_sync.files_changed > 0

    # 7. Verify commit in .swarf git log
    r = subprocess.run(
        ["git", "log", "--oneline"],
        cwd=swarf,
        capture_output=True,
        text=True,
    )
    assert "auto: sync" in r.stdout

    # 8. Verify push to bare remote
    r = subprocess.run(
        ["git", "log", "--oneline"],
        cwd=bare,
        capture_output=True,
        text=True,
    )
    assert "auto: sync" in r.stdout

    # 9. swarf status
    result = runner.invoke(main, ["status"])
    assert result.exit_code == 0
    assert "git" in result.output

    # 10. swarf doctor
    result = runner.invoke(main, ["doctor"])
    assert ".swarf/ directory exists" in result.output
