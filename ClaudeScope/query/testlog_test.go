package query_test

import (
	"bytes"
	"encoding/binary"
	"math"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// buildTestLog assembles a minimal .wpilog in memory with three fields:
//   - "CurrentA" (double): 10@0, 50@1000, 45@2000, 5@3000
//   - "CurrentB" (double): 10@0, 20@1000, 55@2000, 5@3000
//   - "Enabled"  (boolean): true@0, false@2500
//
// so `CurrentA>40 and CurrentB>40` is true only in [2000,2500).
func buildTestLog() session.DataSession {
	var buf bytes.Buffer
	buf.WriteString("WPILOG")
	buf.Write([]byte{0x00, 0x01})
	var extraLen [4]byte
	binary.LittleEndian.PutUint32(extraLen[:], 0)
	buf.Write(extraLen[:])

	writeRecord := func(entryID uint32, timestamp uint64, payload []byte) {
		buf.WriteByte(0x34) // id_len=1B, size_len=2B, ts_len=4B
		buf.WriteByte(byte(entryID))
		var ps [2]byte
		binary.LittleEndian.PutUint16(ps[:], uint16(len(payload)))
		buf.Write(ps[:])
		var ts [4]byte
		binary.LittleEndian.PutUint32(ts[:], uint32(timestamp))
		buf.Write(ts[:])
		buf.Write(payload)
	}
	writeLenString := func(w *bytes.Buffer, s string) {
		var l [4]byte
		binary.LittleEndian.PutUint32(l[:], uint32(len(s)))
		w.Write(l[:])
		w.WriteString(s)
	}
	startField := func(id uint32, name, typ string) {
		var p bytes.Buffer
		p.WriteByte(0)
		var eid [4]byte
		binary.LittleEndian.PutUint32(eid[:], id)
		p.Write(eid[:])
		writeLenString(&p, name)
		writeLenString(&p, typ)
		writeLenString(&p, "")
		writeRecord(0, 0, p.Bytes())
	}
	float64Bytes := func(f float64) []byte {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(f))
		return b[:]
	}
	boolByte := func(v bool) []byte {
		if v {
			return []byte{1}
		}
		return []byte{0}
	}

	startField(1, "CurrentA", "double")
	startField(2, "CurrentB", "double")
	startField(3, "Enabled", "boolean")
	startField(4, "Message", "string")
	startField(5, "/PDH/Voltage", "double") // slash-path field: division must not collide

	writeRecord(1, 0, float64Bytes(10))
	writeRecord(2, 0, float64Bytes(10))
	writeRecord(3, 0, boolByte(true))
	writeRecord(4, 0, []byte("Brownout on channel 3"))
	writeRecord(5, 0, float64Bytes(12))

	writeRecord(1, 1000, float64Bytes(50))
	writeRecord(2, 1000, float64Bytes(20))

	writeRecord(1, 2000, float64Bytes(45))
	writeRecord(2, 2000, float64Bytes(55))

	writeRecord(3, 2500, boolByte(false))

	writeRecord(1, 3000, float64Bytes(5))
	writeRecord(2, 3000, float64Bytes(5))
	writeRecord(4, 3000, []byte("Brownout on channel 7"))
	writeRecord(5, 3000, float64Bytes(6))

	sess, err := session.ParseWPILog(buf.Bytes())
	if err != nil {
		panic(err)
	}
	return sess
}
