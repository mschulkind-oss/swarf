"""Tests for swarf config module."""

from __future__ import annotations

import pytest

from swarf.config import (
    DrawerConfig,
    parse_duration,
    read_drawer_config,
    read_drawers,
    register_drawer,
    unregister_drawer,
    write_drawer_config,
)


class TestDrawerConfig:
    def test_write_and_read(self, tmp_path):
        swarf_path = tmp_path / ".swarf"
        swarf_path.mkdir()
        config = DrawerConfig(backend="git", remote="origin", debounce="5s")
        write_drawer_config(swarf_path, config)
        result = read_drawer_config(swarf_path)
        assert result.backend == "git"
        assert result.remote == "origin"
        assert result.debounce == "5s"

    def test_write_rclone_backend(self, tmp_path):
        swarf_path = tmp_path / ".swarf"
        swarf_path.mkdir()
        config = DrawerConfig(backend="rclone", remote="gdrive:swarf", debounce="10s")
        write_drawer_config(swarf_path, config)
        result = read_drawer_config(swarf_path)
        assert result.backend == "rclone"
        assert result.remote == "gdrive:swarf"

    def test_read_defaults(self, tmp_path):
        swarf_path = tmp_path / ".swarf"
        swarf_path.mkdir()
        # Write a minimal config with no sync section
        (swarf_path / "config.toml").write_text("")
        result = read_drawer_config(swarf_path)
        assert result.backend == "git"
        assert result.remote == "origin"
        assert result.debounce == "5s"

    def test_config_toml_format(self, tmp_path):
        swarf_path = tmp_path / ".swarf"
        swarf_path.mkdir()
        config = DrawerConfig(backend="git", remote="origin", debounce="5s")
        write_drawer_config(swarf_path, config)
        content = (swarf_path / "config.toml").read_text()
        assert 'backend = "git"' in content
        assert 'remote = "origin"' in content


class TestDrawersRegistry:
    def test_read_empty(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        monkeypatch.setattr(cfg, "DRAWERS_TOML", tmp_path / "drawers.toml")
        assert read_drawers() == []

    def test_register_and_read(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        drawer_path = tmp_path / "project" / ".swarf"
        drawer_path.mkdir(parents=True)
        register_drawer(drawer_path, "git")

        drawers = read_drawers()
        assert len(drawers) == 1
        assert drawers[0].path == drawer_path.resolve()
        assert drawers[0].backend == "git"

    def test_register_idempotent(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        drawer_path = tmp_path / "project" / ".swarf"
        drawer_path.mkdir(parents=True)
        register_drawer(drawer_path, "git")
        register_drawer(drawer_path, "rclone")  # Update backend

        drawers = read_drawers()
        assert len(drawers) == 1
        assert drawers[0].backend == "rclone"

    def test_unregister(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        drawer_path = tmp_path / "project" / ".swarf"
        drawer_path.mkdir(parents=True)
        register_drawer(drawer_path, "git")
        unregister_drawer(drawer_path)

        assert read_drawers() == []

    def test_multiple_drawers(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        for name in ("alpha", "beta", "gamma"):
            p = tmp_path / name / ".swarf"
            p.mkdir(parents=True)
            register_drawer(p, "git")

        drawers = read_drawers()
        assert len(drawers) == 3


class TestParseDuration:
    def test_seconds(self):
        assert parse_duration("5s") == 5.0

    def test_milliseconds(self):
        assert parse_duration("500ms") == 0.5

    def test_minutes(self):
        assert parse_duration("1m") == 60.0

    def test_hours(self):
        assert parse_duration("2h") == 7200.0

    def test_float_value(self):
        assert parse_duration("1.5s") == 1.5

    def test_with_whitespace(self):
        assert parse_duration("  5s  ") == 5.0

    def test_invalid_format(self):
        with pytest.raises(ValueError, match="Invalid duration"):
            parse_duration("five seconds")

    def test_no_unit(self):
        with pytest.raises(ValueError, match="Invalid duration"):
            parse_duration("5")

    def test_empty(self):
        with pytest.raises(ValueError, match="Invalid duration"):
            parse_duration("")
