package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestRunCommand_Range_CSV(t *testing.T) {
	serveFake(t, map[string]any{"/range": map[string]any{
		"/Drive/Vel": []map[string]any{
			{"timestamp": 100, "value": 1.5},
			{"timestamp": 200, "value": 2.5},
		},
	}})
	out, err := RunCommand([]string{"range", "/Drive/Vel", "--session", "abc", "--format", "csv"})
	if err != nil {
		t.Fatal(err)
	}
	want := "key,timestamp,value\n/Drive/Vel,100,1.5\n/Drive/Vel,200,2.5\n"
	if string(out) != want {
		t.Errorf("expected %q, got %q", want, string(out))
	}
}

func TestRunCommand_Range_Parquet_TypedValue(t *testing.T) {
	serveFake(t, map[string]any{"/range": map[string]any{
		"/Drive/Vel": []map[string]any{
			{"timestamp": 100, "value": 1.5},
			{"timestamp": 200, "value": 2.5},
		},
	}})
	out, err := RunCommand([]string{"range", "/Drive/Vel", "--session", "abc", "--format", "parquet"})
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
	// Single numeric key: value column keeps native double type.
	if v, ok := rows[0]["value"].(float64); !ok || v != 1.5 {
		t.Errorf("row 0 value: expected float64 1.5, got %#v", rows[0]["value"])
	}
	if v, ok := rows[0]["key"].(string); !ok || v != "/Drive/Vel" {
		t.Errorf("row 0 key: expected string /Drive/Vel, got %#v", rows[0]["key"])
	}
}
