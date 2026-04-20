package recipe

import "testing"

func TestPlatform_Matches_EmptyPinMatchesAny(t *testing.T) {
	host := Platform{OS: "linux", Arch: "amd64", Libc: "glibc"}
	if !host.Matches(Platform{}) {
		t.Fatal("empty pin should match any host")
	}
}

func TestPlatform_Matches_PartialPinMatchesByField(t *testing.T) {
	host := Platform{OS: "linux", Arch: "amd64", Libc: "glibc"}
	if !host.Matches(Platform{OS: "linux"}) {
		t.Fatal("OS-only pin should match host with same OS")
	}
	if host.Matches(Platform{OS: "darwin"}) {
		t.Fatal("OS pin should not match different OS")
	}
}

func TestPlatform_Matches_FullPinExactness(t *testing.T) {
	host := Platform{OS: "linux", Arch: "amd64", Libc: "glibc"}
	if !host.Matches(host) {
		t.Fatal("host should match itself")
	}
	if host.Matches(Platform{OS: "linux", Arch: "arm64", Libc: "glibc"}) {
		t.Fatal("arch mismatch should not match")
	}
}

func TestPlatform_String(t *testing.T) {
	p := Platform{OS: "linux", Arch: "amd64", Libc: "glibc"}
	if p.String() != "linux/amd64/glibc" {
		t.Fatalf("Platform.String() = %q, want %q", p.String(), "linux/amd64/glibc")
	}
}

func TestPlatform_IsZero(t *testing.T) {
	if !(Platform{}).IsZero() {
		t.Fatal("zero Platform should report IsZero == true")
	}
	if (Platform{OS: "linux"}).IsZero() {
		t.Fatal("Platform with OS set should not report IsZero == true")
	}
}

func TestHash_IsEmpty(t *testing.T) {
	if !(Hash{}).IsEmpty() {
		t.Fatal("zero Hash should report IsEmpty")
	}
	if (Hash{Algorithm: "sha256", Digest: []byte{0x01}}).IsEmpty() {
		t.Fatal("populated Hash should not report IsEmpty")
	}
}

func TestOutputMode_IsPreserve(t *testing.T) {
	if !PreserveMode.IsPreserve() {
		t.Fatal("PreserveMode should report IsPreserve")
	}
	if (OutputMode(0644)).IsPreserve() {
		t.Fatal("non-zero mode should not report IsPreserve")
	}
}
