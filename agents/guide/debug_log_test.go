package guide

import "testing"

func TestShouldWriteGuideDebugLogToSharedFileForProcess(t *testing.T) {
	tests := []struct {
		name  string
		argv0 string
		want  bool
	}{
		{name: "empty", argv0: "", want: true},
		{name: "production binary", argv0: "/usr/local/bin/sylk", want: true},
		{name: "go test binary", argv0: "/tmp/go-build12345/guide.test", want: false},
		{name: "package test binary", argv0: "channel_bus.test", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldWriteGuideDebugLogToSharedFileForProcess(tt.argv0); got != tt.want {
				t.Fatalf("shouldWriteGuideDebugLogToSharedFileForProcess(%q) = %v, want %v", tt.argv0, got, tt.want)
			}
		})
	}
}
