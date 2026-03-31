"""Shared Rich console for swarf CLI output."""

from __future__ import annotations

from rich.console import Console

console = Console()
err_console = Console(stderr=True)


def ok(msg: str) -> None:
    """Print a success message."""
    console.print(f"[green]✓[/green] {msg}")


def warn(msg: str) -> None:
    """Print a warning message."""
    err_console.print(f"[yellow]![/yellow] {msg}")


def error(msg: str) -> None:
    """Print an error message."""
    err_console.print(f"[red]✗[/red] {msg}")


def info(msg: str) -> None:
    """Print an info message."""
    console.print(msg)
