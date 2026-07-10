package session

import (
	"encoding/binary"
	"math"
	"testing"
)

// packDoubles builds a WPILib-style little-endian float64 payload.
func packDoubles(vals ...float64) []byte {
	buf := make([]byte, len(vals)*8)
	for i, v := range vals {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v))
	}
	return buf
}

func TestDecodeStructPose2d(t *testing.T) {
	raw := packDoubles(1.5, -2.25, 0.75)
	got, ok := DecodeStruct("struct:Pose2d", raw)
	if !ok {
		t.Fatal("expected Pose2d to decode")
	}
	m, isMap := got.(map[string]any)
	if !isMap {
		t.Fatalf("expected map, got %T", got)
	}
	for k, want := range map[string]float64{"x": 1.5, "y": -2.25, "theta": 0.75} {
		if m[k] != want {
			t.Errorf("field %s = %v, want %v", k, m[k], want)
		}
	}
}

func TestDecodeStructSwerveModuleStateArray(t *testing.T) {
	// two modules: (speed, angle) each
	raw := packDoubles(3.0, 0.1, 4.0, -0.2)
	got, ok := DecodeStruct("struct:SwerveModuleState[]", raw)
	if !ok {
		t.Fatal("expected SwerveModuleState[] to decode")
	}
	arr, isArr := got.([]any)
	if !isArr || len(arr) != 2 {
		t.Fatalf("expected 2-element array, got %T len=%d", got, len(arr))
	}
	m0 := arr[0].(map[string]any)
	if m0["speed"] != 3.0 || m0["angle"] != 0.1 {
		t.Errorf("module0 = %v", m0)
	}
	m1 := arr[1].(map[string]any)
	if m1["speed"] != 4.0 || m1["angle"] != -0.2 {
		t.Errorf("module1 = %v", m1)
	}
}

func TestDecodeStructRejectsBadLength(t *testing.T) {
	// Pose2d needs 24 bytes; give 16.
	if _, ok := DecodeStruct("struct:Pose2d", packDoubles(1, 2)); ok {
		t.Error("expected short Pose2d payload to be rejected")
	}
	// Array length not a multiple of element size.
	if _, ok := DecodeStruct("struct:SwerveModuleState[]", packDoubles(1, 2, 3)); ok {
		t.Error("expected ragged array payload to be rejected")
	}
}

func TestDecodeStructUnknownAndNonStruct(t *testing.T) {
	if _, ok := DecodeStruct("struct:MysteryType", packDoubles(1)); ok {
		t.Error("unknown struct should not decode")
	}
	if _, ok := DecodeStruct("double", packDoubles(1)); ok {
		t.Error("non-struct type should not decode")
	}
	if IsKnownStruct("struct:MysteryType") {
		t.Error("MysteryType should not be reported known")
	}
	if !IsKnownStruct("struct:ChassisSpeeds") {
		t.Error("ChassisSpeeds should be reported known")
	}
}

func TestDecodeValueWiresStructs(t *testing.T) {
	raw := packDoubles(0.5) // Rotation2d
	got, err := decodeValue("struct:Rotation2d", raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.(map[string]any)
	if !ok || m["value"] != 0.5 {
		t.Errorf("Rotation2d decode = %v (%T)", got, got)
	}
}
