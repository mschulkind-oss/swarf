"""swarf init — initialize a .swarf/ directory in the current project."""

from __future__ import annotations

from pathlib import Path

import click

from swarf.config import DrawerConfig, register_drawer, write_drawer_config
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
from swarf.paths import links_dir, swarf_dir

_MISE_HOOK = "command -v swarf >/dev/null && [ -d .swarf/links ] && swarf link --quiet"

_MISE_LOCAL_TOML = f"""\
[hooks]
enter = "{_MISE_HOOK}"
"""


def run_init(
    backend: str = "git",
    remote: str | None = None,
    host_root: Path | None = None,
) -> None:
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

    # 3. Create directory structure
    sd.mkdir()
    (sd / "docs" / "research").mkdir(parents=True)
    (sd / "docs" / "design").mkdir(parents=True)
    links_dir(host_root).mkdir()
    (sd / "open-questions.md").write_text("# Open Questions\n")

    # 4. Write config
    config = DrawerConfig(
        backend=backend,
        remote=remote or "origin",
        debounce="5s",
    )
    write_drawer_config(sd, config)

    # 5. git init inside .swarf, propagate user config from host repo
    git_init(sd)
    for key in ("user.name", "user.email"):
        val = git_config_get(host_root, key)
        if val:
            git_config_set(sd, key, val)

    # 6. Add git remote if provided and backend is git
    if remote is not None and backend == "git":
        git_add_remote(sd, "origin", remote)

    # 7. Create .mise.local.toml
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

    # 8. Update .git/info/exclude
    update_excludes(host_root)

    # 9. Register drawer
    register_drawer(sd, backend)

    # 10. Initial commit
    git_add_all(sd)
    git_commit(sd, "init: swarf drawer")

    # 11. Run link (no-op if links/ is empty)
    run_link(host_root, quiet=True)

    # 12. Summary
    click.echo(f"\nInitialized swarf in {sd}")
    click.echo(f"  Backend: {backend}")
    if remote:
        click.echo(f"  Remote: {remote}")
