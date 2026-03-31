"""swarf clone — pull the central store from a remote."""

from __future__ import annotations

import shutil
import subprocess

import swarf.paths as paths
from swarf.config import read_global_config
from swarf.console import error, info, ok
from swarf.git import git_clone, git_is_repo


def run_clone() -> None:
    """Clone the central store from the configured remote."""
    config = read_global_config()
    if config is None:
        error("No global config found. Run 'swarf init' in a project first.")
        raise SystemExit(1)

    if not config.remote:
        error("No remote configured in global config.")
        raise SystemExit(1)

    if paths.STORE_DIR.is_dir() and git_is_repo(paths.STORE_DIR):
        error(f"Store already exists at {paths.STORE_DIR}. Use 'swarf pull' to update.")
        raise SystemExit(1)

    if config.backend == "git":
        _clone_git(config.remote)
    elif config.backend == "rclone":
        _clone_rclone(config.remote)
    else:
        error(f"Unknown backend: {config.backend}")
        raise SystemExit(1)

    # List discovered projects
    projects = [p.name for p in paths.STORE_DIR.iterdir() if p.is_dir() and p.name != ".git"]
    if projects:
        ok(f"Cloned store with {len(projects)} project(s):")
        for name in sorted(projects):
            info(f"  {name}")
        info("\nRun 'swarf init' inside each project to link it.")
    else:
        ok("Cloned store (empty — no projects yet).")


def _clone_git(remote: str) -> None:
    """Clone via git."""
    # If store dir exists but isn't a git repo, remove it first
    if paths.STORE_DIR.exists():
        shutil.rmtree(paths.STORE_DIR)
    git_clone(remote, paths.STORE_DIR)
    ok(f"Cloned from {remote}")


def _clone_rclone(remote: str) -> None:
    """Clone via rclone copy."""
    if not shutil.which("rclone"):
        error("rclone is not installed.")
        raise SystemExit(1)

    paths.STORE_DIR.mkdir(parents=True, exist_ok=True)
    try:
        subprocess.run(
            ["rclone", "copy", remote, str(paths.STORE_DIR)],
            capture_output=True,
            text=True,
            check=True,
        )
        ok(f"Copied from {remote}")
    except subprocess.CalledProcessError as e:
        error(f"rclone copy failed: {e.stderr}")
        raise SystemExit(1) from None
