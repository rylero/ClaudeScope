package session

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// makeTestLog builds a minimal valid .wpilog in memory.
// It defines two fields:
//   - "/voltage" (double): values 12.0@t=1000, 11.5@t=2000, 11.0@t=3000
//   - "/enabled" (boolean): true@t=1000, false@t=2500
func makeTestLog() []byte {
	var buf bytes.Buffer

	// Magic + version (6-byte magic, then LE uint16 version 0x0100)
	buf.WriteString("WPILOG")
	buf.Write([]byte{0x00, 0x01}) // version 0x0100 LE

	// Extra header (empty)
	var extraLen [4]byte
	binary.LittleEndian.PutUint32(extraLen[:], 0)
	buf.Write(extraLen[:])

	// writeRecord uses WPILib bitfield format:
	// 1 header byte (bits 0-1=id_len-1, bits 2-3=ps_len-1, bits 4-6=ts_len-1)
	// then LE integers for id, payload_size, timestamp, then payload bytes
	writeRecord := func(entryID uint32, timestamp uint64, payload []byte) {
		// Use fixed widths: id=1B (max255), ps=2B (max65535), ts=4B
		// bitfield: id_len=1→00, ps_len=2→01, ts_len=4→011 → 0b0011_0100 = 0x34
		buf.WriteByte(0x34)
		buf.WriteByte(byte(entryID))
		var ps [2]byte
		binary.LittleEndian.PutUint16(ps[:], uint16(len(payload)))
		buf.Write(ps[:])
		var ts [4]byte
		binary.LittleEndian.PutUint32(ts[:], uint32(timestamp))
		buf.Write(ts[:])
		buf.Write(payload)
	}

	// Control: Start "/voltage" as double, entry_id=1
	var p bytes.Buffer
	p.WriteByte(0) // Start type
	var eid [4]byte
	binary.LittleEndian.PutUint32(eid[:], 1)
	p.Write(eid[:])
	writeLenString(&p, "/voltage")
	writeLenString(&p, "double")
	writeLenString(&p, "")
	writeRecord(0, 0, p.Bytes())

	// Control: Start "/enabled" as boolean, entry_id=2
	p.Reset()
	p.WriteByte(0)
	binary.LittleEndian.PutUint32(eid[:], 2)
	p.Write(eid[:])
	writeLenString(&p, "/enabled")
	writeLenString(&p, "boolean")
	writeLenString(&p, "")
	writeRecord(0, 0, p.Bytes())

	// Data: /voltage=12.0 @ t=1000
	writeRecord(1, 1000, float64Bytes(12.0))
	// Data: /enabled=true @ t=1000
	writeRecord(2, 1000, []byte{1})
	// Data: /voltage=11.5 @ t=2000
	writeRecord(1, 2000, float64Bytes(11.5))
	// Data: /enabled=false @ t=2500
	writeRecord(2, 2500, []byte{0})
	// Data: /voltage=11.0 @ t=3000
	writeRecord(1, 3000, float64Bytes(11.0))

	return buf.Bytes()
}

func writeLenString(w *bytes.Buffer, s string) {
	var lenBytes [4]byte
	binary.LittleEndian.PutUint32(lenBytes[:], uint32(len(s)))
	w.Write(lenBytes[:])
	w.WriteString(s)
}

func float64Bytes(v float64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, math.Float64bits(v))
	return b
}

func loadTestLog(t *testing.T) DataSession {
	t.Helper()
	data := makeTestLog()
	s, err := ParseWPILog(data)
	if err != nil {
		t.Fatalf("ParseWPILog failed: %v", err)
	}
	return s
}

func TestLogSession_Type(t *testing.T) {
	s := loadTestLog(t)
	if s.Type() != LogSession {
		t.Fatalf("expected LogSession, got %v", s.Type())
	}
}

func TestLogSession_Fields(t *testing.T) {
	s := loadTestLog(t)
	fields, err := s.Fields()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, f := range fields {
		keys[f.Key] = f.Type
	}
	if keys["/voltage"] != "double" {
		t.Errorf("expected /voltage=double, got %q", keys["/voltage"])
	}
	if keys["/enabled"] != "boolean" {
		t.Errorf("expected /enabled=boolean, got %q", keys["/enabled"])
	}
}

func TestLogSession_TimeRange(t *testing.T) {
	s := loadTestLog(t)
	start, end, err := s.TimeRange()
	if err != nil {
		t.Fatal(err)
	}
	if start != 1000 {
		t.Errorf("expected start=1000, got %d", start)
	}
	if end != 3000 {
		t.Errorf("expected end=3000, got %d", end)
	}
}

