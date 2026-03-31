"""Doctor check functions for validating swarf setup."""

from __future__ import annotations

import os
import signal
from pathlib import Path

from swarf.git import check_ignore, git_remote_url, is_inside_work_tree
from swarf.paths import PID_FILE


def check_swarf_dir_exists(cwd: Path | None = None) -> tuple[str, bool, str]:
    """Check that .swarf/ directory exists."""
    swarf = Path(".swarf") if cwd is None else cwd / ".swarf"
    if swarf.is_dir():
        return (".swarf/", True, ".swarf/ directory exists")
    return (".swarf/", False, ".swarf/ directory not found — run 'swarf init'")


def check_gitignore(cwd: Path | None = None) -> list[tuple[str, bool, str]]:
    """Check that required paths are gitignored.

    Returns a list of (path, ok, message) tuples.
    """
    checks: list[tuple[str, bool, str]] = []

    if not is_inside_work_tree(cwd):
        checks.append(("git", False, "Not inside a git repository"))
        return checks

    required_ignored = {
        ".swarf/": "swarf data directory must be gitignored",
        ".mise.local.toml": "mise local config (used by swarf enter hook) must be gitignored",
    }

    for path, reason in required_ignored.items():
        ignored = check_ignore(path, cwd=cwd)
        if ignored:
            checks.append((path, True, f"{path} is gitignored"))
        else:
            checks.append((path, False, f"{path} is NOT gitignored — {reason}"))

    # Check linked files if .swarf/links/ exists
    swarf_links = Path(".swarf/links") if cwd is None else cwd / ".swarf" / "links"
    if swarf_links.is_dir():
        for link_source in swarf_links.rglob("*"):
            if not link_source.is_file():
                continue
            projected = str(link_source.relative_to(swarf_links))
            ignored = check_ignore(projected, cwd=cwd)
            if ignored:
                checks.append((projected, True, f"{projected} is gitignored"))
            else:
                checks.append(
                    (projected, False, f"{projected} is NOT gitignored — linked from .swarf/links/")
                )

    return checks


def check_mise_local(cwd: Path | None = None) -> tuple[str, bool, str]:
    """Check that .mise.local.toml exists with an enter hook."""
    mise = Path(".mise.local.toml") if cwd is None else cwd / ".mise.local.toml"
    if not mise.exists():
        return (".mise.local.toml", False, ".mise.local.toml not found — run 'swarf init'")
    content = mise.read_text()
    if "swarf link" in content:
        return (".mise.local.toml", True, ".mise.local.toml has swarf enter hook")
    return (".mise.local.toml", False, ".mise.local.toml missing swarf enter hook")


def check_swarf_is_git_repo(cwd: Path | None = None) -> tuple[str, bool, str]:
    """Check that .swarf/ is its own git repository (has .git/)."""
    swarf = Path(".swarf") if cwd is None else cwd / ".swarf"
    if not swarf.is_dir():
        return (".swarf git", False, ".swarf/ does not exist")
    if (swarf / ".git").is_dir():
        return (".swarf git", True, ".swarf/ is a git repository")
    return (".swarf git", False, ".swarf/ is not a git repository")


def check_remote_configured(cwd: Path | None = None) -> tuple[str, bool, str]:
    """Check that a remote is configured in .swarf/."""
    swarf = Path(".swarf") if cwd is None else cwd / ".swarf"
    if not swarf.is_dir():
        return ("remote", False, ".swarf/ does not exist")
    url = git_remote_url(swarf)
    if url:
        return ("remote", True, f"Remote configured: {url}")
    return ("remote", False, "No remote configured in .swarf/ — add one with 'git remote add'")


def check_daemon_running() -> tuple[str, bool, str]:
    """Check if the daemon is running via PID file."""
    if not PID_FILE.exists():
        return ("daemon", False, "Daemon is not running (no PID file)")
    try:
        pid = int(PID_FILE.read_text().strip())
        os.kill(pid, signal.SIG_DFL)
        return ("daemon", True, f"Daemon is running (PID {pid})")
    except (ValueError, ProcessLookupError, PermissionError):
        return ("daemon", False, "Daemon is not running (stale PID file)")


def check_links_healthy(cwd: Path | None = None) -> tuple[str, bool, str]:
    """Check that all symlinks pointing into .swarf/ are healthy."""
    swarf_links = Path(".swarf/links") if cwd is None else cwd / ".swarf" / "links"
    if not swarf_links.is_dir():
        return ("links", True, "No links directory")

    broken = []
    for source in swarf_links.rglob("*"):
        if not source.is_file():
            continue
        relative = source.relative_to(swarf_links)
        root = cwd or Path.cwd()
        target = root / relative
        if target.is_symlink() and not target.exists():
            broken.append(str(relative))

    if broken:
        return ("links", False, f"Broken symlinks: {', '.join(broken)}")
    return ("links", True, "All links healthy")


def run_all_checks(cwd: Path | None = None) -> list[tuple[str, bool, str]]:
    """Run all doctor checks and return results."""
    results: list[tuple[str, bool, str]] = []
    results.append(check_swarf_dir_exists(cwd))
    results.extend(check_gitignore(cwd))
    results.append(check_mise_local(cwd))
    results.append(check_swarf_is_git_repo(cwd))
    results.append(check_remote_configured(cwd))
    results.append(check_daemon_running())
    results.append(check_links_healthy(cwd))
    return results
