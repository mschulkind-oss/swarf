"""Daemon lifecycle functions (start, stop, install)."""

from __future__ import annotations

import asyncio
import logging
import os
import sys

from swarf.paths import LOG_FILE, PID_FILE


def do_start(foreground: bool = False) -> None:
    """Start the daemon. Called by the CLI and by swarf init."""
    from swarf.daemon.runner import DaemonRunner

    if PID_FILE.exists():
        try:
            pid = int(PID_FILE.read_text().strip())
            os.kill(pid, 0)
            return  # already running
        except (ValueError, ProcessLookupError):
            PID_FILE.unlink(missing_ok=True)

    if foreground:
        _setup_logging()
        PID_FILE.parent.mkdir(parents=True, exist_ok=True)
        PID_FILE.write_text(str(os.getpid()))
        try:
            asyncio.run(DaemonRunner().run())
        finally:
            PID_FILE.unlink(missing_ok=True)
    else:
        _daemonize()


def do_install() -> None:
    """Install systemd user service. Called by the CLI and by swarf init."""
    from swarf.daemon.service import install_systemd_service

    install_systemd_service()


def _setup_logging() -> None:
    """Configure logging for the daemon."""
    LOG_FILE.parent.mkdir(parents=True, exist_ok=True)
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(name)s %(levelname)s %(message)s",
        handlers=[
            logging.FileHandler(LOG_FILE),
            logging.StreamHandler(),
        ],
    )


def _daemonize() -> None:
    """Double-fork to daemonize the process."""
    pid = os.fork()
    if pid > 0:
        return

    os.setsid()

    pid = os.fork()
    if pid > 0:
        sys.exit(0)

    sys.stdin.close()
    LOG_FILE.parent.mkdir(parents=True, exist_ok=True)
    log_fd = os.open(str(LOG_FILE), os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o644)
    os.dup2(log_fd, 1)
    os.dup2(log_fd, 2)

    PID_FILE.parent.mkdir(parents=True, exist_ok=True)
    PID_FILE.write_text(str(os.getpid()))

    _setup_logging()
    from swarf.daemon.runner import DaemonRunner

    try:
        asyncio.run(DaemonRunner().run())
    finally:
        PID_FILE.unlink(missing_ok=True)
