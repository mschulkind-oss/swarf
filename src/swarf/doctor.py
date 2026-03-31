"""Doctor check functions for validating swarf setup."""

from __future__ import annotations

import os
import shutil
import signal
import subprocess
from pathlib import Path

from swarf.config import read_global_config
from swarf.exclude import read_managed_excludes
from swarf.git import check_ignore, git_remote_url, is_inside_work_tree
from swarf.paths import GLOBAL_CONFIG_TOML, PID_FILE


def check_swarf_dir_exists(cwd: Path | None = None) -> tuple[str, bool, str]:
    """Check that .swarf/ directory exists."""
    swarf = Path(".swarf") if cwd is None else cwd / ".swarf"
    if swarf.is_dir():
        return (".swarf/", True, ".swarf/ directory exists")
    return (".swarf/", False, ".swarf/ directory not found — run 'swarf init'")


def check_gitignore(cwd: Path | None = None) -> list[tuple[str, bool, str]]:
    """Check that required paths are gitignored.

    Checks the .git/info/exclude managed section first, then falls back to
    git check-ignore (which covers global gitignore and .gitignore).

    Returns a list of (path, ok, message) tuples.
    """
    checks: list[tuple[str, bool, str]] = []

    if not is_inside_work_tree(cwd):
        checks.append(("git", False, "Not inside a git repository"))
        return checks

    root = cwd or Path.cwd()
    managed = read_managed_excludes(root)

    required_ignored = {
        ".swarf/": ("/.swarf/", "swarf data directory must be gitignored"),
        ".mise.local.toml": ("/.mise.local.toml", "mise local config must be gitignored"),
    }

    for path, (exclude_entry, reason) in required_ignored.items():
        if exclude_entry in managed or check_ignore(path, cwd=cwd):
            checks.append((path, True, f"{path} is gitignored"))
        else:
            checks.append(
                (path, False, f"{path} is NOT gitignored — run 'swarf init' to fix, or {reason}")
            )

    # Check linked files if .swarf/links/ exists
    swarf_links = root / ".swarf" / "links"
    if swarf_links.is_dir():
        for link_source in swarf_links.rglob("*"):
            if not link_source.is_file():
                continue
            projected = str(link_source.relative_to(swarf_links))
            exclude_entry = f"/{projected}"
            if exclude_entry in managed or check_ignore(projected, cwd=cwd):
                checks.append((projected, True, f"{projected} is gitignored"))
            else:
                checks.append(
                    (
                        projected,
                        False,
                        f"{projected} is NOT gitignored — run 'swarf link' to fix",
                    )
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


def check_global_config() -> tuple[str, bool, str]:
    """Check that the global config exists and is valid."""
    config = read_global_config()
    if config is None:
        return (
            "global config",
            False,
            f"Global config not found — create {GLOBAL_CONFIG_TOML}",
        )
    if not config.remote:
        return ("global config", False, "Global config has no remote configured")
    msg = f"Global config: backend={config.backend}, remote={config.remote}"
    return ("global config", True, msg)


def check_remote_reachable() -> tuple[str, bool, str]:
    """Check that the configured remote backend is reachable."""
    config = read_global_config()
    if config is None:
        return ("remote", False, "No global config — cannot check remote")

    if config.backend == "git":
        try:
            subprocess.run(
                ["git", "ls-remote", config.remote],
                capture_output=True,
                text=True,
                check=True,
                timeout=15,
            )
            return ("remote", True, f"Git remote reachable: {config.remote}")
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError):
            return ("remote", False, f"Git remote not reachable: {config.remote}")

    if config.backend == "rclone":
        if not shutil.which("rclone"):
            return ("remote", False, "rclone not installed")
        try:
            subprocess.run(
                ["rclone", "lsd", config.remote],
                capture_output=True,
                text=True,
                check=True,
                timeout=15,
            )
            return ("remote", True, f"Rclone remote reachable: {config.remote}")
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError):
            return ("remote", False, f"Rclone remote not reachable: {config.remote}")

    return ("remote", False, f"Unknown backend: {config.backend}")


def run_all_checks(cwd: Path | None = None) -> list[tuple[str, bool, str]]:
    """Run all doctor checks and return results."""
    results: list[tuple[str, bool, str]] = []
    # Global checks
    results.append(check_global_config())
    results.append(check_remote_reachable())
    results.append(check_daemon_running())
    # Per-project checks (only if inside a swarf project)
    results.append(check_swarf_dir_exists(cwd))
    results.extend(check_gitignore(cwd))
    results.append(check_mise_local(cwd))
    results.append(check_swarf_is_git_repo(cwd))
    results.append(check_remote_configured(cwd))
    results.append(check_links_healthy(cwd))
    return results
