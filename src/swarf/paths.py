"""Path constants and helpers for swarf."""

from __future__ import annotations

from pathlib import Path

SWARF_DIR_NAME = ".swarf"
CONFIG_DIR = Path("~/.config/swarf").expanduser()
GLOBAL_CONFIG_TOML = CONFIG_DIR / "config.toml"
DRAWERS_TOML = CONFIG_DIR / "drawers.toml"
PID_FILE = CONFIG_DIR / "daemon.pid"
LOG_FILE = CONFIG_DIR / "daemon.log"


def swarf_dir(host_root: Path) -> Path:
    """Return the .swarf directory for a given host root."""
    return host_root / SWARF_DIR_NAME


def config_toml(host_root: Path) -> Path:
    """Return the path to the per-drawer config.toml."""
    return swarf_dir(host_root) / "config.toml"


def links_dir(host_root: Path) -> Path:
    """Return the path to the links directory."""
    return swarf_dir(host_root) / "links"


def find_host_root(start: Path | None = None) -> Path | None:
    """Walk up from start looking for a .swarf/ directory.

    Returns the parent of .swarf/ (the host root), or None if not found.
    """
    current = (start or Path.cwd()).resolve()
    while True:
        if (current / SWARF_DIR_NAME).is_dir():
            return current
        parent = current.parent
        if parent == current:
            return None
        current = parent
