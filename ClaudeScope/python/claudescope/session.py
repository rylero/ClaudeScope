from __future__ import annotations

import io
from typing import Any, Optional

import pandas as pd

from . import _cli


class Session:
    """A handle to one ClaudeScope session (a loaded .wpilog or a live NT connection)."""

    def __init__(self, session_id: str, kind: str, label: str):
        self.session_id = session_id
        self.kind = kind  # "log" or "live"
        self.label = label

    def __repr__(self) -> str:
        return f"Session(session_id={self.session_id!r}, kind={self.kind!r}, label={self.label!r})"

    def __enter__(self) -> "Session":
        return self

    def __exit__(self, *exc_info) -> None:
        self.disconnect()

    def _session_flags(self, start: int = 0, end: int = 0, time: int = 0) -> list[str]:
        flags = ["--session", self.session_id]
        if start:
            flags += ["--start", str(start)]
        if end:
            flags += ["--end", str(end)]
        if time:
            flags += ["--time", str(time)]
        return flags

    def info(self) -> dict:
        """Fields and time range for this session."""
        return _cli.invoke(["info", "--session", self.session_id])

    def get(self, *keys: str, time: int = 0) -> dict:
        """Value(s) at a specific timestamp; time=0 (default) returns the latest."""
        args = ["get", *keys] + self._session_flags(time=time)
        return _cli.invoke(args)

    def range(self, *keys: str, start: int = 0, end: int = 0) -> dict:
        """Raw {key: [{timestamp, value}, ...]} data points between start and end.

        For analysis prefer `range_df`, which pulls the same data as Parquet and
        returns a tidy DataFrame ready for pandas — pushing raw series into
        pandas is the recommended path over composing multi-stage SPL transforms.
        """
        args = ["range", *keys] + self._session_flags(start=start, end=end)
        return _cli.invoke(args)

    def range_df(self, *keys: str, start: int = 0, end: int = 0, pivot: bool = False) -> pd.DataFrame:
        """Time-series data for key(s) as a DataFrame, fetched via Parquet.

        Returns long-format rows with columns ``key``, ``timestamp`` (µs), and
        ``value`` (native dtype for a single numeric key; stringified across
        mixed-type keys). Pass ``pivot=True`` to get a wide frame indexed by
        ``timestamp`` with one column per key (forward-fill yourself if you need
        a shared axis: ``df.ffill()``).

        This is the "cs -> parquet -> pandas" path: pull the raw series once and
        do eval/filter/correlate/resample in pandas, which handles nulls and
        formatting correctly and costs no extra query-language surface.
        """
        args = ["range", *keys, "--format", "parquet"] + self._session_flags(start=start, end=end)
        raw = _cli.invoke_bytes(args)
        if not raw:
            return pd.DataFrame(columns=["key", "timestamp", "value"])
        df = pd.read_parquet(io.BytesIO(raw))
        if pivot and not df.empty:
            return df.pivot(index="timestamp", columns="key", values="value").sort_index()
        return df

    def find_bool(self, key: str, value: bool) -> pd.DataFrame:
        """Time ranges (as a start/end DataFrame) where `key` equals `value`."""
        args = ["find-bool", key, "true" if value else "false", "--session", self.session_id]
        return pd.DataFrame(_cli.invoke(args))

    def find_threshold(self, key: str, min: Optional[float] = None, max: Optional[float] = None) -> pd.DataFrame:
        """Time ranges (as a start/end DataFrame) where `key` is within [min, max]."""
        args = ["find-threshold", key, "--session", self.session_id]
        if min is not None:
            args += ["--min", str(min)]
        if max is not None:
            args += ["--max", str(max)]
        return pd.DataFrame(_cli.invoke(args))

    def stats(self, key: str, start: int = 0, end: int = 0) -> dict:
        """Descriptive statistics (mean, median, min, max, quartiles, deltas) for a numeric field."""
        args = ["stats", key] + self._session_flags(start=start, end=end)
        return _cli.invoke(args)

    def query(self, spl: str, start: int = 0, end: int = 0) -> pd.DataFrame:
        """Run an SPL-subset pipe query and return the result rows as a DataFrame.

        Fetched as Parquet rather than JSON: faster to transfer/parse for
        large results and preserves column dtypes (bool/float/string) instead
        of going through pandas' JSON type inference.

        Prefer this only for the SPL access/correlation you can't trivially do
        in pandas (e.g. `transaction`, `ranges`, cross-field forward-fill joins).
        For eval/filter/stats/resample transforms, pulling raw series with
        `range_df` and transforming in pandas is the recommended path — pandas
        handles nulls and numeric formatting correctly and the SPL *transform*
        stages are frozen (not receiving further investment); see
        USABILITY_FRICTION.md.
        """
        args = ["query", spl, "--format", "parquet"] + self._session_flags(start=start, end=end)
        raw = _cli.invoke_bytes(args)
        if not raw:
            return pd.DataFrame()
        return pd.read_parquet(io.BytesIO(raw))

    def set(self, **pairs: Any) -> None:
        """Publish key/value pairs to a live NT session. Fails on log sessions."""
        args = ["set"] + [f"{k}={v}" for k, v in pairs.items()] + ["--session", self.session_id]
        _cli.invoke(args)

    def disconnect(self) -> None:
        """Close this session and free its resources."""
        _cli.invoke(["disconnect", "--session", self.session_id])
