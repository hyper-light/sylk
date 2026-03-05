package versioning

import (
	"encoding/json"
	"fmt"
)

// SemanticVersion represents a two-component version: Major.Minor.
// Major bumps on disk flushes (checkpoints), Minor bumps on pipeline merges (deltas).
type SemanticVersion struct {
	Major uint32 `json:"major"`
	Minor uint32 `json:"minor"`
}

// VersionZero is the zero-value version, before any changes.
var VersionZero = SemanticVersion{}

// Compare returns -1 if v < other, 0 if equal, 1 if v > other.
func (v SemanticVersion) Compare(other SemanticVersion) int {
	if v.Major != other.Major {
		return compareUint32(v.Major, other.Major)
	}
	return compareUint32(v.Minor, other.Minor)
}

func compareUint32(a, b uint32) int {
	if a < b {
		return -1
	}
	return 1
}

// String returns "Major.Minor" representation.
func (v SemanticVersion) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// IsZero returns true if both components are zero.
func (v SemanticVersion) IsZero() bool {
	return v.Major == 0 && v.Minor == 0
}

// BumpMinor returns a new version with Minor incremented by 1.
func (v SemanticVersion) BumpMinor() SemanticVersion {
	return SemanticVersion{Major: v.Major, Minor: v.Minor + 1}
}

// BumpMajor returns a new version with Major incremented by 1 and Minor reset to 0.
func (v SemanticVersion) BumpMajor() SemanticVersion {
	return SemanticVersion{Major: v.Major + 1, Minor: 0}
}

// MarshalJSON implements json.Marshaler.
func (v SemanticVersion) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Major uint32 `json:"major"`
		Minor uint32 `json:"minor"`
	}{Major: v.Major, Minor: v.Minor})
}

// UnmarshalJSON implements json.Unmarshaler.
func (v *SemanticVersion) UnmarshalJSON(data []byte) error {
	var raw struct {
		Major uint32 `json:"major"`
		Minor uint32 `json:"minor"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v.Major = raw.Major
	v.Minor = raw.Minor
	return nil
}
