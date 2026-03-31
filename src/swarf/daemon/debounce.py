"""Debounce timer for the swarf daemon."""

from __future__ import annotations

import asyncio
from collections.abc import Callable


class Debouncer:
    """Per-drawer debounce timer.

    Call trigger() on file change. After `duration` seconds of quiet,
    the callback fires.
    """

    def __init__(self, duration: float, callback: Callable[[], None]) -> None:
        self.duration = duration
        self.callback = callback
        self._task: asyncio.Task[None] | None = None

    def trigger(self) -> None:
        """Reset the debounce timer."""
        if self._task is not None:
            self._task.cancel()
        self._task = asyncio.get_event_loop().create_task(self._wait())

    async def _wait(self) -> None:
        """Wait for the quiet period, then fire the callback."""
        await asyncio.sleep(self.duration)
        self.callback()

    def cancel(self) -> None:
        """Cancel the pending debounce timer."""
        if self._task is not None:
            self._task.cancel()
            self._task = None
