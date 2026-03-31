"""Tests for swarf sweep — move files into .swarf/links/ and symlink back."""

from __future__ import annotations

import pytest
from click.testing import CliRunner

from swarf.cli import main
from swarf.exclude import read_managed_excludes


class TestSweep:
    def test_sweep_moves_and_symlinks(self, initialized_swarf):
        root = initialized_swarf
        target = root / "AGENTS.md"
        target.write_text("# Agents\n")

        runner = CliRunner()
        result = runner.invoke(main, ["sweep", "AGENTS.md"])
        assert result.exit_code == 0, result.output
        assert "swept AGENTS.md" in result.output

        # Original is now a symlink
        assert target.is_symlink()
        assert target.resolve() == (root / ".swarf" / "links" / "AGENTS.md").resolve()

        # Content preserved
        assert target.read_text() == "# Agents\n"

    def test_sweep_nested_path(self, initialized_swarf):
        root = initialized_swarf
        (root / ".copilot" / "skills").mkdir(parents=True)
        skill = root / ".copilot" / "skills" / "SKILL.md"
        skill.write_text("# Skill\n")

        runner = CliRunner()
        result = runner.invoke(main, ["sweep", ".copilot/skills/SKILL.md"])
        assert result.exit_code == 0, result.output

        assert skill.is_symlink()
        dest = root / ".swarf" / "links" / ".copilot" / "skills" / "SKILL.md"
        assert dest.exists()
        assert skill.resolve() == dest.resolve()

    def test_sweep_updates_exclude(self, initialized_swarf):
        root = initialized_swarf
        (root / "AGENTS.md").write_text("# Agents\n")

        runner = CliRunner()
        runner.invoke(main, ["sweep", "AGENTS.md"])

        managed = read_managed_excludes(root)
        assert "/AGENTS.md" in managed

    def test_sweep_multiple_files(self, initialized_swarf):
        root = initialized_swarf
        (root / "A.md").write_text("a")
        (root / "B.md").write_text("b")

        runner = CliRunner()
        result = runner.invoke(main, ["sweep", "A.md", "B.md"])
        assert result.exit_code == 0, result.output
        assert (root / "A.md").is_symlink()
        assert (root / "B.md").is_symlink()

    def test_sweep_already_symlink(self, initialized_swarf):
        root = initialized_swarf
        dest = root / ".swarf" / "links" / "AGENTS.md"
        dest.write_text("# Agents\n")
        (root / "AGENTS.md").symlink_to(dest)

        runner = CliRunner()
        result = runner.invoke(main, ["sweep", "AGENTS.md"])
        assert "already a symlink" in result.output

    @pytest.mark.usefixtures("initialized_swarf")
    def test_sweep_file_not_found(self):
        runner = CliRunner()
        result = runner.invoke(main, ["sweep", "nonexistent.md"])
        assert "does not exist" in result.output

    def test_sweep_inside_swarf(self, initialized_swarf):
        root = initialized_swarf
        target = root / ".swarf" / "docs" / "notes.md"
        target.write_text("notes")

        runner = CliRunner()
        result = runner.invoke(main, ["sweep", ".swarf/docs/notes.md"])
        assert "already inside .swarf" in result.output

    def test_sweep_already_in_links(self, initialized_swarf):
        root = initialized_swarf
        dest = root / ".swarf" / "links" / "AGENTS.md"
        dest.write_text("# Agents\n")
        (root / "AGENTS.md").write_text("# Different\n")

        runner = CliRunner()
        result = runner.invoke(main, ["sweep", "AGENTS.md"])
        assert "already exists in .swarf/links" in result.output
        # Original should not have been touched
        assert not (root / "AGENTS.md").is_symlink()

    def test_sweep_no_swarf(self, git_repo):
        (git_repo / "file.md").write_text("hi")
        runner = CliRunner()
        result = runner.invoke(main, ["sweep", "file.md"])
        assert result.exit_code != 0
        assert "swarf init" in result.output

    @pytest.mark.usefixtures("initialized_swarf")
    def test_sweep_requires_args(self):
        runner = CliRunner()
        result = runner.invoke(main, ["sweep"])
        assert result.exit_code != 0
