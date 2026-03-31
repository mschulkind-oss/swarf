"""Tests for swarf enter — mise hook with auto-sweep."""

from __future__ import annotations

import pytest
from helpers import invoke

from swarf.config import GlobalConfig, write_global_config


class TestEnter:
    def test_enter_links_files(self, initialized_swarf):
        root = initialized_swarf
        (root / ".swarf" / "links" / "AGENTS.md").write_text("# Agents\n")

        result = invoke(["enter"])
        assert result.exit_code == 0, result.output
        assert (root / "AGENTS.md").is_symlink()

    def test_enter_auto_sweeps(self, initialized_swarf):
        root = initialized_swarf
        write_global_config(GlobalConfig(backend="git", remote="origin", auto_sweep=["AGENTS.md"]))
        (root / "AGENTS.md").write_text("# Agents\n")

        result = invoke(["enter"])
        assert result.exit_code == 0, result.output
        assert (root / "AGENTS.md").is_symlink()
        assert (root / ".swarf" / "links" / "AGENTS.md").exists()

    @pytest.mark.usefixtures("initialized_swarf")
    def test_enter_skips_missing_auto_sweep(self):
        """Auto-sweep paths that don't exist are silently skipped."""
        write_global_config(
            GlobalConfig(backend="git", remote="origin", auto_sweep=["NONEXISTENT.md"])
        )
        result = invoke(["enter"])
        assert result.exit_code == 0, result.output

    def test_enter_skips_already_swept(self, initialized_swarf):
        """Files already symlinked are not re-swept."""
        root = initialized_swarf
        write_global_config(GlobalConfig(backend="git", remote="origin", auto_sweep=["AGENTS.md"]))
        dest = root / ".swarf" / "links" / "AGENTS.md"
        dest.write_text("# Agents\n")
        (root / "AGENTS.md").symlink_to(dest)

        result = invoke(["enter"])
        assert result.exit_code == 0, result.output
        assert (root / "AGENTS.md").is_symlink()

    @pytest.mark.usefixtures("git_repo")
    def test_enter_no_swarf_dir(self):
        """Enter silently does nothing if no .swarf/ exists."""
        result = invoke(["enter"])
        assert result.exit_code == 0

    def test_enter_no_auto_sweep_config(self, initialized_swarf):
        """Enter works fine with no auto_sweep in config."""
        root = initialized_swarf
        (root / ".swarf" / "links" / "AGENTS.md").write_text("# Agents\n")
        write_global_config(GlobalConfig(backend="git", remote="origin"))

        result = invoke(["enter"])
        assert result.exit_code == 0, result.output
        assert (root / "AGENTS.md").is_symlink()
