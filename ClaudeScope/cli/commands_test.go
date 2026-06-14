package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestRunCommand_Set(t *testing.T) {
	serveFake(t, map[string]any{"/set": struct{}{}})
	_, err := RunCommand([]string{"set", "/key=1.0", "--session", "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
