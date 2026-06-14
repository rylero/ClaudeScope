package daemon

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

const sessionTTL = 10 * time.Minute
const sweepInterval = 60 * time.Second

// ErrNoSession indicates no active sessions exist to default to.
var ErrNoSession = errors.New("no active session; run 'load <file>' or 'connect <ip>' first")

// AmbiguousSessionError is returned when a default session cannot be chosen
// because more than one session is active. It lists the candidate IDs.
type AmbiguousSessionError struct{ IDs []string }

func (e *AmbiguousSessionError) Error() string {
	return fmt.Sprintf("multiple active sessions (%s); specify --session <id>", strings.Join(e.IDs, ", "))
}

type entry struct {
	sess     session.DataSession
	lastUsed time.Time
}

// Registry holds active DataSessions keyed by random session IDs.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// NewRegistry creates a Registry and starts the background expiry goroutine.
func NewRegistry() *Registry {
	r := &Registry{entries: make(map[string]*entry)}
	go r.runSweep()
	return r
}

// Add registers sess and returns its new session ID.
func (r *Registry) Add(sess session.DataSession) string {
	id := newID()
	r.mu.Lock()
	r.entries[id] = &entry{sess: sess, lastUsed: time.Now()}
	r.mu.Unlock()
	return id
}

// Resolve returns the session for id. If id is empty, it returns the sole
// active session; it returns ErrNoSession if none exist or an
// *AmbiguousSessionError if more than one does. The resolved ID is returned so
// callers (e.g. disconnect) can act on the concrete session.
func (r *Registry) Resolve(id string) (session.DataSession, string, error) {
	if id != "" {
		s, err := r.Get(id)
		return s, id, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch len(r.entries) {
	case 0:
		return nil, "", ErrNoSession
	case 1:
		for k, e := range r.entries {
			e.lastUsed = time.Now()
			return e.sess, k, nil
		}
	}
	ids := make([]string, 0, len(r.entries))
	for k := range r.entries {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return nil, "", &AmbiguousSessionError{IDs: ids}
}

// Get returns the session for id, refreshing its last-used time.
func (r *Registry) Get(id string) (session.DataSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	e.lastUsed = time.Now()
	return e.sess, nil
}

// Remove closes and deletes the session. Returns an error if not found.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("session not found: %s", id)
	}
	delete(r.entries, id)
	r.mu.Unlock()
	return e.sess.Close()
}

// Touch refreshes the last-used time. No-op for unknown IDs.
func (r *Registry) Touch(id string) {
	r.mu.Lock()
	if e, ok := r.entries[id]; ok {
		e.lastUsed = time.Now()
	}
	r.mu.Unlock()
}

// sweep closes and removes all sessions idle longer than sessionTTL.
func (r *Registry) sweep() {
	r.mu.Lock()
	var expired []string
	for id, e := range r.entries {
		if time.Since(e.lastUsed) > sessionTTL {
			expired = append(expired, id)
		}
	}
	sessions := make([]session.DataSession, 0, len(expired))
	for _, id := range expired {
		sessions = append(sessions, r.entries[id].sess)
		delete(r.entries, id)
	}
	r.mu.Unlock()
	for _, s := range sessions {
		_ = s.Close()
	}
}

func (r *Registry) runSweep() {
	ticker := time.NewTicker(sweepInterval)
	for range ticker.C {
		r.sweep()
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x%x%x%x%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
