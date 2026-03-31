"""Tests for swarf status command."""

from __future__ import annotations

import subprocess

from click.testing import CliRunner

from swarf.cli import main
from swarf.status import get_drawer_status


class TestStatus:
    def test_status_no_drawers(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        runner = CliRunner()
        result = runner.invoke(main, ["status"])
        assert result.exit_code == 0
        assert "No drawers registered" in result.output

    def test_status_with_drawer(self, git_repo, monkeypatch):
        import swarf.config as cfg

        config_dir = git_repo / ".config" / "swarf"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        # Create a swarf drawer with git repo
        swarf = git_repo / ".swarf"
        swarf.mkdir()
        subprocess.run(["git", "init"], cwd=swarf, capture_output=True, check=True)
        subprocess.run(
            ["git", "config", "user.email", "test@test.com"],
            cwd=swarf,
            capture_output=True,
            check=True,
        )
        subprocess.run(
            ["git", "config", "user.name", "Test"],
            cwd=swarf,
            capture_output=True,
            check=True,
        )
        (swarf / "config.toml").write_text('[sync]\nbackend = "git"\nremote = "origin"\n')
        subprocess.run(["git", "add", "-A"], cwd=swarf, capture_output=True, check=True)
        subprocess.run(["git", "commit", "-m", "init"], cwd=swarf, capture_output=True, check=True)

        cfg.register_drawer(swarf, "git")

        runner = CliRunner()
        result = runner.invoke(main, ["status"])
        assert result.exit_code == 0
        assert "git" in result.output

    def test_status_missing_drawer(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        # Register a non-existent drawer
        fake_path = tmp_path / "nonexistent" / ".swarf"
        cfg.register_drawer(fake_path, "git")

        runner = CliRunner()
        result = runner.invoke(main, ["status"])
        assert result.exit_code == 0
        assert "missing" in result.output

    def test_status_daemon_not_running(self, git_repo, monkeypatch):
        import swarf.config as cfg
        import swarf.status as st

        config_dir = git_repo / ".config" / "swarf"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")
        monkeypatch.setattr(st, "PID_FILE", git_repo / "daemon.pid")

        # Register a drawer so the table renders
        swarf = git_repo / ".swarf"
        swarf.mkdir()
        (swarf / "config.toml").write_text('[sync]\nbackend = "git"\n')
        cfg.register_drawer(swarf, "git")

        runner = CliRunner()
        result = runner.invoke(main, ["status"])
        assert result.exit_code == 0
        assert "not running" in result.output


class TestGetDrawerStatus:
    def test_missing_path(self, tmp_path):
        status = get_drawer_status(tmp_path / "nonexistent")
        assert not status.exists

    def test_existing_drawer(self, initialized_swarf):
        swarf = initialized_swarf / ".swarf"
        (swarf / "config.toml").write_text('[sync]\nbackend = "git"\nremote = "origin"\n')
        subprocess.run(["git", "add", "-A"], cwd=swarf, capture_output=True, check=True)
        subprocess.run(["git", "commit", "-m", "test"], cwd=swarf, capture_output=True, check=True)
        status = get_drawer_status(swarf)
        assert status.exists
        assert status.backend == "git"
        assert status.last_commit_message == "test"
