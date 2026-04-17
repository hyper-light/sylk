//go:build windows
// +build windows

package tea

import "testing"

func TestIntToUint32(t *testing.T) {
	got, err := intToUint32(16)
	if err != nil {
		t.Fatalf("intToUint32 returned error: %v", err)
	}
	if got != 16 {
		t.Fatalf("intToUint32 = %d, want 16", got)
	}
}

func TestIntToUint32_RejectsNegativeValues(t *testing.T) {
	if _, err := intToUint32(-1); err == nil {
		t.Fatal("expected error for negative value")
	}
}
