import io

import pandas as pd
import pytest

import claudescope as scope
from claudescope import _cli
from claudescope.exceptions import ClaudeScopeError


def _parquet_bytes(rows: list[dict]) -> bytes:
    buf = io.BytesIO()
    pd.DataFrame(rows).to_parquet(buf)
    return buf.getvalue()


def test_load_returns_session(monkeypatch):
    calls = []

    def fake_invoke(args):
        calls.append(args)
        return {"session_id": "s1", "fields": []}

    monkeypatch.setattr(_cli, "invoke", fake_invoke)
    session = scope.load("/path/log.wpilog")
    assert session.session_id == "s1"
    assert session.kind == "log"
    assert session.label == "/path/log.wpilog"
    assert calls == [["load", "/path/log.wpilog"]]


def test_query_returns_dataframe(monkeypatch):
    def fake_invoke_bytes(args):
        assert args == ["query", "stats avg(BatteryVoltage) by Subsystem", "--format", "parquet", "--session", "s1"]
        return _parquet_bytes([{"Subsystem": "Drive", "avg(BatteryVoltage)": 12.1}])

    monkeypatch.setattr(_cli, "invoke_bytes", fake_invoke_bytes)
    session = scope.Session("s1", "log", "x.wpilog")
    df = session.query("stats avg(BatteryVoltage) by Subsystem")
    assert isinstance(df, pd.DataFrame)
    assert df.iloc[0]["Subsystem"] == "Drive"


def test_query_passes_start_end(monkeypatch):
    def fake_invoke_bytes(args):
        assert args == ["query", "table Timestamp", "--format", "parquet", "--session", "s1", "--start", "100", "--end", "200"]
        return _parquet_bytes([])

    monkeypatch.setattr(_cli, "invoke_bytes", fake_invoke_bytes)
    session = scope.Session("s1", "log", "x.wpilog")
    session.query("table Timestamp", start=100, end=200)


def test_query_empty_bytes_returns_empty_dataframe(monkeypatch):
    monkeypatch.setattr(_cli, "invoke_bytes", lambda args: b"")
    session = scope.Session("s1", "log", "x.wpilog")
    df = session.query("table Timestamp")
    assert isinstance(df, pd.DataFrame)
    assert df.empty


def test_query_multi_union_attaches_errors(monkeypatch):
    def fake_invoke(args):
        return {
            "result": [{"Timestamp": 0, "session_id": "s1"}],
            "errors": [{"session_id": "s2", "error": "missing field"}],
        }

    monkeypatch.setattr(_cli, "invoke", fake_invoke)
    df = scope.query_multi("table Timestamp", sessions=["s1", "s2"], union=True)
    assert isinstance(df, pd.DataFrame)
    assert df.attrs["errors"] == [{"session_id": "s2", "error": "missing field"}]


def test_query_multi_comparison_mode(monkeypatch):
    def fake_invoke(args):
        return {
            "results": [
                {"session_id": "s1", "result": [{"Timestamp": 0}]},
                {"session_id": "s2", "error": "no such field"},
            ]
        }

    monkeypatch.setattr(_cli, "invoke", fake_invoke)
    out = scope.query_multi("table Timestamp", sessions=["s1", "s2"])
    assert isinstance(out["s1"], pd.DataFrame)
    assert isinstance(out["s2"], ClaudeScopeError)
    assert out["s2"].code == "QUERY_ERROR"


def test_query_multi_requires_sessions_or_all():
    with pytest.raises(ValueError):
        scope.query_multi("table Timestamp")


def test_set_serializes_pairs(monkeypatch):
    def fake_invoke(args):
        assert args == ["set", "Speed=1.5", "Enabled=True", "--session", "s1"]
        return {}

    monkeypatch.setattr(_cli, "invoke", fake_invoke)
    session = scope.Session("s1", "live", "10.0.0.2")
    session.set(Speed=1.5, Enabled=True)


def test_context_manager_disconnects(monkeypatch):
    calls = []
    monkeypatch.setattr(_cli, "invoke", lambda args: calls.append(args) or {})

    with scope.Session("s1", "log", "x.wpilog") as session:
        assert session.session_id == "s1"
    assert calls == [["disconnect", "--session", "s1"]]
