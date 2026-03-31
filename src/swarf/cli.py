"""CLI entrypoint for swarf."""

from __future__ import annotations

import click


@click.group()
@click.version_option()
def main() -> None:
    """Invisible, auto-syncing personal storage for any git repo."""


@main.command()
def init() -> None:
    """Initialize a .swarf/ directory in the current project."""
    from swarf.init import run_init

    run_init()


@main.command()
def status() -> None:
    """Show sync status across all registered swarf directories."""
    from swarf.status import run_status

    run_status()


@main.command()
def doctor() -> None:
    """Validate swarf setup is healthy."""
    from swarf.doctor import run_all_checks

    all_ok = True
    for _, ok, message in run_all_checks():
        if ok:
            click.echo(click.style("\u2713", fg="green") + f" {message}")
        else:
            click.echo(click.style("\u2717", fg="red") + f" {message}")
            all_ok = False

    if all_ok:
        click.echo("\nAll checks passed.")
    else:
        click.echo("\nSome checks failed. See above for details.")
        raise SystemExit(1)


def _register_daemon() -> None:
    from swarf.daemon.cli import daemon

    main.add_command(daemon)


_register_daemon()


@main.command()
@click.option("--quiet", "-q", is_flag=True, help="Only show warnings.")
def link(quiet: bool) -> None:
    """Project .swarf/links/ into the host repo tree via symlinks."""
    from swarf.link import run_link

    run_link(quiet=quiet)


@main.command()
def enter() -> None:
    """Run on project enter (mise hook). Links files and auto-sweeps."""
    from swarf.enter import run_enter

    run_enter()


@main.command()
@click.argument("paths", nargs=-1, required=True)
def sweep(paths: tuple[str, ...]) -> None:
    """Sweep files into .swarf/links/ and symlink them back.

    Moves each file into .swarf/links/ (preserving its path relative to the
    project root), replaces the original with a symlink, and updates
    .git/info/exclude so the host repo ignores it.
    """
    from swarf.sweep import run_sweep

    run_sweep(paths)
