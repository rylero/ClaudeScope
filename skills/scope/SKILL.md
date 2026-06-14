---
name: scope
description: >
  Use ClaudeScope to analyze FRC robot .wpilog files or query live NetworkTables.
  Invoke when the user asks to analyze a log file, query robot data, check field values,
  find time ranges, compute statistics, or investigate robot performance from telemetry.
  Trigger on: "analyze log", "load wpilog", "check NT", "query robot data", "/scope".
---

# ClaudeScope — AI Agent Guide

ClaudeScope is a CLI tool that parses FRC `.wpilog` files and queries live NetworkTables. It runs a daemon on port 5812 (auto-starts on first use).

## Setup

Download the binary for your platform from the [GitHub Releases page](https://github.com/rylero/TheFRCSuite/releases) and add it to PATH:

| Platform | Binary |
|---|---|
| Windows | `ClaudeScope-windows-amd64.exe` → rename to `ClaudeScope.exe` |
| macOS (Apple Silicon) | `ClaudeScope-darwin-arm64` → rename to `ClaudeScope` |
| macOS (Intel) | `ClaudeScope-darwin-amd64` → rename to `ClaudeScope` |
| Linux | `ClaudeScope-linux-amd64` → rename to `ClaudeScope` |

Verify: `ClaudeScope version`

## Workflow

```
1. Load the log (or connect to NT) → get session_id
2. Run queries using --session <id>
3. Disconnect when done
```

## Critical Notes

- **Git Bash path issue**: Keys starting with `/` get mangled by MSYS2. Always prefix commands with `MSYS_NO_PATHCONV=1`.
- **Timestamps** are microseconds (µs) since log start.
- **Negative start/end** = offset from end of log. `-5000000` = last 5 seconds.
- **end=0** means end of log. **time=0** in `get` means latest value.

---

## Commands

### Load a .wpilog file
```bash
MSYS_NO_PATHCONV=1 ClaudeScope load "C:/path/to/file.wpilog"
```
Returns: `{"session_id":"<id>","fields":[{"key":"...","type":"double|boolean|string|..."},...]}`

### Connect to live NT
```bash
ClaudeScope connect 10.0.0.2
```
Returns: `{"session_id":"<id>"}`

### Disconnect
```bash
ClaudeScope disconnect --session <id>
```

### List fields and time range
```bash
ClaudeScope info --session <id>
```
Returns: `{"fields":[...],"start":<µs>,"end":<µs>}`

### Get value at timestamp (time=0 → latest)
```bash
MSYS_NO_PATHCONV=1 ClaudeScope get /RealOutputs/Superstructure/State --session <id> --time 1500000
```
Returns: `{"/key":{"timestamp":<µs>,"value":<any>}}`

### Get time-series data for a range
```bash
MSYS_NO_PATHCONV=1 ClaudeScope range /RealOutputs/Drive/LeftVelocity --session <id> --start 1000000 --end 5000000
```
Returns: `{"/key":[{"timestamp":<µs>,"value":<any>},...]}`

### Find bool ranges (e.g. when robot was enabled)
```bash
MSYS_NO_PATHCONV=1 ClaudeScope find-bool /RealOutputs/Robot/Enabled true --session <id>
```
Returns: `[{"start":<µs>,"end":<µs>},...]`

### Find threshold ranges (e.g. when voltage was low)
```bash
MSYS_NO_PATHCONV=1 ClaudeScope find-threshold /RealOutputs/PowerDistribution/Voltage --min 10.0 --max 11.5 --session <id>
```
`--min` and `--max` are each optional — supply just one for a one-sided test (at least one is required):
```bash
# voltage below 11.0
MSYS_NO_PATHCONV=1 ClaudeScope find-threshold /RealOutputs/PowerDistribution/Voltage --max 11.0 --session <id>
# current above 40
MSYS_NO_PATHCONV=1 ClaudeScope find-threshold /RealOutputs/PowerDistribution/Current --min 40.0 --session <id>
```
Returns: `[{"start":<µs>,"end":<µs>},...]`

### Statistics for a numeric field
```bash
MSYS_NO_PATHCONV=1 ClaudeScope stats /RealOutputs/Drive/LeftVelocity --session <id> --start 0 --end 0
```
Returns: `{"mean":<f>,"median":<f>,"min":<f>,"max":<f>,"q1":<f>,"q3":<f>,"avg_delta":<f/s>,"min_delta":<f/s>,"max_delta":<f/s>}`

### Set NT value (live sessions only)
```bash
MSYS_NO_PATHCONV=1 ClaudeScope set /SmartDashboard/SetSpeed=2.5 --session <id>
```

> **SendableChooser warning**: Do NOT set `<prefix>/active` — the robot re-publishes that field every loop and will immediately overwrite your value. To change a chooser selection, write to `<prefix>/selected`:
> ```bash
> MSYS_NO_PATHCONV=1 ClaudeScope set "/SmartDashboard/Auto Choices/selected=Depot" --session <id>
> ```
> ClaudeScope will return an error if you try to set `/active` on a `String Chooser` topic, telling you the correct key to use.

### Full machine-readable schema
```bash
ClaudeScope help
```

---

## Common Analysis Patterns

### Superstructure state time analysis
```bash
# Get all state transitions across whole log
MSYS_NO_PATHCONV=1 ClaudeScope range /RealOutputs/Superstructure/CurrentSuperState --session <id> --start 0 --end 0
# Parse returned array: group by value, sum timestamp deltas to get time-in-state
```

### Swerve tracking error
```bash
MSYS_NO_PATHCONV=1 ClaudeScope range /RealOutputs/Drive/Module0/TurnSetpointRads --session <id>
MSYS_NO_PATHCONV=1 ClaudeScope range /RealOutputs/Drive/Module0/TurnPositionRads --session <id>
# Compute per-point error; group by setpoint magnitude to identify velocity-proportional lag
```

### Find match periods
```bash
MSYS_NO_PATHCONV=1 ClaudeScope find-bool /RealOutputs/Robot/DSAttached true --session <id>
```

---

## AdvantageKit Log Notes

- Fields follow the pattern `/RealOutputs/<Subsystem>/<Field>` and `/RobotState/<Field>`
- Struct fields (e.g. `SwerveModuleState`) are logged as `structschema` type (raw bytes); decode manually
- Use `info` to discover all field names before querying
- String fields hold enum values (e.g. superstructure states like `"IDLE"`, `"SHOOTING"`)
