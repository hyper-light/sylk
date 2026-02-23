package redact

import (
	"errors"
	"testing"
)

func TestText_RedactsSecretsWithoutTrimming(t *testing.T) {
	input := " token=sk-abcdefghijklmnopqrstuvwxyz123456 \n"
	got := Text(input)
	if got == input {
		t.Fatalf("expected redaction, got %q", got)
	}
	if got[len(got)-1] != '\n' {
		t.Fatalf("expected trailing newline to be preserved, got %q", got)
	}
}

func TestError_RedactsWhenNeeded(t *testing.T) {
	err := Error(errors.New("bearer abcdefghijklmnopqrstuvwxyz1234"))
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() == "bearer abcdefghijklmnopqrstuvwxyz1234" {
		t.Fatalf("expected redaction, got %q", err.Error())
	}
}
