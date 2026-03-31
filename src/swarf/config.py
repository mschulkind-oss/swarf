"""Config read/write for global config and drawer registry."""

from __future__ import annotations

import re
import tomllib
from dataclasses import dataclass
from pathlib import Path

import tomli_w

from swarf.paths import CONFIG_DIR, DRAWERS_TOML, GLOBAL_CONFIG_TOML


@dataclass
class GlobalConfig:
    """Global user configuration stored in ~/.config/swarf/config.toml."""

    backend: str = "git"  # "git" | "rclone"
    remote: str = ""
    debounce: str = "5s"
    auto_sweep: list[str] | None = None


def read_global_config() -> GlobalConfig | None:
    """Read the global config.toml. Returns None if it doesn't exist."""
    if not GLOBAL_CONFIG_TOML.exists():
        return None
    with open(GLOBAL_CONFIG_TOML, "rb") as f:
        data = tomllib.load(f)
    sync = data.get("sync", {})
    auto = data.get("auto_sweep", {})
    return GlobalConfig(
        backend=sync.get("backend", "git"),
        remote=sync.get("remote", ""),
        debounce=sync.get("debounce", "5s"),
        auto_sweep=auto.get("paths"),
    )


def write_global_config(config: GlobalConfig) -> None:
    """Write the global config.toml."""
    CONFIG_DIR.mkdir(parents=True, exist_ok=True)
    data: dict = {
        "sync": {
            "backend": config.backend,
            "remote": config.remote,
            "debounce": config.debounce,
        },
    }
    if config.auto_sweep:
        data["auto_sweep"] = {"paths": config.auto_sweep}
    with open(GLOBAL_CONFIG_TOML, "wb") as f:
        tomli_w.dump(data, f)


@dataclass
class DrawerEntry:
    """Entry in the global drawers.toml registry."""

    slug: str
    host: Path


def read_drawers() -> list[DrawerEntry]:
    """Read the global drawers.toml registry."""
    if not DRAWERS_TOML.exists():
        return []
    with open(DRAWERS_TOML, "rb") as f:
        data = tomllib.load(f)
    entries = []
    for d in data.get("drawers", []):
        entries.append(DrawerEntry(slug=d["slug"], host=Path(d["host"])))
    return entries


def register_drawer(slug: str, host: Path) -> None:
    """Register a drawer in the global drawers.toml."""
    CONFIG_DIR.mkdir(parents=True, exist_ok=True)
    drawers = read_drawers()
    resolved = host.resolve()
    for d in drawers:
        if d.slug == slug:
            d.host = resolved
            _write_drawers(drawers)
            return
    drawers.append(DrawerEntry(slug=slug, host=resolved))
    _write_drawers(drawers)


def unregister_drawer(slug: str) -> None:
    """Remove a drawer from the global drawers.toml."""
    drawers = read_drawers()
    drawers = [d for d in drawers if d.slug != slug]
    _write_drawers(drawers)


def _write_drawers(drawers: list[DrawerEntry]) -> None:
    """Write the global drawers.toml."""
    CONFIG_DIR.mkdir(parents=True, exist_ok=True)
    data = {"drawers": [{"slug": d.slug, "host": str(d.host)} for d in drawers]}
    with open(DRAWERS_TOML, "wb") as f:
        tomli_w.dump(data, f)


_DURATION_RE = re.compile(r"^(\d+(?:\.\d+)?)\s*(ms|s|m|h)$")


def parse_duration(s: str) -> float:
    """Parse a duration string like '5s', '1m', '500ms' to seconds.

    Raises ValueError if the format is invalid.
    """
    m = _DURATION_RE.match(s.strip())
    if not m:
        msg = f"Invalid duration format: {s!r}. Expected e.g. '5s', '1m', '500ms', '2h'."
        raise ValueError(msg)
    value = float(m.group(1))
    unit = m.group(2)
    multipliers = {"ms": 0.001, "s": 1.0, "m": 60.0, "h": 3600.0}
    return value * multipliers[unit]
