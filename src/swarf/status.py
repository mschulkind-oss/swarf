"""swarf status — show sync status across all registered drawers."""

from __future__ import annotations

import os
import subprocess
from dataclasses import dataclass
from pathlib import Path

from rich.console import Console
from rich.table import Table

from swarf.config import read_drawer_config, read_drawers
from swarf.git import git_remote_url, git_status_porcelain
from swarf.paths import PID_FILE


@dataclass
class DrawerStatus:
    """Status of a single drawer."""

    path: Path
    exists: bool
    backend: str
    remote: str
    pending_changes: int
    last_commit_time: str
    last_commit_message: str


def get_drawer_status(path: Path) -> DrawerStatus:
    """Get the status of a single drawer."""
    if not path.is_dir():
        return DrawerStatus(
            path=path,
            exists=False,
            backend="unknown",
            remote="",
            pending_changes=0,
            last_commit_time="",
            last_commit_message="",
        )

    try:
        config = read_drawer_config(path)
        backend = config.backend
        remote_str = config.remote
    except Exception:
        backend = "unknown"
        remote_str = ""

    # Get remote URL for git backends
    if backend == "git":
        url = git_remote_url(path)
        if url:
            remote_str = f"{remote_str} ({url})"

    # Count pending changes
    pending = 0
    if backend == "git" and (path / ".git").is_dir():
        try:
            status = git_status_porcelain(path)
            pending = len([line for line in status.strip().splitlines() if line.strip()])
        except Exception:
            pass

    # Get last commit info
    last_time = ""
    last_msg = ""
    if (path / ".git").is_dir():
        try:
            r = subprocess.run(
                ["git", "log", "-1", "--format=%cr|%s"],
                cwd=path,
                capture_output=True,
                text=True,
            )
            if r.returncode == 0 and r.stdout.strip():
                parts = r.stdout.strip().split("|", 1)
                last_time = parts[0]
                last_msg = parts[1] if len(parts) > 1 else ""
        except Exception:
            pass

    return DrawerStatus(
        path=path,
        exists=True,
        backend=backend,
        remote=remote_str,
        pending_changes=pending,
        last_commit_time=last_time,
        last_commit_message=last_msg,
    )


def run_status() -> None:
    """Display status of all registered drawers."""
    console = Console()
    drawers = read_drawers()

    if not drawers:
        console.print("No drawers registered. Run 'swarf init' in a project.")
        return

    table = Table(show_header=True, header_style="bold")
    table.add_column("Drawer")
    table.add_column("Backend")
    table.add_column("Remote")
    table.add_column("Pending", justify="right")
    table.add_column("Last Sync")

    for entry in drawers:
        status = get_drawer_status(entry.path)

        # Shorten path for display
        display_path = str(status.path).replace(str(Path.home()), "~")

        if not status.exists:
            table.add_row(display_path, entry.backend, "", "", "[red]missing[/red]")
            continue

        table.add_row(
            display_path,
            status.backend,
            status.remote,
            str(status.pending_changes),
            status.last_commit_time or "never",
        )

    console.print(table)

    # Daemon status
    if PID_FILE.exists():
        try:
            pid = int(PID_FILE.read_text().strip())
            os.kill(pid, 0)
            console.print(f"\nDaemon: [green]running[/green] (PID {pid})")
        except (ValueError, ProcessLookupError):
            console.print("\nDaemon: [red]not running[/red] (stale PID file)")
    else:
        console.print("\nDaemon: [yellow]not running[/yellow]")
