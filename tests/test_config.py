"""Tests for swarf config module."""

from __future__ import annotations

from pathlib import Path

import pytest

from swarf.config import (
    parse_duration,
    read_drawers,
    register_drawer,
    unregister_drawer,
)


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

        host = tmp_path / "project"
        host.mkdir(parents=True)
        register_drawer("project", host)

        drawers = read_drawers()
        assert len(drawers) == 1
        assert drawers[0].slug == "project"
        assert drawers[0].host == host.resolve()

    def test_register_idempotent(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        host = tmp_path / "project"
        host.mkdir(parents=True)
        register_drawer("project", host)
        register_drawer("project", Path("/new/path"))

        drawers = read_drawers()
        assert len(drawers) == 1
        assert drawers[0].host == Path("/new/path").resolve()

    def test_unregister(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        host = tmp_path / "project"
        host.mkdir(parents=True)
        register_drawer("project", host)
        unregister_drawer("project")

        assert read_drawers() == []

    def test_multiple_drawers(self, monkeypatch, tmp_path):
        import swarf.config as cfg

        config_dir = tmp_path / "config"
        monkeypatch.setattr(cfg, "CONFIG_DIR", config_dir)
        monkeypatch.setattr(cfg, "DRAWERS_TOML", config_dir / "drawers.toml")

        for name in ("alpha", "beta", "gamma"):
            p = tmp_path / name
            p.mkdir(parents=True)
            register_drawer(name, p)

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
