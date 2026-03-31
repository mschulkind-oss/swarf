"""swarf link — project .swarf/links/ into the host repo tree via symlinks."""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path

import click

from swarf.exclude import add_linked_excludes
from swarf.paths import find_host_root, links_dir


@dataclass
class LinkResult:
    """Result of a link operation."""

    created: list[Path] = field(default_factory=list)
    skipped: list[Path] = field(default_factory=list)
    warnings: list[str] = field(default_factory=list)


def run_link(host_root: Path | None = None, quiet: bool = False) -> LinkResult:
    """Create symlinks from .swarf/links/ into the host repo tree.

    Returns a LinkResult with details of what happened.
    """
    if host_root is None:
        host_root = find_host_root()
    if host_root is None:
        click.echo(
            click.style("Error:", fg="red")
            + " Not inside a swarf project. Run 'swarf init' first.",
            err=True,
        )
        raise SystemExit(1)

    ld = links_dir(host_root)
    result = LinkResult()

    if not ld.is_dir() or not any(ld.iterdir()):
        return result

    for source in ld.rglob("*"):
        if not source.is_file():
            continue

        relative = source.relative_to(ld)
        target = host_root / relative

        if target.is_symlink():
            if target.resolve() == source.resolve():
                # Already correct
                result.skipped.append(relative)
                continue
            # Stale symlink — remove and recreate
            target.unlink()
        elif target.exists():
            # Real file exists — warn, don't overwrite
            msg = f"{relative}: real file exists, skipping (won't overwrite)"
            result.warnings.append(msg)
            if not quiet:
                click.echo(click.style("Warning:", fg="yellow") + f" {msg}")
            continue

        # Create parent dirs and symlink
        target.parent.mkdir(parents=True, exist_ok=True)
        target.symlink_to(source)
        result.created.append(relative)
        if not quiet:
            click.echo(f"  linked {relative}")

    # Always show warnings, even in quiet mode
    if quiet:
        for msg in result.warnings:
            click.echo(click.style("Warning:", fg="yellow") + f" {msg}")

    # Update .git/info/exclude so linked files are ignored by the host repo
    all_linked = [str(p) for p in result.created + result.skipped]
    if all_linked:
        add_linked_excludes(host_root, all_linked)

    return result
