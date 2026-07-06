"""Thin subprocess wrapper around the ClaudeScope CLI binary.

Wraps the binary rather than talking to the daemon's HTTP API directly, so
session/daemon lifecycle (auto-spawn, port, health check) stays owned by the
Go code in one place instead of being re-implemented here.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
from typing import Any

from .exceptions import ClaudeScopeError

BIN_ENV_VAR = "CLAUDESCOPE_BIN"
_BINARY_NAMES = ("ClaudeScope", "ClaudeScope.exe", "claudescope", "claudescope.exe")


def _binary() -> str:
    override = os.environ.get(BIN_ENV_VAR)
    if override:
        return override
    for name in _BINARY_NAMES:
        found = shutil.which(name)
        if found:
            return found
    raise ClaudeScopeError(
        "ClaudeScope binary not found on PATH. Build it (`go build ./ClaudeScope`) "
        f"and put it on PATH, or set {BIN_ENV_VAR} to its full path.",
        code="BINARY_NOT_FOUND",
    )


def invoke(args: list[str]) -> Any:
    """Run one CLI subcommand and return its decoded JSON payload.

    Raises ClaudeScopeError on a non-zero exit, using the CLI's own
    {"error", "code"} JSON body when present (main.go: writeErrorAndExit
    writes that shape to stdout, not stderr).
    """
    cmd = [_binary(), *args]
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True)
    except FileNotFoundError as e:
        raise ClaudeScopeError(f"failed to launch ClaudeScope binary: {e}", code="BINARY_NOT_FOUND") from e

    stdout = proc.stdout.strip()
    data = None
    if stdout:
        try:
            data = json.loads(stdout)
        except json.JSONDecodeError as e:
            if proc.returncode != 0:
                raise ClaudeScopeError(
                    proc.stderr.strip() or stdout or f"ClaudeScope exited with code {proc.returncode}",
                    code="COMMAND_FAILED",
                ) from e
            raise ClaudeScopeError(f"could not parse ClaudeScope output as JSON: {stdout!r}", code="BAD_OUTPUT") from e

    if proc.returncode != 0:
        if isinstance(data, dict) and "error" in data:
            message = data["error"]
            code = data.get("code", "COMMAND_FAILED")
            # The CLI double-wraps a daemon HTTP error: main.go's
            # writeErrorAndExit takes the *already-JSON* error text from a
            # failed DoRequest and stuffs it verbatim into another "error"
            # field. Unwrap one level so callers see the daemon's own
            # message/code instead of a raw JSON blob.
            if isinstance(message, str):
                try:
                    inner = json.loads(message)
                except json.JSONDecodeError:
                    inner = None
                if isinstance(inner, dict) and "error" in inner:
                    message = inner["error"]
                    code = inner.get("code", code)
            raise ClaudeScopeError(message, code=code)
        raise ClaudeScopeError(
            proc.stderr.strip() or f"ClaudeScope exited with code {proc.returncode}",
            code="COMMAND_FAILED",
        )

    return data
