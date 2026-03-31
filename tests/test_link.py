"""Tests for swarf link command."""

from __future__ import annotations

from pathlib import Path

import pytest
from helpers import invoke

from swarf.link import LinkResult, run_link


class TestLink:
    def test_link_creates_symlink(self, initialized_swarf):
        root = initialized_swarf
        # Place a file in .swarf/links/
        (root / ".swarf" / "links" / "AGENTS.md").write_text("# Agents\n")

        result = run_link(root)
        assert Path("AGENTS.md") in result.created
        target = root / "AGENTS.md"
        assert target.is_symlink()
        assert target.read_text() == "# Agents\n"

    def test_link_nested_directory(self, initialized_swarf):
        root = initialized_swarf
        nested = root / ".swarf" / "links" / ".copilot" / "skills"
        nested.mkdir(parents=True)
        (nested / "skill.md").write_text("# Skill\n")

        result = run_link(root)
        assert Path(".copilot/skills/skill.md") in result.created
        target = root / ".copilot" / "skills" / "skill.md"
        assert target.is_symlink()
        assert target.read_text() == "# Skill\n"

    def test_link_idempotent(self, initialized_swarf):
        root = initialized_swarf
        (root / ".swarf" / "links" / "AGENTS.md").write_text("# Agents\n")

        first = run_link(root)
        assert len(first.created) == 1

        second = run_link(root)
        assert len(second.created) == 0
        assert len(second.skipped) == 1

    def test_link_warns_real_file(self, initialized_swarf):
        root = initialized_swarf
        (root / ".swarf" / "links" / "README.md").write_text("from swarf\n")
        # Create a real file at the target
        (root / "README.md").write_text("original\n")

        result = run_link(root)
        assert len(result.warnings) == 1
        assert "real file exists" in result.warnings[0]
        # Original not overwritten
        assert (root / "README.md").read_text() == "original\n"

    def test_link_fixes_stale_symlink(self, initialized_swarf):
        root = initialized_swarf
        source = root / ".swarf" / "links" / "AGENTS.md"
        source.write_text("# Agents\n")
        target = root / "AGENTS.md"
        # Create a stale symlink pointing to a nonexistent file
        target.symlink_to(root / "nonexistent")
        assert target.is_symlink()
        assert not target.exists()  # broken

        result = run_link(root)
        assert Path("AGENTS.md") in result.created
        assert target.is_symlink()
        assert target.read_text() == "# Agents\n"

    def test_link_quiet_suppresses_output(self, initialized_swarf):
        root = initialized_swarf
        (root / ".swarf" / "links" / "AGENTS.md").write_text("# Agents\n")

        result = invoke(["link", "--quiet"])
        assert result.exit_code == 0
        # No "linked" output in quiet mode
        assert "linked" not in result.output

    def test_link_quiet_still_shows_warnings(self, initialized_swarf):
        root = initialized_swarf
        (root / ".swarf" / "links" / "README.md").write_text("from swarf\n")
        (root / "README.md").write_text("original\n")

        result = invoke(["link", "--quiet"])
        assert "real file exists" in result.output

    def test_link_empty_links_dir(self, initialized_swarf):
        result = run_link(initialized_swarf)
        assert isinstance(result, LinkResult)
        assert len(result.created) == 0
        assert len(result.warnings) == 0

    @pytest.mark.usefixtures("git_repo")
    def test_link_no_swarf_dir(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("GIT_CEILING_DIRECTORIES", str(tmp_path))
        result = invoke(["link"])
        assert result.exit_code != 0
        assert "Not inside a swarf project" in result.output
