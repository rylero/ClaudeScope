package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// mockSession is a do-nothing DataSession for registry tests.
type mockSession struct{ closed bool }

func (m *mockSession) Type() session.SessionType                                         { return session.LogSession }
func (m *mockSession) Fields() ([]session.FieldInfo, error)                              { return nil, nil }
func (m *mockSession) TimeRange() (int64, int64, error)                                  { return 0, 0, nil }
func (m *mockSession) GetValues([]string, int64) (map[string]*session.DataPoint, error)  { return nil, nil }
func (m *mockSession) GetRanges([]string, int64, int64) (map[string][]session.DataPoint, error) {
	return nil, nil
}
func (m *mockSession) FindBoolRanges(string, bool) ([]session.TimeRange, error)             { return nil, nil }
func (m *mockSession) FindThresholdRanges(string, float64, float64) ([]session.TimeRange, error) {
	return nil, nil
}
func (m *mockSession) Stats(string, int64, int64) (*session.Stats, error) { return nil, nil }
func (m *mockSession) Set(map[string]any) error {
	return errors.New("read-only")
}
func (m *mockSession) Close() error {
	m.closed = true
	return nil
}

func TestRegistry_AddGet(t *testing.T) {
	r := NewRegistry()
	s := &mockSession{}
	id := r.Add(s)
	if id == "" {
		t.Fatal("Add returned empty ID")
	}
	got, err := r.Get(id)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != s {
		t.Fatal("Get returned wrong session")
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestRegistry_Resolve_Explicit(t *testing.T) {
	r := NewRegistry()
	s := &mockSession{}
	id := r.Add(s)
	got, gotID, err := r.Resolve(id)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got != s || gotID != id {
		t.Fatal("Resolve returned wrong session/id for explicit lookup")
	}
}

func TestRegistry_Resolve_DefaultSingle(t *testing.T) {
	r := NewRegistry()
	s := &mockSession{}
	id := r.Add(s)
	got, gotID, err := r.Resolve("")
	if err != nil {
		t.Fatalf("expected default resolution, got error: %v", err)
	}
	if got != s || gotID != id {
		t.Fatal("Resolve(\"\") should return the sole session")
	}
}

func TestRegistry_Resolve_NoSession(t *testing.T) {
	r := NewRegistry()
	_, _, err := r.Resolve("")
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("expected ErrNoSession, got %v", err)
	}
}

func TestRegistry_Resolve_Ambiguous(t *testing.T) {
	r := NewRegistry()
	r.Add(&mockSession{})
	r.Add(&mockSession{})
	_, _, err := r.Resolve("")
	var amb *AmbiguousSessionError
	if !errors.As(err, &amb) {
		t.Fatalf("expected AmbiguousSessionError, got %v", err)
	}
	if len(amb.IDs) != 2 {
		t.Errorf("expected 2 candidate IDs, got %d", len(amb.IDs))
	}
}

func TestRegistry_Remove(t *testing.T) {
	r := NewRegistry()
	s := &mockSession{}
	id := r.Add(s)
	if err := r.Remove(id); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if !s.closed {
		t.Fatal("Remove should call Close on the session")
	}
	_, err := r.Get(id)
	if err == nil {
		t.Fatal("expected error after Remove")
	}
}

func TestRegistry_Remove_NotFound(t *testing.T) {
	r := NewRegistry()
	err := r.Remove("nonexistent")
	if err == nil {
		t.Fatal("expected error removing nonexistent session")
	}
}

func TestRegistry_Expiry(t *testing.T) {
	r := NewRegistry()
	s := &mockSession{}
	id := r.Add(s)

	// Backdate the last-used time past the expiry threshold.
	r.mu.Lock()
	r.entries[id].lastUsed = time.Now().Add(-11 * time.Minute)
	r.mu.Unlock()

	r.sweep()

	if !s.closed {
		t.Fatal("expired session should be closed by sweep")
	}
	_, err := r.Get(id)
	if err == nil {
		t.Fatal("expired session should be removed")
	}
}

func TestRegistry_Touch_ResetsExpiry(t *testing.T) {
	r := NewRegistry()
	s := &mockSession{}
	id := r.Add(s)

	r.mu.Lock()
	r.entries[id].lastUsed = time.Now().Add(-11 * time.Minute)
	r.mu.Unlock()

	r.Touch(id)
	r.sweep()

	if s.closed {
		t.Fatal("touched session should not be expired")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	idA := r.AddLabeled(&mockSession{}, "/logs/a.wpilog")
	idB := r.AddLabeled(&mockSession{}, "/logs/b.wpilog")

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(list))
	}
	// List is sorted by ID; build a lookup to assert per-session fields.
	byID := map[string]SessionInfo{}
	for _, s := range list {
		byID[s.ID] = s
	}
	if byID[idA].Label != "/logs/a.wpilog" {
		t.Errorf("expected label for A, got %q", byID[idA].Label)
	}
	if byID[idB].Type != "log" {
		t.Errorf("expected type 'log', got %q", byID[idB].Type)
	}
}

func TestRegistry_List_SortedByID(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 5; i++ {
		r.Add(&mockSession{})
	}
	list := r.List()
	for i := 1; i < len(list); i++ {
		if list[i-1].ID > list[i].ID {
			t.Fatalf("List not sorted by ID: %q before %q", list[i-1].ID, list[i].ID)
		}
	}
}

func TestRegistry_UniqueIDs(t *testing.T) {
	r := NewRegistry()
	ids := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		id := r.Add(&mockSession{})
		if _, exists := ids[id]; exists {
			t.Fatalf("duplicate session ID: %s", id)
		}
		ids[id] = struct{}{}
	}
}
