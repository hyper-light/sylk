package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"
)

const (
	codeAssistEndpoint      = "https://cloudcode-pa.googleapis.com"
	codeAssistStreamVersion = "v1internal"
)

// codeAssistGenerateRequest wraps a standard Vertex AI request in the
// Code Assist envelope (matches gemini-cli converter.ts).
type codeAssistGenerateRequest struct {
	Model        string `json:"model"`
	Project      string `json:"project,omitempty"`
	UserPromptID string `json:"user_prompt_id,omitempty"`
	Request      any    `json:"request"`
}

type codeAssistStreamResponse struct {
	Response json.RawMessage `json:"response"`
	TraceID  string          `json:"traceId,omitempty"`
}

// vertexStreamResponse is the inner Vertex AI response format nested
// inside Code Assist SSE data payloads.
type vertexStreamResponse struct {
	Candidates    []vertexCandidate    `json:"candidates,omitempty"`
	UsageMetadata *vertexUsageMetadata `json:"usageMetadata,omitempty"`
}

type vertexCandidate struct {
	Content      *vertexContent `json:"content,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
}

type vertexContent struct {
	Parts []vertexPart `json:"parts,omitempty"`
	Role  string       `json:"role,omitempty"`
}

type vertexPart struct {
	Text             string              `json:"text,omitempty"`
	Thought          bool                `json:"thought,omitempty"`
	ThoughtSignature []byte              `json:"thoughtSignature,omitempty"`
	FunctionCall     *vertexFunctionCall `json:"functionCall,omitempty"`
}

type vertexFunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

type vertexUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// codeAssistSSEReader reads Server-Sent Events from a response body.
type codeAssistSSEReader struct {
	scanner *bufio.Scanner
}

func newCodeAssistSSEReader(body io.Reader) *codeAssistSSEReader {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &codeAssistSSEReader{scanner: scanner}
}

// Next returns the next SSE data payload as raw JSON.
// Returns (nil, false, nil) on EOF.
func (r *codeAssistSSEReader) Next() (json.RawMessage, bool, error) {
	var buf bytes.Buffer
	for r.scanner.Scan() {
		line := r.scanner.Text()

		// Empty line marks end of an event.
		if strings.TrimSpace(line) == "" {
			if buf.Len() > 0 {
				raw := make(json.RawMessage, buf.Len())
				copy(raw, buf.Bytes())
				return raw, true, nil
			}
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(line[len("data: "):])
		}
		// Skip event:, id:, retry: fields — we only need data.
	}
	if err := r.scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("SSE read: %w", err)
	}
	// Final buffered data without trailing newline.
	if buf.Len() > 0 {
		raw := make(json.RawMessage, buf.Len())
		copy(raw, buf.Bytes())
		return raw, true, nil
	}
	return nil, false, nil
}

func buildCodeAssistStreamURL() string {
	return codeAssistEndpoint + "/" + codeAssistStreamVersion + ":streamGenerateContent?alt=sse"
}

func buildCodeAssistGenerateURL() string {
	return codeAssistEndpoint + "/" + codeAssistStreamVersion + ":generateContent"
}

// codeAssistHTTPError constructs a ProviderError from a non-200 Code Assist
// HTTP response, enabling the retry infrastructure to detect retryable status
// codes (429, 5xx) and parse server-specified backoff durations.
func codeAssistHTTPError(operation string, statusCode int, body []byte) *ProviderError {
	bodyStr := strings.TrimSpace(string(body))
	retryAfter := codeAssistRetryAfterFromBody(bodyStr)

	return &ProviderError{
		Provider:   ProviderTypeGoogle,
		Operation:  "code assist " + operation,
		StatusCode: statusCode,
		Message:    fmt.Sprintf("code assist %s HTTP %d: %s", operation, statusCode, bodyStr),
		Retryable:  isGoogleRetryableStatus(statusCode),
		RetryAfter: retryAfter,
	}
}

// codeAssistRetryAfterFromBody extracts a retry duration from Code Assist
// JSON error response bodies that contain human-readable messages like
// "Your quota will reset after 39s."
func codeAssistRetryAfterFromBody(body string) time.Duration {
	d, ok := googleRetryAfterFromMessage(body)
	if ok {
		return d
	}
	return 0
}

func (g *GoogleProvider) generateWithCodeAssist(ctx context.Context, req *Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = g.config.Model
	}

	googleTrace("code_assist_generate", "start", map[string]any{
		"model":   model,
		"project": g.codeAssistProject,
		"url":     buildCodeAssistGenerateURL(),
	})

	contents := g.convertMessages(req.Messages)
	genConfig := g.buildGenerateConfig(req)
	envelope := g.buildCodeAssistEnvelope(model, contents, genConfig)

	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal code assist request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, buildCodeAssistGenerateURL(), bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("create code assist request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := g.codeAssistHTTP.Do(httpReq)
	if err != nil {
		googleTrace("code_assist_generate", "http_error", map[string]any{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("code assist generate request: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read code assist response: %w", err)
	}

	googleTrace("code_assist_generate", "http_response", map[string]any{
		"status_code":  httpResp.StatusCode,
		"body_length":  len(body),
		"body_preview": googleTracePreview(string(body), 500),
	})

	if httpResp.StatusCode != http.StatusOK {
		return nil, codeAssistHTTPError("generate", httpResp.StatusCode, body)
	}

	return parseCodeAssistGenerateResponse(body, model)
}

func parseCodeAssistGenerateResponse(body []byte, model string) (*Response, error) {
	// Try unwrapping Code Assist envelope first.
	var envelope codeAssistStreamResponse
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Response) > 0 {
		return parseVertexGenerateResponse(envelope.Response, model)
	}
	// Fall back to direct vertex response.
	return parseVertexGenerateResponse(body, model)
}

func parseVertexGenerateResponse(raw json.RawMessage, model string) (*Response, error) {
	var vResp vertexStreamResponse
	if err := json.Unmarshal(raw, &vResp); err != nil {
		return nil, fmt.Errorf("parse code assist vertex response: %w", err)
	}

	resp := &Response{
		Model:            model,
		ProviderMetadata: make(map[string]any),
	}

	text, thought := extractVertexStreamParts(&vResp)
	resp.Content = strings.TrimSpace(text)
	if thought != "" {
		resp.ProviderMetadata["thought"] = thought
	}

	resp.ToolCalls = extractVertexFunctionCalls(&vResp)

	// Preserve raw candidate content for multi-turn replay with thought signatures.
	if raw := extractVertexRawContent(&vResp); raw != nil {
		resp.ProviderMetadata[googleRawContentKey] = raw
	}

	if vResp.UsageMetadata != nil {
		resp.Usage = Usage{
			InputTokens:  vResp.UsageMetadata.PromptTokenCount,
			OutputTokens: vResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  vResp.UsageMetadata.TotalTokenCount,
		}
	}

	resp.StopReason = vertexFinishReasonToStopReason(extractVertexFinishReason(&vResp))
	return resp, nil
}

func extractVertexFinishReason(resp *vertexStreamResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}
	return resp.Candidates[0].FinishReason
}

func vertexFinishReasonToStopReason(reason string) StopReason {
	switch reason {
	case "STOP":
		return StopReasonEndTurn
	case "MAX_TOKENS":
		return StopReasonMaxTokens
	case "SAFETY":
		return StopReasonError
	default:
		return StopReasonEndTurn
	}
}

func (g *GoogleProvider) buildCodeAssistEnvelope(
	model string,
	contents []*genai.Content,
	genConfig *genai.GenerateContentConfig,
) *codeAssistGenerateRequest {
	// Build inner request matching Vertex AI format.
	inner := map[string]any{}
	if genConfig != nil {
		inner["generationConfig"] = buildCodeAssistGenerationConfig(genConfig)
		if genConfig.SystemInstruction != nil {
			inner["systemInstruction"] = genConfig.SystemInstruction
		}
		if len(genConfig.Tools) > 0 {
			inner["tools"] = genConfig.Tools
		}
		if genConfig.ToolConfig != nil {
			inner["toolConfig"] = genConfig.ToolConfig
		}
		if len(genConfig.SafetySettings) > 0 {
			inner["safetySettings"] = genConfig.SafetySettings
		}
	}
	inner["contents"] = contents

	return &codeAssistGenerateRequest{
		Model:        model,
		Project:      g.codeAssistProject,
		UserPromptID: uuid.NewString(),
		Request:      inner,
	}
}

func buildCodeAssistGenerationConfig(gc *genai.GenerateContentConfig) map[string]any {
	cfg := map[string]any{}
	if gc.MaxOutputTokens > 0 {
		cfg["maxOutputTokens"] = gc.MaxOutputTokens
	}
	if gc.Temperature != nil {
		cfg["temperature"] = *gc.Temperature
	}
	if gc.TopP != nil {
		cfg["topP"] = *gc.TopP
	}
	if gc.TopK != nil {
		cfg["topK"] = *gc.TopK
	}
	if len(gc.StopSequences) > 0 {
		cfg["stopSequences"] = gc.StopSequences
	}
	if gc.ThinkingConfig != nil {
		cfg["thinkingConfig"] = gc.ThinkingConfig
	}
	if gc.ResponseMIMEType != "" {
		cfg["responseMimeType"] = gc.ResponseMIMEType
	}
	if gc.ResponseSchema != nil {
		cfg["responseSchema"] = gc.ResponseSchema
	}
	return cfg
}

func parseCodeAssistVertexResponse(raw json.RawMessage) (*vertexStreamResponse, error) {
	var resp vertexStreamResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse vertex response: %w", err)
	}
	return &resp, nil
}

func (g *GoogleProvider) streamWithCodeAssist(
	ctx context.Context,
	req *Request,
	handler StreamHandler,
) error {
	model := req.Model
	if model == "" {
		model = g.config.Model
	}

	googleTrace("code_assist_stream", "start", map[string]any{
		"model":         model,
		"project":       g.codeAssistProject,
		"message_count": len(req.Messages),
		"url":           buildCodeAssistStreamURL(),
	})

	contents := g.convertMessages(req.Messages)
	genConfig := g.buildGenerateConfig(req)
	envelope := g.buildCodeAssistEnvelope(model, contents, genConfig)

	encoded, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal code assist request: %w", err)
	}

	googleTrace("code_assist_stream", "request_built", map[string]any{
		"envelope_size": len(encoded),
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, buildCodeAssistStreamURL(), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create code assist request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := g.codeAssistHTTP.Do(httpReq)
	if err != nil {
		googleTrace("code_assist_stream", "http_error", map[string]any{
			"error": err.Error(),
		})
		return fmt.Errorf("code assist stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		googleTrace("code_assist_stream", "http_status_error", map[string]any{
			"status_code":  resp.StatusCode,
			"body_preview": googleTracePreview(string(body), 500),
		})
		return codeAssistHTTPError("stream", resp.StatusCode, body)
	}

	googleTrace("code_assist_stream", "http_ok", map[string]any{
		"status_code":  resp.StatusCode,
		"content_type": resp.Header.Get("Content-Type"),
	})

	return g.processCodeAssistSSE(resp.Body, handler)
}

func (g *GoogleProvider) processCodeAssistSSE(
	body io.Reader,
	handler StreamHandler,
) error {
	if err := handler(&StreamChunk{
		Index:     0,
		Type:      ChunkTypeStart,
		Timestamp: time.Now(),
	}); err != nil {
		return err
	}

	reader := newCodeAssistSSEReader(body)
	var chunkIndex int
	var totalInputTokens, totalOutputTokens int
	var emittedEarlyUsage bool
	var stopReason StopReason
	textDelta := &providerDeltaEmitter{}
	thoughtDelta := &providerDeltaEmitter{}
	toolCallsSeen := make(map[googleToolCallKey]bool)

	var totalTextLen, totalThoughtLen int
	var sseEventCount, unwrapErrors, parseErrors int

	for {
		raw, ok, err := reader.Next()
		if err != nil {
			googleTrace("code_assist_sse", "read_error", map[string]any{
				"error":       err.Error(),
				"chunk_index": chunkIndex,
			})
			return err
		}
		if !ok {
			break
		}

		chunkIndex++
		sseEventCount++

		// Unwrap Code Assist envelope → inner Vertex response.
		vertexRaw, err := unwrapCodeAssistResponse(raw)
		if err != nil {
			unwrapErrors++
			googleTrace("code_assist_sse", "unwrap_error", map[string]any{
				"chunk_index": chunkIndex,
				"error":       err.Error(),
				"raw_preview": googleTracePreview(string(raw), 300),
			})
			continue // Skip malformed events.
		}

		vResp, err := parseCodeAssistVertexResponse(vertexRaw)
		if err != nil {
			parseErrors++
			googleTrace("code_assist_sse", "parse_error", map[string]any{
				"chunk_index":    chunkIndex,
				"error":          err.Error(),
				"vertex_preview": googleTracePreview(string(vertexRaw), 300),
			})
			continue
		}

		text, thought := extractVertexStreamParts(vResp)
		totalTextLen += len(text)
		totalThoughtLen += len(thought)

		if delta := textDelta.Delta(text); delta != "" {
			if err := handler(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeText,
				Text:      delta,
				Timestamp: time.Now(),
			}); err != nil {
				return err
			}
		}
		if delta := thoughtDelta.Delta(thought); delta != "" {
			if err := handler(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeThought,
				Text:      delta,
				Timestamp: time.Now(),
			}); err != nil {
				return err
			}
		}

		for _, fc := range extractVertexFunctionCalls(vResp) {
			key := googleToolCallKey{ID: fc.ID, Name: fc.Name}
			if !toolCallsSeen[key] {
				toolCallsSeen[key] = true
				if err := handler(&StreamChunk{
					Index: chunkIndex,
					Type:  ChunkTypeToolStart,
					ToolCall: &ToolCallChunk{
						ID:   fc.ID,
						Name: fc.Name,
					},
					Timestamp: time.Now(),
				}); err != nil {
					return err
				}
			}
			if err := handler(&StreamChunk{
				Index: chunkIndex,
				Type:  ChunkTypeToolDelta,
				ToolCall: &ToolCallChunk{
					ID:             fc.ID,
					ArgumentsDelta: fc.Arguments,
				},
				Timestamp: time.Now(),
			}); err != nil {
				return err
			}
		}

		if vResp.UsageMetadata != nil {
			totalInputTokens = vResp.UsageMetadata.PromptTokenCount
			totalOutputTokens = vResp.UsageMetadata.CandidatesTokenCount
			if !emittedEarlyUsage && totalInputTokens > 0 {
				emittedEarlyUsage = true
				_ = handler(&StreamChunk{
					Index:     chunkIndex,
					Type:      ChunkTypeStart,
					Usage:     &Usage{InputTokens: totalInputTokens},
					Timestamp: time.Now(),
				})
			}
		}

		stopReason = extractVertexStopReason(vResp, stopReason)
	}

	if stopReason == "" {
		stopReason = StopReasonEndTurn
	}

	googleTrace("code_assist_sse", "stream_complete", map[string]any{
		"sse_event_count":    sseEventCount,
		"total_text_len":     totalTextLen,
		"total_thought_len":  totalThoughtLen,
		"unwrap_errors":      unwrapErrors,
		"parse_errors":       parseErrors,
		"stop_reason":        string(stopReason),
		"total_input_tokens": totalInputTokens,
		"total_output_tokens": totalOutputTokens,
	})

	return handler(&StreamChunk{
		Index:      chunkIndex + 1,
		Type:       ChunkTypeEnd,
		StopReason: stopReason,
		Usage: &Usage{
			InputTokens:  totalInputTokens,
			OutputTokens: totalOutputTokens,
			TotalTokens:  totalInputTokens + totalOutputTokens,
		},
		Timestamp: time.Now(),
	})
}

func unwrapCodeAssistResponse(raw json.RawMessage) (json.RawMessage, error) {
	var envelope codeAssistStreamResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Response) == 0 {
		return raw, nil // Assume raw is already the vertex response.
	}
	return envelope.Response, nil
}

func extractVertexStreamParts(resp *vertexStreamResponse) (string, string) {
	if resp == nil || len(resp.Candidates) == 0 {
		return "", ""
	}
	candidate := resp.Candidates[0]
	if candidate.Content == nil {
		return "", ""
	}
	var text, thought strings.Builder
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			continue
		}
		if part.Text == "" {
			continue
		}
		if part.Thought {
			thought.WriteString(part.Text)
			continue
		}
		text.WriteString(part.Text)
	}
	return text.String(), thought.String()
}

func extractVertexFunctionCalls(resp *vertexStreamResponse) []ToolCall {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil
	}
	candidate := resp.Candidates[0]
	if candidate.Content == nil {
		return nil
	}
	var calls []ToolCall
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall == nil {
			continue
		}
		argsJSON, err := json.Marshal(part.FunctionCall.Args)
		if err != nil {
			continue
		}
		calls = append(calls, ToolCall{
			ID:        part.FunctionCall.ID,
			Name:      part.FunctionCall.Name,
			Arguments: string(argsJSON),
		})
	}
	return calls
}

// extractVertexRawContent converts the first candidate's vertexContent to a
// genai.Content, preserving thought signatures for multi-turn replay.
func extractVertexRawContent(resp *vertexStreamResponse) *genai.Content {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil
	}
	vc := resp.Candidates[0].Content
	if vc == nil || len(vc.Parts) == 0 {
		return nil
	}
	content := &genai.Content{Role: vc.Role}
	for _, vp := range vc.Parts {
		part := &genai.Part{
			Thought:          vp.Thought,
			ThoughtSignature: vp.ThoughtSignature,
		}
		switch {
		case vp.FunctionCall != nil:
			part.FunctionCall = &genai.FunctionCall{
				ID:   vp.FunctionCall.ID,
				Name: vp.FunctionCall.Name,
				Args: vp.FunctionCall.Args,
			}
		case vp.Text != "":
			part.Text = vp.Text
		default:
			continue
		}
		content.Parts = append(content.Parts, part)
	}
	if len(content.Parts) == 0 {
		return nil
	}
	return content
}

func extractVertexStopReason(resp *vertexStreamResponse, current StopReason) StopReason {
	if resp == nil || len(resp.Candidates) == 0 {
		return current
	}
	switch resp.Candidates[0].FinishReason {
	case "STOP":
		return StopReasonEndTurn
	case "MAX_TOKENS":
		return StopReasonMaxTokens
	case "SAFETY":
		return StopReasonError
	default:
		return current
	}
}
