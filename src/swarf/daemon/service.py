"""Systemd service installation for the swarf daemon."""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

SYSTEMD_UNIT = """\
[Unit]
Description=Swarf Auto-Sync Daemon
After=network.target

[Service]
Type=simple
ExecStart={swarf_path} daemon start --foreground
Restart=on-failure
RestartSec=5
Environment=PATH={path_env}

[Install]
WantedBy=default.target
"""


def install_systemd_service() -> None:
    """Install and enable a systemd user service for swarf."""
    swarf_path = shutil.which("swarf")
    if swarf_path is None:
        msg = "swarf not found on PATH. Install it first with 'uv tool install swarf'."
        raise RuntimeError(msg)

    unit_content = SYSTEMD_UNIT.format(
        swarf_path=swarf_path,
        path_env=os.environ.get("PATH", ""),
    )

    service_dir = Path("~/.config/systemd/user").expanduser()
    service_dir.mkdir(parents=True, exist_ok=True)
    service_file = service_dir / "swarf.service"
    service_file.write_text(unit_content)

    subprocess.run(
        ["systemctl", "--user", "daemon-reload"],
        check=True,
        capture_output=True,
    )
    subprocess.run(
        ["systemctl", "--user", "enable", "swarf"],
        check=True,
        capture_output=True,
    )
    subprocess.run(
        ["systemctl", "--user", "start", "swarf"],
        check=True,
        capture_output=True,
    )
