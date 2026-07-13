"""Python client for ClaudeScope: pull .wpilog files and live NetworkTables
straight into pandas DataFrames instead of shelling out to the CLI and
hand-parsing JSON yourself.

    import claudescope as scope
    session = scope.load("/path/to/log.wpilog")
    df = session.range_df("BatteryVoltage", "DrivetrainCurrent", pivot=True)
"""

from __future__ import annotations

from . import _cli
from .exceptions import ClaudeScopeError
from .session import Session

__all__ = [
    "load",
    "connect",
    "sessions",
    "Session",
    "ClaudeScopeError",
]


def load(path: str) -> Session:
    """Parse a .wpilog file and open a read-only session."""
    data = _cli.invoke(["load", path])
    return Session(data["session_id"], kind="log", label=path)


def connect(ip: str) -> Session:
    """Connect to a live NetworkTables 4 instance."""
    data = _cli.invoke(["connect", ip])
    return Session(data["session_id"], kind="live", label=ip)


def sessions() -> list[dict]:
    """List all active sessions, e.g. to recover a session ID after losing it."""
    return _cli.invoke(["sessions"]).get("sessions", [])
