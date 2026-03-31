"""Manage the swarf section in .git/info/exclude."""

from __future__ import annotations

from pathlib import Path

_FENCE_START = "# --- swarf managed (do not edit) ---"
_FENCE_END = "# --- end swarf ---"

# Base paths that swarf always needs ignored
_BASE_EXCLUDES = ["/.swarf/", "/.mise.local.toml"]


def _exclude_file(host_root: Path) -> Path:
    """Return the path to .git/info/exclude."""
    return host_root / ".git" / "info" / "exclude"


def read_managed_excludes(host_root: Path) -> list[str]:
    """Read the swarf-managed entries from .git/info/exclude."""
    path = _exclude_file(host_root)
    if not path.exists():
        return []

    content = path.read_text()
    in_fence = False
    entries = []
    for line in content.splitlines():
        if line.strip() == _FENCE_START:
            in_fence = True
            continue
        if line.strip() == _FENCE_END:
            in_fence = False
            continue
        if in_fence and line.strip() and not line.startswith("#"):
            entries.append(line.strip())
    return entries


def write_managed_excludes(host_root: Path, entries: list[str]) -> None:
    """Write the swarf-managed section in .git/info/exclude.

    Preserves all user content outside the fenced section.
    """
    path = _exclude_file(host_root)
    path.parent.mkdir(parents=True, exist_ok=True)

    # Read existing content, stripping out the old fenced section
    existing_lines: list[str] = []
    if path.exists():
        in_fence = False
        for line in path.read_text().splitlines():
            if line.strip() == _FENCE_START:
                in_fence = True
                continue
            if line.strip() == _FENCE_END:
                in_fence = False
                continue
            if not in_fence:
                existing_lines.append(line)

    # Remove trailing blank lines before we append
    while existing_lines and not existing_lines[-1].strip():
        existing_lines.pop()

    # Build new content
    parts = existing_lines[:]
    if parts:
        parts.append("")  # blank line separator
    parts.append(_FENCE_START)
    for entry in sorted(set(entries)):
        parts.append(entry)
    parts.append(_FENCE_END)
    parts.append("")  # trailing newline

    path.write_text("\n".join(parts))


def update_excludes(host_root: Path, extra: list[str] | None = None) -> None:
    """Update the swarf section with base excludes + any extra paths.

    Merges with existing managed entries so nothing is lost.
    """
    current = read_managed_excludes(host_root)
    entries = list(set(_BASE_EXCLUDES + current + (extra or [])))
    write_managed_excludes(host_root, entries)


def add_linked_excludes(host_root: Path, linked_paths: list[str]) -> None:
    """Add linked file paths to the managed excludes."""
    if not linked_paths:
        return
    # Prefix with / to anchor to repo root
    extra = [f"/{p}" if not p.startswith("/") else p for p in linked_paths]
    update_excludes(host_root, extra)
