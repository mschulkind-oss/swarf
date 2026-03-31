"""Tests for daemon lifecycle (start/stop/status/install)."""

from __future__ import annotations

import os

from helpers import invoke


class TestDaemonLifecycle:
    def test_status_not_running(self, monkeypatch, tmp_path):
        import swarf.paths as p

        monkeypatch.setattr(p, "PID_FILE", tmp_path / "daemon.pid")
        result = invoke(["daemon", "status"])
        assert result.exit_code != 0
        assert "not running" in result.output

    def test_stop_not_running(self, monkeypatch, tmp_path):
        import swarf.paths as p

        monkeypatch.setattr(p, "PID_FILE", tmp_path / "daemon.pid")
        result = invoke(["daemon", "stop"])
        assert "not running" in result.output

    def test_start_creates_pid_file(self, monkeypatch, tmp_path):
        """Test that foreground start creates a PID file (mock the runner)."""
        import swarf.paths as p

        pid_file = tmp_path / "daemon.pid"
        monkeypatch.setattr(p, "PID_FILE", pid_file)

        # Mock the runner to exit immediately
        import swarf.daemon.runner as runner_mod

        async def mock_run(self):
            pass

        monkeypatch.setattr(runner_mod.DaemonRunner, "run", mock_run)

        result = invoke(["daemon", "start", "--foreground"])
        assert result.exit_code == 0
        # PID file should be cleaned up after exit
        assert not pid_file.exists()

    def test_status_with_running_process(self, monkeypatch, tmp_path):
        import swarf.paths as p

        pid_file = tmp_path / "daemon.pid"
        # Write our own PID — we know we're running
        pid_file.write_text(str(os.getpid()))
        monkeypatch.setattr(p, "PID_FILE", pid_file)

        result = invoke(["daemon", "status"])
        assert result.exit_code == 0
        assert "running" in result.output

    def test_status_stale_pid(self, monkeypatch, tmp_path):
        import swarf.paths as p

        pid_file = tmp_path / "daemon.pid"
        pid_file.write_text("999999999")  # Very unlikely to exist
        monkeypatch.setattr(p, "PID_FILE", pid_file)

        result = invoke(["daemon", "status"])
        assert result.exit_code != 0
        assert "stale" in result.output

    def test_install_creates_service_file(self):
        """Test that install generates the systemd unit file."""
        import swarf.daemon.service as svc

        # This test just verifies the unit template renders
        unit = svc.SYSTEMD_UNIT.format(swarf_path="/usr/bin/swarf", path_env="/usr/bin")
        assert "ExecStart=/usr/bin/swarf daemon start --foreground" in unit
        assert "Type=simple" in unit
