package cmd

import "testing"

func TestStripStdLogPrefix(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "with standard timestamp prefix",
			line: "2026/02/21 17:34:06 Error context deadline exceeded",
			want: "Error context deadline exceeded",
		},
		{
			name: "without timestamp prefix",
			line: "Error context deadline exceeded",
			want: "Error context deadline exceeded",
		},
		{
			name: "invalid timestamp shape",
			line: "2026-02-21 17:34:06 Error context deadline exceeded",
			want: "2026-02-21 17:34:06 Error context deadline exceeded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripStdLogPrefix(tt.line)
			if got != tt.want {
				t.Fatalf("stripStdLogPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsBenignContextShutdownLog(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    bool
	}{
		{
			name:    "deadline exceeded",
			message: "Error context deadline exceeded",
			want:    true,
		},
		{
			name:    "deadline exceeded with colon",
			message: "Error context: deadline exceeded",
			want:    true,
		},
		{
			name:    "canceled",
			message: "Error context canceled",
			want:    true,
		},
		{
			name:    "non-benign error",
			message: "Error unable to decode response",
			want:    false,
		},
		{
			name:    "context but not deadline/canceled",
			message: "Error context metadata missing",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBenignContextShutdownLog(tt.message)
			if got != tt.want {
				t.Fatalf("isBenignContextShutdownLog() = %v, want %v", got, tt.want)
			}
		})
	}
}

