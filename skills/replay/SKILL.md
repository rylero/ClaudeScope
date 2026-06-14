---
name: replay
description: Use when asked to replay an FRC log file through the robot code, re-run a real-world or simulation .wpilog deterministically, regenerate AdvantageKit outputs, or compare recorded outputs against recomputed ones. Trigger on: "/replay", "replay the log", "run replay", "deterministic replay", "re-run this log", "replay this match".
---

# Replay — AdvantageKit Deterministic Log Replay

Feeds an existing `.wpilog` (from a real robot or a sim run) back through the robot code in AdvantageKit **REPLAY** mode. The code re-executes against the logged inputs as fast as possible, producing a new log of recomputed outputs. Then loads that output log into ClaudeScope to compare recorded vs. recomputed behavior.

Use this to test code changes against real match data, reproduce a field bug offline, or verify a fix would have changed an outcome — without hardware and without re-running the original scenario.

## Trigger

`/replay <log-path> [goal]` — log-path is the `.wpilog` to replay; goal is optional natural language describing what to investigate.

- `/replay "C:/logs/match_qual_12.wpilog"`
- `/replay "logs/akit_26-05-08.wpilog" did the new shooter PID hold setpoint better`
- `/replay "C:/logs/auto_fail.wpilog" why did the robot drift off the auto path`

---

## How replay works

AdvantageKit replay is **deterministic**: every input the robot code read during the original run (joystick, sensors, NT, timers) was logged. In REPLAY mode the code reads those same inputs back instead of real hardware, so the logic re-runs identically — except for any code you changed since the log was recorded.

| Mode | Source | Output |
|---|---|---|
| REAL / SIM (original run) | hardware or physics sim | `/RealOutputs/...`, `/RobotState/...` |
| REPLAY (this skill) | the input `.wpilog` | `/ReplayOutputs/...` written to `<log>_sim.wpilog` |

The output log contains **both** the original `/RealOutputs` (carried from the source) and the freshly computed `/ReplayOutputs`. Diffing the two shows exactly what your current code does differently from what was recorded.

> If the code is unchanged since the log was recorded, `/ReplayOutputs` should match `/RealOutputs` closely (small float diffs from non-determinism are normal). Divergence = the effect of your code changes.

---

## Step 0 — Prerequisites Check

**Do this before running anything.**

1. **Confirm the input log exists** and is a `.wpilog` produced by an AdvantageKit (`LoggedRobot`) project. Logs from a different robot project will replay against the wrong code and produce garbage.
2. **Confirm the robot project supports REPLAY.** Read `<robot-project>/src/main/java/frc/robot/Robot.java` — the constructor must contain a `case REPLAY:` block that calls `setUseTiming(false)`, `Logger.setReplaySource(new WPILOGReader(logPath))`, and writes a `WPILOGWriter(LogFileUtil.addPathSuffix(logPath, "_sim"))`. The 2026 template already has this. If missing, the project is not replay-capable — report this to the user rather than patching robot internals.
3. **Read `<robot-project>/src/main/java/frc/robot/Constants.java`** and note the current `simMode` value so you can restore it in Phase 4.

---

## Phase 1 — Configure & Run

1. **Find the robot project path** — check memory and CLAUDE.md first; if not found, search upward from cwd for `build.gradle` containing `GradleRIO`; ask the user if still not found.

2. **Switch the project into REPLAY mode** — edit `Constants.java`:
   ```java
   public static final Mode simMode = Mode.REPLAY;
   ```
   > `currentMode = RobotBase.isReal() ? Mode.REAL : simMode`. On a desktop sim run `isReal()` is false, so `simMode` decides between physics-sim and replay. This is a **temporary** change — revert it in Phase 4.

3. **Run the replay** using the **PowerShell tool** with `run_in_background: true` (the Bash tool / Git Bash cannot run `.bat` files). Point AdvantageKit at the input log with the `AKIT_LOG_PATH` environment variable — this is what `LogFileUtil.findReplayLog()` reads, and it avoids the AdvantageScope file-picker dialog that would otherwise block headless:
   ```powershell
   $env:AKIT_LOG_PATH = "C:\path\to\input.wpilog"
   .\gradlew.bat simulateJava --console=plain 2>&1
   ```

