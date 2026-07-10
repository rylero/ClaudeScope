package session

import (
	"encoding/binary"
	"math"
	"strings"
)

// WPILib struct-serialized telemetry (Pose2d, ChassisSpeeds, SwerveModuleState[],
// ...) arrives as a fixed little-endian packing of float64s under a type string
// like "struct:Pose2d" or "struct:SwerveModuleState[]". Left undecoded these
// serialize to an opaque base64 blob in JSON output — the single most common
// thing you want to read on a drivetrain (robot pose, module angles, chassis
// speeds) is exactly what's unreadable. DecodeStruct turns the known WPILib
// schemas into named-field maps so `get`/`range` emit {"x":..,"y":..,"theta":..}
// instead of "+f/rQnzl...". The packing is fixed and published by WPILib; every
// field below is a float64, so a schema is just an ordered list of field names.
//
// Field names follow the WPILib member order. Rotation members are flattened to
// their radian value (Rotation2d) or quaternion components (Rotation3d), matching
// how WPILib packs them inline.
var structSchemas = map[string][]string{
	"Rotation2d":           {"value"},                                   // radians
	"Rotation3d":           {"q_w", "q_x", "q_y", "q_z"},                // quaternion
	"Quaternion":           {"w", "x", "y", "z"},                        //
	"Translation2d":        {"x", "y"},                                  //
	"Translation3d":        {"x", "y", "z"},                             //
	"Pose2d":               {"x", "y", "theta"},                         // theta in radians
	"Pose3d":               {"x", "y", "z", "q_w", "q_x", "q_y", "q_z"}, //
	"Transform2d":          {"x", "y", "theta"},                         //
	"Transform3d":          {"x", "y", "z", "q_w", "q_x", "q_y", "q_z"}, //
	"Twist2d":              {"dx", "dy", "dtheta"},                      //
	"Twist3d":              {"dx", "dy", "dz", "rx", "ry", "rz"},        //
	"ChassisSpeeds":        {"vx", "vy", "omega"},                       // m/s, m/s, rad/s
	"SwerveModuleState":    {"speed", "angle"},                          // m/s, radians
	"SwerveModulePosition": {"distance", "angle"},                       // m, radians
}

// structTypeName pulls "Pose2d" out of "struct:Pose2d" / "struct:Pose2d[]" and
// reports whether the type is an array. Returns ok=false for anything that is
// not a struct type string.
func structTypeName(typeStr string) (name string, isArray, ok bool) {
	const prefix = "struct:"
	if !strings.HasPrefix(typeStr, prefix) {
		return "", false, false
	}
	name = strings.TrimPrefix(typeStr, prefix)
	if strings.HasSuffix(name, "[]") {
		return strings.TrimSuffix(name, "[]"), true, true
	}
	return name, false, true
}

// IsKnownStruct reports whether typeStr names a WPILib struct (or struct array)
// this package can decode into named fields.
func IsKnownStruct(typeStr string) bool {
	name, _, ok := structTypeName(typeStr)
	if !ok {
		return false
	}
	_, known := structSchemas[name]
	return known
}

// DecodeStruct decodes a WPILib struct-serialized payload into a named-field
// map (or, for a "T[]" array type, a slice of such maps). It returns ok=false
// when the type is unknown or the payload length doesn't match the schema, so
// callers can fall back to the raw bytes rather than emit a wrong decode.
func DecodeStruct(typeStr string, raw []byte) (any, bool) {
	name, isArray, ok := structTypeName(typeStr)
	if !ok {
		return nil, false
	}
	fields, known := structSchemas[name]
	if !known {
		return nil, false
	}
	elemSize := len(fields) * 8

	if isArray {
		if elemSize == 0 || len(raw)%elemSize != 0 {
			return nil, false
		}
		n := len(raw) / elemSize
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = decodeStructFields(fields, raw[i*elemSize:(i+1)*elemSize])
		}
		return out, true
	}

	if len(raw) != elemSize {
		return nil, false
	}
	return decodeStructFields(fields, raw), true
}

// decodeStructFields reads len(fields) little-endian float64s from raw (which
// must be exactly len(fields)*8 bytes) into a name→value map.
func decodeStructFields(fields []string, raw []byte) map[string]any {
	m := make(map[string]any, len(fields))
	for i, f := range fields {
		bits := binary.LittleEndian.Uint64(raw[i*8 : i*8+8])
		m[f] = math.Float64frombits(bits)
	}
	return m
}
