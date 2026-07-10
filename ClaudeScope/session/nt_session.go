package session

import (
	"fmt"
	"strings"
	"sync"
	"time"

	nt4 "github.com/levifitzpatrick1/go-nt4"
)

// ntSession wraps a live go-nt4 client and implements DataSession.
// Subscribes to all topics on connect and buffers all received values.
type ntSession struct {
	client      *nt4.Client
	sub         *nt4.Subscription
	mu          sync.RWMutex
	history     map[string][]DataPoint // key → sorted data points
	fieldMeta   map[string]string      // key → type string
	connectedAt time.Time
}

// NewNTSession connects to an NT4 server at addr and returns a DataSession.
func NewNTSession(addr string) (DataSession, error) {
	opts := nt4.DefaultClientOptions(addr)
	opts.Identity = "ClaudeScope"
	c := nt4.NewClient(opts)
	if err := c.Connect(); err != nil {
		return nil, fmt.Errorf("NT4 connect failed: %w", err)
	}
	return NewNTSessionFromClient(c), nil
}

// NewNTSessionFromClient wraps an already-connected nt4.Client.
func NewNTSessionFromClient(c *nt4.Client) DataSession {
	s := &ntSession{
		client:      c,
		history:     make(map[string][]DataPoint),
		fieldMeta:   make(map[string]string),
		connectedAt: time.Now(),
	}
	sub := c.Subscribe([]string{"/"}, &nt4.SubscribeOptions{Prefix: true})
	s.sub = sub
	go s.pump(sub)
	return s
}

func (s *ntSession) pump(sub *nt4.Subscription) {
	for update := range sub.Updates() {
		s.mu.Lock()
		key := update.Topic.Name
		s.fieldMeta[key] = update.Topic.Type
		s.history[key] = append(s.history[key], DataPoint{
			Timestamp: update.Timestamp,
			Value:     decodeNTValue(update.Topic.Type, update.Value),
		})
		s.mu.Unlock()
	}
}

func (s *ntSession) Type() SessionType { return LiveSession }

func (s *ntSession) Fields() ([]FieldInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]FieldInfo, 0, len(s.fieldMeta))
	for key, typ := range s.fieldMeta {
		out = append(out, FieldInfo{Key: key, Type: typ})
	}
	return out, nil
}

func (s *ntSession) TimeRange() (int64, int64, error) {
	elapsed := time.Since(s.connectedAt).Microseconds()
	return 0, elapsed, nil
}

func (s *ntSession) GetValues(keys []string, t int64) (map[string]*DataPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*DataPoint, len(keys))
	for _, key := range keys {
		pts := s.history[key]
		if len(pts) == 0 {
			return nil, fmt.Errorf("key not found: %s", key)
		}
		if t == 0 {
			cp := pts[len(pts)-1]
			result[key] = &cp
			continue
		}
		// Find the last point at or before t.
		for i := len(pts) - 1; i >= 0; i-- {
			if pts[i].Timestamp <= t {
				cp := pts[i]
				result[key] = &cp
				break
			}
		}
	}
	return result, nil
}

func (s *ntSession) GetRanges(keys []string, start, end int64) (map[string][]DataPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if end == 0 {
		end = time.Since(s.connectedAt).Microseconds()
	}
	result := make(map[string][]DataPoint, len(keys))
	for _, key := range keys {
		pts := s.history[key]
		var out []DataPoint
		for _, p := range pts {
			if p.Timestamp >= start && p.Timestamp <= end {
				out = append(out, p)
			}
		}
		// If no points fell in the window, include the last value before start
		// so the caller knows what was active at window open (carry-over).
		if len(out) == 0 && len(pts) > 0 {
			for i := len(pts) - 1; i >= 0; i-- {
				if pts[i].Timestamp <= start {
					out = append(out, pts[i])
					break
				}
			}
		}
		result[key] = out
	}
	return result, nil
}

func (s *ntSession) FindBoolRanges(key string, value bool) ([]TimeRange, error) {
	s.mu.RLock()
	pts := s.history[key]
	s.mu.RUnlock()
	if pts == nil {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	tmp := &wpilogSession{
		keyToID: map[string]uint32{key: 1},
		data:    map[uint32][]DataPoint{1: pts},
		end:     pts[len(pts)-1].Timestamp,
	}
	return tmp.FindBoolRanges(key, value)
}

func (s *ntSession) FindThresholdRanges(key string, min, max float64) ([]TimeRange, error) {
	s.mu.RLock()
	pts := s.history[key]
	s.mu.RUnlock()
	if pts == nil {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	tmp := &wpilogSession{
		keyToID: map[string]uint32{key: 1},
		data:    map[uint32][]DataPoint{1: pts},
		end:     pts[len(pts)-1].Timestamp,
	}
	return tmp.FindThresholdRanges(key, min, max)
}

func (s *ntSession) Stats(key string, start, end int64) (*Stats, error) {
	s.mu.RLock()
	pts := s.history[key]
	s.mu.RUnlock()
	if pts == nil {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	if end == 0 {
		end = pts[len(pts)-1].Timestamp
	}
	tmp := &wpilogSession{
		keyToID: map[string]uint32{key: 1},
		data:    map[uint32][]DataPoint{1: pts},
		end:     end,
	}
	return tmp.Stats(key, start, end)
}

func (s *ntSession) Set(pairs map[string]any) error {
	for key, val := range pairs {
		if err := s.checkChooserActive(key); err != nil {
			return err
		}
		s.mu.RLock()
		typeStr, ok := s.fieldMeta[key]
		s.mu.RUnlock()
		if !ok {
			typeStr = inferNTType(val)
		}
		topic := s.client.Publish(key, typeStr, nil)
		s.client.SetValue(topic, val)
	}
	return nil
}

// checkChooserActive returns an error when the caller tries to set a SendableChooser's
// "active" field. The robot re-publishes that field every loop, so any write is
// immediately overwritten. Dashboards must write to "<prefix>/selected" instead.
func (s *ntSession) checkChooserActive(key string) error {
	if !strings.HasSuffix(key, "/active") {
		return nil
	}
	prefix := strings.TrimSuffix(key, "/active")
	s.mu.RLock()
	pts := s.history[prefix+"/.type"]
	s.mu.RUnlock()
	if len(pts) > 0 {
		if v, ok := pts[len(pts)-1].Value.(string); ok && v == "String Chooser" {
			return fmt.Errorf(
				"cannot set SendableChooser 'active' field — the robot re-publishes it each loop and will immediately overwrite your value. Write to %q instead",
				prefix+"/selected",
			)
		}
	}
	return nil
}

func (s *ntSession) Close() error {
	if s.sub != nil {
		s.client.Unsubscribe(s.sub)
	}
	s.client.Disconnect()
	return nil
}

// decodeNTValue decodes a known WPILib struct topic (delivered by NT4 as raw
// packed bytes) into a named-field map, matching the log path. Non-struct
// topics and unknown/mismatched struct payloads pass through unchanged.
func decodeNTValue(typeStr string, v any) any {
	if raw, ok := v.([]byte); ok {
		if decoded, ok := DecodeStruct(typeStr, raw); ok {
			return decoded
		}
	}
	return v
}

func inferNTType(v any) string {
	switch v.(type) {
	case float64, float32:
		return nt4.TypeDouble
	case bool:
		return nt4.TypeBoolean
	case int64, int:
		return nt4.TypeInt
	default:
		return nt4.TypeString
	}
}

// Compile-time check that ntSession satisfies DataSession.
var _ DataSession = (*ntSession)(nil)
