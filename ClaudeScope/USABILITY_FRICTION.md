# ClaudeScope — Usability Friction Log

Findings from an agent-driven usability pass (Opus 4.8 as the ClaudeScope consumer).
Real log: `2026-Robot/logs/akit_26-05-08_13-23-43.wpilog` (30.5 s, 390 fields). Binary: `version: dev`.

Severity: 🔴 bug (wrong/blocking) · 🟠 trap (correct-but-surprising) · 🟡 polish.

---

## 🔴 F1 — `eval` math functions hard-error on `nil` instead of propagating null

**Repro**
```bash
# velocity is null at t=265787 (log start, before its first sample)
ClaudeScope query "eval e = /DriverStation/Enabled | eval spd = abs(/Drive/Module0/DriveVelocityRadPerSec) | table _time spd | head 3"
# -> COMMAND_FAILED: argument 1 of abs() is not numeric: not a numeric value: <nil>
```
**Why it bites:** a single-field pipeline evaluates on that field's own timestamps (no nils), so `eval abs(v)` works. The moment you join a *second* field (here `/DriverStation/Enabled` for a `transaction`/correlation), the forward-fill axis now includes rows *before* the first field's first sample, where its value is `nil`. `abs(nil)` then throws and kills the whole query. So the exact same `eval` that worked in isolation breaks when you add correlation — which is precisely when you reach for the pipe language.
**Real SPL behavior:** `abs(null)` → `null`, no error. ClaudeScope deviates.
**Fix:** math funcs (`abs round sqrt ceil floor min max pow`) should return `nil`/null on `nil` input, not error. Applies to any function taking a numeric arg.
**Impact:** blocks the headline use case (multi-field `eval`+`transaction`+`stats`).

## 🔴 F2 — `where <field> != 0` does not filter out `nil` rows

**Repro**
```bash
ClaudeScope query "where /Drive/Module0/DriveVelocityRadPerSec != 0 | eval spd = abs(/Drive/Module0/DriveVelocityRadPerSec) | ..."
# -> still COMMAND_FAILED on <nil> ; the null row survived the != 0 filter
```
**Why it bites:** the intuitive guard against F1 (`where v != 0`, or any comparison) does **not** drop nulls — a `nil` row passes a `!= 0` test — so users can't easily work around F1 either. There is no documented `isnotnull()` / `isnull()` predicate.
**Fix:** (a) make comparison operators treat `nil` as non-matching (SQL/SPL three-valued logic: `nil != 0` is not true), and/or (b) add `isnotnull(field)` / `isnull(field)` predicates and document them.

## 🟠 F3 — `eval` columns don't persist across `query` invocations (each call is stateless)

**Repro:** define `eval spd = abs(v)` in one `query`, reference `spd` in the next `query` → `key not found: spd`.
**Assessment:** correct/expected (sessions hold *data*, not *derived columns*), but the error `key not found: spd` doesn't hint that eval columns are per-pipeline. A one-line hint ("no such field or eval column in this pipeline; eval columns don't carry across invocations") would save a confused retry.

## 🟠 F4 — `load` dumps the entire field list inline

**Repro:** `ClaudeScope load <file>` returns `{"fields":[...390 entries...]}` in one blob.
**Why it bites (agents especially):** hundreds of `{key,type}` objects = a large token dump on every load, most of it `/.schema/...` noise. An agent almost always follows up with `search-fields`/`info` anyway.
**Fix:** return `{"session_id","field_count", "fields": <first N>, "truncated": true}` or gate the full list behind a `--fields` flag. `session_id` should lead the object regardless (see F5).

## 🟡 F5 — `load` response omits `session_id` up front / buries it after the field firehose
The `session_id` is the one thing the caller needs next, but the field array dominates the payload. Put `session_id` first (and ideally make it survivable via `sessions`, which already works well — good).

## 🟡 F6 — `range` on a change-logged field returns a single sample with no note
`range <field> --start 0 --end 3000000` on an all-zero/rarely-changing field returns one point. Correct (WPILOG logs on change), but a `"note":"value constant over range"` or an explicit boundary sample would stop the reader suspecting a bug. (I did suspect a bug and had to cross-check with the `stats` verb.)

## 🟡 F7 — `stats` verb's `--start 0 --end 0` is unnecessary boilerplate in the docs
`ClaudeScope stats <field>` with **no** range flags returns the full-log stats correctly (verified identical to `--start 0 --end 0`). Every skill example threads `--start 0 --end 0` anyway, teaching readers to type noise. Either document that the range is optional (defaults to whole log) or drop it from the canonical examples.

