"""swarf init — initialize swarf for the current project."""

from __future__ import annotations

import os
import signal
import sys
from pathlib import Path

from rich.prompt import Confirm, Prompt

import swarf.paths as paths
from swarf.config import (
    GlobalConfig,
    read_global_config,
    register_drawer,
    write_global_config,
)
from swarf.console import error, info, ok, warn
from swarf.exclude import update_excludes
from swarf.git import (
    get_repo_root,
    git_add_all,
    git_add_remote,
    git_commit,
    git_config_get,
    git_config_set,
    git_init,
    git_is_repo,
)
from swarf.paths import project_slug, store_project_dir, swarf_dir

_MISE_HOOK = "command -v swarf >/dev/null && [ -d .swarf/links ] && swarf enter"

_MISE_LOCAL_TOML = f"""\
[hooks]
enter = "{_MISE_HOOK}"
"""


def _ensure_global_config() -> GlobalConfig:
    """Read global config, prompting the user to create it if missing."""
    config = read_global_config()
    if config is not None:
        return config

    info("\nNo global config found. Let's set one up.\n")
    backend = Prompt.ask("Backend", choices=["git", "rclone"], default="git")
    remote = Prompt.ask("Remote URL")
    config = GlobalConfig(backend=backend, remote=remote)
    write_global_config(config)
    ok(f"Wrote {paths.GLOBAL_CONFIG_TOML}\n")
    return config


def _daemon_is_running() -> bool:
    """Check if the daemon is running."""
    if not paths.PID_FILE.exists():
        return False
    try:
        pid = int(paths.PID_FILE.read_text().strip())
        os.kill(pid, signal.SIG_DFL)
        return True
    except (ValueError, ProcessLookupError, PermissionError):
        return False


def _offer_daemon_install() -> None:
    """Offer to install and start the daemon if not running."""
    if _daemon_is_running():
        ok("Daemon is running.")
        return

    if not Confirm.ask("\nDaemon is not running. Install as user service?", default=True):
        info("Skipped. Start manually with: swarf daemon start")
        return

    from swarf.daemon.cli import do_install, do_start

    do_install()
    do_start(foreground=False)
    ok("Installed and started swarf daemon.")


def _ensure_store(host_root: Path, global_config: GlobalConfig) -> None:
    """Initialize the central store if it doesn't exist yet."""
    if paths.STORE_DIR.is_dir() and git_is_repo(paths.STORE_DIR):
        return

    paths.STORE_DIR.mkdir(parents=True, exist_ok=True)
    git_init(paths.STORE_DIR)

    # Propagate git user config from host repo
    for key in ("user.name", "user.email"):
        val = git_config_get(host_root, key)
        if val:
            git_config_set(paths.STORE_DIR, key, val)

    # Add remote if git backend
    if global_config.backend == "git" and global_config.remote:
        git_add_remote(paths.STORE_DIR, "origin", global_config.remote)

    ok(f"Created central store at {paths.STORE_DIR}")


def run_init(host_root: Path | None = None) -> None:
    """Initialize swarf for the current project."""
    # 1. Resolve host root
    if host_root is None:
        host_root = get_repo_root()
    if host_root is None:
        error("Not inside a git repository.")
        raise SystemExit(1)

    sd = swarf_dir(host_root)
    slug = project_slug(host_root)
    proj_dir = store_project_dir(host_root)

    # 2. Abort if already initialized
    if sd.is_symlink() or sd.is_dir():
        error("swarf is already initialized here.")
        raise SystemExit(1)

    # 3. Ensure global config exists (prompt if not)
    global_config = _ensure_global_config()

    # 4. Ensure central store exists
    _ensure_store(host_root, global_config)

    # 5. Create project directory in store (with just links/)
    if proj_dir.is_dir():
        # Already exists in store (e.g., from a clone) — just link
        ok(f"Found existing project '{slug}' in store.")
    else:
        proj_dir.mkdir(parents=True)
        (proj_dir / "links").mkdir()
        (proj_dir / "links" / ".gitkeep").write_text("")

    # 6. Symlink .swarf -> store project dir
    sd.symlink_to(proj_dir)
    ok(f"Linked .swarf → {proj_dir}")

    # 7. Create .mise.local.toml
    mise_local = host_root / ".mise.local.toml"
    if mise_local.exists():
        warn(".mise.local.toml already exists. Add this hook manually:")
        info(f'  [hooks]\n  enter = "{_MISE_HOOK}"')
    else:
        mise_local.write_text(_MISE_LOCAL_TOML)
        ok("Created .mise.local.toml with enter hook.")

    # 8. Update .git/info/exclude
    update_excludes(host_root)

    # 9. Register drawer
    register_drawer(slug, host_root)

    # 10. Commit to store (if there's anything new)
    git_add_all(paths.STORE_DIR)
    import contextlib

    with contextlib.suppress(Exception):
        git_commit(paths.STORE_DIR, f"init: {slug}")

    # 11. Summary
    ok(f"Initialized swarf for {slug}")
    info(f"  Backend: {global_config.backend}")
    if global_config.remote:
        info(f"  Remote: {global_config.remote}")

    # 12. Offer to install/start daemon (only if interactive terminal)
    if sys.stdin.isatty():
        _offer_daemon_install()
