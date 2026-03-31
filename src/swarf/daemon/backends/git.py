"""Git sync backend for the swarf daemon."""

from __future__ import annotations

import logging
from pathlib import Path

from swarf.daemon.backends import SyncBackend, SyncResult
from swarf.git import git_add_all, git_commit, git_push, git_remote_url, git_status_porcelain

logger = logging.getLogger(__name__)


class GitBackend(SyncBackend):
    """Sync via git add/commit/push."""

    def sync(self, swarf_path: Path) -> SyncResult:
        """Add, commit, and push changes in the swarf directory."""
        git_add_all(swarf_path)

        status = git_status_porcelain(swarf_path)
        if not status.strip():
            return SyncResult(success=True, message="No changes to sync", files_changed=0)

        n_files = len([line for line in status.strip().splitlines() if line.strip()])

        try:
            git_commit(swarf_path, f"auto: sync {n_files} file{'s' if n_files != 1 else ''}")
        except Exception:
            logger.exception("Failed to commit in %s", swarf_path)
            return SyncResult(success=False, message="Commit failed", files_changed=0)

        # Push only if a remote is configured
        if git_remote_url(swarf_path):
            try:
                git_push(swarf_path)
            except Exception:
                logger.warning("Push failed for %s (will retry later)", swarf_path)

        return SyncResult(success=True, message=f"Synced {n_files} files", files_changed=n_files)

    def has_changes(self, swarf_path: Path) -> bool:
        """Check for uncommitted changes."""
        status = git_status_porcelain(swarf_path)
        return bool(status.strip())
