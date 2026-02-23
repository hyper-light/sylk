package providers

import (
	"testing"

	"google.golang.org/genai"
)

func TestExtractGoogleCandidatePartsSplitsThoughts(t *testing.T) {
	text, thought := extractGoogleCandidateParts([]*genai.Candidate{
		{
			Content: &genai.Content{
				Parts: []*genai.Part{
					{Text: "reasoning step", Thought: true},
					{Text: "answer"},
				},
			},
		},
	})

	if thought != "reasoning step" {
		t.Fatalf("thought = %q, want %q", thought, "reasoning step")
	}
	if text != "answer" {
		t.Fatalf("text = %q, want %q", text, "answer")
	}
}

func TestProviderDeltaEmitter(t *testing.T) {
	emitter := &providerDeltaEmitter{}

	if delta := emitter.Delta("hello"); delta != "hello" {
		t.Fatalf("first delta = %q, want %q", delta, "hello")
	}
	if delta := emitter.Delta("hello world"); delta != " world" {
		t.Fatalf("second delta = %q, want %q", delta, " world")
	}
}
