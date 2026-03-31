"""swarf sweep — move a file into .swarf/links/ and symlink it back."""

from __future__ import annotations

import shutil
from pathlib import Path

from swarf.console import error, info, warn
from swarf.exclude import add_linked_excludes
from swarf.paths import find_host_root, links_dir


def run_sweep(paths: tuple[str, ...] | list[str], host_root: Path | None = None) -> None:
    """Move files into .swarf/links/ and replace with symlinks."""
    if host_root is None:
        host_root = find_host_root()
    if host_root is None:
        error("Not inside a swarf project. Run 'swarf init' first.")
        raise SystemExit(1)

    ld = links_dir(host_root)
    if not ld.is_dir():
        error(".swarf/links/ does not exist. Run 'swarf init' first.")
        raise SystemExit(1)

    swept: list[str] = []

    for path_str in paths:
        source = Path(path_str)
        if not source.is_absolute():
            source = Path.cwd() / source

        # Check symlink before resolving
        if source.is_symlink():
            relative = source.relative_to(host_root) if source.is_relative_to(host_root) else source
            warn(f"{relative} is already a symlink, skipping.")
            continue

        # Check if inside .swarf/ BEFORE resolving (resolving follows symlinks)
        try:
            raw_relative = source.relative_to(host_root)
            if ".swarf" in raw_relative.parts:
                error(f"{path_str} is already inside .swarf/.")
                continue
        except ValueError:
            pass

        source = source.resolve()

        # Must be inside the host root
        try:
            relative = source.relative_to(host_root)
        except ValueError:
            error(f"{path_str} is not inside the project root.")
            continue

        if not source.exists():
            error(f"{path_str} does not exist.")
            continue

        # Destination in .swarf/links/
        dest = ld / relative
        dest.parent.mkdir(parents=True, exist_ok=True)

        if dest.exists():
            warn(f"{relative} already exists in .swarf/links/, skipping.")
            continue

        # Move file, create symlink
        shutil.move(str(source), str(dest))
        source.symlink_to(dest)
        swept.append(str(relative))
        info(f"  swept {relative}")

    # Update .git/info/exclude for all swept files
    if swept:
        add_linked_excludes(host_root, swept)
