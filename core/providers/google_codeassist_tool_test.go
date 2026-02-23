package providers

import (
	"encoding/json"
	"testing"
)

func TestVertexPartUnmarshalFunctionCall(t *testing.T) {
	raw := `{
		"functionCall": {
			"id": "call_1",
			"name": "route",
			"args": {"target": "librarian", "input": "find auth middleware"}
		}
	}`
	var part vertexPart
	if err := json.Unmarshal([]byte(raw), &part); err != nil {
		t.Fatal(err)
	}
	if part.FunctionCall == nil {
		t.Fatal("FunctionCall should not be nil")
	}
	if part.FunctionCall.ID != "call_1" {
		t.Errorf("got ID %q, want %q", part.FunctionCall.ID, "call_1")
	}
	if part.FunctionCall.Name != "route" {
		t.Errorf("got Name %q, want %q", part.FunctionCall.Name, "route")
	}
	if part.FunctionCall.Args["target"] != "librarian" {
		t.Errorf("got target %v, want %q", part.FunctionCall.Args["target"], "librarian")
	}
}

func TestExtractVertexFunctionCalls(t *testing.T) {
	resp := &vertexStreamResponse{
		Candidates: []vertexCandidate{{
			Content: &vertexContent{
				Parts: []vertexPart{
					{Text: "I'll route this for you.", Thought: false},
					{
						FunctionCall: &vertexFunctionCall{
							ID:   "fc_1",
							Name: "route",
							Args: map[string]any{
								"target": "librarian",
								"input":  "find auth middleware",
							},
						},
					},
					{
						FunctionCall: &vertexFunctionCall{
							ID:   "fc_2",
							Name: "status",
							Args: map[string]any{},
						},
					},
				},
				Role: "model",
			},
		}},
	}

	calls := extractVertexFunctionCalls(resp)
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if calls[0].Name != "route" {
		t.Errorf("call[0].Name = %q, want %q", calls[0].Name, "route")
	}
	if calls[0].ID != "fc_1" {
		t.Errorf("call[0].ID = %q, want %q", calls[0].ID, "fc_1")
	}
	if calls[1].Name != "status" {
		t.Errorf("call[1].Name = %q, want %q", calls[1].Name, "status")
	}
}

func TestExtractVertexFunctionCallsNil(t *testing.T) {
	if calls := extractVertexFunctionCalls(nil); calls != nil {
		t.Errorf("nil response should return nil, got %v", calls)
	}
	if calls := extractVertexFunctionCalls(&vertexStreamResponse{}); calls != nil {
		t.Errorf("empty candidates should return nil, got %v", calls)
	}
}

func TestExtractVertexStreamPartsSkipsFunctionCalls(t *testing.T) {
	resp := &vertexStreamResponse{
		Candidates: []vertexCandidate{{
			Content: &vertexContent{
				Parts: []vertexPart{
					{Text: "hello", Thought: false},
					{Text: "thinking...", Thought: true},
					{FunctionCall: &vertexFunctionCall{Name: "route"}},
				},
			},
		}},
	}
	text, thought := extractVertexStreamParts(resp)
	if text != "hello" {
		t.Errorf("text = %q, want %q", text, "hello")
	}
	if thought != "thinking..." {
		t.Errorf("thought = %q, want %q", thought, "thinking...")
	}
}

func TestParseVertexGenerateResponseWithFunctionCalls(t *testing.T) {
	payload := map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{
					{"text": "Routing now."},
					{
						"functionCall": map[string]any{
							"id":   "fc_abc",
							"name": "route_to",
							"args": map[string]any{
								"target": "architect",
								"input":  "plan new feature",
							},
						},
					},
				},
				"role": "model",
			},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     100,
			"candidatesTokenCount": 50,
			"totalTokenCount":      150,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := parseVertexGenerateResponse(raw, "gemini-test")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Routing now." {
		t.Errorf("Content = %q, want %q", resp.Content, "Routing now.")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "route_to" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", resp.ToolCalls[0].Name, "route_to")
	}
	if resp.ToolCalls[0].ID != "fc_abc" {
		t.Errorf("ToolCalls[0].ID = %q, want %q", resp.ToolCalls[0].ID, "fc_abc")
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(resp.ToolCalls[0].Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["target"] != "architect" {
		t.Errorf("args.target = %v, want %q", args["target"], "architect")
	}
}

func TestParseVertexGenerateResponseTextOnly(t *testing.T) {
	payload := map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{
					{"text": "Just text, no tools."},
				},
				"role": "model",
			},
			"finishReason": "STOP",
		}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := parseVertexGenerateResponse(raw, "gemini-test")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Just text, no tools." {
		t.Errorf("Content = %q, want %q", resp.Content, "Just text, no tools.")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("got %d tool calls, want 0", len(resp.ToolCalls))
	}
}
