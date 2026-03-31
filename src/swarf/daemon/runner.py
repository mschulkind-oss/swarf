"""Main daemon loop and watcher orchestration."""

from __future__ import annotations

import asyncio
import logging

import watchfiles

from swarf.config import DrawerEntry, parse_duration, read_drawer_config, read_drawers
from swarf.daemon.backends import SyncBackend
from swarf.daemon.backends.git import GitBackend
from swarf.daemon.backends.rclone import RcloneBackend
from swarf.daemon.debounce import Debouncer

logger = logging.getLogger(__name__)

# Minimum seconds between pushes per drawer
PUSH_RATE_LIMIT = 30.0


class DaemonRunner:
    """Watches all registered drawers and syncs on changes."""

    def __init__(self) -> None:
        self._last_push: dict[str, float] = {}

    async def run(self) -> None:
        """Load drawers and start watching them concurrently."""
        drawers = read_drawers()
        active = [d for d in drawers if d.path.is_dir()]

        if not active:
            logger.info("No active drawers found. Nothing to watch.")
            return

        logger.info("Watching %d drawer(s)", len(active))
        tasks = [asyncio.create_task(self._watch_drawer(d)) for d in active]
        try:
            await asyncio.gather(*tasks)
        except asyncio.CancelledError:
            logger.info("Daemon shutting down")
            for t in tasks:
                t.cancel()
            # Final sync for any pending changes
            for d in active:
                backend = self._make_backend(d)
                if backend.has_changes(d.path):
                    logger.info("Final sync for %s", d.path)
                    backend.sync(d.path)

    async def _watch_drawer(self, drawer: DrawerEntry) -> None:
        """Watch a single drawer for changes."""
        config = read_drawer_config(drawer.path)
        duration = parse_duration(config.debounce)
        backend = self._make_backend(drawer)

        def on_debounce_expired() -> None:
            result = backend.sync(drawer.path)
            if result.success and result.files_changed > 0:
                logger.info("%s: %s", drawer.path, result.message)
            elif not result.success:
                logger.warning("%s: %s", drawer.path, result.message)

        debouncer = Debouncer(duration, on_debounce_expired)

        logger.info(
            "Watching %s (backend=%s, debounce=%s)", drawer.path, drawer.backend, config.debounce
        )

        try:
            async for changes in watchfiles.awatch(drawer.path):
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
            raise

    def _make_backend(self, drawer: DrawerEntry) -> SyncBackend:
        """Create the appropriate backend for a drawer."""
        if drawer.backend == "rclone":
            config = read_drawer_config(drawer.path)
            return RcloneBackend(remote=config.remote)
        return GitBackend()

    async def shutdown(self) -> None:
        """Cancel all watchers and perform final syncs."""
        for task in asyncio.all_tasks():
            if task is not asyncio.current_task():
                task.cancel()
