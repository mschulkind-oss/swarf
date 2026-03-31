"""Path constants and helpers for swarf."""

from __future__ import annotations

import os
from pathlib import Path

SWARF_DIR_NAME = ".swarf"

# XDG-compliant paths
CONFIG_DIR = Path(os.environ.get("XDG_CONFIG_HOME", "~/.config")).expanduser() / "swarf"
STORE_DIR = Path(os.environ.get("XDG_DATA_HOME", "~/.local/share")).expanduser() / "swarf"
GLOBAL_CONFIG_TOML = CONFIG_DIR / "config.toml"
DRAWERS_TOML = CONFIG_DIR / "drawers.toml"
PID_FILE = CONFIG_DIR / "daemon.pid"
LOG_FILE = CONFIG_DIR / "daemon.log"


def swarf_dir(host_root: Path) -> Path:
    """Return the .swarf directory for a given host root."""
    return host_root / SWARF_DIR_NAME


def links_dir(host_root: Path) -> Path:
    """Return the path to the links directory."""
    return swarf_dir(host_root) / "links"


def project_slug(host_root: Path) -> str:
    """Derive a project slug from the host repo root directory name."""
    return host_root.resolve().name


def store_project_dir(host_root: Path) -> Path:
    """Return the project's directory inside the central store."""
    return STORE_DIR / project_slug(host_root)


def find_host_root(start: Path | None = None) -> Path | None:
    """Walk up from start looking for a .swarf/ directory or symlink.

    Returns the parent of .swarf/ (the host root), or None if not found.
    """
    current = (start or Path.cwd()).resolve()
    while True:
        candidate = current / SWARF_DIR_NAME
        if candidate.is_dir() or candidate.is_symlink():
            return current
        parent = current.parent
        if parent == current:
            return None
        current = parent
