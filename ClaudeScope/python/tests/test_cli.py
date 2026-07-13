import json
import subprocess

import pytest

from claudescope import _cli
from claudescope.exceptions import ClaudeScopeError


class FakeProc:
    def __init__(self, stdout="", stderr="", returncode=0):
        self.stdout = stdout
        self.stderr = stderr
        self.returncode = returncode


def test_binary_uses_env_override(monkeypatch):
    monkeypatch.setenv(_cli.BIN_ENV_VAR, "/custom/path/ClaudeScope")
    assert _cli._binary() == "/custom/path/ClaudeScope"


def test_binary_not_found_raises(monkeypatch):
    monkeypatch.delenv(_cli.BIN_ENV_VAR, raising=False)
    monkeypatch.setattr(_cli.shutil, "which", lambda name: None)
    with pytest.raises(ClaudeScopeError) as exc:
        _cli._binary()
    assert exc.value.code == "BINARY_NOT_FOUND"


def test_invoke_returns_parsed_json(monkeypatch):
    monkeypatch.setattr(_cli, "_binary", lambda: "ClaudeScope")
    payload = {"session_id": "abc123"}

    def fake_run(cmd, capture_output, text):
        assert cmd == ["ClaudeScope", "load", "foo.wpilog"]
        return FakeProc(stdout=json.dumps(payload), returncode=0)

    monkeypatch.setattr(_cli.subprocess, "run", fake_run)
    assert _cli.invoke(["load", "foo.wpilog"]) == payload


def test_invoke_raises_on_cli_error_payload(monkeypatch):
    monkeypatch.setattr(_cli, "_binary", lambda: "ClaudeScope")
    error_payload = {"error": "no active session", "code": "NO_SESSION"}

    def fake_run(cmd, capture_output, text):
        return FakeProc(stdout=json.dumps(error_payload), returncode=1)

    monkeypatch.setattr(_cli.subprocess, "run", fake_run)
    with pytest.raises(ClaudeScopeError) as exc:
        _cli.invoke(["info"])
    assert exc.value.code == "NO_SESSION"
    assert "no active session" in str(exc.value)


def test_invoke_bytes_returns_raw_stdout(monkeypatch):
    monkeypatch.setattr(_cli, "_binary", lambda: "ClaudeScope")
    raw = b"\x50\x41\x52\x31fake-parquet-bytes"

    def fake_run(cmd, capture_output):
        assert cmd == ["ClaudeScope", "range", "/Drive/Vel", "--format", "parquet"]
        return FakeProc(stdout=raw, returncode=0)

    monkeypatch.setattr(_cli.subprocess, "run", fake_run)
    assert _cli.invoke_bytes(["range", "/Drive/Vel", "--format", "parquet"]) == raw


def test_invoke_bytes_raises_on_cli_error_payload(monkeypatch):
    monkeypatch.setattr(_cli, "_binary", lambda: "ClaudeScope")
    error_payload = {"error": "session not found: abc", "code": "SESSION_NOT_FOUND"}

    def fake_run(cmd, capture_output):
        return FakeProc(stdout=json.dumps(error_payload).encode(), returncode=1)

    monkeypatch.setattr(_cli.subprocess, "run", fake_run)
    with pytest.raises(ClaudeScopeError) as exc:
        _cli.invoke_bytes(["range", "/Drive/Vel", "--format", "parquet", "--session", "abc"])
    assert exc.value.code == "SESSION_NOT_FOUND"
    assert "session not found" in str(exc.value)


def test_invoke_raises_on_nonjson_failure(monkeypatch):
    monkeypatch.setattr(_cli, "_binary", lambda: "ClaudeScope")

    def fake_run(cmd, capture_output, text):
        return FakeProc(stdout="", stderr="segfault", returncode=1)

    monkeypatch.setattr(_cli.subprocess, "run", fake_run)
    with pytest.raises(ClaudeScopeError) as exc:
        _cli.invoke(["range", "bad"])
    assert exc.value.code == "COMMAND_FAILED"
    assert "segfault" in str(exc.value)
