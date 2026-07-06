package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func serveFake(t *testing.T, responses map[string]any) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(v)
	}))
	t.Cleanup(srv.Close)
	patchAddr(t, srv.URL)
}

func TestRunCommand_Connect(t *testing.T) {
	serveFake(t, map[string]any{"/connect": map[string]string{"session_id": "abc"}})
	out, err := RunCommand([]string{"connect", "10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]string
	json.Unmarshal(out, &resp)
	if resp["session_id"] != "abc" {
		t.Errorf("expected abc, got %q", resp["session_id"])
	}
}

func TestRunCommand_Load(t *testing.T) {
	serveFake(t, map[string]any{"/load": map[string]string{"session_id": "xyz"}})
	out, err := RunCommand([]string{"load", "/path/to/file.wpilog"})
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]string
	json.Unmarshal(out, &resp)
	if resp["session_id"] != "xyz" {
		t.Errorf("expected xyz, got %q", resp["session_id"])
	}
}

func TestRunCommand_Disconnect(t *testing.T) {
	serveFake(t, map[string]any{"/disconnect": struct{}{}})
	_, err := RunCommand([]string{"disconnect", "--session", "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCommand_Sessions(t *testing.T) {
	serveFake(t, map[string]any{
		"/sessions": map[string]any{"sessions": []map[string]any{
			{"id": "abc", "type": "log", "label": "/logs/a.wpilog", "idle_seconds": 3},
		}},
	})
	out, err := RunCommand([]string{"sessions"})
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	json.Unmarshal(out, &resp)
	if resp["sessions"] == nil {
		t.Error("expected sessions in response")
	}
}

func TestRunCommand_Info(t *testing.T) {
	serveFake(t, map[string]any{
		"/info": map[string]any{"fields": []map[string]string{{"key": "/voltage", "type": "double"}}, "start": 0, "end": 3000},
	})
	out, err := RunCommand([]string{"info", "--session", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	json.Unmarshal(out, &resp)
	if resp["end"] == nil {
		t.Error("expected end in info response")
	}
}

func TestRunCommand_Get(t *testing.T) {
	serveFake(t, map[string]any{"/get": map[string]any{"/voltage": map[string]any{"timestamp": 1000, "value": 12.0}}})
	out, err := RunCommand([]string{"get", "/voltage", "--session", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	json.Unmarshal(out, &resp)
	if resp["/voltage"] == nil {
		t.Error("expected /voltage in get response")
	}
}

func TestRunCommand_OmittedSession(t *testing.T) {
	// --session is now optional at the CLI; the daemon resolves a default.
	// The CLI should forward the request with an empty session_id, not error.
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{"/voltage": map[string]any{"timestamp": 0, "value": 12.0}})
	}))
	t.Cleanup(srv.Close)
	patchAddr(t, srv.URL)

	if _, err := RunCommand([]string{"get", "/voltage"}); err != nil {
		t.Fatalf("omitting --session should not error at the CLI: %v", err)
	}
	if got["session_id"] != "" {
		t.Errorf("expected empty session_id forwarded, got %v", got["session_id"])
	}
}

func TestRunCommand_UnknownSubcommand(t *testing.T) {
	_, err := RunCommand([]string{"frobnicate"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

func TestRunCommand_Stats(t *testing.T) {
	serveFake(t, map[string]any{"/stats": map[string]any{"mean": 11.5, "min": 11.0, "max": 12.0}})
	out, err := RunCommand([]string{"stats", "/voltage", "--session", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	json.Unmarshal(out, &resp)
	if resp["mean"] == nil {
		t.Error("expected mean in stats response")
	}
}

func TestRunCommand_FindBool(t *testing.T) {
	serveFake(t, map[string]any{"/find-bool": []map[string]any{{"start": 1000, "end": 2500}}})
	out, err := RunCommand([]string{"find-bool", "/enabled", "true", "--session", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var resp []any
	json.Unmarshal(out, &resp)
	if len(resp) != 1 {
		t.Errorf("expected 1 range, got %d", len(resp))
	}
}

func TestRunCommand_FindThreshold(t *testing.T) {
	serveFake(t, map[string]any{"/find-threshold": []map[string]any{{"start": 2000, "end": 3000}}})
	out, err := RunCommand([]string{"find-threshold", "/voltage", "--min", "11.0", "--max", "11.5", "--session", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var resp []any
	json.Unmarshal(out, &resp)
	if len(resp) != 1 {
		t.Errorf("expected 1 range, got %d", len(resp))
	}
}

func TestRunCommand_FindThreshold_OneSided(t *testing.T) {
	// Capture the request body so we can assert the unbounded side is filled in.
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode([]map[string]any{{"start": 0, "end": 1000}})
	}))
	t.Cleanup(srv.Close)
	patchAddr(t, srv.URL)

	// Only --max: min should default to a large negative bound.
	if _, err := RunCommand([]string{"find-threshold", "/voltage", "--max", "11.0", "--session", "abc"}); err != nil {
		t.Fatal(err)
	}
	if got["min"].(float64) >= 0 {
		t.Errorf("expected large-negative default min, got %v", got["min"])
	}
	if got["max"].(float64) != 11.0 {
		t.Errorf("expected max 11.0, got %v", got["max"])
	}
}

func TestRunCommand_FindThreshold_NoBounds(t *testing.T) {
	_, err := RunCommand([]string{"find-threshold", "/voltage", "--session", "abc"})
	if err == nil {
		t.Fatal("expected error when neither --min nor --max is given")
	}
}

func TestRunCommand_Set(t *testing.T) {
	serveFake(t, map[string]any{"/set": struct{}{}})
	_, err := RunCommand([]string{"set", "/key=1.0", "--session", "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCommand_SearchFields(t *testing.T) {
	serveFake(t, map[string]any{
		"/info": map[string]any{"fields": []map[string]string{
			{"key": "/drive/leftVoltage", "type": "double"},
			{"key": "/drive/rightVoltage", "type": "double"},
			{"key": "/arm/angle", "type": "double"},
		}, "start": 0, "end": 3000},
	})
	out, err := RunCommand([]string{"search-fields", "voltage", "--session", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Fields []struct {
			Key string `json:"key"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Fields) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(resp.Fields), resp.Fields)
	}
}

func TestRunCommand_SearchFields_CaseInsensitiveNoMatch(t *testing.T) {
	serveFake(t, map[string]any{
		"/info": map[string]any{"fields": []map[string]string{
			{"key": "/arm/Angle", "type": "double"},
		}, "start": 0, "end": 3000},
	})
	out, err := RunCommand([]string{"search-fields", "ANGLE", "--session", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Fields []map[string]string `json:"fields"`
	}
	json.Unmarshal(out, &resp)
	if len(resp.Fields) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", resp.Fields)
	}

	out, err = RunCommand([]string{"search-fields", "nonexistent", "--session", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(out, &resp)
	if len(resp.Fields) != 0 {
		t.Fatalf("expected no matches, got %+v", resp.Fields)
	}
}

func TestRunCommand_SearchFields_NoArg(t *testing.T) {
	if _, err := RunCommand([]string{"search-fields"}); err == nil {
		t.Fatal("expected error when substring is omitted")
	}
}

func TestResolveLiveSessionID_RejectsLogSession(t *testing.T) {
	serveFake(t, map[string]any{
		"/sessions": map[string]any{"sessions": []map[string]any{
			{"id": "abc", "type": "log", "label": "/logs/a.wpilog", "idle_seconds": 3},
		}},
	})
	if _, err := resolveLiveSessionID("abc"); err == nil {
		t.Fatal("expected error for log session")
	}
}

func TestResolveLiveSessionID_DefaultsToSoleSession(t *testing.T) {
	serveFake(t, map[string]any{
		"/sessions": map[string]any{"sessions": []map[string]any{
			{"id": "abc", "type": "live", "label": "10.0.0.2", "idle_seconds": 0},
		}},
	})
	id, err := resolveLiveSessionID("")
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc" {
		t.Errorf("expected abc, got %q", id)
	}
}

func TestResolveLiveSessionID_Ambiguous(t *testing.T) {
	serveFake(t, map[string]any{
		"/sessions": map[string]any{"sessions": []map[string]any{
			{"id": "abc", "type": "live", "label": "10.0.0.2", "idle_seconds": 0},
			{"id": "def", "type": "live", "label": "10.0.0.3", "idle_seconds": 0},
		}},
	})
	if _, err := resolveLiveSessionID(""); err == nil {
		t.Fatal("expected ambiguous-session error")
	}
}

func TestResolveLiveSessionID_NoSessions(t *testing.T) {
	serveFake(t, map[string]any{"/sessions": map[string]any{"sessions": []map[string]any{}}})
	if _, err := resolveLiveSessionID(""); err == nil {
		t.Fatal("expected no-session error")
	}
}

func TestRunCommand_Query_FollowRejectsLogSession(t *testing.T) {
	serveFake(t, map[string]any{
		"/sessions": map[string]any{"sessions": []map[string]any{
			{"id": "abc", "type": "log", "label": "/logs/a.wpilog", "idle_seconds": 0},
		}},
	})
	_, err := RunCommand([]string{"query", "table", "--session", "abc", "--follow", "true"})
	if err == nil {
		t.Fatal("expected error when --follow is used against a log session")
	}
}

// TestRunQueryFollow_StreamsChangedResults exercises the actual follow loop:
// it stubs /query to return a fresh value on each call and expects one NDJSON
// line per distinct result on stdout, until the server starts erroring
// (standing in for the daemon/session going away), which is what makes the
// otherwise-infinite loop return.
func TestRunQueryFollow_StreamsChangedResults(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"sessions": []map[string]any{
			{"id": "abc", "type": "live", "label": "10.0.0.2", "idle_seconds": 0},
		}})
	})
	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		switch {
		case n <= 2:
			// Same result twice in a row: should only print once.
			json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"Timestamp": 1, "v": 1}}})
		case n == 3:
			json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"Timestamp": 2, "v": 2}}})
		default:
			http.Error(w, `{"error":"session not found","code":"SESSION_NOT_FOUND"}`, http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	patchAddr(t, srv.URL)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	errCh := make(chan error, 1)
	go func() { errCh <- runQueryFollow("abc", "table", 1) }()

	err = <-errCh
	w.Close()
	os.Stdout = oldStdout
	if err == nil {
		t.Fatal("expected runQueryFollow to return an error once the server starts failing")
	}

	out, _ := io.ReadAll(r)
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 distinct-result lines, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], `"v":1`) || !strings.Contains(lines[1], `"v":2`) {
		t.Errorf("unexpected line contents: %q", lines)
	}
}

func TestRunCommand_Query_CSV(t *testing.T) {
	serveFake(t, map[string]any{"/query": map[string]any{
		"result": []map[string]any{
			{"Timestamp": 100, "BatteryVoltage": 12.1, "Subsystem": "drive"},
			{"Timestamp": 200, "BatteryVoltage": 11.9, "Subsystem": "arm"},
		},
	}})
	out, err := RunCommand([]string{"query", "table Timestamp, BatteryVoltage, Subsystem", "--session", "abc", "--format", "csv"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Timestamp,BatteryVoltage,Subsystem\n100,12.1,drive\n200,11.9,arm\n"
	if string(out) != want {
		t.Errorf("expected %q, got %q", want, string(out))
	}
}

func TestRunCommand_Query_CSV_HeterogeneousColumns(t *testing.T) {
	serveFake(t, map[string]any{"/query": map[string]any{
		"result": []map[string]any{
			{"Timestamp": 100, "A": 1},
			{"Timestamp": 200, "B": 2},
		},
	}})
	out, err := RunCommand([]string{"query", "stats avg(A) by B", "--session", "abc", "--format", "csv"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Timestamp,A,B\n100,1,\n200,,2\n"
	if string(out) != want {
		t.Errorf("expected %q, got %q", want, string(out))
	}
}

func TestRunCommand_Query_UnknownFormat(t *testing.T) {
	serveFake(t, map[string]any{"/query": map[string]any{"result": []map[string]any{}}})
	_, err := RunCommand([]string{"query", "table Timestamp", "--session", "abc", "--format", "xml"})
	if err == nil {
		t.Fatal("expected error for unsupported --format")
	}
}

func TestRunCommand_QueryMulti_CSVRequiresUnion(t *testing.T) {
	_, err := RunCommand([]string{"query-multi", "table Timestamp", "--all", "true", "--format", "csv"})
	if err == nil {
		t.Fatal("expected error when --format csv is used without --union true")
	}
}

func TestRunCommand_QueryMulti_ParquetRequiresUnion(t *testing.T) {
	_, err := RunCommand([]string{"query-multi", "table Timestamp", "--all", "true", "--format", "parquet"})
	if err == nil {
		t.Fatal("expected error when --format parquet is used without --union true")
	}
}

func TestRunCommand_Query_CSV_BoolFormattedForPandas(t *testing.T) {
	serveFake(t, map[string]any{"/query": map[string]any{
		"result": []map[string]any{
			{"Timestamp": 100, "Enabled": true},
			{"Timestamp": 200, "Enabled": false},
		},
	}})
	out, err := RunCommand([]string{"query", "table Timestamp, Enabled", "--session", "abc", "--format", "csv"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Timestamp,Enabled\n100,True\n200,False\n"
	if string(out) != want {
		t.Errorf("expected %q, got %q", want, string(out))
	}
}

func TestRunCommand_Query_Parquet_RoundTrip(t *testing.T) {
	serveFake(t, map[string]any{"/query": map[string]any{
		"result": []map[string]any{
			{"Timestamp": 100.0, "BatteryVoltage": 12.1, "Enabled": true, "Subsystem": "drive"},
			{"Timestamp": 200.0, "Enabled": false},
		},
	}})
	out, err := RunCommand([]string{"query", "table Timestamp, BatteryVoltage, Enabled, Subsystem", "--session", "abc", "--format", "parquet"})
	if err != nil {
		t.Fatal(err)
	}

	f, err := parquet.OpenFile(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("output is not a valid parquet file: %v", err)
	}
	r := parquet.NewGenericReader[map[string]any](bytes.NewReader(out), f.Schema())
	rows := make([]map[string]any, 2)
	for i := range rows {
		rows[i] = map[string]any{}
	}
	n, err := r.Read(rows)
	if n != 2 {
		t.Fatalf("expected 2 rows, got %d (err=%v)", n, err)
	}

	if v, ok := rows[0]["Enabled"].(bool); !ok || v != true {
		t.Errorf("row 0 Enabled: expected bool true, got %#v", rows[0]["Enabled"])
	}
	if v, ok := rows[0]["BatteryVoltage"].(float64); !ok || v != 12.1 {
		t.Errorf("row 0 BatteryVoltage: expected float64 12.1, got %#v", rows[0]["BatteryVoltage"])
	}
	if v, ok := rows[0]["Subsystem"].(string); !ok || v != "drive" {
		t.Errorf("row 0 Subsystem: expected string drive, got %#v", rows[0]["Subsystem"])
	}
	// Row 1 omits BatteryVoltage/Subsystem entirely -- must round-trip as
	// Parquet nulls, not zero values or empty strings.
	if rows[1]["BatteryVoltage"] != nil {
		t.Errorf("row 1 BatteryVoltage: expected nil, got %#v", rows[1]["BatteryVoltage"])
	}
	if rows[1]["Subsystem"] != nil {
		t.Errorf("row 1 Subsystem: expected nil, got %#v", rows[1]["Subsystem"])
	}
}

// TestRunCommand_Query_Parquet_PreservesFalsyValues guards against a real
// bug found via end-to-end testing: parquet-go's reflect-based map writer
// (the plain w.Write([]map[string]any{...}) path) treats any Go zero value
// -- false, 0.0, "" -- as equivalent to an absent key when the column is
// Optional, so a legitimate "Enabled: false" or "Current: 0.0" reading was
// silently coming back as a Parquet null. rowsToParquet must build rows
// manually (parquet.Row with explicit definition levels) to avoid that path.
func TestRunCommand_Query_Parquet_PreservesFalsyValues(t *testing.T) {
	serveFake(t, map[string]any{"/query": map[string]any{
		"result": []map[string]any{
			{"Timestamp": 0.0, "Enabled": false, "Current": 0.0, "Label": ""},
			{"Timestamp": 1000.0, "Enabled": true, "Current": 5.0, "Label": "ok"},
		},
	}})
	out, err := RunCommand([]string{"query", "table Timestamp, Enabled, Current, Label", "--session", "abc", "--format", "parquet"})
	if err != nil {
		t.Fatal(err)
	}

	f, err := parquet.OpenFile(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("output is not a valid parquet file: %v", err)
	}
	r := parquet.NewGenericReader[map[string]any](bytes.NewReader(out), f.Schema())
	rows := make([]map[string]any, 2)
	for i := range rows {
		rows[i] = map[string]any{}
	}
	if n, err := r.Read(rows); n != 2 {
		t.Fatalf("expected 2 rows, got %d (err=%v)", n, err)
	}

	if v, ok := rows[0]["Timestamp"].(float64); !ok || v != 0.0 {
		t.Errorf("row 0 Timestamp: expected float64 0.0 (not null), got %#v", rows[0]["Timestamp"])
	}
	if v, ok := rows[0]["Enabled"].(bool); !ok || v != false {
		t.Errorf("row 0 Enabled: expected bool false (not null), got %#v", rows[0]["Enabled"])
	}
	if v, ok := rows[0]["Current"].(float64); !ok || v != 0.0 {
		t.Errorf("row 0 Current: expected float64 0.0 (not null), got %#v", rows[0]["Current"])
	}
	if v, ok := rows[0]["Label"].(string); !ok || v != "" {
		t.Errorf("row 0 Label: expected empty string (not null), got %#v", rows[0]["Label"])
	}
}
