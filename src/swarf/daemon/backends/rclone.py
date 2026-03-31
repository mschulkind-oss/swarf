"""Rclone sync backend for the swarf daemon."""

from __future__ import annotations

import logging
import shutil
import subprocess
from pathlib import Path

from swarf.daemon.backends import SyncBackend, SyncResult
from swarf.git import git_add_all, git_commit, git_status_porcelain

logger = logging.getLogger(__name__)


class RcloneBackend(SyncBackend):
    """Sync via rclone, with local git commits for version history."""

    def __init__(self, remote: str) -> None:
        self.remote = remote

    def sync(self, swarf_path: Path) -> SyncResult:
        """Commit locally, then rclone sync to the remote."""
        # Local git commit for version history (same as git backend minus push)
        git_add_all(swarf_path)
        status = git_status_porcelain(swarf_path)
        n_files = 0
        if status.strip():
            n_files = len([line for line in status.strip().splitlines() if line.strip()])
            try:
                git_commit(swarf_path, f"auto: sync {n_files} file{'s' if n_files != 1 else ''}")
            except Exception:
                logger.exception("Failed to commit in %s", swarf_path)

        # Rclone sync to remote
        if not shutil.which("rclone"):
            return SyncResult(success=False, message="rclone not installed", files_changed=n_files)

        try:
            subprocess.run(
                [
                    "rclone",
                    "sync",
                    str(swarf_path),
                    self.remote,
                    "--exclude",
                    ".git/**",
                ],
                capture_output=True,
                text=True,
                check=True,
            )
        except subprocess.CalledProcessError as e:
            logger.warning("rclone sync failed: %s", e.stderr)
            msg = f"rclone sync failed: {e.stderr}"
            return SyncResult(success=False, message=msg, files_changed=n_files)

        msg = f"Synced {n_files} files via rclone"
        return SyncResult(success=True, message=msg, files_changed=n_files)

    def has_changes(self, swarf_path: Path) -> bool:  # noqa: ARG002
        """Rclone can't cheaply diff — always assume changes."""
        return True
