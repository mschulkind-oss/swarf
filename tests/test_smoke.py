"""Smoke tests to verify the package loads and CLI runs."""

from helpers import invoke


def test_cli_help():
    result = invoke(["--help"])
    assert result.exit_code == 0
    assert "auto-syncing" in result.output


def test_cli_version():
    result = invoke(["--version"])
    assert result.exit_code == 0
    assert "0.1.0" in result.output
