"""Sync backends for the swarf daemon."""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass
from pathlib import Path


@dataclass
class SyncResult:
    """Result of a sync operation."""

    success: bool
    message: str
    files_changed: int


class SyncBackend(ABC):
    """Abstract base class for sync backends."""

    @abstractmethod
    def sync(self, swarf_path: Path) -> SyncResult:
        """Sync the swarf directory."""

    @abstractmethod
    def has_changes(self, swarf_path: Path) -> bool:
        """Check if there are pending changes to sync."""
