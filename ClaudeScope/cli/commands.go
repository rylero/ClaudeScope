package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// marshalJSON encodes v without HTML-escaping < > & so field names/values
// containing those characters (e.g. an SPL comparison operator quoted back
// in an error) round-trip unmangled instead of becoming <-style escapes.
func marshalJSON(v any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

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
	case "search-fields":
		return runSearchFields(args[1:])
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
	case "query-multi":
		return runQueryMulti(args[1:])
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

// flagBool requires an explicit value (--all true, not bare --all) so it
// doesn't need special-casing in parseFlags, which always consumes the token
// after a "--flag" as that flag's value.
func flagBool(flags map[string]string, key string) bool {
	v, ok := flags[key]
	if !ok {
		return false
	}
	b, _ := strconv.ParseBool(v)
	return b
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

// runSearchFields filters the session's field list (from the existing /info
// endpoint) by a case-insensitive substring match, done client-side since
// /info already returns the full list and field counts don't warrant a
// server-side fuzzy-match endpoint.
func runSearchFields(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf("usage: search-fields <substr> --session <id>")
	}
	id := flags["session"]
	data, err := DoRequest(http.MethodGet, "/info?session="+id, nil)
	if err != nil {
		return nil, err
	}
	var info struct {
		Fields []struct {
			Key  string `json:"key"`
			Type string `json:"type"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse info response: %w", err)
	}
	substr := strings.ToLower(pos[0])
	matches := make([]struct {
		Key  string `json:"key"`
		Type string `json:"type"`
	}, 0)
	for _, f := range info.Fields {
		if strings.Contains(strings.ToLower(f.Key), substr) {
			matches = append(matches, f)
		}
	}
	return marshalJSON(map[string]any{"fields": matches}, "")
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
		return nil, fmt.Errorf(`usage: query "<pipe string>" --session <id> [--start <us>] [--end <us>] [--follow true] [--interval-ms <n>] [--format json|csv|parquet]`)
	}
	id := flags["session"]
	if flagBool(flags, "follow") {
		if format := flags["format"]; format != "" && format != "json" {
			return nil, fmt.Errorf("--format %s is not valid with --follow true (streamed output is always NDJSON)", format)
		}
		return nil, runQueryFollow(id, pos[0], flagInt64(flags, "interval-ms", 500))
	}
	data, err := DoRequest(http.MethodPost, "/query", map[string]any{
		"session_id": id,
		"query":      pos[0],
		"start":      flagInt64(flags, "start", 0),
		"end":        flagInt64(flags, "end", 0),
	})
	if err != nil {
		return nil, err
	}
	return applyResultFormat(data, flags)
}

// applyResultFormat rewrites a {"result": [...]} response as CSV or Parquet
// when --format requests it. Only row-shaped results (array of objects, as
// produced by query/query-multi --union) can be converted; ranges output
// ({"start","end"} pairs) also qualifies since it's an array of objects.
func applyResultFormat(data []byte, flags map[string]string) ([]byte, error) {
	format := flags["format"]
	if format == "" || format == "json" {
		return data, nil
	}
	if format != "csv" && format != "parquet" {
		return nil, fmt.Errorf("unsupported --format %q (use json, csv, or parquet)", format)
	}
	var wrapper struct {
		Result []map[string]any `json:"result"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("--format %s requires a row-shaped result: %w", format, err)
	}
	if len(wrapper.Errors) > 0 {
		return nil, fmt.Errorf("%d session(s) failed during query-multi and --format %s cannot carry per-session errors alongside row data; rerun with --format json to see them", len(wrapper.Errors), format)
	}
	if format == "parquet" {
		return rowsToParquet(wrapper.Result)
	}
	return rowsToCSV(wrapper.Result)
}

// resultColumns returns the union of keys across rows, sorted alphabetically
// except "Timestamp", which is pinned first when present. Used to give both
// the CSV and Parquet writers a stable column order over heterogeneous rows
// (e.g. stats/timechart output where different groups produce different
// aggregate columns).
func resultColumns(rows []map[string]any) []string {
	colSet := map[string]bool{}
	for _, row := range rows {
		for k := range row {
			colSet[k] = true
		}
	}
	hasTimestamp := colSet["Timestamp"]
	delete(colSet, "Timestamp")
	cols := make([]string, 0, len(colSet))
	for k := range colSet {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	if hasTimestamp {
		cols = append([]string{"Timestamp"}, cols...)
	}
	return cols
}

// csvValue renders a value for CSV output. Bools are written as "True"/
// "False" (not Go's lowercase "true"/"false") because pandas.read_csv's C
// parser only recognizes that capitalization as boolean and otherwise leaves
// the column as object dtype.
func csvValue(v any) string {
	if b, ok := v.(bool); ok {
		if b {
			return "True"
		}
		return "False"
	}
	return fmt.Sprintf("%v", v)
}

// rowsToCSV renders rows sharing (possibly heterogeneous) columns as CSV.
func rowsToCSV(rows []map[string]any) ([]byte, error) {
	cols := resultColumns(rows)

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(cols); err != nil {
		return nil, err
	}
	record := make([]string, len(cols))
	for _, row := range rows {
		for i, c := range cols {
			v, ok := row[c]
			if !ok || v == nil {
				record[i] = ""
				continue
			}
			record[i] = csvValue(v)
		}
		if err := w.Write(record); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// runQueryFollow re-runs the query against the full live range on an
// interval, printing one NDJSON line to stdout per changed result so a
// long-running consumer (e.g. a Python dashboard) can tail stdout instead of
// re-polling the daemon itself. It re-evaluates the whole pipeline each tick
// rather than incrementally, so stats/timechart aggregations stay correct
// over the growing window; it only writes a line when the result actually
// changed, to avoid spamming an idle stream. It never returns except on
// error (e.g. daemon lost, session disconnected) — the process is expected
// to run until the caller kills it, like `tail -f`.
func runQueryFollow(sessionID, q string, intervalMs int64) error {
	if intervalMs <= 0 {
		intervalMs = 500
	}
	resolvedID, err := resolveLiveSessionID(sessionID)
	if err != nil {
		return err
	}
	var last []byte
	for {
		result, err := DoRequest(http.MethodPost, "/query", map[string]any{
			"session_id": resolvedID,
			"query":      q,
			"start":      int64(0),
			"end":        int64(0),
		})
		if err != nil {
			return err
		}
		// The daemon's JSON encoder already appends a trailing newline; trim
		// it so each NDJSON line has exactly one, not a blank line between
		// records (some strict line-based JSON readers choke on those).
		result = bytes.TrimRight(result, "\n")
		if !bytes.Equal(result, last) {
			os.Stdout.Write(result)
			os.Stdout.Write([]byte("\n"))
			last = append(last[:0], result...)
		}
		time.Sleep(time.Duration(intervalMs) * time.Millisecond)
	}
}

// resolveLiveSessionID mirrors the daemon's default-session resolution
// (empty id => sole active session) and rejects log sessions, since
// --follow only makes sense against a live NT session whose time range
// keeps growing; a log session would just repeat the same result forever.
func resolveLiveSessionID(id string) (string, error) {
	data, err := DoRequest(http.MethodGet, "/sessions", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		Sessions []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("parse sessions response: %w", err)
	}
	if id == "" {
		switch len(resp.Sessions) {
		case 0:
			return "", fmt.Errorf("no active session; run 'load <file>' or 'connect <ip>' first")
		case 1:
			id = resp.Sessions[0].ID
		default:
			ids := make([]string, len(resp.Sessions))
			for i, s := range resp.Sessions {
				ids[i] = s.ID
			}
			return "", fmt.Errorf("multiple active sessions (%s); specify --session <id>", strings.Join(ids, ", "))
		}
	}
	for _, s := range resp.Sessions {
		if s.ID == id {
			if s.Type != "live" {
				return "", fmt.Errorf("--follow requires a live session (got type %q); log sessions have a fixed end", s.Type)
			}
			return id, nil
		}
	}
	return "", fmt.Errorf("session not found: %s", id)
}

// rowsToParquet renders rows as a Parquet file, preserving each column's
// native type (bool/float64/string) instead of stringifying everything the
// way CSV must. A column's type is inferred from its non-nil values: if every
// non-nil value is a bool it's written as BOOLEAN, if every non-nil value is
// a float64 (all JSON numbers decode to float64 in Go) it's written as
// DOUBLE, otherwise it falls back to a stringified UTF8 column so mixed or
// unexpected value shapes never fail the write. Missing/nil values become
// Parquet nulls (every column is declared Optional) rather than empty
// strings, which round-trips cleanly through pandas.read_parquet as NaN/None.
func rowsToParquet(rows []map[string]any) ([]byte, error) {
	cols := resultColumns(rows)

	type colKind int
	const (
		kindBool colKind = iota
		kindDouble
		kindString
	)
	kinds := make(map[string]colKind, len(cols))
	for _, c := range cols {
		kind := kindBool
		seen := false
		for _, row := range rows {
			v, ok := row[c]
			if !ok || v == nil {
				continue
			}
			var vKind colKind
			switch v.(type) {
			case bool:
				vKind = kindBool
			case float64:
				vKind = kindDouble
			default:
				vKind = kindString
			}
			if !seen {
				kind, seen = vKind, true
			} else if vKind != kind {
				kind = kindString
				break
			}
		}
		kinds[c] = kind
	}

	group := make(parquet.Group, len(cols))
	for _, c := range cols {
		switch kinds[c] {
		case kindBool:
			group[c] = parquet.Optional(parquet.Leaf(parquet.BooleanType))
		case kindDouble:
			group[c] = parquet.Optional(parquet.Leaf(parquet.DoubleType))
		default:
			group[c] = parquet.Optional(parquet.String())
		}
	}
	schema := parquet.NewSchema("row", group)

	// Column index per name, needed to build parquet.Row values directly
	// below. We deliberately do NOT go through GenericWriter's map/reflect
	// path (e.g. w.Write([]map[string]any{...})): its isNullValue check
	// (column_buffer_reflect.go) treats any Go zero value -- false, 0,
	// "" -- as null for Optional columns, indistinguishable from a genuinely
	// absent key. That would silently turn a real "Enabled: false" or
	// "Current: 0.0" reading into a null in the output. Building Row values
	// explicitly with Level(repetition, definition, columnIndex) lets us
	// mark exactly the absent keys as null (definition level 0) and every
	// present value -- including zero values -- as non-null (level 1).
	colIndex := make(map[string]int, len(cols))
	for _, c := range cols {
		leaf, ok := schema.Lookup(c)
		if !ok {
			return nil, fmt.Errorf("internal error: column %q missing from generated parquet schema", c)
		}
		colIndex[c] = leaf.ColumnIndex
	}

	prows := make([]parquet.Row, len(rows))
	for i, row := range rows {
		pr := make(parquet.Row, len(cols))
		for _, c := range cols {
			ci := colIndex[c]
			v, ok := row[c]
			if !ok || v == nil {
				pr[ci] = parquet.Value{}.Level(0, 0, ci)
				continue
			}
			if kinds[c] == kindString {
				if _, isStr := v.(string); !isStr {
					v = fmt.Sprintf("%v", v)
				}
			}
			pr[ci] = parquet.ValueOf(v).Level(0, 1, ci)
		}
		prows[i] = pr
	}

	var buf bytes.Buffer
	w := parquet.NewGenericWriter[map[string]any](&buf, schema)
	if _, err := w.WriteRows(prows); err != nil {
		return nil, fmt.Errorf("write parquet rows: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close parquet writer: %w", err)
	}
	return buf.Bytes(), nil
}

func runQueryMulti(args []string) ([]byte, error) {
	pos, flags := parseFlags(args)
	if len(pos) < 1 {
		return nil, fmt.Errorf(`usage: query-multi "<pipe string>" (--sessions id1,id2,... | --all true) [--union true] [--start <us>] [--end <us>] [--format json|csv|parquet]`)
	}
	all := flagBool(flags, "all")
	var ids []string
	if v, ok := flags["sessions"]; ok && v != "" {
		ids = strings.Split(v, ",")
	}
	if !all && len(ids) == 0 {
		return nil, fmt.Errorf("query-multi requires --sessions id1,id2,... or --all true")
	}
	union := flagBool(flags, "union")
	if format := flags["format"]; (format == "csv" || format == "parquet") && !union {
		return nil, fmt.Errorf("--format %s requires --union true (comparison mode isn't row-shaped)", format)
	}
	data, err := DoRequest(http.MethodPost, "/query-multi", map[string]any{
		"session_ids": ids,
		"all":         all,
		"query":       pos[0],
		"start":       flagInt64(flags, "start", 0),
		"end":         flagInt64(flags, "end", 0),
		"union":       union,
	})
	if err != nil {
		return nil, err
	}
	return applyResultFormat(data, flags)
}

func runVersion() ([]byte, error) {
	return marshalJSON(map[string]string{"version": Version}, "")
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
			"query --follow true ignores --out and --format: it streams NDJSON (one JSON object per line) directly to stdout and runs until killed, rather than returning once.",
			"query and query-multi (--union true) accept --format csv|parquet to emit CSV or Parquet instead of JSON, for direct pandas.read_csv()/read_parquet() consumption. Header/column order is alphabetical with Timestamp pinned first. Parquet preserves native bool/float64/string types and nulls; CSV stringifies everything (booleans as \"True\"/\"False\" so pandas infers bool dtype).",
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
				Name:  "search-fields",
				Desc:  "Case-insensitive substring search over a session's field list (filters the same data 'info' returns). Useful when writing SPL queries against a log with hundreds of NT keys.",
				Usage: "ClaudeScope search-fields <substr> --session <id>",
				Params: []param{
					{Name: "substr", Type: "string", Required: true, Desc: "Substring to match against field keys, case-insensitive"},
					{Name: "--session", Type: "string", Required: false, Desc: "Session ID; optional when exactly one session is active"},
				},
				Returns: `{"fields":[{"key":"...","type":"..."},...]}`,
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
				Desc:  "Run a pipe query joining multiple fields on a shared, forward-filled timestamp axis. This is a SUBSET OF SPLUNK SPL: write standard SPL and it works. Supported: where (alias: search), eval <name> = <expr>, rex field=<field> \"<regex>\", stats <agg>(<field>) [as <alias>] [by <field>...], timechart span=<duration> <agg>(<field>)... [by <field>], lookup \"<path>.csv\" <field> output <col> [as <alias>][, ...] (CSV must have a header row; joins on <field> against a same-named CSV column), table (alias: fields), sort [-]<field>, head N, tail N, and the ClaudeScope-only extensions 'ranges' (must be last; collapses matching rows into [{start,end}] intervals) and 'transaction start=<expr> end=<expr>' (groups rows into episodes bounded by the two predicates, stamping a transactionID column; rows outside any episode are dropped). Aggregates: avg,min,max,sum,count,median,stdev,p50,p90,p99. timechart span units: us,ms,s,m,h,d (e.g. span=500ms, span=1m). eval operators: + - * / and functions abs,round,sqrt,ceil,floor,min,max,pow (put spaces around operators). rex uses (?<name>...) named groups. Comparison ops: > < >= <= == (= also works) != and or NOT. '_time' aliases the Timestamp column. Backtick-quoted `name` references expand named macros from ~/.claudescope/macros.json (flat JSON: name -> pipe-query text). NOT SUPPORTED (errors): dedup, subsearches, join.",
				Usage: `ClaudeScope query "<pipe string>" --session <id> [--start <us>] [--end <us>] [--follow true] [--interval-ms <n>] [--format json|csv|parquet]`,
				Params: []param{
					{Name: "query", Type: "string", Required: true, Desc: `SPL-subset pipe query, e.g. "where CurrentA > 40 and CurrentB > 40 | stats avg(BatteryVoltage) by Subsystem"`},
					{Name: "--session", Type: "string", Required: false, Desc: "Session ID; optional when exactly one session is active"},
					{Name: "--start", Type: "int64", Required: false, Desc: "Start µs; 0=beginning; negative=offset from end. Ignored when --follow true (always runs 0 to now)."},
					{Name: "--end", Type: "int64", Required: false, Desc: "End µs; 0=end of log; negative=offset from end. Ignored when --follow true."},
					{Name: "--follow", Type: "bool", Required: false, Desc: "Must be spelled out (--follow true). Re-runs the query against a live session on an interval, printing one NDJSON line to stdout per changed result instead of returning once. Runs until killed; only valid for live (connect) sessions, not loaded logs. Incompatible with --format (NDJSON only)."},
					{Name: "--interval-ms", Type: "int64", Required: false, Desc: "Poll interval in milliseconds when --follow true. Default 500."},
					{Name: "--format", Type: "string", Required: false, Desc: `"json" (default), "csv", or "parquet". CSV is a plain header+rows table, ready for pandas.read_csv(). Parquet preserves native types/nulls, ready for pandas.read_parquet(). Not valid with --follow true.`},
				},
				Returns: `{"result":[{"Timestamp":<us>,"<field>":<value>,...},...]} or {"result":[{"start":<us>,"end":<us>},...]} when the pipeline ends in 'ranges'. With --follow true, one such JSON object per line (NDJSON) on stdout instead of a single response. With --format csv/parquet, a CSV/Parquet byte stream instead of JSON.`,
			},
			{
				Name:  "query-multi",
				Desc:  "Run the same query across several sessions at once (e.g. every match log from an event) and either compare or merge the results. Parses the query once, so one typo doesn't repeat N times; each session's own execution error (e.g. a field missing from that particular log) is reported per-session instead of aborting the whole batch. Comparison mode (default) returns one entry per session_id. Union mode (--union true) flattens every session's successful rows into one table tagged with a 'session_id' column (or 'session_id'/'start'/'end' rows if the query ends in 'ranges'); errored sessions are reported separately under 'errors' rather than silently dropped.",
				Usage: `ClaudeScope query-multi "<pipe string>" (--sessions id1,id2,... | --all true) [--union true] [--start <us>] [--end <us>] [--format json|csv|parquet]`,
				Params: []param{
					{Name: "query", Type: "string", Required: true, Desc: "SPL-subset pipe query, same grammar as `query`"},
					{Name: "--sessions", Type: "string", Required: false, Desc: "Comma-separated session IDs; required unless --all true"},
					{Name: "--all", Type: "bool", Required: false, Desc: "Run against every currently active session instead of --sessions. Must be spelled out (--all true), not bare --all."},
					{Name: "--union", Type: "bool", Required: false, Desc: "Flatten successful results into one session_id-tagged table instead of one entry per session. Must be spelled out (--union true)."},
					{Name: "--start", Type: "int64", Required: false, Desc: "Start µs; 0=beginning; negative=offset from end"},
					{Name: "--end", Type: "int64", Required: false, Desc: "End µs; 0=end of log; negative=offset from end"},
					{Name: "--format", Type: "string", Required: false, Desc: `"json" (default), "csv", or "parquet". Both require --union true (comparison mode isn't row-shaped).`},
				},
				Returns: `{"results":[{"session_id":"...","label":"...","result":...},...]} (comparison mode) or {"result":[{"session_id":"...",...},...],"errors":[...]} (union mode); with --format csv/parquet and --union true, a CSV/Parquet byte stream of the result rows instead of JSON`,
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
	return marshalJSON(s, "  ")
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
