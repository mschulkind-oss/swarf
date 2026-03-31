"""swarf pull — update the central store from the remote."""

from __future__ import annotations

import shutil
import subprocess

import swarf.paths as paths
from swarf.config import read_global_config
from swarf.console import error, ok
from swarf.git import git_is_repo, git_pull


def run_pull() -> None:
    """Pull the latest changes from the remote into the central store."""
    config = read_global_config()
    if config is None:
        error("No global config found. Run 'swarf init' in a project first.")
        raise SystemExit(1)

    if not paths.STORE_DIR.is_dir():
        error(f"Store does not exist at {paths.STORE_DIR}. Run 'swarf clone' first.")
        raise SystemExit(1)

    if config.backend == "git":
        _pull_git()
    elif config.backend == "rclone":
        _pull_rclone(config.remote)
    else:
        error(f"Unknown backend: {config.backend}")
        raise SystemExit(1)


def _pull_git() -> None:
    """Pull via git."""
    if not git_is_repo(paths.STORE_DIR):
        error(f"Store at {paths.STORE_DIR} is not a git repository.")
        raise SystemExit(1)

    try:
        git_pull(paths.STORE_DIR)
        ok("Pulled latest changes.")
    except subprocess.CalledProcessError as e:
        error(f"git pull failed: {e.stderr}")
        raise SystemExit(1) from None


def _pull_rclone(remote: str) -> None:
    """Pull via rclone copy."""
    if not shutil.which("rclone"):
        error("rclone is not installed.")
        raise SystemExit(1)

    try:
        subprocess.run(
            ["rclone", "copy", remote, str(paths.STORE_DIR)],
            capture_output=True,
            text=True,
            check=True,
        )
        ok("Pulled latest changes via rclone.")
    except subprocess.CalledProcessError as e:
        error(f"rclone copy failed: {e.stderr}")
        raise SystemExit(1) from None
