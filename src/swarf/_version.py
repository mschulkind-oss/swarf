"""Version info including git hash and dirty status."""

from __future__ import annotations

import subprocess

from swarf import __version__


def _git_info() -> tuple[str, bool] | None:
    """Get (short_hash, is_dirty) from git, or None if unavailable."""
    try:
        rev = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if rev.returncode != 0:
            return None
        short_hash = rev.stdout.strip()
        dirty = subprocess.run(
            ["git", "diff", "--quiet", "HEAD"],
            capture_output=True,
            timeout=5,
        )
        return (short_hash, dirty.returncode != 0)
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None


def version_string() -> str:
    """Return version string like '0.1.0 (abc1234, dirty)'."""
    parts = [__version__]
    info = _git_info()
    if info:
        short_hash, is_dirty = info
        tag = f"{short_hash}, dirty" if is_dirty else short_hash
        parts.append(f"({tag})")
    return " ".join(parts)
