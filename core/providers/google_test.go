package providers

import (
	"bytes"
	"testing"
	"time"

	"google.golang.org/genai"
)

func TestGoogleConvertTools_IncludesNativeWebSearch(t *testing.T) {
	g := &GoogleProvider{}

	tools := g.convertTools([]Tool{
		{
			Kind: ToolKindNativeWebSearch,
			Name: "web_search",
			WebSearch: &WebSearchOptions{
				EnableURLContext: true,
			},
		},
		{
			Name:        "web_fetch",
			Description: "Fetch a URL",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string"},
				},
			},
		},
	})

	if len(tools) != 1 {
		t.Fatalf("expected single aggregated tool entry, got %d", len(tools))
	}
	if tools[0].GoogleSearch == nil {
		t.Fatal("expected google search tool to be enabled")
	}
	if tools[0].URLContext == nil {
		t.Fatal("expected URL context tool to be enabled")
	}
	if len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected one function declaration, got %d", len(tools[0].FunctionDeclarations))
	}
	if tools[0].FunctionDeclarations[0].Name != "web_fetch" {
		t.Fatalf("expected function declaration web_fetch, got %q", tools[0].FunctionDeclarations[0].Name)
	}
}

func TestGoogleConvertMessages_RestoresThoughtSignatureFromRawProviderData(t *testing.T) {
	g := &GoogleProvider{}
	raw := &googleSerializableContent{
		Role: "model",
		Parts: []googleSerializablePartItem{
			{
				Text:             "internal reasoning",
				Thought:          true,
				ThoughtSignature: []byte("sig-123"),
			},
			{
				FunctionCallID:   "fc_1",
				FunctionCallName: "default_api:clarify",
				FunctionCallArgs: map[string]any{"question": "Need more detail?"},
			},
		},
	}

	contents := g.convertMessages([]Message{{
		Role:     RoleAssistant,
		Content:  "ignored once raw content exists",
		Metadata: googleRawProviderData(raw),
	}})

	if len(contents) != 1 {
		t.Fatalf("got %d contents, want 1", len(contents))
	}
	if contents[0].Role != "model" {
		t.Fatalf("content role = %q, want %q", contents[0].Role, "model")
	}
	if len(contents[0].Parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(contents[0].Parts))
	}
	if !contents[0].Parts[0].Thought {
		t.Fatal("expected first part to be marked as thought")
	}
	if !bytes.Equal(contents[0].Parts[0].ThoughtSignature, []byte("sig-123")) {
		t.Fatalf("thought signature = %q, want %q", string(contents[0].Parts[0].ThoughtSignature), "sig-123")
	}
	if contents[0].Parts[1].FunctionCall == nil {
		t.Fatal("expected function call part")
	}
	if contents[0].Parts[1].FunctionCall.Name != "default_api:clarify" {
		t.Fatalf("function call name = %q, want %q", contents[0].Parts[1].FunctionCall.Name, "default_api:clarify")
	}
}

func TestExtractGoogleRawContent_PreservesThoughtSignatureForReplay(t *testing.T) {
	raw := extractGoogleRawContent(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{
						Text:             "chain of thought",
						Thought:          true,
						ThoughtSignature: []byte("sig-google"),
					},
					{
						FunctionCall: &genai.FunctionCall{
							ID:   "fc_google",
							Name: "default_api:clarify",
							Args: map[string]any{"question": "Clarify scope"},
						},
					},
				},
			},
		}},
	})
	if raw == nil {
		t.Fatal("expected raw content")
	}

	restored := restoreGoogleRawContent(googleRawProviderData(raw))
	if restored == nil {
		t.Fatal("expected restored content")
	}
	if len(restored.Parts) != 2 {
		t.Fatalf("got %d restored parts, want 2", len(restored.Parts))
	}
	if !bytes.Equal(restored.Parts[0].ThoughtSignature, []byte("sig-google")) {
		t.Fatalf("restored thought signature = %q, want %q", string(restored.Parts[0].ThoughtSignature), "sig-google")
	}
	if restored.Parts[1].FunctionCall == nil || restored.Parts[1].FunctionCall.Name != "default_api:clarify" {
		t.Fatal("expected restored function call")
	}
}

func TestGoogleStreamCollectorResponse_PreservesRawContentForReplay(t *testing.T) {
	raw := extractGoogleRawContent(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{
						Text:             "streamed thought",
						Thought:          true,
						ThoughtSignature: []byte("sig-stream"),
					},
					{
						FunctionCall: &genai.FunctionCall{
							ID:   "fc_stream",
							Name: "default_api:clarify",
							Args: map[string]any{"question": "Clarify input"},
						},
					},
				},
			},
		}},
	})
	collector := NewStreamCollector(nil)
	collector.Add(&StreamChunk{
		Type:         ChunkTypeEnd,
		ProviderData: googleRawProviderData(raw),
		Timestamp:    time.Now(),
	})

	restored := restoreGoogleRawContent(collector.Response().ProviderMetadata)
	if restored == nil {
		t.Fatal("expected restored raw content from stream collector response")
	}
	if !bytes.Equal(restored.Parts[0].ThoughtSignature, []byte("sig-stream")) {
		t.Fatalf("restored thought signature = %q, want %q", string(restored.Parts[0].ThoughtSignature), "sig-stream")
	}
}

func TestExtractVertexRawContent_PreservesThoughtSignatureForReplay(t *testing.T) {
	raw := extractVertexRawContent(&vertexStreamResponse{
		Candidates: []vertexCandidate{{
			Content: &vertexContent{
				Role: "model",
				Parts: []vertexPart{
					{
						Text:             "vertex thought",
						Thought:          true,
						ThoughtSignature: []byte("sig-vertex"),
					},
					{
						FunctionCall: &vertexFunctionCall{
							ID:   "fc_vertex",
							Name: "default_api:clarify",
							Args: map[string]any{"question": "Need clarification"},
						},
					},
				},
			},
		}},
	})
	if raw == nil {
		t.Fatal("expected raw content")
	}

	restored := restoreGoogleRawContent(googleRawProviderData(raw))
	if restored == nil {
		t.Fatal("expected restored content")
	}
	if len(restored.Parts) != 2 {
		t.Fatalf("got %d restored parts, want 2", len(restored.Parts))
	}
	if !bytes.Equal(restored.Parts[0].ThoughtSignature, []byte("sig-vertex")) {
		t.Fatalf("restored thought signature = %q, want %q", string(restored.Parts[0].ThoughtSignature), "sig-vertex")
	}
	if restored.Parts[1].FunctionCall == nil || restored.Parts[1].FunctionCall.Name != "default_api:clarify" {
		t.Fatal("expected restored function call")
	}
}
