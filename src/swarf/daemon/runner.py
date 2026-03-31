"""Main daemon loop — watches the central store and syncs."""

from __future__ import annotations

import asyncio
import logging

import watchfiles

import swarf.paths as paths
from swarf.config import parse_duration, read_global_config
from swarf.daemon.backends import SyncBackend
from swarf.daemon.backends.git import GitBackend
from swarf.daemon.backends.rclone import RcloneBackend
from swarf.daemon.debounce import Debouncer

logger = logging.getLogger(__name__)


class DaemonRunner:
    """Watches the central store and syncs on changes."""

    async def run(self) -> None:
        """Watch the store directory and sync on changes."""
        global_config = read_global_config()
        if global_config is None:
            logger.error("No global config found. Run 'swarf init' first.")
            return

        if not paths.STORE_DIR.is_dir():
            logger.info("Store directory %s does not exist. Waiting...", paths.STORE_DIR)
            # Poll until it appears
            while not paths.STORE_DIR.is_dir():
                await asyncio.sleep(5.0)

        debounce_str = global_config.debounce
        duration = parse_duration(debounce_str)
        backend = self._make_backend(global_config.backend, global_config.remote)

        def on_debounce_expired() -> None:
            result = backend.sync(paths.STORE_DIR)
            if result.success and result.files_changed > 0:
                logger.info("store: %s", result.message)
            elif not result.success:
                logger.warning("store: %s", result.message)

        debouncer = Debouncer(duration, on_debounce_expired)

        logger.info(
            "Watching %s (backend=%s, debounce=%s)",
            paths.STORE_DIR,
            global_config.backend,
            debounce_str,
        )

        try:
            async for changes in watchfiles.awatch(paths.STORE_DIR):
                # Filter out .git/ changes to avoid feedback loops
                real_changes = [
                    (change_type, path)
                    for change_type, path in changes
                    if "/.git/" not in path and not path.endswith("/.git")
                ]
                if real_changes:
                    debouncer.trigger()
        except asyncio.CancelledError:
            debouncer.cancel()
            # Final sync for any pending changes
            if backend.has_changes(paths.STORE_DIR):
                logger.info("Final sync before shutdown")
                backend.sync(paths.STORE_DIR)
            raise

    def _make_backend(self, backend_type: str, remote: str) -> SyncBackend:
        """Create the appropriate backend."""
        if backend_type == "rclone":
            return RcloneBackend(remote=remote)
        return GitBackend()

    async def shutdown(self) -> None:
        """Cancel all tasks."""
        for task in asyncio.all_tasks():
            if task is not asyncio.current_task():
                task.cancel()
