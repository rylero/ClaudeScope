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
            raise ClaudeScopeError(data["error"], code=data.get("code", "COMMAND_FAILED"))
        raise ClaudeScopeError(
            proc.stderr.strip() or f"ClaudeScope exited with code {proc.returncode}",
            code="COMMAND_FAILED",
        )

    return data


def invoke_bytes(args: list[str]) -> bytes:
    """Run a CLI subcommand expecting raw bytes on stdout (e.g. --format parquet).

    On a non-zero exit the CLI still writes its {"error", "code"} JSON body
    to stdout (main.go's writeErrorAndExit ignores --format on failure), so
    that case is decoded and raised the same way as `invoke`.
    """
    cmd = [_binary(), *args]
    try:
        proc = subprocess.run(cmd, capture_output=True)
    except FileNotFoundError as e:
        raise ClaudeScopeError(f"failed to launch ClaudeScope binary: {e}", code="BINARY_NOT_FOUND") from e

    if proc.returncode != 0:
        stdout = proc.stdout.decode("utf-8", errors="replace").strip()
        try:
            data = json.loads(stdout) if stdout else None
        except json.JSONDecodeError:
            data = None
        if isinstance(data, dict) and "error" in data:
            raise ClaudeScopeError(data["error"], code=data.get("code", "COMMAND_FAILED"))
        raise ClaudeScopeError(
            proc.stderr.decode("utf-8", errors="replace").strip() or f"ClaudeScope exited with code {proc.returncode}",
            code="COMMAND_FAILED",
        )

    return proc.stdout