4. **Wait for the run to exit on its own.** Replay runs as fast as possible (`setUseTiming(false)`) and the process terminates when the input log is exhausted — unlike `/simulate`, you do **not** enable the robot or drive it; it just plays the log through. First build is slow (60–90s Gradle + JVM); replay of the log itself is usually seconds. Watch for `BUILD SUCCESSFUL` / process exit, or a stack trace.

   Common failure: `Could not find log file` → `AKIT_LOG_PATH` was not set or the path is wrong. A struct/schema mismatch error usually means the log came from a different/older code version.

---

## Phase 2 — Locate the Output

The replay writes `<input-name>_sim.wpilog` **next to the input log** (from `addPathSuffix(logPath, "_sim")`). Confirm it exists and is newer than the input:
```powershell
Get-Item "C:\path\to\input_sim.wpilog" | Select-Object FullName, Length, LastWriteTime
```

---

## Phase 3 — Analyze with ClaudeScope

Load the **output** log and investigate, following the `/scope` skill's command reference.

```powershell
ClaudeScope load "C:/path/to/input_sim.wpilog"   # returns session_id
```

**Replay-specific analysis — compare recorded vs. recomputed:**

| Want to know | How |
|---|---|
| Did my code change anything? | Diff `/ReplayOutputs/<Subsystem>/<Field>` against `/RealOutputs/<Subsystem>/<Field>` over the same timestamps with `range` |
| Where did behavior diverge first | Walk both series forward; the first timestamp where they differ beyond float noise is the divergence point |
| Did the fix improve tracking | Compute setpoint-vs-actual error on `/ReplayOutputs` and on `/RealOutputs`; compare the two error profiles |

```powershell
# Recorded vs recomputed for the same signal
MSYS_NO_PATHCONV=1 ClaudeScope range /RealOutputs/Drive/LeftVelocity --session <id> --start 0 --end 0
MSYS_NO_PATHCONV=1 ClaudeScope range /ReplayOutputs/Drive/LeftVelocity --session <id> --start 0 --end 0
# Pair points by timestamp, subtract, flag the first meaningful delta
```

> If you only see `/RealOutputs` and no `/ReplayOutputs`, the code recorded nothing new — confirm the run actually entered REPLAY mode (Constants edit applied, build picked it up) and that the subsystem produces `recordOutput` calls.

---

## Phase 4 — Report & Restore

1. **Report** against the goal: state whether recomputed outputs matched or diverged, the divergence timestamp(s), the field(s) affected, and the magnitude. Tie divergence back to specific code changes when known. Propose concrete next steps.
2. **Disconnect ClaudeScope:**
   ```powershell
   ClaudeScope disconnect --session <id>
   ```
3. **Restore `Constants.java`** to its original `simMode` value (Step 0 captured it). **This is mandatory** — leaving `simMode = Mode.REPLAY` breaks normal `/simulate` runs.
4. Terminate any lingering process if it did not self-exit:
   ```powershell
   Stop-Process -Name "java" -ErrorAction SilentlyContinue
   ```

---

## Constraints

| Constraint | Detail |
|---|---|
| Windows build | PowerShell tool + `.\gradlew.bat`, never the Bash tool (Git Bash can't run `.bat`) |
| Log must match code | Replay re-runs *current* code against *logged inputs*; a log from a different project/season will diverge meaninglessly or crash on schema mismatch |
| Self-terminating | Replay exits when the log ends — do not enable/drive the robot (that's `/simulate`) |
| Output location | `<input>_sim.wpilog`, written beside the input, not in `logs/` |
| `AKIT_LOG_PATH` | Required to run headless; without it `findReplayLog()` opens a blocking file dialog |
| Restore Constants | Always revert `simMode` in Phase 4 — temporary change only |
| Path prefix | `MSYS_NO_PATHCONV=1` only needed in Bash tool, not PowerShell |
