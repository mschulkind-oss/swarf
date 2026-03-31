"""Tests for the daemon debounce timer."""

from __future__ import annotations

import asyncio

import pytest

from swarf.daemon.debounce import Debouncer


@pytest.mark.asyncio
async def test_debounce_fires_after_quiet_period():
    fired = []

    def callback():
        fired.append(True)

    debouncer = Debouncer(0.05, callback)
    debouncer.trigger()
    await asyncio.sleep(0.1)
    assert len(fired) == 1


@pytest.mark.asyncio
async def test_debounce_resets_on_retrigger():
    fired = []

    def callback():
        fired.append(True)

    debouncer = Debouncer(0.1, callback)
    debouncer.trigger()
    await asyncio.sleep(0.05)
    debouncer.trigger()  # Reset — should wait another 0.1s
    await asyncio.sleep(0.05)
    assert len(fired) == 0  # Hasn't fired yet
    await asyncio.sleep(0.1)
    assert len(fired) == 1  # Now it fired


@pytest.mark.asyncio
async def test_debounce_cancel():
    fired = []

    def callback():
        fired.append(True)

    debouncer = Debouncer(0.05, callback)
    debouncer.trigger()
    debouncer.cancel()
    await asyncio.sleep(0.1)
    assert len(fired) == 0
