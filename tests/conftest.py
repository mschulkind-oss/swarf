"""Shared test fixtures."""

from __future__ import annotations

import subprocess

import pytest

import swarf.config as cfg
import swarf.paths as paths
from swarf.config import GlobalConfig, write_global_config


@pytest.fixture()
def git_repo(tmp_path, monkeypatch):
    """Create a temporary git repo with isolated config, and cd into it."""
    monkeypatch.chdir(tmp_path)
    fake_config = tmp_path / ".config"
    fake_config.mkdir()
    fake_data = tmp_path / ".local" / "share"
    fake_data.mkdir(parents=True)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(fake_config))
    monkeypatch.setenv("XDG_DATA_HOME", str(fake_data))
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

    # Isolate global swarf config and store to tmp_path
    config_dir = fake_config / "swarf"
    config_dir.mkdir(parents=True)
    store_dir = fake_data / "swarf"
    monkeypatch.setattr(paths, "CONFIG_DIR", config_dir)
    monkeypatch.setattr(paths, "GLOBAL_CONFIG_TOML", config_dir / "config.toml")
    monkeypatch.setattr(paths, "DRAWERS_TOML", config_dir / "drawers.toml")
    monkeypatch.setattr(paths, "PID_FILE", config_dir / "daemon.pid")
    monkeypatch.setattr(paths, "LOG_FILE", config_dir / "daemon.log")
    monkeypatch.setattr(paths, "STORE_DIR", store_dir)
    monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
    monkeypatch.setattr(cfg, "GLOBAL_CONFIG_TOML", config_dir / "config.toml")
    monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

    # Pre-create a default global config so init doesn't prompt
    write_global_config(GlobalConfig(backend="git", remote="origin"))

    return tmp_path


@pytest.fixture()
def initialized_swarf(git_repo):
    """Git repo with swarf initialized (central store + symlink)."""
    store_dir = paths.STORE_DIR
    slug = paths.project_slug(git_repo)
    proj_dir = store_dir / slug

    # Create central store as a git repo
    store_dir.mkdir(parents=True)
    subprocess.run(["git", "init"], capture_output=True, check=True, cwd=store_dir)
    subprocess.run(
        ["git", "config", "user.email", "test@test.com"],
        capture_output=True,
        check=True,
        cwd=store_dir,
    )
    subprocess.run(
        ["git", "config", "user.name", "Test"],
        capture_output=True,
        check=True,
        cwd=store_dir,
    )

    # Create project directory with links/
    proj_dir.mkdir(parents=True)
    (proj_dir / "links").mkdir()

    # Symlink .swarf -> store project dir
    (git_repo / ".swarf").symlink_to(proj_dir)

    return git_repo


@pytest.fixture()
def bare_remote(tmp_path):
    """Bare git repo to act as a push target."""
    remote_path = tmp_path / "remote.git"
    remote_path.mkdir()
    subprocess.run(["git", "init", "--bare"], capture_output=True, check=True, cwd=remote_path)
    return remote_path