## 🟡 F8 — no single-call `|value| > x` (absolute-value threshold)
`find-threshold --min 40` catches only the positive side; symmetric motion (`abs(v) > 40`) needs the `eval abs | where | ranges` pipe. Fine, but a common ask — worth an example in the skill, or a `--abs` flag on `find-threshold`.

---

## What worked well (keep)
- `sessions` recovering the ID after the fact — exactly right for post-compaction agents.
- Default-session behavior (`--session` optional with one session) — removes real friction.
- SPL correctness where data is present: `stats avg/max/min/count` **matched the dedicated `stats` verb exactly** (mean `-2.2395`, max `81.949`, n=695).
- `timechart span=5s` bucketed cleanly (7 buckets over 30 s).
- `eval abs(v) | where spd > 40 | ranges` produced correct motion intervals in the single-field case.
- `table _time v | sort -v | head 3` — top-N pattern is ergonomic and worked first try.
- `find-bool /DriverStation/Enabled true` → clean single interval.
- `MSYS_NO_PATHCONV=1` guidance in the skill was necessary and correct on this Git Bash setup.

---

## Live-NT run (2nd pass — `thefrc-suite:simulate` against a headless sim)

Drove the real `2026-RobotRevamp` sim over live NT4 to verify the FrcPhysics-ported shooter/pivot.

**Worked well (keep):**
- `connect 127.0.0.1` → session, poll loop, `set /Sim/...`, `get --time 0`, `stats --start -Ns`, and **`query` with `eval` on a LIVE session** all worked first try. The `eval pos_rot = field / 6.283185 | eval err_rot = target - pos_rot | table ... | tail 3` correlated two live fields cleanly — no F1 nil issue here because both fields were populated by query time.
- Negative-offset windows (`--start -5000000 --end 0`) are the right ergonomic for "last N seconds of a live feed."
- `get` returning the carry-over timestamp let me confirm settle (min=max=mean, avg_delta=0) at a glance.

