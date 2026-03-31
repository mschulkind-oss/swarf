"""CLI entrypoint for swarf."""

from __future__ import annotations

import subprocess
from pathlib import Path

import click


@click.group()
@click.version_option()
def main() -> None:
    """Invisible, auto-syncing personal storage for any git repo."""


@main.command()
def init() -> None:
    """Initialize a .swarf/ directory in the current project."""
    click.echo("swarf init — not yet implemented")


@main.command()
def status() -> None:
    """Show sync status across all registered swarf directories."""
    click.echo("swarf status — not yet implemented")


def _is_inside_git_repo() -> bool:
    """Check if we're inside a git working tree."""
    result = subprocess.run(
        ["git", "rev-parse", "--is-inside-work-tree"],
        capture_output=True,
        text=True,
    )
    return result.returncode == 0


def _is_gitignored(path: str) -> bool:
    """Check if a path would be ignored by git."""
    result = subprocess.run(
        ["git", "check-ignore", "-q", path],
        capture_output=True,
    )
    return result.returncode == 0


def _check_gitignore() -> list[tuple[str, bool, str]]:
    """Check that required paths are gitignored.

    Returns a list of (path, ok, message) tuples.
    """
    checks: list[tuple[str, bool, str]] = []

    if not _is_inside_git_repo():
        checks.append(("git", False, "Not inside a git repository"))
        return checks

    # Paths that must be gitignored for swarf to work correctly
    required_ignored = {
        ".swarf/": "swarf data directory must be gitignored",
        ".mise.local.toml": "mise local config (used by swarf enter hook) must be gitignored",
    }

    for path, reason in required_ignored.items():
        ignored = _is_gitignored(path)
        if ignored:
            checks.append((path, True, f"{path} is gitignored"))
        else:
            checks.append((path, False, f"{path} is NOT gitignored — {reason}"))

    # Check linked files if .swarf/links/ exists
    swarf_links = Path(".swarf/links")
    if swarf_links.is_dir():
        for link_source in swarf_links.rglob("*"):
            if not link_source.is_file():
                continue
            # The projected path in the host tree
            projected = str(link_source.relative_to(swarf_links))
            ignored = _is_gitignored(projected)
            if ignored:
                checks.append((projected, True, f"{projected} is gitignored"))
            else:
                checks.append(
                    (projected, False, f"{projected} is NOT gitignored — linked from .swarf/links/")
                )

    return checks


@main.command()
def doctor() -> None:
    """Validate swarf setup is healthy."""
    swarf_dir = Path(".swarf")
    all_ok = True

    # Check .swarf/ exists
    if swarf_dir.is_dir():
        click.echo(click.style("✓", fg="green") + " .swarf/ directory exists")
    else:
        click.echo(click.style("✗", fg="red") + " .swarf/ directory not found — run 'swarf init'")
        all_ok = False

    # Check gitignore rules
    gitignore_results = _check_gitignore()
    for _, ok, message in gitignore_results:
        if ok:
            click.echo(click.style("✓", fg="green") + f" {message}")
        else:
            click.echo(click.style("✗", fg="red") + f" {message}")
            all_ok = False

    if all_ok:
        click.echo("\nAll checks passed.")
    else:
        click.echo("\nSome checks failed. See above for details.")
        raise SystemExit(1)


@main.command()
def link() -> None:
    """Project .swarf/links/ into the host repo tree via symlinks."""
    click.echo("swarf link — not yet implemented")
