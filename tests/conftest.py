"""Shared test fixtures."""

from __future__ import annotations

import subprocess

import pytest


@pytest.fixture()
def git_repo(tmp_path, monkeypatch):
    """Create a temporary git repo with isolated config, and cd into it."""
    monkeypatch.chdir(tmp_path)
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


@pytest.fixture()
def initialized_swarf(git_repo):
    """Git repo with swarf already initialized (minimal, no init command)."""
    swarf = git_repo / ".swarf"
    swarf.mkdir()
    (swarf / "docs" / "research").mkdir(parents=True)
    (swarf / "docs" / "design").mkdir(parents=True)
    (swarf / "links").mkdir()
    (swarf / "open-questions.md").write_text("# Open Questions\n")
    # Init git inside .swarf
    subprocess.run(["git", "init"], capture_output=True, check=True, cwd=swarf)
    subprocess.run(
        ["git", "config", "user.email", "test@test.com"],
        capture_output=True,
        check=True,
        cwd=swarf,
    )
    subprocess.run(
        ["git", "config", "user.name", "Test"],
        capture_output=True,
        check=True,
        cwd=swarf,
    )
    return git_repo


@pytest.fixture()
def bare_remote(tmp_path):
    """Bare git repo to act as a push target."""
    remote_path = tmp_path / "remote.git"
    remote_path.mkdir()
    subprocess.run(["git", "init", "--bare"], capture_output=True, check=True, cwd=remote_path)
    return remote_path