**🟠 F9 — `simulate` skill needs robot-side code injection with no joystick.** Headless sim has no joystick, so exercising a button-driven superstructure required hand-adding an NT→`setWanted` hook to `RobotContainer` (on top of the skill's documented `Robot.java` DS-enable hook). The skill covers enabling the robot but not *commanding subsystems* whose only trigger is an operator button. Worth a skill section: "if your setpoints are joystick/button-driven, add a temporary NT command-injection shim," or ship a reusable `SimCommandInjector` helper. (Belongs to the `simulate` skill, not ClaudeScope core — filed here since it surfaced same run.)

---

## Token efficiency & ergonomics (3rd pass — reducing agent token cost)

The queries are ~3 lines; the **responses** dominate token cost. These target that.

## 🟠 F10 — numeric over-precision on every numeric result *(highest ROI)*
Values return full float64: `496.752066502583`, `2.089281833100243`, `0.0008137858267354137`. Agents reason fine at ~6 sig figs. A `range`/`timechart` returns hundreds of these — the trailing digits are pure token waste.
**Fix:** `--precision N` flag on numeric-returning verbs (`get stats range timechart query`), and/or round to ~6 sig figs by default. Roughly halves digits per number. Cheapest big win — a formatter change, no query-engine touch.

## 🟠 F11 — JSON default is verbose for multi-row results; make `--format csv` the documented default for tabular verbs
JSON repeats every key on every row (`{"Timestamp":..,"pos_rot":..,"err_rot":..}` × N). CSV writes the header once → 40–60% cut on `range`/`table`/`timechart`/multi-row `query`. `--format csv` already exists — under-advertised.
**Fix:** document CSV as the go-to for tabular output (JSON only for single `get`); consider auto-CSV when row-count > threshold.

## 🟠 F12 — leaf-name field resolution
Full AdvantageKit paths (`/AdvantageKit/IntakePivot/PivotPosition`, 40+ chars) dominate query text and are often typed twice. If a trailing path segment resolves uniquely, accept the leaf:
```
eval pos = PivotPosition / tau | eval err = TargetPositionRot - pos | table _time pos err | tail 3
```
~40% shorter, far more readable. Ambiguous leaf → error listing the full-path candidates.

## 🟡 F13 — microsecond-int timestamps are long and unreadable
`30036400` on every row. Optional `--time-unit s|ms|us` → `30.04`. Shorter tokens + human-readable for match-relative reasoning.

## 🟡 F14 — built-in constants + unit functions
`6.283185` is a magic number the caller must hardcode correctly. Provide `tau`/`pi` constants and `rad2rot() rot2deg() deg2rad()` in `eval`. Removes an error-prone literal and shortens queries.

## 🟡 F15 — surface macros prominently (already shipped, underused)
`~/.claudescope/macros.json` can collapse the convert+error pipe into one backticked token `` `piverr` ``. The feature exists but is buried at the end of the query docs. Move it up; add an FRC-flavored example (settle-error, brownout).

## 🟠 F17 — `struct:`/`structschema` fields return raw base64, undecodable inline
`get` on any `struct:Pose2d` / `struct:Rotation2d` / `struct:ChassisSpeeds` / `struct:SwerveModuleState[]` returns `{"value":"+f/rQnzl..."}` (base64 of the packed doubles). During a swerve verification I had to hand-decode every pose/heading/steer-angle with a PowerShell `[BitConverter]::ToDouble` helper — the single most common thing you want to read on a drivetrain (robot pose, module angles, chassis speeds) is exactly what's opaque. WPILib ships the struct schema (`/AdvantageKit/.schema/struct:Pose2d` is right there in the field list, and the packing is fixed little-endian doubles). **Fix:** decode known WPILib structs (Pose2d/Pose3d/Rotation2d/Translation2d/ChassisSpeeds/SwerveModuleState[]) to named fields using the published schema; at minimum offer `--decode` to emit the double array. Turns "unreadable blob" into `{x,y,theta}`. Highest-value telemetry gap found this session.

## 🟢 F16 — domain verb: `settle` / `step` (bigger idea)
FRC telemetry work is mostly "did it reach setpoint and hold." `settle <field> --target T` → `{final, band, settle_time_ms, steady_err}` in one call, replacing the manual `stats`+`range`+eyeball-`avg_delta` dance done on both mechanisms this session. High-value convenience verb.

---

## Suggested priority
1. **F1 + F2** (blocking the multi-field pipe language — the plugin's differentiator).
2. **F10 + F11 + F12** (token cost on *every* query; cheapest big wins — formatter/resolver changes, no engine rewrite). ← implementing now.
3. **F4** (token cost on every session; hits agents hardest).
4. F13, F14, F15 (query ergonomics), F16 (`settle` verb).
5. F3, F5, F6, F9 (error-message / skill polish).

---

## Resolution log (branch `feat/scope-struct-decode-parquet-range`)

Strategic pivot decided this pass: **the SPL *transform* layer (eval/where/timechart/rex/stats-by) is frozen** — correct where data is present, but not receiving further investment. Rather than reimplement SQL/SPL null semantics (F1/F2) and per-response token trimming (F10) in ~3k LOC of Go, the direction is **cs → parquet → pandas**: pull raw series and transform in pandas, which handles nulls and formatting correctly for free. The SPL *access/correlation* surface (multi-field forward-fill join, `transaction`, `ranges`) and the dedicated verbs stay first-class.

- **F17 — DONE.** WPILib structs now decode to named-field objects in `get`/`range` (both log and live NT paths), verified on a real log: `Pose2d → {x,y,theta}`, `Rotation2d → {value}`, `ChassisSpeeds → {vx,vy,omega}`, `SwerveModuleState[] → [{speed,angle},...]`, plus Pose3d/Translation/Transform/Twist/Quaternion/SwerveModulePosition. Unknown or length-mismatched payloads fall back to raw bytes. (`session/structs.go`)
- **F11 — DONE for `range`.** `range` now takes `--format csv|parquet`, emitting long-format `{key,timestamp,value}` rows straight into `pandas.read_csv/read_parquet`. Documented as the preferred raw-series path.
- **F10/F13 (partial) — DONE for CSV.** CSV float formatting switched to fixed notation, so large timestamps render `30046400` not `3.00464e+07` and integral values drop the trailing `.0`.
- **Python `Session` — extended.** Added `range_df(*keys, pivot=False)` (parquet → tidy DataFrame) as the documented "python control over CS" path; `query()`/`range()` docstrings now point transform work at pandas.
- **F1, F2, F10 (JSON precision), F12, F14, F15, F16** — intentionally NOT pursued in the SPL engine per the freeze. F1/F2 stop mattering once transforms move to pandas.

### Checkout note
This repo (`TheFRCSuite`, origin `github.com/rylero/TheFRCSuite`) had two local clones: `Dev/TheFRCSuite` (behind, ~PR #25, no parquet/csv/python) and `Dev/TheFRCSuite-main` (ahead, has PRs #26/#28/#29 — csv, `--follow`, `search-fields`, `claudescope-py` parquet). This work landed in `-main`. The stale `Dev/TheFRCSuite` clone can be deleted once you confirm nothing local-only remains there (this friction log was its only unique file and is now ported here).
