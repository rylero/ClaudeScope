package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// NTSessionFactory creates a live NT session. Injected so tests can mock it.
type NTSessionFactory func(addr string) (session.DataSession, error)

// --- JSON types ---

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type connectRequest struct {
	IP string `json:"ip"`
}

type connectResponse struct {
	SessionID string `json:"session_id"`
}

type connectResponseWFields struct {
	SessionID string   `json:"session_id"`
	Keys      []string `json:"keys"`
}

type loadRequest struct {
	Path string `json:"path"`
}

type disconnectRequest struct {
	SessionID string `json:"session_id"`
}

type getRequest struct {
	SessionID string   `json:"session_id"`
	Keys      []string `json:"keys"`
	Time      int64    `json:"time"`
}

type rangeRequest struct {
	SessionID string   `json:"session_id"`
	Keys      []string `json:"keys"`
	Start     int64    `json:"start"`
	End       int64    `json:"end"`
}

type findBoolRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key"`
	Value     bool   `json:"value"`
}

type findThresholdRequest struct {
	SessionID string  `json:"session_id"`
	Key       string  `json:"key"`
	Min       float64 `json:"min"`
	Max       float64 `json:"max"`
}

type statsRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
}

type setRequest struct {
	SessionID string         `json:"session_id"`
	Pairs     map[string]any `json:"pairs"`
}

// --- helpers ---

func writeError(w http.ResponseWriter, status int, msg, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(errorResponse{Error: msg, Code: code})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func sessionNotFound(w http.ResponseWriter, id string) {
	writeError(w, http.StatusNotFound,
		fmt.Sprintf("session not found: %s", id), "SESSION_NOT_FOUND")
}

// resolveSession looks up a session, applying default-session resolution when
// id is empty. On failure it writes the appropriate error response and returns
// ok=false. On success it returns the session and its concrete ID.
func resolveSession(reg *Registry, w http.ResponseWriter, id string) (session.DataSession, string, bool) {
	sess, resolvedID, err := reg.Resolve(id)
	if err != nil {
		var amb *AmbiguousSessionError
		switch {
		case errors.Is(err, ErrNoSession):
			writeError(w, http.StatusNotFound, err.Error(), "NO_SESSION")
		case errors.As(err, &amb):
			writeError(w, http.StatusBadRequest, err.Error(), "AMBIGUOUS_SESSION")
		default:
			sessionNotFound(w, id)
		}
		return nil, "", false
	}
	return sess, resolvedID, true
}

// --- handlers ---

// HandlePing responds 200 OK so the CLI can detect a running daemon.
func HandlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
}

// HandleSessions lists all active sessions.
func HandleSessions(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"sessions": reg.List()})
	}
}

// HandleConnect starts a live NT session.
func HandleConnect(reg *Registry, factory NTSessionFactory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req connectRequest
		if err := decodeBody(r, &req); err != nil || req.IP == "" {
			writeError(w, http.StatusBadRequest, "missing ip field", "BAD_REQUEST")
			return
		}
		sess, err := factory(req.IP)
		if err != nil {
			writeError(w, http.StatusBadGateway,
				fmt.Sprintf("connect failed: %v", err), "CONNECT_FAILED")
			return
		}
		id := reg.AddLabeled(sess, req.IP)
		writeJSON(w, connectResponse{SessionID: id})
	}
}

// HandleLoad parses a .wpilog file and creates a log session.
func HandleLoad(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loadRequest
		if err := decodeBody(r, &req); err != nil || req.Path == "" {
			writeError(w, http.StatusBadRequest, "missing path field", "BAD_REQUEST")
			return
		}
		data, err := os.ReadFile(req.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("cannot read file: %v", err), "INVALID_LOG")
			return
		}
		sess, err := session.ParseWPILog(data)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("invalid wpilog: %v", err), "INVALID_LOG")
			return
		}
		id := reg.AddLabeled(sess, req.Path)
		fields, _ := sess.Fields()
		writeJSON(w, map[string]any{"session_id": id, "fields": fields})
	}
}

// HandleDisconnect closes and removes a session.
func HandleDisconnect(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req disconnectRequest
		if err := decodeBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON", "BAD_REQUEST")
			return
		}
		_, resolvedID, ok := resolveSession(reg, w, req.SessionID)
		if !ok {
			return
		}
		if err := reg.Remove(resolvedID); err != nil {
			sessionNotFound(w, resolvedID)
			return
		}
		writeJSON(w, struct{}{})
	}
}

// HandleInfo returns field list and time range for a session.
func HandleInfo(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("session")
		sess, _, ok := resolveSession(reg, w, id)
		if !ok {
			return
		}
		fields, err := sess.Fields()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "INTERNAL")
			return
		}
		start, end, err := sess.TimeRange()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "INTERNAL")
			return
		}
		writeJSON(w, map[string]any{
			"fields": fields,
			"start":  start,
			"end":    end,
		})
	}
}

// HandleGet returns values for one or more keys at a timestamp.
func HandleGet(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req getRequest
		if err := decodeBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON", "BAD_REQUEST")
			return
		}
		sess, _, ok := resolveSession(reg, w, req.SessionID)
		if !ok {
			return
		}
		result, err := sess.GetValues(req.Keys, req.Time)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "KEY_NOT_FOUND")
			return
		}
		writeJSON(w, result)
	}
}

// HandleRange returns time-series data for one or more keys.
func HandleRange(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req rangeRequest
		if err := decodeBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON", "BAD_REQUEST")
			return
		}
		sess, _, ok := resolveSession(reg, w, req.SessionID)
		if !ok {
			return
		}
		result, err := sess.GetRanges(req.Keys, req.Start, req.End)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "KEY_NOT_FOUND")
			return
		}
		writeJSON(w, result)
	}
}

// HandleFindBool returns time ranges where a bool field matches a value.
func HandleFindBool(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req findBoolRequest
		if err := decodeBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON", "BAD_REQUEST")
			return
		}
		sess, _, ok := resolveSession(reg, w, req.SessionID)
		if !ok {
			return
		}
		ranges, err := sess.FindBoolRanges(req.Key, req.Value)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "KEY_NOT_FOUND")
			return
		}
		writeJSON(w, ranges)
	}
}

// HandleFindThreshold returns time ranges where a numeric field is within [min, max].
func HandleFindThreshold(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req findThresholdRequest
		if err := decodeBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON", "BAD_REQUEST")
			return
		}
		sess, _, ok := resolveSession(reg, w, req.SessionID)
		if !ok {
			return
		}
		ranges, err := sess.FindThresholdRanges(req.Key, req.Min, req.Max)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "KEY_NOT_FOUND")
			return
		}
		writeJSON(w, ranges)
	}
}

// HandleStats returns descriptive statistics for a numeric field.
func HandleStats(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req statsRequest
		if err := decodeBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON", "BAD_REQUEST")
			return
		}
		sess, _, ok := resolveSession(reg, w, req.SessionID)
		if !ok {
			return
		}
		stats, err := sess.Stats(req.Key, req.Start, req.End)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "KEY_NOT_FOUND")
			return
		}
		writeJSON(w, stats)
	}
}

// HandleSet publishes key/value pairs to a live NT session.
func HandleSet(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setRequest
		if err := decodeBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON", "BAD_REQUEST")
			return
		}
		sess, _, ok := resolveSession(reg, w, req.SessionID)
		if !ok {
			return
		}
		if err := sess.Set(req.Pairs); err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "READ_ONLY_SESSION")
			return
		}
		writeJSON(w, struct{}{})
	}
}
