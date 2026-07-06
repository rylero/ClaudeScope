package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// RunCommand routes CLI args to the correct daemon endpoint.
func RunCommand(args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no subcommand provided")
	}
	switch args[0] {
	case "connect":
		return runConnect(args[1:])
	case "load":
		return runLoad(args[1:])
	case "disconnect":
		return runDisconnect(args[1:])
	case "sessions":
		return runSessions(args[1:])
	case "info":
		return runInfo(args[1:])
	case "get":
		return runGet(args[1:])
	case "range":
		return runRange(args[1:])
	case "find-bool":
		return runFindBool(args[1:])
	case "find-threshold":
		return runFindThreshold(args[1:])
	case "stats":
		return runStats(args[1:])
	case "query":
		return runQuery(args[1:])
	case "set":
		return runSet(args[1:])
	case "help":
		return runHelp()
	case "version":
		return runVersion()
	default:
		return nil, fmt.Errorf("unknown subcommand: %s. Run 'help' for usage", args[0])
	}
}

func parseFlags(args []string) (positional []string, flags map[string]string) {
	flags = make(map[string]string)
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") && i+1 < len(args) {
			key := strings.TrimPrefix(args[i], "--")
			flags[key] = args[i+1]
			i++
		} else {
			positional = append(positional, args[i])
		}
	}
	return
}

func flagInt64(flags map[string]string, key string, defaultVal int64) int64 {
	s, ok := flags[key]
	if !ok {
		return defaultVal
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultVal
	}
	return v
}

func flagFloat64(flags map[string]string, key string) (float64, error) {
	s, ok := flags[key]
	if !ok {
		return 0, fmt.Errorf("missing required flag --%s", key)
	}
	return strconv.ParseFloat(s, 64)
}

func runConnect(args []string) ([]byte, error) {
	pos, _ := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf("usage: connect <ip>")
	}
	return DoRequest(http.MethodPost, "/connect", map[string]string{"ip": pos[0]})
}

func runLoad(args []string) ([]byte, error) {
	pos, _ := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf("usage: load <path.wpilog>")
	}
	return DoRequest(http.MethodPost, "/load", map[string]string{"path": pos[0]})
}

func runDisconnect(args []string) ([]byte, error) {
	_, flags := parseFlags(args)
	id := flags["session"]
	return DoRequest(http.MethodPost, "/disconnect", map[string]string{"session_id": id})
}

func runSessions(_ []string) ([]byte, error) {
	return DoRequest(http.MethodGet, "/sessions", nil)
}

func runInfo(args []string) ([]byte, error) {
	_, flags := parseFlags(args)
	id := flags["session"]
	return DoRequest(http.MethodGet, "/info?session="+id, nil)
}

func runGet(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf("usage: get <key> [key2 ...] --session <id> [--time <us>]")
	}
	id := flags["session"]
	return DoRequest(http.MethodPost, "/get", map[string]any{
		"session_id": id,
		"keys":       pos,
		"time":       flagInt64(flags, "time", 0),
	})
}

func runRange(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf("usage: range <key> [key2 ...] --session <id> [--start <us>] [--end <us>]")
	}
	id := flags["session"]
	return DoRequest(http.MethodPost, "/range", map[string]any{
		"session_id": id,
		"keys":       pos,
		"start":      flagInt64(flags, "start", 0),
		"end":        flagInt64(flags, "end", 0),
	})
}

func runFindBool(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 2 {
		return nil, fmt.Errorf("usage: find-bool <key> <true|false> --session <id>")
	}
	id := flags["session"]
	return DoRequest(http.MethodPost, "/find-bool", map[string]any{
		"session_id": id,
		"key":        pos[0],
		"value":      pos[1] == "true",
	})
}

