"""Python client for ClaudeScope: query .wpilog files and live NetworkTables
straight into pandas DataFrames instead of shelling out to the CLI and
hand-parsing JSON yourself.

    import claudescope as scope
    session = scope.load("/path/to/log.wpilog")
    df = session.query("stats avg(BatteryVoltage) by Subsystem")
"""

from __future__ import annotations

from typing import Iterable, Optional, Union

import pandas as pd

from . import _cli
from .exceptions import ClaudeScopeError
from .session import Session

__all__ = [
    "load",
    "connect",
    "sessions",
    "query_multi",
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


def query_multi(
    spl: str,
    sessions: Optional[Union[str, Iterable[str]]] = None,
    all: bool = False,
    union: bool = False,
    start: int = 0,
    end: int = 0,
) -> Union[pd.DataFrame, dict[str, Union[pd.DataFrame, ClaudeScopeError]]]:
    """Run the same query across several sessions at once.

    In union mode (union=True) returns one DataFrame tagged with a
    'session_id' column; per-session errors are attached as
    `df.attrs["errors"]` rather than raised, since one bad log shouldn't
    blank out the rest of the batch.

    In comparison mode (the default) returns a dict mapping session_id to
    either its result DataFrame or a ClaudeScopeError for sessions whose
    query failed.
    """
    if not all and not sessions:
        raise ValueError("query_multi requires sessions=[...] or all=True")

    args = ["query-multi", spl]
    if all:
        args += ["--all", "true"]
    else:
        ids = sessions if isinstance(sessions, str) else ",".join(sessions)
        args += ["--sessions", ids]
    if union:
        args += ["--union", "true"]
    if start:
        args += ["--start", str(start)]
    if end:
        args += ["--end", str(end)]

    data = _cli.invoke(args)

    if union:
        df = pd.DataFrame(data.get("result", []))
        df.attrs["errors"] = data.get("errors") or []
        return df

    out: dict[str, Union[pd.DataFrame, ClaudeScopeError]] = {}
    for entry in data.get("results", []):
        sid = entry["session_id"]
        if entry.get("error"):
            out[sid] = ClaudeScopeError(entry["error"], code="QUERY_ERROR")
        else:
            out[sid] = pd.DataFrame(entry.get("result") or [])
    return out