func TestLogSession_GetValues_AtTimestamp(t *testing.T) {
	s := loadTestLog(t)
	pts, err := s.GetValues([]string{"/voltage"}, 2500)
	if err != nil {
		t.Fatal(err)
	}
	pt := pts["/voltage"]
	if pt == nil {
		t.Fatal("expected /voltage, got nil")
	}
	if pt.Value.(float64) != 11.5 {
		t.Errorf("expected 11.5, got %v", pt.Value)
	}
}

func TestLogSession_GetValues_Latest(t *testing.T) {
	s := loadTestLog(t)
	// t=0 means latest
	pts, err := s.GetValues([]string{"/voltage"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pts["/voltage"].Value.(float64) != 11.0 {
		t.Errorf("expected 11.0, got %v", pts["/voltage"].Value)
	}
}

func TestLogSession_GetValues_KeyNotFound(t *testing.T) {
	s := loadTestLog(t)
	_, err := s.GetValues([]string{"/nonexistent"}, 0)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestLogSession_GetRanges(t *testing.T) {
	s := loadTestLog(t)
	ranges, err := s.GetRanges([]string{"/voltage"}, 1000, 2500)
	if err != nil {
		t.Fatal(err)
	}
	pts := ranges["/voltage"]
	if len(pts) != 2 {
		t.Fatalf("expected 2 points in [1000,2500], got %d", len(pts))
	}
	if pts[0].Value.(float64) != 12.0 {
		t.Errorf("expected 12.0, got %v", pts[0].Value)
	}
	if pts[1].Value.(float64) != 11.5 {
		t.Errorf("expected 11.5, got %v", pts[1].Value)
	}
}

func TestLogSession_FindBoolRanges(t *testing.T) {
	s := loadTestLog(t)
	ranges, err := s.FindBoolRanges("/enabled", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("expected 1 true range, got %d", len(ranges))
	}
	if ranges[0].Start != 1000 || ranges[0].End != 2500 {
		t.Errorf("expected [1000,2500], got [%d,%d]", ranges[0].Start, ranges[0].End)
	}
}

func TestLogSession_FindThresholdRanges(t *testing.T) {
	s := loadTestLog(t)
	// voltage is in [11.0, 11.5] for t=2000..3000
	ranges, err := s.FindThresholdRanges("/voltage", 11.0, 11.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("expected 1 range, got %d", len(ranges))
	}
	if ranges[0].Start != 2000 {
		t.Errorf("expected start=2000, got %d", ranges[0].Start)
	}
}

func TestLogSession_Stats(t *testing.T) {
	s := loadTestLog(t)
	stats, err := s.Stats("/voltage", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// values: 12.0, 11.5, 11.0 → mean=11.5, min=11.0, max=12.0
	if stats.Min != 11.0 {
		t.Errorf("expected min=11.0, got %f", stats.Min)
	}
	if stats.Max != 12.0 {
		t.Errorf("expected max=12.0, got %f", stats.Max)
	}
	if stats.Mean != 11.5 {
		t.Errorf("expected mean=11.5, got %f", stats.Mean)
	}
}

func TestLogSession_GetRanges_CarryOver(t *testing.T) {
	// Window starts at t=2600, after the last /enabled change at t=2500.
	// Expect carry-over: enabled=false (set at t=2500) returned even though
	// no change occurred inside the window.
	s := loadTestLog(t)
	ranges, err := s.GetRanges([]string{"/enabled"}, 2600, 3000)
	if err != nil {
		t.Fatal(err)
	}
	pts := ranges["/enabled"]
	if len(pts) != 1 {
		t.Fatalf("expected 1 carry-over point, got %d", len(pts))
	}
	if pts[0].Value.(bool) != false {
		t.Errorf("expected carry-over value false, got %v", pts[0].Value)
	}
}

func TestLogSession_Set_ReturnsError(t *testing.T) {
	s := loadTestLog(t)
	err := s.Set(map[string]any{"/voltage": 12.0})
	if err == nil {
		t.Fatal("expected error: log sessions are read-only")
	}
}

// Worlds / AdvantageKit logs sometimes include empty control records (entry
// ID 0, zero-length payload). Those must be skipped, not fail the whole file.
func TestParseWPILog_SkipsEmptyControlPayload(t *testing.T) {
	base := makeTestLog()

	var buf bytes.Buffer
	buf.Write(base[:12]) // magic(6) + version(2) + extraLen(4) = header with empty extra

	// Empty control record: tag 0x34, id=0, payload_size=0, timestamp=0
	buf.WriteByte(0x34)
	buf.WriteByte(0) // entry id
	buf.Write([]byte{0, 0}) // payload size
	buf.Write([]byte{0, 0, 0, 0}) // timestamp
	// no payload bytes

	buf.Write(base[12:]) // rest of valid records

	s, err := ParseWPILog(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseWPILog failed on log with empty control: %v", err)
	}
	fields, err := s.Fields()
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) < 2 {
		t.Fatalf("expected fields after empty control skip, got %d", len(fields))
	}
}
