package test

import "testing"

func TestCanonicalFrameworkLanguage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "typescript", want: "javascript"},
		{input: "ts", want: "javascript"},
		{input: "kotlin", want: "java"},
		{input: "c", want: "cpp"},
		{input: "c#", want: "csharp"},
		{input: "go", want: "go"},
	}

	for _, tt := range tests {
		if got := CanonicalFrameworkLanguage(tt.input); got != tt.want {
			t.Fatalf("CanonicalFrameworkLanguage(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFallbackFrameworkForLanguage(t *testing.T) {
	tests := []struct {
		lang string
		want TestFrameworkID
	}{
		{lang: "typescript", want: FrameworkVitest},
		{lang: "javascript", want: FrameworkVitest},
		{lang: "kotlin", want: FrameworkGradleTest},
		{lang: "java", want: FrameworkGradleTest},
		{lang: "scala", want: FrameworkSBTTest},
		{lang: "haskell", want: FrameworkStackTest},
		{lang: "c", want: FrameworkCTest},
		{lang: "dart", want: FrameworkDartTest},
		{lang: "elixir", want: FrameworkExUnit},
	}

	for _, tt := range tests {
		got := FallbackFrameworkForLanguage(tt.lang)
		if got == nil {
			t.Fatalf("FallbackFrameworkForLanguage(%q) returned nil", tt.lang)
		}
		if got.ID != tt.want {
			t.Fatalf("FallbackFrameworkForLanguage(%q) = %s, want %s", tt.lang, got.ID, tt.want)
		}
	}
}
