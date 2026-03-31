"""CLI entrypoint for swarf."""

from __future__ import annotations

from cyclopts import App

from swarf._version import version_string

app = App(
    name="swarf",
    help="Invisible, auto-syncing personal storage for any git repo.",
    help_format="rich",
    version=version_string(),
    version_flags=["--version", "-V"],
)

daemon = App(
    name="daemon",
    help="Manage the swarf background sync daemon.",
    help_format="rich",
)
app.command(daemon)


@app.command
def init() -> None:
    """Initialize swarf for the current project.

    Creates a project directory in the central store (~/.local/share/swarf/),
    symlinks .swarf/ to it, configures .git/info/exclude, and installs a mise
    enter hook. On first run, prompts for backend and remote, and initializes
    the store as a git repo.
    """
    from swarf.init import run_init

    run_init()


@app.command
def clone() -> None:
    """Clone the central store from your configured remote.

    Use this on a new machine after setting up ~/.config/swarf/config.toml.
    Pulls the entire store, then run 'swarf init' in each project to link.
    """
    from swarf.clone import run_clone

    run_clone()


@app.command
def pull() -> None:
    """Pull the latest changes from the remote into the store.

    Fetches updates from the configured remote. For git backends this is
    a git pull; for rclone it copies from the remote.
    """
    from swarf.pull import run_pull

    run_pull()


@app.command
def status() -> None:
    """Show sync status for the central store and all projects.

    Shows the store's backend, remote, pending changes, last sync, and
    lists all registered projects.
    """
    from swarf.status import run_status

    run_status()


@app.command
def doctor() -> None:
    """Validate swarf setup is healthy.

    Checks global config, central store, remote reachability, daemon status,
    and per-project symlinks and gitignore. Run this after setup or when
    something seems wrong.
    """
    from swarf.console import error as err
    from swarf.console import ok
    from swarf.doctor import run_all_checks

    all_ok = True
    for _, is_ok, message in run_all_checks():
        if is_ok:
            ok(message)
        else:
            err(message)
            all_ok = False

    from swarf.console import console

    if all_ok:
        console.print("\n[green]All checks passed.[/green]")
    else:
        console.print("\n[red]Some checks failed. See above for details.[/red]")
        raise SystemExit(1)


@app.command
def link(*, quiet: bool = False) -> None:
    """Create symlinks from .swarf/links/ into the host repo tree.

    This is called automatically by the mise enter hook. You rarely need
    to run it manually.

    Parameters
    ----------
    quiet
        Only show warnings, suppress normal output.
    """
    from swarf.link import run_link

    run_link(quiet=quiet)


@app.command
def enter() -> None:
    """Run on project enter (mise hook). Links files and auto-sweeps.

    Called automatically by the mise enter hook when you cd into a project.
    Runs swarf link, then sweeps any files listed in the [auto_sweep]
    section of ~/.config/swarf/config.toml.
    """
    from swarf.enter import run_enter

    run_enter()


@app.command
def sweep(paths: tuple[str, ...]) -> None:
    """Sweep files into .swarf/links/ and symlink them back.

    Moves each file into .swarf/links/ (preserving its path relative to
    the project root), replaces the original with a symlink, and updates
    .git/info/exclude so the host repo ignores it.

    Parameters
    ----------
    paths
        One or more file paths to sweep into swarf.
    """
    from swarf.sweep import run_sweep

    run_sweep(paths)


@daemon.command
def start(*, foreground: bool = False) -> None:
    """Start the background sync daemon.

    Watches the central store for changes and syncs to the configured
    backend after a debounce period.

    Parameters
    ----------
    foreground
        Run in the foreground instead of daemonizing. Useful for debugging.
    """
    import os
    import signal

    from swarf.console import info, ok
    from swarf.daemon.cli import do_start
    from swarf.paths import PID_FILE

    if PID_FILE.exists():
        try:
            pid = int(PID_FILE.read_text().strip())
            os.kill(pid, signal.SIG_DFL)
            info(f"Daemon already running (PID {pid})")
            return
        except (ValueError, ProcessLookupError):
            PID_FILE.unlink(missing_ok=True)

    do_start(foreground=foreground)
    if not foreground:
        ok("Daemon started.")


@daemon.command
def stop() -> None:
    """Stop the background sync daemon."""
    import os
    import signal

    from swarf.console import info, ok
    from swarf.paths import PID_FILE

    if not PID_FILE.exists():
        info("Daemon is not running.")
        return

    try:
        pid = int(PID_FILE.read_text().strip())
        os.kill(pid, signal.SIGTERM)
        ok(f"Sent SIGTERM to daemon (PID {pid})")
    except (ValueError, ProcessLookupError):
        info("Daemon is not running (stale PID file).")
    finally:
        PID_FILE.unlink(missing_ok=True)


@daemon.command(name="status")
def daemon_status() -> None:
    """Check if the daemon is running."""
    import os
    import signal

    from swarf.console import error as err
    from swarf.console import ok
    from swarf.paths import PID_FILE

    if not PID_FILE.exists():
        err("Daemon is not running.")
        raise SystemExit(1)

    try:
        pid = int(PID_FILE.read_text().strip())
        os.kill(pid, signal.SIG_DFL)
        ok(f"Daemon is running (PID {pid})")
    except (ValueError, ProcessLookupError):
        err("Daemon is not running (stale PID file).")
        PID_FILE.unlink(missing_ok=True)
        raise SystemExit(1) from None


@daemon.command
def install() -> None:
    """Install systemd user service for auto-start on login.

    Creates a systemd user service so the daemon starts automatically
    when you log in. Check logs with: journalctl --user -u swarf -f
    """
    from swarf.console import error as err
    from swarf.console import ok
    from swarf.daemon.cli import do_install

    try:
        do_install()
        ok("Systemd user service installed and started.")
    except RuntimeError as e:
        err(str(e))
        raise SystemExit(1) from None
    except Exception as e:
        err(f"Failed to install service: {e}")
        raise SystemExit(1) from None


def main() -> None:
    """Entrypoint for the swarf CLI."""
    app()