func runFindThreshold(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf("usage: find-threshold <key> [--min <n>] [--max <n>] --session <id>")
	}
	id := flags["session"]
	var err error
	_, hasMin := flags["min"]
	_, hasMax := flags["max"]
	if !hasMin && !hasMax {
		return nil, fmt.Errorf("find-threshold requires at least one of --min or --max")
	}
	// Unbounded sides default to ±max float so the daemon's min <= v <= max
	// comparison degenerates to a one-sided test.
	minVal := -math.MaxFloat64
	maxVal := math.MaxFloat64
	if hasMin {
		if minVal, err = flagFloat64(flags, "min"); err != nil {
			return nil, err
		}
	}
	if hasMax {
		if maxVal, err = flagFloat64(flags, "max"); err != nil {
			return nil, err
		}
	}
	return DoRequest(http.MethodPost, "/find-threshold", map[string]any{
		"session_id": id,
		"key":        pos[0],
		"min":        minVal,
		"max":        maxVal,
	})
}

func runStats(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf("usage: stats <key> --session <id> [--start <us>] [--end <us>]")
	}
	id := flags["session"]
	return DoRequest(http.MethodPost, "/stats", map[string]any{
		"session_id": id,
		"key":        pos[0],
		"start":      flagInt64(flags, "start", 0),
		"end":        flagInt64(flags, "end", 0),
	})
}

func runQuery(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf(`usage: query "<pipe string>" --session <id> [--start <us>] [--end <us>]`)
	}
	id := flags["session"]
	return DoRequest(http.MethodPost, "/query", map[string]any{
		"session_id": id,
		"query":      pos[0],
		"start":      flagInt64(flags, "start", 0),
		"end":        flagInt64(flags, "end", 0),
	})
}

func runVersion() ([]byte, error) {
	return json.Marshal(map[string]string{"version": Version})
}

