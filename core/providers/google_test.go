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

func TestGoogleConvertMessages_PreservesThoughtSignatureOnlyReplayParts(t *testing.T) {
	g := &GoogleProvider{}
	raw := &googleSerializableContent{
		Role: "model",
		Parts: []googleSerializablePartItem{
			{
				Thought:          true,
				ThoughtSignature: []byte("sig-only"),
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
	if len(contents[0].Parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(contents[0].Parts))
	}
	if !bytes.Equal(contents[0].Parts[0].ThoughtSignature, []byte("sig-only")) {
		t.Fatalf("thought signature = %q, want %q", string(contents[0].Parts[0].ThoughtSignature), "sig-only")
	}
	if contents[0].Parts[1].FunctionCall == nil {
		t.Fatal("expected function call part")
	}
	if contents[0].Parts[1].FunctionCall.Name != "default_api:clarify" {
		t.Fatalf("function call name = %q, want %q", contents[0].Parts[1].FunctionCall.Name, "default_api:clarify")
	}
}

func TestMergeGoogleRawContent_PreservesEarlierThoughtSignature(t *testing.T) {
	current := &googleSerializableContent{
		Role: "model",
		Parts: []googleSerializablePartItem{
			{
				FunctionCallID:   "fc_1",
				FunctionCallName: "default_api:clarify",
				FunctionCallArgs: map[string]any{"question": "draft"},
				ThoughtSignature: []byte("sig-123"),
			},
		},
	}
	next := &googleSerializableContent{
		Role: "model",
		Parts: []googleSerializablePartItem{
			{
				FunctionCallID:   "fc_1",
				FunctionCallName: "default_api:clarify",
				FunctionCallArgs: map[string]any{"question": "final"},
			},
		},
	}

	merged := mergeGoogleRawContent(current, next)
	if merged == nil {
		t.Fatal("expected merged raw content")
	}
	if len(merged.Parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(merged.Parts))
	}
	if !bytes.Equal(merged.Parts[0].ThoughtSignature, []byte("sig-123")) {
		t.Fatalf("thought signature = %q, want %q", string(merged.Parts[0].ThoughtSignature), "sig-123")
	}
	if got := merged.Parts[0].FunctionCallArgs["question"]; got != "final" {
		t.Fatalf("function call args = %#v, want final question", merged.Parts[0].FunctionCallArgs)
	}
}

func TestGoogleConvertMessages_GroupsConsecutiveToolResponsesIntoSingleUserTurn(t *testing.T) {
	g := &GoogleProvider{}

	contents := g.convertMessages([]Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "tool_1", Name: "search_code", Arguments: `{"query":"alpha"}`},
				{ID: "tool_2", Name: "read_file", Arguments: `{"path":"main.go"}`},
			},
		},
		{
			Role:       RoleTool,
			ToolCallID: "tool_1",
			ToolName:   "search_code",
			Content:    `{"matches":1}`,
		},
		{
			Role:       RoleTool,
			ToolCallID: "tool_2",
			ToolName:   "read_file",
			Content:    `{"content":"package main"}`,
		},
	})

	if len(contents) != 2 {
		t.Fatalf("got %d contents, want 2", len(contents))
	}
	if contents[0].Role != "model" {
		t.Fatalf("assistant role = %q, want model", contents[0].Role)
	}
	if len(contents[0].Parts) != 2 {
		t.Fatalf("assistant parts = %d, want 2", len(contents[0].Parts))
	}
	if contents[1].Role != "user" {
		t.Fatalf("tool response role = %q, want user", contents[1].Role)
	}
	if len(contents[1].Parts) != 2 {
		t.Fatalf("tool response parts = %d, want 2", len(contents[1].Parts))
	}
	for i, want := range []struct {
		id   string
		name string
	}{
		{id: "tool_1", name: "search_code"},
		{id: "tool_2", name: "read_file"},
	} {
		part := contents[1].Parts[i]
		if part.FunctionResponse == nil {
			t.Fatalf("tool response part %d missing function response", i)
		}
		if part.FunctionResponse.ID != want.id {
			t.Fatalf("tool response id %d = %q, want %q", i, part.FunctionResponse.ID, want.id)
		}
		if part.FunctionResponse.Name != want.name {
			t.Fatalf("tool response name %d = %q, want %q", i, part.FunctionResponse.Name, want.name)
		}
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

func TestExtractGoogleRawContent_PreservesThoughtSignatureOnlyParts(t *testing.T) {
	raw := extractGoogleRawContent(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{
						Thought:          true,
						ThoughtSignature: []byte("sig-only"),
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
	if len(raw.Parts) != 2 {
		t.Fatalf("got %d raw parts, want 2", len(raw.Parts))
	}
	if !bytes.Equal(raw.Parts[0].ThoughtSignature, []byte("sig-only")) {
		t.Fatalf("thought signature = %q, want %q", string(raw.Parts[0].ThoughtSignature), "sig-only")
	}
	if raw.Parts[1].FunctionCallName != "default_api:clarify" {
		t.Fatalf("function call name = %q, want %q", raw.Parts[1].FunctionCallName, "default_api:clarify")
	}
}

func TestRestoreGoogleRawContent_SplitsMixedReplayPartIntoSeparateGenaiParts(t *testing.T) {
	raw := &googleSerializableContent{
		Role: "model",
		Parts: []googleSerializablePartItem{{
			Text:             "streamed thought",
			Thought:          true,
			ThoughtSignature: []byte("sig-mixed"),
			FunctionCallID:   "fc_mixed",
			FunctionCallName: "default_api:clarify",
			FunctionCallArgs: map[string]any{"question": "Clarify input"},
		}},
	}

	restored := restoreGoogleRawContent(googleRawProviderData(raw))
	if restored == nil {
		t.Fatal("expected restored content")
	}
	if len(restored.Parts) != 2 {
		t.Fatalf("got %d restored parts, want 2", len(restored.Parts))
	}
	if restored.Parts[0].FunctionCall != nil {
		t.Fatal("expected first restored part to be text/thought only")
	}
	if restored.Parts[0].Text != "streamed thought" {
		t.Fatalf("restored text = %q, want %q", restored.Parts[0].Text, "streamed thought")
	}
	if !restored.Parts[0].Thought {
		t.Fatal("expected first restored part to be marked as thought")
	}
	if len(restored.Parts[0].ThoughtSignature) != 0 {
		t.Fatalf("text part should not own the signature; got %q", string(restored.Parts[0].ThoughtSignature))
	}
	if restored.Parts[1].Text != "" {
		t.Fatalf("second restored part text = %q, want empty", restored.Parts[1].Text)
	}
	if restored.Parts[1].FunctionCall == nil {
		t.Fatal("expected second restored part to contain the function call")
	}
	if restored.Parts[1].FunctionCall.Name != "default_api:clarify" {
		t.Fatalf("function call name = %q, want %q", restored.Parts[1].FunctionCall.Name, "default_api:clarify")
	}
	if !bytes.Equal(restored.Parts[1].ThoughtSignature, []byte("sig-mixed")) {
		t.Fatalf("function call signature = %q, want %q", string(restored.Parts[1].ThoughtSignature), "sig-mixed")
	}
}

func TestRestoreGoogleRawContent_KeepsSignatureOnFunctionCallPart(t *testing.T) {
	raw := &googleSerializableContent{
		Role: "model",
		Parts: []googleSerializablePartItem{{
			ThoughtSignature: []byte("sig-fc"),
			FunctionCallID:   "fc_g3",
			FunctionCallName: "default_api:store_archivalist",
			FunctionCallArgs: map[string]any{"summary": "ok"},
		}},
	}

	restored := restoreGoogleRawContent(googleRawProviderData(raw))
	if restored == nil {
		t.Fatal("expected restored content")
	}
	if len(restored.Parts) != 1 {
		t.Fatalf("got %d restored parts, want 1", len(restored.Parts))
	}
	if restored.Parts[0].FunctionCall == nil {
		t.Fatal("expected function call part")
	}
	if !bytes.Equal(restored.Parts[0].ThoughtSignature, []byte("sig-fc")) {
		t.Fatalf("function call signature = %q, want %q", string(restored.Parts[0].ThoughtSignature), "sig-fc")
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
