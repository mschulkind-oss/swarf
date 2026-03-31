"""Test helpers for invoking swarf CLI commands."""

from __future__ import annotations

import io
from dataclasses import dataclass
from unittest.mock import patch

from rich.console import Console

from swarf.cli import app


@dataclass
class CliResult:
    """Result of invoking a CLI command in tests."""

    output: str
    exit_code: int


def invoke(args: list[str] | str) -> CliResult:
    """Invoke the swarf CLI with the given args and capture output.

    Returns a CliResult with captured output and exit code.
    """
    buf = io.StringIO()
    console = Console(file=buf, force_terminal=False, no_color=True)
    exit_code = 0
    with (
        patch("swarf.console.console", console),
        patch("swarf.console.err_console", console),
    ):
        try:
            app(
                args,
                console=console,
                error_console=console,
                exit_on_error=False,
            )
        except SystemExit as e:
            exit_code = e.code if isinstance(e.code, int) else 1
        except Exception:
            exit_code = 1
    output = buf.getvalue()
    return CliResult(output=output, exit_code=exit_code)
