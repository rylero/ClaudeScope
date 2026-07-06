package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

// wpilogSession holds all data parsed from a .wpilog file.
type wpilogSession struct {
	fields  map[uint32]FieldInfo   // entry_id → field
	data    map[uint32][]DataPoint // entry_id → sorted data points
	keyToID map[string]uint32      // field name → entry_id
	start   int64
	end     int64
}

// ParseWPILog parses a .wpilog binary and returns a ready-to-query session.
func ParseWPILog(raw []byte) (DataSession, error) {
	r := bytes.NewReader(raw)

	// Magic
	magic := make([]byte, 6)
	if _, err := r.Read(magic); err != nil || string(magic) != "WPILOG" {
		return nil, errors.New("invalid wpilog: bad magic")
	}

	// Version (2 bytes LE, minor then major — we accept any 1.x)
	var version [2]byte
	if _, err := r.Read(version[:]); err != nil {
		return nil, fmt.Errorf("invalid wpilog: missing version: %w", err)
	}

	// Extra header string
	var extraLen uint32
	if err := binary.Read(r, binary.LittleEndian, &extraLen); err != nil {
		return nil, fmt.Errorf("invalid wpilog: missing extra header length: %w", err)
	}
	if _, err := r.Seek(int64(extraLen), 1); err != nil {
		return nil, fmt.Errorf("invalid wpilog: truncated extra header: %w", err)
	}

	s := &wpilogSession{
		fields:  make(map[uint32]FieldInfo),
		data:    make(map[uint32][]DataPoint),
		keyToID: make(map[string]uint32),
		start:   math.MaxInt64,
		end:     math.MinInt64,
	}

	for r.Len() > 0 {
		tag, err := r.ReadByte()
		if err != nil {
			break
		}

		// Decode bitfield lengths (00=1B, 01=2B, 10=3B, 11=4B)
		idLen := int((tag & 0x03) + 1)
		sizeLen := int(((tag >> 2) & 0x03) + 1)
		tsLen := int(((tag >> 4) & 0x07) + 1)

		// Read the packed integers
		entryID := uint32(readUintLE(r, idLen))
		payloadSize := uint32(readUintLE(r, sizeLen))
		timestamp := readUintLE(r, tsLen)

		payload := make([]byte, payloadSize)
		if _, err := io.ReadFull(r, payload); err != nil {
			break // truncated record at end of file (normal for FRC logs)
		}

		if entryID == 0 {
			// ID 0 = Control Record. Start records populate the field map.
			if err := s.applyControl(payload); err != nil {
				return nil, fmt.Errorf("control record error: %w", err)
			}
		} else {
			// Regular data record
			if fi, ok := s.fields[entryID]; ok {
				val, _ := decodeValue(fi.Type, payload)
				ts := int64(timestamp)
				s.data[entryID] = append(s.data[entryID], DataPoint{Timestamp: ts, Value: val})
				if ts < s.start {
					s.start = ts
				}
				if ts > s.end {
					s.end = ts
				}
			}
		}
	}

	return s, nil
}

func readUintLE(r *bytes.Reader, n int) uint64 {
	var val uint64
	for i := 0; i < n; i++ {
		b, _ := r.ReadByte()
		val |= uint64(b) << (8 * i)
	}
	return val
}

func (s *wpilogSession) applyControl(payload []byte) error {
	if len(payload) < 1 {
		return errors.New("empty control payload")
	}
	r := bytes.NewReader(payload[1:])
	switch payload[0] {
	case 0: // Start
		var eid uint32
		if err := binary.Read(r, binary.LittleEndian, &eid); err != nil {
			return err
		}
		name, err := readLenString(r)
		if err != nil {
			return err
		}
		typeStr, err := readLenString(r)
		if err != nil {
			return err
		}
		s.fields[eid] = FieldInfo{Key: name, Type: typeStr}
		s.keyToID[name] = eid
	case 1: // SetMetadata — ignore
	case 2: // Finish — ignore
	}
	return nil
}

