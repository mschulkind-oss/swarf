"""Thin wrappers around shelling out to git."""

from __future__ import annotations

import subprocess
from pathlib import Path


def git_init(path: Path) -> subprocess.CompletedProcess[str]:
    """Initialize a new git repository."""
    return subprocess.run(
        ["git", "init"],
        cwd=path,
        capture_output=True,
        text=True,
        check=True,
    )


def git_config_set(path: Path, key: str, value: str) -> subprocess.CompletedProcess[str]:
    """Set a git config value in a repository."""
    return subprocess.run(
        ["git", "config", key, value],
        cwd=path,
        capture_output=True,
        text=True,
        check=True,
    )


def git_config_get(path: Path, key: str) -> str | None:
    """Get a git config value, or None if not set."""
    result = subprocess.run(
        ["git", "config", key],
        cwd=path,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def git_add_remote(path: Path, name: str, url: str) -> subprocess.CompletedProcess[str]:
    """Add a remote to a git repository."""
    return subprocess.run(
        ["git", "remote", "add", name, url],
        cwd=path,
        capture_output=True,
        text=True,
        check=True,
    )


def git_add_all(path: Path) -> subprocess.CompletedProcess[str]:
    """Stage all changes."""
    return subprocess.run(
        ["git", "add", "-A"],
        cwd=path,
        capture_output=True,
        text=True,
        check=True,
    )


def git_commit(path: Path, message: str) -> subprocess.CompletedProcess[str]:
    """Create a commit with the given message."""
    return subprocess.run(
        ["git", "commit", "-m", message],
        cwd=path,
        capture_output=True,
        text=True,
        check=True,
    )


def git_push(path: Path, remote: str = "origin") -> subprocess.CompletedProcess[str]:
    """Push to a remote."""
    return subprocess.run(
        ["git", "push", remote],
        cwd=path,
        capture_output=True,
        text=True,
        check=True,
    )


def git_clone(url: str, dest: Path) -> subprocess.CompletedProcess[str]:
    """Clone a git repository."""
    return subprocess.run(
        ["git", "clone", url, str(dest)],
        capture_output=True,
        text=True,
        check=True,
    )


def git_pull(path: Path) -> subprocess.CompletedProcess[str]:
    """Pull from the default remote."""
    return subprocess.run(
        ["git", "pull"],
        cwd=path,
        capture_output=True,
        text=True,
        check=True,
    )


def git_status_porcelain(path: Path) -> str:
    """Return git status in porcelain format."""
    result = subprocess.run(
        ["git", "status", "--porcelain"],
        cwd=path,
        capture_output=True,
        text=True,
        check=True,
    )
    return result.stdout


def git_is_repo(path: Path) -> bool:
    """Check if path is inside a git repository."""
    result = subprocess.run(
        ["git", "rev-parse", "--is-inside-work-tree"],
        cwd=path,
        capture_output=True,
        text=True,
    )
    return result.returncode == 0


def git_remote_url(path: Path, name: str = "origin") -> str | None:
    """Get the URL for a named remote, or None if not configured."""
    result = subprocess.run(
        ["git", "remote", "get-url", name],
        cwd=path,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def is_inside_work_tree(path: Path | None = None) -> bool:
    """Check if we're inside a git working tree."""
    kwargs: dict = {"capture_output": True, "text": True}
    if path is not None:
        kwargs["cwd"] = path
    result = subprocess.run(
        ["git", "rev-parse", "--is-inside-work-tree"],
        **kwargs,
    )
    return result.returncode == 0


def check_ignore(path: str, cwd: Path | None = None) -> bool:
    """Check if a path would be ignored by git."""
    cmd = ["git", "check-ignore", "-q", path]
    kwargs: dict = {"capture_output": True}
    if cwd is not None:
        kwargs["cwd"] = cwd
    result = subprocess.run(cmd, **kwargs)
    return result.returncode == 0


def get_repo_root(start: Path | None = None) -> Path | None:
    """Get the root of the git repository containing start."""
    kwargs: dict = {"capture_output": True, "text": True}
    if start is not None:
        kwargs["cwd"] = start
    result = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        **kwargs,
    )
    if result.returncode != 0:
        return None
    return Path(result.stdout.strip())
