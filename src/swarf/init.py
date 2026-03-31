"""swarf init — initialize a .swarf/ directory in the current project."""

from __future__ import annotations

import os
import signal
import sys
from pathlib import Path

import click

from swarf.config import (
    DrawerConfig,
    GlobalConfig,
    read_global_config,
    register_drawer,
    write_drawer_config,
    write_global_config,
)
from swarf.exclude import update_excludes
from swarf.git import (
    get_repo_root,
    git_add_all,
    git_add_remote,
    git_commit,
    git_config_get,
    git_config_set,
    git_init,
)
from swarf.link import run_link
from swarf.paths import PID_FILE, links_dir, swarf_dir

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

    click.echo("\nNo global config found. Let's set one up.\n")
    backend = click.prompt(
        "Backend",
        type=click.Choice(["git", "rclone"]),
        default="git",
    )
    remote = click.prompt("Remote URL")
    config = GlobalConfig(backend=backend, remote=remote)
    write_global_config(config)
    click.echo(click.style("✓", fg="green") + f" Wrote {config_path()}\n")
    return config


def config_path() -> Path:
    """Return the global config path (for display)."""
    from swarf.paths import GLOBAL_CONFIG_TOML

    return GLOBAL_CONFIG_TOML


def _daemon_is_running() -> bool:
    """Check if the daemon is running."""
    if not PID_FILE.exists():
        return False
    try:
        pid = int(PID_FILE.read_text().strip())
        os.kill(pid, signal.SIG_DFL)
        return True
    except (ValueError, ProcessLookupError, PermissionError):
        return False


def _offer_daemon_install() -> None:
    """Offer to install and start the daemon if not running."""
    if _daemon_is_running():
        click.echo(click.style("✓", fg="green") + " Daemon is running.")
        return

    install = click.confirm(
        "\nDaemon is not running. Install as system service?",
        default=True,
    )
    if not install:
        click.echo("Skipped. Start manually with: swarf daemon start")
        return

    from swarf.daemon.cli import do_install, do_start

    do_install()
    do_start(foreground=False)
    click.echo(click.style("✓", fg="green") + " Installed and started swarf daemon.")


def run_init(host_root: Path | None = None) -> None:
    """Initialize a .swarf/ directory in the current project."""
    # 1. Resolve host root
    if host_root is None:
        host_root = get_repo_root()
    if host_root is None:
        click.echo(click.style("Error:", fg="red") + " Not inside a git repository.", err=True)
        raise SystemExit(1)

    sd = swarf_dir(host_root)

    # 2. Abort if already initialized
    if sd.is_dir():
        click.echo(
            click.style("Error:", fg="red") + " swarf is already initialized here.",
            err=True,
        )
        raise SystemExit(1)

    # 3. Ensure global config exists (prompt if not)
    global_config = _ensure_global_config()
    backend = global_config.backend
    remote = global_config.remote

    # 4. Create directory structure
    sd.mkdir()
    (sd / "docs" / "research").mkdir(parents=True)
    (sd / "docs" / "design").mkdir(parents=True)
    links_dir(host_root).mkdir()
    (sd / "open-questions.md").write_text("# Open Questions\n")

    # 5. Write per-drawer config
    config = DrawerConfig(
        backend=backend,
        remote=remote,
        debounce=global_config.debounce,
    )
    write_drawer_config(sd, config)

    # 6. git init inside .swarf, propagate user config from host repo
    git_init(sd)
    for key in ("user.name", "user.email"):
        val = git_config_get(host_root, key)
        if val:
            git_config_set(sd, key, val)

    # 7. Add git remote if backend is git and remote is set
    if remote and backend == "git":
        git_add_remote(sd, "origin", remote)

    # 8. Create .mise.local.toml
    mise_local = host_root / ".mise.local.toml"
    if mise_local.exists():
        click.echo(
            click.style("Warning:", fg="yellow")
            + " .mise.local.toml already exists. Add this hook manually:"
        )
        click.echo(f'  [hooks]\n  enter = "{_MISE_HOOK}"')
    else:
        mise_local.write_text(_MISE_LOCAL_TOML)
        click.echo("Created .mise.local.toml with enter hook.")

    # 9. Update .git/info/exclude
    update_excludes(host_root)

    # 10. Register drawer
    register_drawer(sd, backend)

    # 11. Initial commit
    git_add_all(sd)
    git_commit(sd, "init: swarf drawer")

    # 12. Run link (no-op if links/ is empty)
    run_link(host_root, quiet=True)

    # 13. Summary
    click.echo(f"\n{click.style('✓', fg='green')} Initialized swarf in {sd}")
    click.echo(f"  Backend: {backend}")
    if remote:
        click.echo(f"  Remote: {remote}")

    # 14. Offer to install/start daemon (only if interactive terminal)
    if sys.stdin.isatty():
        _offer_daemon_install()
