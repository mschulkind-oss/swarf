"""Daemon CLI subcommand group."""

from __future__ import annotations

import asyncio
import logging
import os
import signal
import sys

import click

from swarf.paths import LOG_FILE, PID_FILE


@click.group()
def daemon() -> None:
    """Manage the swarf background sync daemon."""


@daemon.command()
@click.option("--foreground", is_flag=True, help="Run in foreground (for systemd).")
def start(foreground: bool) -> None:
    """Start the daemon."""
    if PID_FILE.exists():
        try:
            pid = int(PID_FILE.read_text().strip())
            os.kill(pid, signal.SIG_DFL)
            click.echo(f"Daemon already running (PID {pid})")
            return
        except (ValueError, ProcessLookupError):
            PID_FILE.unlink(missing_ok=True)

    do_start(foreground=foreground)
    if not foreground:
        click.echo("Daemon started.")


@daemon.command()
def stop() -> None:
    """Stop the daemon."""
    if not PID_FILE.exists():
        click.echo("Daemon is not running.")
        return

    try:
        pid = int(PID_FILE.read_text().strip())
        os.kill(pid, signal.SIGTERM)
        click.echo(f"Sent SIGTERM to daemon (PID {pid})")
    except (ValueError, ProcessLookupError):
        click.echo("Daemon is not running (stale PID file).")
    finally:
        PID_FILE.unlink(missing_ok=True)


@daemon.command("status")
def status_cmd() -> None:
    """Show daemon status."""
    if not PID_FILE.exists():
        click.echo("Daemon is not running.")
        raise SystemExit(1)

    try:
        pid = int(PID_FILE.read_text().strip())
        os.kill(pid, signal.SIG_DFL)
        click.echo(f"Daemon is running (PID {pid})")
    except (ValueError, ProcessLookupError):
        click.echo("Daemon is not running (stale PID file).")
        PID_FILE.unlink(missing_ok=True)
        raise SystemExit(1) from None


@daemon.command()
def install() -> None:
    """Install systemd user service."""
    try:
        do_install()
        click.echo("Systemd user service installed and started.")
    except RuntimeError as e:
        click.echo(click.style("Error:", fg="red") + f" {e}", err=True)
        raise SystemExit(1) from None
    except Exception as e:
        click.echo(click.style("Error:", fg="red") + f" Failed to install service: {e}", err=True)
        raise SystemExit(1) from None


def do_start(foreground: bool = False) -> None:
    """Start the daemon programmatically (used by swarf init)."""
    from swarf.daemon.runner import DaemonRunner

    if PID_FILE.exists():
        try:
            pid = int(PID_FILE.read_text().strip())
            os.kill(pid, signal.SIG_DFL)
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
    """Install systemd user service programmatically (used by swarf init)."""
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
    # First fork
    pid = os.fork()
    if pid > 0:
        click.echo(f"Daemon starting (PID {pid})")
        return

    os.setsid()

    # Second fork
    pid = os.fork()
    if pid > 0:
        sys.exit(0)

    # Redirect stdio
    sys.stdin.close()
    LOG_FILE.parent.mkdir(parents=True, exist_ok=True)
    log_fd = os.open(str(LOG_FILE), os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o644)
    os.dup2(log_fd, 1)
    os.dup2(log_fd, 2)

    # Write PID file and run
    PID_FILE.parent.mkdir(parents=True, exist_ok=True)
    PID_FILE.write_text(str(os.getpid()))

    _setup_logging()
    from swarf.daemon.runner import DaemonRunner

    try:
        asyncio.run(DaemonRunner().run())
    finally:
        PID_FILE.unlink(missing_ok=True)
