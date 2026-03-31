"""Tests for the .git/info/exclude managed section."""

from __future__ import annotations

import subprocess

from swarf.exclude import (
    _BASE_EXCLUDES,
    _FENCE_END,
    _FENCE_START,
    add_linked_excludes,
    read_managed_excludes,
    update_excludes,
    write_managed_excludes,
)


def _init_git(path):
    """Create a minimal git repo at path."""
    subprocess.run(["git", "init"], cwd=path, capture_output=True, check=True)
    subprocess.run(
        ["git", "config", "user.email", "test@test.com"],
        cwd=path,
        capture_output=True,
        check=True,
    )
    subprocess.run(
        ["git", "config", "user.name", "Test"],
        cwd=path,
        capture_output=True,
        check=True,
    )


class TestReadManagedExcludes:
    def test_no_exclude_file(self, tmp_path):
        _init_git(tmp_path)
        assert read_managed_excludes(tmp_path) == []

    def test_exclude_file_no_fence(self, tmp_path):
        _init_git(tmp_path)
        exclude = tmp_path / ".git" / "info" / "exclude"
        exclude.parent.mkdir(parents=True, exist_ok=True)
        exclude.write_text("# user stuff\n*.pyc\n")
        assert read_managed_excludes(tmp_path) == []

    def test_reads_fenced_entries(self, tmp_path):
        _init_git(tmp_path)
        exclude = tmp_path / ".git" / "info" / "exclude"
        exclude.parent.mkdir(parents=True, exist_ok=True)
        exclude.write_text(
            f"# user stuff\n*.pyc\n{_FENCE_START}\n/.swarf/\n/.mise.local.toml\n{_FENCE_END}\n"
        )
        result = read_managed_excludes(tmp_path)
        assert "/.swarf/" in result
        assert "/.mise.local.toml" in result

    def test_ignores_comments_in_fence(self, tmp_path):
        _init_git(tmp_path)
        exclude = tmp_path / ".git" / "info" / "exclude"
        exclude.parent.mkdir(parents=True, exist_ok=True)
        exclude.write_text(f"{_FENCE_START}\n# comment\n/.swarf/\n{_FENCE_END}\n")
        result = read_managed_excludes(tmp_path)
        assert result == ["/.swarf/"]


class TestWriteManagedExcludes:
    def test_creates_exclude_file(self, tmp_path):
        _init_git(tmp_path)
        write_managed_excludes(tmp_path, ["/.swarf/", "/.mise.local.toml"])
        exclude = tmp_path / ".git" / "info" / "exclude"
        content = exclude.read_text()
        assert _FENCE_START in content
        assert _FENCE_END in content
        assert "/.swarf/" in content

    def test_preserves_user_content(self, tmp_path):
        _init_git(tmp_path)
        exclude = tmp_path / ".git" / "info" / "exclude"
        exclude.parent.mkdir(parents=True, exist_ok=True)
        exclude.write_text("# My custom ignores\n*.pyc\n__pycache__/\n")
        write_managed_excludes(tmp_path, ["/.swarf/"])
        content = exclude.read_text()
        assert "# My custom ignores" in content
        assert "*.pyc" in content
        assert "/.swarf/" in content

    def test_replaces_old_fence(self, tmp_path):
        _init_git(tmp_path)
        exclude = tmp_path / ".git" / "info" / "exclude"
        exclude.parent.mkdir(parents=True, exist_ok=True)
        exclude.write_text(f"{_FENCE_START}\n/.old-entry/\n{_FENCE_END}\n")
        write_managed_excludes(tmp_path, ["/.swarf/"])
        content = exclude.read_text()
        assert "/.old-entry/" not in content
        assert "/.swarf/" in content
        # Only one fence
        assert content.count(_FENCE_START) == 1

    def test_sorts_and_dedupes(self, tmp_path):
        _init_git(tmp_path)
        write_managed_excludes(tmp_path, ["/b", "/a", "/b"])
        exclude = tmp_path / ".git" / "info" / "exclude"
        content = exclude.read_text()
        lines = content.splitlines()
        fence_idx = lines.index(_FENCE_START)
        end_idx = lines.index(_FENCE_END)
        entries = lines[fence_idx + 1 : end_idx]
        assert entries == ["/a", "/b"]


class TestUpdateExcludes:
    def test_includes_base_excludes(self, tmp_path):
        _init_git(tmp_path)
        update_excludes(tmp_path)
        result = read_managed_excludes(tmp_path)
        for entry in _BASE_EXCLUDES:
            assert entry in result

    def test_merges_extra(self, tmp_path):
        _init_git(tmp_path)
        update_excludes(tmp_path, extra=["/AGENTS.md"])
        result = read_managed_excludes(tmp_path)
        assert "/AGENTS.md" in result
        for entry in _BASE_EXCLUDES:
            assert entry in result

    def test_preserves_existing_managed_entries(self, tmp_path):
        _init_git(tmp_path)
        write_managed_excludes(tmp_path, ["/.swarf/", "/custom-entry"])
        update_excludes(tmp_path)
        result = read_managed_excludes(tmp_path)
        assert "/custom-entry" in result


class TestAddLinkedExcludes:
    def test_adds_prefixed_paths(self, tmp_path):
        _init_git(tmp_path)
        update_excludes(tmp_path)  # seed base excludes
        add_linked_excludes(tmp_path, ["AGENTS.md", ".copilot/skills/SKILL.md"])
        result = read_managed_excludes(tmp_path)
        assert "/AGENTS.md" in result
        assert "/.copilot/skills/SKILL.md" in result

    def test_no_op_on_empty(self, tmp_path):
        _init_git(tmp_path)
        exclude = tmp_path / ".git" / "info" / "exclude"
        before = exclude.read_text() if exclude.exists() else ""
        add_linked_excludes(tmp_path, [])
        after = exclude.read_text() if exclude.exists() else ""
        assert before == after

    def test_already_prefixed(self, tmp_path):
        _init_git(tmp_path)
        add_linked_excludes(tmp_path, ["/AGENTS.md"])
        result = read_managed_excludes(tmp_path)
        assert "/AGENTS.md" in result
