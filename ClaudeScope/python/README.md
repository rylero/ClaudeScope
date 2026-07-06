# claudescope-py

Python client for [ClaudeScope](../CLAUDE.md): go straight from a query to a
`pandas.DataFrame` instead of shelling out to the CLI and hand-parsing
JSON/CSV yourself.

```python
import claudescope as scope

session = scope.load("/path/to/log.wpilog")
df = session.query("stats avg(BatteryVoltage) by Subsystem")
session.disconnect()

# or, as a context manager:
with scope.load("/path/to/log.wpilog") as session:
    df = session.query("where CurrentA > 40 | timechart span=1s avg(CurrentA)")
```

Live NetworkTables sessions work the same way via `scope.connect("10.0.0.2")`,
and support `session.set(Speed=1.5)` in addition to querying.

## Design

- **Transport**: wraps the `ClaudeScope` CLI binary via `subprocess`, rather
  than talking to the daemon's HTTP API directly. This reuses the Go code's
  daemon auto-start/health-check logic (`cli/client.go`) instead of
  re-implementing session/daemon lifecycle handling in Python. The tradeoff
  is a process spawn per call; revisit if that becomes a bottleneck for
  high-frequency callers.
- **Binary discovery**: looks for `ClaudeScope`/`ClaudeScope.exe` on `PATH`,
  or an explicit path in the `CLAUDESCOPE_BIN` environment variable.
- **Errors**: the CLI writes `{"error": ..., "code": ...}` JSON on failure
  (`main.go: writeErrorAndExit`, exit code 1). The wrapper raises
  `claudescope.ClaudeScopeError` with that message and `.code` instead of a
  generic `subprocess.CalledProcessError`.
- **`query()`**: returns a `pandas.DataFrame` built directly from the
  `query` command's `{"result": [...]}` JSON — no CSV round-trip needed,
  since `pd.DataFrame(list_of_dicts)` already infers dtypes.
- **`query_multi()`**: union mode (`union=True`) returns one DataFrame
  tagged with a `session_id` column, with any per-session errors attached
  at `df.attrs["errors"]` (a bad log in a batch shouldn't blank out the
  rest). Comparison mode (default) returns `dict[session_id, DataFrame |
  ClaudeScopeError]`.

## Requirements

The `ClaudeScope` binary must be built and reachable (see the main
[CLAUDE.md](../CLAUDE.md)); this package does not vendor or build it.

## Install (editable, for development)

```
pip install -e .[dev]
pytest
```
