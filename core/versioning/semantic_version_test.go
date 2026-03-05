package versioning

import (
	"encoding/json"
	"testing"
)

func TestSemanticVersion_Compare(t *testing.T) {
	tests := []struct {
		name string
		a, b SemanticVersion
		want int
	}{
		{"equal zeros", VersionZero, VersionZero, 0},
		{"equal non-zero", SemanticVersion{1, 3}, SemanticVersion{1, 3}, 0},
		{"major less", SemanticVersion{0, 9}, SemanticVersion{1, 0}, -1},
		{"major greater", SemanticVersion{2, 0}, SemanticVersion{1, 9}, 1},
		{"minor less", SemanticVersion{1, 0}, SemanticVersion{1, 1}, -1},
		{"minor greater", SemanticVersion{1, 5}, SemanticVersion{1, 2}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Compare(tt.b)
			if got != tt.want {
				t.Errorf("(%s).Compare(%s) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSemanticVersion_BumpMinor(t *testing.T) {
	v := SemanticVersion{Major: 1, Minor: 3}
	bumped := v.BumpMinor()
	if bumped.Major != 1 || bumped.Minor != 4 {
		t.Errorf("BumpMinor() = %s, want 1.4", bumped)
	}
}

func TestSemanticVersion_BumpMajor(t *testing.T) {
	v := SemanticVersion{Major: 1, Minor: 7}
	bumped := v.BumpMajor()
	if bumped.Major != 2 || bumped.Minor != 0 {
		t.Errorf("BumpMajor() = %s, want 2.0", bumped)
	}
}

func TestSemanticVersion_String(t *testing.T) {
	v := SemanticVersion{Major: 3, Minor: 14}
	if s := v.String(); s != "3.14" {
		t.Errorf("String() = %q, want %q", s, "3.14")
	}
}

func TestSemanticVersion_IsZero(t *testing.T) {
	if !VersionZero.IsZero() {
		t.Error("VersionZero.IsZero() = false")
	}
	if (SemanticVersion{1, 0}).IsZero() {
		t.Error("{1,0}.IsZero() = true")
	}
}

func TestSemanticVersion_JSON(t *testing.T) {
	v := SemanticVersion{Major: 5, Minor: 12}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded SemanticVersion
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != v {
		t.Errorf("round-trip: got %s, want %s", decoded, v)
	}
}
