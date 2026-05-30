package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

type ClaimsGatewayBackendConfig struct {
	Provider             ProviderAdapter
	Request              *Request
	ResponseSummaryLimit int
	PartialSummaryLimit  int
	Clock                func() time.Time
}

type ClaimsGatewayBackend struct {
	provider             ProviderAdapter
	request              *Request
	responseSummaryLimit int
	partialSummaryLimit  int
	clock                func() time.Time
}

type ProviderEmbeddingAdapter interface {
	Embed(ctx context.Context, input string) ([]float32, error)
}

type claimsGatewayArgs struct {
	Messages     []Message      `json:"messages,omitempty"`
	Model        string         `json:"model,omitempty"`
	MaxTokens    int            `json:"max_tokens,omitempty"`
	SystemPrompt string         `json:"system_prompt,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Prompt       string         `json:"prompt,omitempty"`
	Input        string         `json:"input,omitempty"`
}

func NewClaimsGatewayBackend(cfg ClaimsGatewayBackendConfig) *ClaimsGatewayBackend {
	return &ClaimsGatewayBackend{
		provider:             cfg.Provider,
		request:              cloneProviderRequest(cfg.Request),
		responseSummaryLimit: cfg.ResponseSummaryLimit,
		partialSummaryLimit:  cfg.PartialSummaryLimit,
		clock:                firstClaimsGatewayClock(cfg.Clock),
	}
}

func (b *ClaimsGatewayBackend) HandleProviderGatewayCall(ctx context.Context, req claims.ProviderGatewayCallRequest) (claims.ProviderGatewayCallArtifactData, error) {
	data, _, err := b.ExecuteProviderGatewayCall(ctx, req)
	return data, err
}

func (b *ClaimsGatewayBackend) ExecuteProviderGatewayCall(ctx context.Context, req claims.ProviderGatewayCallRequest) (claims.ProviderGatewayCallArtifactData, *Response, error) {
	data := req.Requested
	if b == nil || b.provider == nil {
		data.Error = "provider adapter not configured"
		return data, nil, nil
	}
	providerReq, err := providerRequestFromClaims(req.Call.Arguments, data)
	if err != nil {
		data.Error = err.Error()
		return data, nil, nil
	}
	if b.request != nil {
		providerReq = cloneProviderRequest(b.request)
	}
	started := b.clock()
	var resp *Response
	switch data.Operation {
	case claims.ProviderGatewayToolCountTokens:
		data = b.countTokens(data, providerReq)
	case claims.ProviderGatewayToolCompleteStreaming:
		data, resp = b.stream(ctx, data, providerReq)
	case claims.ProviderGatewayToolEmbedding:
		data = b.embedding(ctx, data, providerReq)
	default:
		data, resp = b.complete(ctx, data, providerReq)
	}
	data.Latency = b.clock().Sub(started)
	return data, resp, nil
}

func (b *ClaimsGatewayBackend) complete(ctx context.Context, data claims.ProviderGatewayCallArtifactData, req *Request) (claims.ProviderGatewayCallArtifactData, *Response) {
	resp, err := b.provider.Complete(ctx, req)
	if err != nil {
		data.Error = err.Error()
		return data, nil
	}
	return b.applyResponse(data, req, resp), resp
}

func (b *ClaimsGatewayBackend) stream(ctx context.Context, data claims.ProviderGatewayCallArtifactData, req *Request) (claims.ProviderGatewayCallArtifactData, *Response) {
	stream, err := b.provider.Stream(ctx, req)
	if err != nil {
		data.Error = err.Error()
		return data, nil
	}
	resp, partials, err := collectClaimsGatewayStream(ctx, stream, b.responseLimit(data, req), b.partialLimit(data))
	data.PartialSummaries = partials
	if err != nil {
		data.Error = err.Error()
		return data, resp
	}
	return b.applyResponse(data, req, resp), resp
}

func (b *ClaimsGatewayBackend) countTokens(data claims.ProviderGatewayCallArtifactData, req *Request) claims.ProviderGatewayCallArtifactData {
	total, err := b.provider.CountTokens(req.Messages)
	if err != nil {
		data.Error = err.Error()
		return data
	}
	data.InputTokens = total
	data.TotalTokens = total
	return data
}

func (b *ClaimsGatewayBackend) embedding(ctx context.Context, data claims.ProviderGatewayCallArtifactData, req *Request) claims.ProviderGatewayCallArtifactData {
	embedder, ok := b.provider.(ProviderEmbeddingAdapter)
	if !ok {
		data.Error = "provider embedding backend not configured"
		return data
	}
	vector, err := embedder.Embed(ctx, providerEmbeddingInput(req))
	if err != nil {
		data.Error = err.Error()
		return data
	}
	data.ResponseSummary = "embedding generated"
	data.Metadata = mergeClaimsGatewayMetadata(map[string]any{"embedding_dimension": len(vector)}, data.Metadata)
	return data
}

func (b *ClaimsGatewayBackend) applyResponse(data claims.ProviderGatewayCallArtifactData, req *Request, resp *Response) claims.ProviderGatewayCallArtifactData {
	if resp == nil {
		data.Error = "provider returned nil response"
		return data
	}
	data.Model = firstClaimsGatewayString(resp.Model, data.Model, req.Model)
	data.ResponseSummary = boundedClaimsGatewayText(firstClaimsGatewayString(resp.Content, data.ResponseSummary, "[empty provider response]"), b.responseLimit(data, req))
	data.FinishReason = firstClaimsGatewayString(string(resp.StopReason), data.FinishReason)
	data.InputTokens = firstClaimsGatewayInt(resp.Usage.InputTokens, data.InputTokens)
	data.OutputTokens = firstClaimsGatewayInt(resp.Usage.OutputTokens, data.OutputTokens)
	data.TotalTokens = firstClaimsGatewayInt(resp.Usage.TotalTokens, data.TotalTokens, data.InputTokens+data.OutputTokens)
	data.ReasoningTokens = firstClaimsGatewayInt(resp.Usage.ReasoningTokens, data.ReasoningTokens)
	data.CacheReadTokens = firstClaimsGatewayInt(resp.Usage.CacheReadTokens, data.CacheReadTokens)
	data.CacheWriteTokens = firstClaimsGatewayInt(resp.Usage.CacheWriteTokens, data.CacheWriteTokens)
	data.ProviderRequestID = firstClaimsGatewayString(providerRequestID(resp), data.ProviderRequestID)
	data.Metadata = mergeClaimsGatewayMetadata(resp.ProviderMetadata, data.Metadata)
	return data
}

func providerRequestFromClaims(args map[string]any, data claims.ProviderGatewayCallArtifactData) (*Request, error) {
	var decoded claimsGatewayArgs
	if err := decodeClaimsGatewayArgs(args, &decoded); err != nil {
		return nil, err
	}
	messages := decoded.Messages
	if len(messages) == 0 {
		messages = promptMessages(decoded)
	}
	return &Request{
		Messages:     messages,
		Model:        firstClaimsGatewayString(decoded.Model, data.Model),
		MaxTokens:    firstClaimsGatewayInt(decoded.MaxTokens, data.BudgetTokens),
		SystemPrompt: decoded.SystemPrompt,
		Metadata:     mergeClaimsGatewayMetadata(decoded.Metadata, map[string]any{"prompt_hash": promptHash(messages, decoded.SystemPrompt)}),
	}, nil
}

func collectClaimsGatewayStream(ctx context.Context, stream <-chan *StreamChunk, summaryLimit, partialLimit int) (*Response, []string, error) {
	collector := newClaimsGatewayStreamCollector(summaryLimit, partialLimit)
	for {
		select {
		case <-ctx.Done():
			return collector.response(), collector.partials, ctx.Err()
		case chunk, ok := <-stream:
			if !ok {
				return collector.response(), collector.partials, collector.err
			}
			collector.add(chunk)
		}
	}
}

type claimsGatewayStreamCollector struct {
	content      strings.Builder
	usage        Usage
	stopReason   StopReason
	summaryLimit int
	partialLimit int
	partials     []string
	providerData map[string]any
	retryCount   int
	err          error
}

func newClaimsGatewayStreamCollector(summaryLimit, partialLimit int) *claimsGatewayStreamCollector {
	return &claimsGatewayStreamCollector{summaryLimit: summaryLimit, partialLimit: partialLimit}
}

func (c *claimsGatewayStreamCollector) add(chunk *StreamChunk) {
	if chunk == nil {
		return
	}
	c.addMetadata(chunk)
	switch chunk.Type {
	case ChunkTypeText:
		c.addText(chunk.Text)
	case ChunkTypeEnd:
		c.addEnd(chunk)
	case ChunkTypeError:
		c.err = fmt.Errorf("stream error: %s", chunk.Text)
	}
}

func (c *claimsGatewayStreamCollector) addMetadata(chunk *StreamChunk) {
	if chunk.RetryReset {
		c.retryCount++
	}
	if len(chunk.ProviderData) != 0 {
		c.providerData = mergeClaimsGatewayMetadata(chunk.ProviderData, c.providerData)
	}
}

func (c *claimsGatewayStreamCollector) addText(text string) {
	remaining := c.remaining()
	if c.summaryLimit <= 0 || remaining > 0 {
		c.content.WriteString(boundedClaimsGatewayText(text, remaining))
	}
	if len(c.partials) < c.partialLimit && strings.TrimSpace(text) != "" {
		c.partials = append(c.partials, boundedClaimsGatewayText(text, c.partialTextLimit()))
	}
}

func (c *claimsGatewayStreamCollector) addEnd(chunk *StreamChunk) {
	if chunk.Usage != nil {
		c.usage = *chunk.Usage
	}
	if chunk.StopReason != "" {
		c.stopReason = chunk.StopReason
	}
}

func (c *claimsGatewayStreamCollector) remaining() int {
	if c.summaryLimit <= 0 {
		return 0
	}
	return c.summaryLimit - c.content.Len()
}

func (c *claimsGatewayStreamCollector) partialTextLimit() int {
	if c.summaryLimit <= 0 {
		return 0
	}
	return c.summaryLimit
}

func (c *claimsGatewayStreamCollector) response() *Response {
	metadata := cloneClaimsGatewayMetadata(c.providerData)
	if c.retryCount > 0 {
		if metadata == nil {
			metadata = make(map[string]any, 1)
		}
		metadata["retry_count"] = c.retryCount
	}
	return &Response{Content: c.content.String(), StopReason: c.stopReason, Usage: c.usage, ProviderMetadata: metadata}
}

func decodeClaimsGatewayArgs(args map[string]any, out *claimsGatewayArgs) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func promptMessages(args claimsGatewayArgs) []Message {
	prompt := firstClaimsGatewayString(args.Prompt, args.Input)
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	return []Message{{Role: RoleUser, Content: prompt}}
}

func providerEmbeddingInput(req *Request) string {
	if req == nil {
		return ""
	}
	var builder strings.Builder
	for _, msg := range req.Messages {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(msg.Content)
	}
	return firstClaimsGatewayString(builder.String(), req.SystemPrompt)
}

func promptHash(messages []Message, systemPrompt string) string {
	raw, _ := json.Marshal(struct {
		Messages     []Message `json:"messages,omitempty"`
		SystemPrompt string    `json:"system_prompt,omitempty"`
	}{Messages: messages, SystemPrompt: systemPrompt})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func providerRequestID(resp *Response) string {
	if resp == nil || resp.ProviderMetadata == nil {
		return ""
	}
	for _, key := range []string{"request_id", "id", "provider_request_id"} {
		if value, ok := resp.ProviderMetadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (b *ClaimsGatewayBackend) responseLimit(data claims.ProviderGatewayCallArtifactData, req *Request) int {
	if b.responseSummaryLimit > 0 {
		return b.responseSummaryLimit
	}
	if data.BudgetTokens > 0 {
		return data.BudgetTokens
	}
	if b.provider != nil && req != nil {
		return b.provider.MaxContextTokens(req.Model)
	}
	return len(data.ResponseSummary)
}

func (b *ClaimsGatewayBackend) partialLimit(data claims.ProviderGatewayCallArtifactData) int {
	if b.partialSummaryLimit > 0 {
		return b.partialSummaryLimit
	}
	if len(data.PartialSummaries) > 0 {
		return len(data.PartialSummaries)
	}
	return 1
}

func boundedClaimsGatewayText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}

func mergeClaimsGatewayMetadata(primary, fallback map[string]any) map[string]any {
	out := make(map[string]any, len(primary)+len(fallback))
	for key, value := range fallback {
		out[key] = value
	}
	for key, value := range primary {
		out[key] = value
	}
	return out
}

func cloneClaimsGatewayMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneProviderRequest(in *Request) *Request {
	if in == nil {
		return nil
	}
	out := *in
	out.Messages = cloneProviderMessages(in.Messages)
	out.StopSequences = append([]string(nil), in.StopSequences...)
	out.Tools = cloneProviderTools(in.Tools)
	out.Metadata = cloneClaimsGatewayMetadata(in.Metadata)
	out.ResponseSchema = cloneClaimsGatewayMetadata(in.ResponseSchema)
	return &out
}

func cloneProviderMessages(in []Message) []Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]Message, len(in))
	for idx, msg := range in {
		out[idx] = msg
		out[idx].ToolCalls = append([]ToolCall(nil), msg.ToolCalls...)
		out[idx].Metadata = cloneClaimsGatewayMetadata(msg.Metadata)
	}
	return out
}

func cloneProviderTools(in []Tool) []Tool {
	if len(in) == 0 {
		return nil
	}
	out := make([]Tool, len(in))
	for idx, tool := range in {
		out[idx] = tool.Clone()
	}
	return out
}

func firstClaimsGatewayString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstClaimsGatewayInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstClaimsGatewayClock(clock func() time.Time) func() time.Time {
	if clock != nil {
		return clock
	}
	return time.Now
}