func readLenString(r *bytes.Reader) (string, error) {
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	buf := make([]byte, length)
	if _, err := r.Read(buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func decodeValue(typeStr string, payload []byte) (any, error) {
	switch typeStr {
	case "double":
		if len(payload) < 8 {
			return nil, errors.New("short double")
		}
		bits := binary.LittleEndian.Uint64(payload[:8])
		return math.Float64frombits(bits), nil
	case "float":
		if len(payload) < 4 {
			return nil, errors.New("short float")
		}
		bits := binary.LittleEndian.Uint32(payload[:4])
		return float64(math.Float32frombits(bits)), nil
	case "int64":
		if len(payload) < 8 {
			return nil, errors.New("short int64")
		}
		return int64(binary.LittleEndian.Uint64(payload[:8])), nil
	case "boolean":
		if len(payload) < 1 {
			return nil, errors.New("short boolean")
		}
		return payload[0] != 0, nil
	case "string":
		return string(payload), nil
	default:
		return payload, nil
	}
}

// readVarint decodes the WPILib variable-length integer encoding.
// Low 2 bits of first byte indicate extra byte count: 0→1B, 1→2B, 2→4B, 3→8B.
func readVarint(r *bytes.Reader) (uint64, error) {
	b0, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	switch b0 & 0x03 {
	case 0:
		return uint64(b0) >> 2, nil
	case 1:
		b1, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		return (uint64(b0) | uint64(b1)<<8) >> 2, nil
	case 2:
		b1, _ := r.ReadByte()
		b2, _ := r.ReadByte()
		b3, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		return (uint64(b0) | uint64(b1)<<8 | uint64(b2)<<16 | uint64(b3)<<24) >> 2, nil
	default:
		b1, _ := r.ReadByte()
		b2, _ := r.ReadByte()
		b3, _ := r.ReadByte()
		b4, _ := r.ReadByte()
		b5, _ := r.ReadByte()
		b6, _ := r.ReadByte()
		b7, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v := uint64(b0) | uint64(b1)<<8 | uint64(b2)<<16 | uint64(b3)<<24 |
			uint64(b4)<<32 | uint64(b5)<<40 | uint64(b6)<<48 | uint64(b7)<<56
		return v >> 2, nil
	}
}

// writeVarintBuf encodes v into the WPILib variable-length integer format.
// Used only by tests to build fixture .wpilog files.
func writeVarintBuf(buf *bytes.Buffer, v uint64) {
	switch {
	case v <= 0x3F:
		buf.WriteByte(byte(v<<2) | 0x00)
	case v <= 0x3FFF:
		encoded := (v << 2) | 0x01
		buf.WriteByte(byte(encoded))
		buf.WriteByte(byte(encoded >> 8))
	case v <= 0x3FFFFFFF:
		encoded := (v << 2) | 0x02
		buf.WriteByte(byte(encoded))
		buf.WriteByte(byte(encoded >> 8))
		buf.WriteByte(byte(encoded >> 16))
		buf.WriteByte(byte(encoded >> 24))
	default:
		encoded := (v << 2) | 0x03
		for i := 0; i < 8; i++ {
			buf.WriteByte(byte(encoded >> (i * 8)))
		}
	}
}

// --- DataSession implementation ---

func (s *wpilogSession) Type() SessionType { return LogSession }

func (s *wpilogSession) Fields() ([]FieldInfo, error) {
	out := make([]FieldInfo, 0, len(s.fields))
	for _, fi := range s.fields {
		out = append(out, fi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *wpilogSession) TimeRange() (int64, int64, error) {
	return s.start, s.end, nil
}

func (s *wpilogSession) GetValues(keys []string, t int64) (map[string]*DataPoint, error) {
	result := make(map[string]*DataPoint, len(keys))
	for _, key := range keys {
		id, ok := s.keyToID[key]
		if !ok {
			return nil, fmt.Errorf("key not found: %s", key)
		}
		pts := s.data[id]
		if len(pts) == 0 {
			continue
		}
		if t == 0 {
			cp := pts[len(pts)-1]
			result[key] = &cp
			continue
		}
		idx := sort.Search(len(pts), func(i int) bool { return pts[i].Timestamp > t }) - 1
		if idx >= 0 {
			cp := pts[idx]
			result[key] = &cp
		}
	}
	return result, nil
}

func (s *wpilogSession) GetRanges(keys []string, start, end int64) (map[string][]DataPoint, error) {
	if start < 0 {
		start = s.end + start
	}
	if end < 0 {
		end = s.end + end
	}
	if end == 0 {
		end = s.end
	}

	result := make(map[string][]DataPoint, len(keys))
	for _, key := range keys {
		id, ok := s.keyToID[key]
		if !ok {
			return nil, fmt.Errorf("key not found: %s", key)
		}
		pts := s.data[id]
		var out []DataPoint
		for _, p := range pts {
			if p.Timestamp >= start && p.Timestamp <= end {
				out = append(out, p)
			}
		}
		// If no points fell in the window, include the last value before start so
		// the caller knows what was active at window open (carry-over).
		if len(out) == 0 && len(pts) > 0 {
			idx := sort.Search(len(pts), func(i int) bool { return pts[i].Timestamp > start }) - 1
			if idx >= 0 {
				out = append(out, pts[idx])
			}
		}
		result[key] = out
	}
	return result, nil
}

func (s *wpilogSession) FindBoolRanges(key string, value bool) ([]TimeRange, error) {
	id, ok := s.keyToID[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return FindRuns(s.data[id], s.end, func(v any) (matches, applicable bool) {
		b, ok := v.(bool)
		return ok && b == value, ok
	}), nil
}

func (s *wpilogSession) FindThresholdRanges(key string, min, max float64) ([]TimeRange, error) {
	id, ok := s.keyToID[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return FindRuns(s.data[id], s.end, func(v any) (matches, applicable bool) {
		f, err := ToFloat64(v)
		return err == nil && f >= min && f <= max, err == nil
	}), nil
}

// FindRuns collapses pts into contiguous [start,end) intervals where pred(value)
// matches. Points pred deems inapplicable (wrong type for the field) are
// skipped without affecting an open run. A run still open at the last point
// is closed at logEnd. Shared by FindBoolRanges, FindThresholdRanges, and the
// query package's `ranges` stage.
func FindRuns(pts []DataPoint, logEnd int64, pred func(any) (matches, applicable bool)) []TimeRange {
	var ranges []TimeRange
	inRange := false
	var rangeStart int64
	for _, p := range pts {
		match, applicable := pred(p.Value)
		if !applicable {
			continue
		}
		if match && !inRange {
			inRange = true
			rangeStart = p.Timestamp
		} else if !match && inRange {
			ranges = append(ranges, TimeRange{Start: rangeStart, End: p.Timestamp})
			inRange = false
		}
	}
	if inRange {
		ranges = append(ranges, TimeRange{Start: rangeStart, End: logEnd})
	}
	return ranges
}

func (s *wpilogSession) Stats(key string, start, end int64) (*Stats, error) {
	if start < 0 {
		start = s.end + start
	}
	if end == 0 {
		end = s.end
	}
	ranges, err := s.GetRanges([]string{key}, start, end)
	if err != nil {
		return nil, err
	}
	pts := ranges[key]
	if len(pts) == 0 {
		return &Stats{}, nil
	}

	var vals []float64
	for _, p := range pts {
		v, err := ToFloat64(p.Value)
		if err != nil {
			continue
		}
		vals = append(vals, v)
	}
	if len(vals) == 0 {
		return &Stats{}, fmt.Errorf("field %s has no numeric values", key)
	}

	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)

	var sum float64
	for _, v := range sorted {
		sum += v
	}

	stats := &Stats{
		Mean:   sum / float64(len(sorted)),
		Median: Percentile(sorted, 0.5),
		Min:    sorted[0],
		Max:    sorted[len(sorted)-1],
		Q1:     Percentile(sorted, 0.25),
		Q3:     Percentile(sorted, 0.75),
	}

	if len(pts) >= 2 {
		var deltas []float64
		for i := 1; i < len(pts); i++ {
			v0, e0 := ToFloat64(pts[i-1].Value)
			v1, e1 := ToFloat64(pts[i].Value)
			if e0 != nil || e1 != nil {
				continue
			}
			dt := float64(pts[i].Timestamp-pts[i-1].Timestamp) / 1e6
			if dt <= 0 {
				continue
			}
			deltas = append(deltas, (v1-v0)/dt)
		}
		if len(deltas) > 0 {
			var dsum float64
			dmin, dmax := deltas[0], deltas[0]
			for _, d := range deltas {
				dsum += d
				if d < dmin {
					dmin = d
				}
				if d > dmax {
					dmax = d
				}
			}
			stats.AvgDelta = dsum / float64(len(deltas))
			stats.MinDelta = dmin
			stats.MaxDelta = dmax
		}
	}

	return stats, nil
}

func (s *wpilogSession) Set(_ map[string]any) error {
	return errors.New("read-only session: cannot set values on a log file")
}

func (s *wpilogSession) Close() error { return nil }

// --- helpers ---

func Percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 1 {
		return sorted[0]
	}
	idx := p * float64(n-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= n {
		return sorted[n-1]
	}
	return sorted[lo]*(1-(idx-float64(lo))) + sorted[hi]*(idx-float64(lo))
}

func ToFloat64(v any) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case int:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("not a numeric value: %T", v)
	}
}
