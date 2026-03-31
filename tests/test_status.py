"""Tests for swarf status command."""

from __future__ import annotations

import pytest
from helpers import invoke


class TestStatus:
    def test_status_no_config(self, monkeypatch, tmp_path):
        import swarf.paths as p

        monkeypatch.setattr(p, "GLOBAL_CONFIG_TOML", tmp_path / "nonexistent.toml")
        result = invoke(["status"])
        assert result.exit_code == 0
        assert "No global config" in result.output

    @pytest.mark.usefixtures("initialized_swarf")
    def test_status_with_store(self):
        result = invoke(["status"])
        assert result.exit_code == 0
        assert "Store" in result.output
        assert "git" in result.output

    def test_status_daemon_not_running(self, initialized_swarf, monkeypatch):
        import swarf.paths as p

        monkeypatch.setattr(p, "PID_FILE", initialized_swarf / "nonexistent.pid")
        result = invoke(["status"])
        assert result.exit_code == 0
        assert "not running" in result.output

    @pytest.mark.usefixtures("git_repo")
    def test_status_no_store(self):
        result = invoke(["status"])
        assert result.exit_code == 0
        assert "not initialized" in result.output.lower() or "Store" in result.output