func runHelp() ([]byte, error) {
	type param struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Required bool   `json:"required"`
		Desc     string `json:"desc"`
	}
	type cmd struct {
		Name    string  `json:"name"`
		Desc    string  `json:"desc"`
		Usage   string  `json:"usage"`
		Params  []param `json:"params"`
		Returns string  `json:"returns"`
	}
	type schema struct {
		Tool     string   `json:"tool"`
		Version  string   `json:"version"`
		Notes    []string `json:"notes"`
		Commands []cmd    `json:"commands"`
	}

	s := schema{
		Tool:    "ClaudeScope",
		Version: Version,
		Notes: []string{
			"Daemon auto-starts on first use; port 5812.",
			"All timestamps are microseconds (µs) since log start.",
			"Negative start/end in range/stats = offset from end of log (e.g. -5000000 = last 5 s).",
			"end=0 means end of log.",
			"time=0 in get means latest value.",
			"On Git Bash/MSYS2, set MSYS_NO_PATHCONV=1 before keys that start with '/'.",
			"--session is optional when exactly one session is active; it defaults to that session. With multiple sessions, an AMBIGUOUS_SESSION error lists the IDs.",
			"Global flag: append --out <file> to any command to write its output to a file instead of stdout.",
			"Workflow: load → query (--session optional) → disconnect when done.",
		},
		Commands: []cmd{
			{
				Name:    "load",
				Desc:    "Parse a .wpilog file and open a read-only session.",
				Usage:   "ClaudeScope load <path.wpilog>",
				Params:  []param{{Name: "path", Type: "string", Required: true, Desc: "Absolute path to .wpilog file"}},
				Returns: `{"session_id":"<id>","fields":[{"key":"...","type":"double|boolean|string|..."},...]}`,
			},
			{
				Name:    "connect",
				Desc:    "Connect to a live NetworkTables 4 instance.",
				Usage:   "ClaudeScope connect <robot-ip>",
				Params:  []param{{Name: "ip", Type: "string", Required: true, Desc: "Robot IP or hostname (e.g. 10.0.0.2)"}},
				Returns: `{"session_id":"<id>"}`,
			},
			{
				Name:    "disconnect",
				Desc:    "Close a session and free resources.",
				Usage:   "ClaudeScope disconnect --session <id>",
				Params:  []param{{Name: "--session", Type: "string", Required: false, Desc: "Session ID from load/connect; optional when exactly one session is active"}},
				Returns: `{}`,
			},
			{
				Name:    "sessions",
				Desc:    "List all active sessions (useful to recover a session ID after losing it).",
				Usage:   "ClaudeScope sessions",
				Params:  []param{},
				Returns: `{"sessions":[{"id":"<id>","type":"log|live","label":"<path-or-ip>","idle_seconds":<n>},...]}`,
			},
			{
				Name:    "info",
				Desc:    "List all fields and time range for a session.",
				Usage:   "ClaudeScope info --session <id>",
				Params:  []param{{Name: "--session", Type: "string", Required: false, Desc: "Session ID; optional when exactly one session is active"}},
				Returns: `{"fields":[{"key":"...","type":"..."}],"start":<us>,"end":<us>}`,
			},
			{
				Name:  "get",
				Desc:  "Get value(s) at a specific timestamp. time=0 returns latest.",
				Usage: "ClaudeScope get <key> [key2 ...] --session <id> [--time <us>]",
				Params: []param{
					{Name: "keys", Type: "[]string", Required: true, Desc: "One or more field keys (positional)"},
					{Name: "--session", Type: "string", Required: false, Desc: "Session ID; optional when exactly one session is active"},
					{Name: "--time", Type: "int64", Required: false, Desc: "Timestamp µs; 0=latest"},
				},
				Returns: `{"<key>":{"timestamp":<us>,"value":<any>},...}`,
			},
			{
				Name:  "range",
				Desc:  "Get all data points for key(s) between start and end.",
				Usage: "ClaudeScope range <key> [key2 ...] --session <id> [--start <us>] [--end <us>]",
				Params: []param{
					{Name: "keys", Type: "[]string", Required: true, Desc: "One or more field keys"},
					{Name: "--session", Type: "string", Required: false, Desc: "Session ID; optional when exactly one session is active"},
					{Name: "--start", Type: "int64", Required: false, Desc: "Start µs; 0=beginning; negative=offset from end"},
					{Name: "--end", Type: "int64", Required: false, Desc: "End µs; 0=end of log; negative=offset from end"},
				},
				Returns: `{"<key>":[{"timestamp":<us>,"value":<any>},...]}`,
			},
			{
				Name:  "find-bool",
				Desc:  "Find all time ranges where a boolean field equals a given value.",
				Usage: "ClaudeScope find-bool <key> <true|false> --session <id>",
				Params: []param{
					{Name: "key", Type: "string", Required: true, Desc: "Boolean field key"},
					{Name: "value", Type: "bool", Required: true, Desc: "true or false"},
					{Name: "--session", Type: "string", Required: false, Desc: "Session ID; optional when exactly one session is active"},
				},
				Returns: `[{"start":<us>,"end":<us>},...]`,
			},
			{
				Name:  "find-threshold",
				Desc:  "Find all time ranges where a numeric field is within [min, max]. At least one bound is required; omit one for a one-sided test.",
				Usage: "ClaudeScope find-threshold <key> [--min <n>] [--max <n>] --session <id>",
				Params: []param{
					{Name: "key", Type: "string", Required: true, Desc: "Numeric field key"},
					{Name: "--min", Type: "float64", Required: false, Desc: "Lower bound (inclusive); omit for no lower bound"},
					{Name: "--max", Type: "float64", Required: false, Desc: "Upper bound (inclusive); omit for no upper bound"},
					{Name: "--session", Type: "string", Required: true, Desc: "Session ID"},
				},
				Returns: `[{"start":<us>,"end":<us>},...]`,
			},
			{
				Name:  "stats",
				Desc:  "Compute descriptive statistics for a numeric field over a time window.",
				Usage: "ClaudeScope stats <key> --session <id> [--start <us>] [--end <us>]",
				Params: []param{
					{Name: "key", Type: "string", Required: true, Desc: "Numeric field key"},
					{Name: "--session", Type: "string", Required: false, Desc: "Session ID; optional when exactly one session is active"},
					{Name: "--start", Type: "int64", Required: false, Desc: "Start µs; 0=beginning"},
					{Name: "--end", Type: "int64", Required: false, Desc: "End µs; 0=end of log"},
				},
				Returns: `{"mean":<f>,"median":<f>,"min":<f>,"max":<f>,"q1":<f>,"q3":<f>,"avg_delta":<f/s>,"min_delta":<f/s>,"max_delta":<f/s>}`,
			},
			{
				Name:  "query",
				Desc:  "Run a pipe query joining multiple fields on a shared, forward-filled timestamp axis. This is a SUBSET OF SPLUNK SPL: write standard SPL and it works. Supported: where (alias: search), eval <name> = <expr>, rex field=<field> \"<regex>\", stats <agg>(<field>) [as <alias>] [by <field>...], timechart span=<duration> <agg>(<field>)... [by <field>], table (alias: fields), sort [-]<field>, head N, tail N, and the ClaudeScope-only extensions 'ranges' (must be last; collapses matching rows into [{start,end}] intervals) and 'transaction start=<expr> end=<expr>' (groups rows into episodes bounded by the two predicates, stamping a transactionID column; rows outside any episode are dropped). Aggregates: avg,min,max,sum,count,median,stdev,p50,p90,p99. timechart span units: us,ms,s,m,h,d (e.g. span=500ms, span=1m). eval operators: + - * / and functions abs,round,sqrt,ceil,floor,min,max,pow (put spaces around operators). rex uses (?<name>...) named groups. Comparison ops: > < >= <= == (= also works) != and or NOT. '_time' aliases the Timestamp column. NOT SUPPORTED (errors): dedup, subsearches, lookup, join.",
				Usage: `ClaudeScope query "<pipe string>" --session <id> [--start <us>] [--end <us>]`,
				Params: []param{
					{Name: "query", Type: "string", Required: true, Desc: `SPL-subset pipe query, e.g. "where CurrentA > 40 and CurrentB > 40 | stats avg(BatteryVoltage) by Subsystem"`},
					{Name: "--session", Type: "string", Required: false, Desc: "Session ID; optional when exactly one session is active"},
					{Name: "--start", Type: "int64", Required: false, Desc: "Start µs; 0=beginning; negative=offset from end"},
					{Name: "--end", Type: "int64", Required: false, Desc: "End µs; 0=end of log; negative=offset from end"},
				},
				Returns: `{"result":[{"Timestamp":<us>,"<field>":<value>,...},...]} or {"result":[{"start":<us>,"end":<us>},...]} when the pipeline ends in 'ranges'`,
			},
			{
				Name:  "set",
				Desc:  "Publish key/value pairs to a live NT session. Fails on log sessions.",
				Usage: "ClaudeScope set <key>=<val> [key2=val2 ...] --session <id>",
				Params: []param{
					{Name: "pairs", Type: "[]string", Required: true, Desc: "key=value pairs; value auto-parsed as float, bool, or string"},
					{Name: "--session", Type: "string", Required: false, Desc: "Session ID (must be a live session); optional when exactly one session is active"},
				},
				Returns: `{}`,
			},
		},
	}
	enc, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	// json.Marshal escapes < > & by default; unescape for readability
	enc = []byte(strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(string(enc)))
	return enc, nil
}

func runSet(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf("usage: set <key>=<val> [key2=val2 ...] --session <id>")
	}
	id := flags["session"]
	pairs := make(map[string]any, len(pos))
	for _, kv := range pos {
		idx := strings.Index(kv, "=")
		if idx < 0 {
			return nil, fmt.Errorf("invalid key=value pair: %q", kv)
		}
		key, val := kv[:idx], kv[idx+1:]
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			pairs[key] = f
		} else if b, err := strconv.ParseBool(val); err == nil {
			pairs[key] = b
		} else {
			pairs[key] = val
		}
	}
	return DoRequest(http.MethodPost, "/set", map[string]any{
		"session_id": id,
		"pairs":      pairs,
	})
}
