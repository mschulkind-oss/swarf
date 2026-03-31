"""swarf enter — mise hook that runs link + auto-sweep."""

from __future__ import annotations

from pathlib import Path

from swarf.config import read_global_config
from swarf.link import run_link
from swarf.paths import find_host_root
from swarf.sweep import run_sweep


def run_enter(host_root: Path | None = None) -> None:
    """Run on project enter: link files, then auto-sweep configured paths."""
    if host_root is None:
        host_root = find_host_root()
    if host_root is None:
        return  # silently — mise hook shouldn't be noisy

    # 1. Link any new files from .swarf/links/
    run_link(host_root, quiet=True)

    # 2. Auto-sweep configured paths
    global_config = read_global_config()
    if global_config is None or not global_config.auto_sweep:
        return

    to_sweep: list[str] = []
    for path_str in global_config.auto_sweep:
        target = host_root / path_str
        if target.exists() and not target.is_symlink():
            to_sweep.append(path_str)

    if to_sweep:
        run_sweep(tuple(to_sweep), host_root=host_root)
