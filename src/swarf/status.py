"""swarf status — show sync status for the central store."""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

from rich.table import Table

import swarf.paths as paths
from swarf.config import read_drawers, read_global_config
from swarf.git import git_is_repo, git_remote_url, git_status_porcelain


def run_status() -> None:
    """Display status of the central store and all registered projects."""
    from swarf.console import console

    config = read_global_config()
    if config is None:
        console.print("No global config found. Run 'swarf init' in a project.")
        return

    # Store status
    console.print("[bold]Store[/bold]")
    console.print(f"  Path:    {paths.STORE_DIR}")
    console.print(f"  Backend: {config.backend}")
    console.print(f"  Remote:  {config.remote or '[dim]not set[/dim]'}")

    if paths.STORE_DIR.is_dir() and git_is_repo(paths.STORE_DIR):
        # Pending changes
        try:
            status = git_status_porcelain(paths.STORE_DIR)
            pending = len([line for line in status.strip().splitlines() if line.strip()])
            console.print(f"  Pending: {pending} file(s)")
        except Exception:
            pass

        # Last commit
        try:
            r = subprocess.run(
                ["git", "log", "-1", "--format=%cr — %s"],
                cwd=paths.STORE_DIR,
                capture_output=True,
                text=True,
            )
            if r.returncode == 0 and r.stdout.strip():
                console.print(f"  Last sync: {r.stdout.strip()}")
        except Exception:
            pass

        # Remote URL
        url = git_remote_url(paths.STORE_DIR)
        if url:
            console.print(f"  Git remote: {url}")
    elif not paths.STORE_DIR.is_dir():
        console.print("  [red]Store not initialized. Run 'swarf init'.[/red]")

    # Projects
    drawers = read_drawers()
    if drawers:
        console.print()
        table = Table(show_header=True, header_style="bold")
        table.add_column("Project")
        table.add_column("Host Path")
        table.add_column("Linked")

        for entry in drawers:
            linked = (entry.host / ".swarf").is_symlink()
            linked_str = "[green]yes[/green]" if linked else "[red]no[/red]"
            display_host = str(entry.host).replace(str(Path.home()), "~")
            table.add_row(entry.slug, display_host, linked_str)

        console.print(table)

    # Daemon status
    console.print()
    if paths.PID_FILE.exists():
        try:
            pid = int(paths.PID_FILE.read_text().strip())
            os.kill(pid, 0)
            console.print(f"Daemon: [green]running[/green] (PID {pid})")
        except (ValueError, ProcessLookupError):
            console.print("Daemon: [red]not running[/red] (stale PID file)")
    else:
        console.print("Daemon: [yellow]not running[/yellow]")
