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

# How often to check drawers.toml for new projects
_REGISTRY_POLL_INTERVAL = 10.0


class DaemonRunner:
    """Watches all registered drawers and syncs on changes."""

    def __init__(self) -> None:
        self._watcher_tasks: dict[str, asyncio.Task] = {}

    async def run(self) -> None:
        """Load drawers and watch them, re-reading registry for new ones."""
        self._load_and_start_drawers()

        if not self._watcher_tasks:
            logger.info("No active drawers found. Waiting for registrations...")

        try:
            while True:
                await asyncio.sleep(_REGISTRY_POLL_INTERVAL)
                self._load_and_start_drawers()
        except asyncio.CancelledError:
            logger.info("Daemon shutting down")
            for t in self._watcher_tasks.values():
                t.cancel()
            # Final sync for any pending changes
            drawers = read_drawers()
            for d in drawers:
                if d.path.is_dir():
                    backend = self._make_backend(d)
                    if backend.has_changes(d.path):
                        logger.info("Final sync for %s", d.path)
                        backend.sync(d.path)

    def _load_and_start_drawers(self) -> None:
        """Read drawers.toml and start watchers for any new drawers."""
        drawers = read_drawers()
        for d in drawers:
            key = str(d.path)
            if key in self._watcher_tasks:
                task = self._watcher_tasks[key]
                if not task.done():
                    continue
                # Task finished (maybe drawer was removed), clean up
                del self._watcher_tasks[key]
            if d.path.is_dir():
                logger.info("Starting watcher for %s", d.path)
                self._watcher_tasks[key] = asyncio.create_task(self._watch_drawer(d))

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
