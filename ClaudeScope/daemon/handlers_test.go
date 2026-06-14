package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// testRegistry returns a Registry pre-loaded with a mockSession under a fixed ID.
func testRegistry(t *testing.T) (*Registry, string, *mockSession) {
	t.Helper()
	r := &Registry{entries: make(map[string]*entry)}
	s := &mockSession{}
	id := "test-session-id"
	r.entries[id] = &entry{sess: s}
	return r, id, s
}

func postJSON(t *testing.T, handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandlePing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	HandlePing(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleConnect_MissingIP(t *testing.T) {
	reg := &Registry{entries: make(map[string]*entry)}
	handler := HandleConnect(reg, func(addr string) (session.DataSession, error) {
		return nil, errors.New("should not be called")
	})
	w := postJSON(t, handler, map[string]string{})
	if w.Code == http.StatusOK {
		t.Fatal("expected error for missing ip")
	}
}

func TestHandleConnect_FactoryError(t *testing.T) {
	reg := &Registry{entries: make(map[string]*entry)}
	handler := HandleConnect(reg, func(addr string) (session.DataSession, error) {
		return nil, errors.New("connection refused")
	})
	w := postJSON(t, handler, map[string]string{"ip": "127.0.0.1"})
	if w.Code == http.StatusOK {
		t.Fatalf("expected non-200, got %d", w.Code)
	}
	var errResp errorResponse
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Code != "CONNECT_FAILED" {
		t.Errorf("expected CONNECT_FAILED, got %q", errResp.Code)
	}
}

func TestHandleConnect_Success(t *testing.T) {
	reg := &Registry{entries: make(map[string]*entry)}
	handler := HandleConnect(reg, func(addr string) (session.DataSession, error) {
		return &mockSession{}, nil
	})
	w := postJSON(t, handler, map[string]string{"ip": "127.0.0.1"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var resp connectResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.SessionID == "" {
		t.Fatal("expected session_id in response")
	}
}

func TestHandleLoad_FileNotFound(t *testing.T) {
	reg := &Registry{entries: make(map[string]*entry)}
	handler := HandleLoad(reg)
	w := postJSON(t, handler, map[string]string{"path": "/nonexistent/file.wpilog"})
	if w.Code == http.StatusOK {
		t.Fatal("expected error for missing file")
	}
	var errResp errorResponse
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Code != "INVALID_LOG" {
		t.Errorf("expected INVALID_LOG, got %q", errResp.Code)
	}
}

func TestHandleDisconnect_Success(t *testing.T) {
	reg, id, s := testRegistry(t)
	handler := HandleDisconnect(reg)
	w := postJSON(t, handler, map[string]string{"session_id": id})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	if !s.closed {
		t.Fatal("expected session to be closed")
	}
}

func TestHandleDisconnect_NotFound(t *testing.T) {
	reg := &Registry{entries: make(map[string]*entry)}
	handler := HandleDisconnect(reg)
	w := postJSON(t, handler, map[string]string{"session_id": "missing"})
	if w.Code == http.StatusOK {
		t.Fatal("expected error")
	}
	var errResp errorResponse
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Code != "SESSION_NOT_FOUND" {
		t.Errorf("expected SESSION_NOT_FOUND, got %q", errResp.Code)
	}
}

func TestHandleInfo_Success(t *testing.T) {
	reg, id, _ := testRegistry(t)
	handler := HandleInfo(reg)
	req := httptest.NewRequest(http.MethodGet, "/info?session="+id, nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
}

func TestHandleGet_DefaultSession(t *testing.T) {
	reg, _, _ := testRegistry(t) // one session under a fixed ID
	handler := HandleGet(reg)
	// Omit session_id entirely — should resolve to the sole session.
	w := postJSON(t, handler, map[string]any{"keys": []string{"/k"}})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 via default session, got %d: %s", w.Code, w.Body)
	}
}

func TestHandleGet_NoSession(t *testing.T) {
	reg := &Registry{entries: make(map[string]*entry)}
	handler := HandleGet(reg)
	w := postJSON(t, handler, map[string]any{"keys": []string{"/k"}})
	if w.Code == http.StatusOK {
		t.Fatal("expected error with no active sessions")
	}
	var errResp errorResponse
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Code != "NO_SESSION" {
		t.Errorf("expected NO_SESSION, got %q", errResp.Code)
	}
}

func TestHandleGet_AmbiguousSession(t *testing.T) {
	reg := &Registry{entries: make(map[string]*entry)}
	reg.Add(&mockSession{})
	reg.Add(&mockSession{})
	handler := HandleGet(reg)
	w := postJSON(t, handler, map[string]any{"keys": []string{"/k"}})
	if w.Code == http.StatusOK {
		t.Fatal("expected error with multiple active sessions")
	}
	var errResp errorResponse
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Code != "AMBIGUOUS_SESSION" {
		t.Errorf("expected AMBIGUOUS_SESSION, got %q", errResp.Code)
	}
}

func TestHandleSet_OnLogSession_ReturnsError(t *testing.T) {
	reg, id, _ := testRegistry(t)
	handler := HandleSet(reg)
	w := postJSON(t, handler, setRequest{
		SessionID: id,
		Pairs:     map[string]any{"/key": 1.0},
	})
	if w.Code == http.StatusOK {
		t.Fatal("expected error: log sessions are read-only")
	}
	var errResp errorResponse
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Code != "READ_ONLY_SESSION" {
		t.Errorf("expected READ_ONLY_SESSION, got %q", errResp.Code)
	}
}
