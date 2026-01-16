package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// SynthesisSystemPrompt is the system prompt for the synthesis component
const SynthesisSystemPrompt = `You are the reasoning component of THE ARCHIVALIST, a shared memory system for AI coding agents.

Your job is to synthesize relevant context into actionable responses for other AI agents.

CRITICAL REQUIREMENTS:
1. TOKEN EFFICIENCY - minimize response size while preserving all necessary information
2. STRUCTURED OUTPUT - format for machine parsing (JSON, tables, bullet points)
3. ACTIONABLE - every response should enable immediate action
4. PRECISE - only include information relevant to the query

OUTPUT FORMATS:
- For pattern queries: Return the pattern with brief rationale
- For failure queries: Return the failure and best resolution
- For context queries: Return structured summary of relevant state
- For coordination queries: Return affected sessions/agents with conflict details

Never include:
- Pleasantries or conversational filler
- Redundant explanations
- Information not directly relevant to the query`

// Synthesizer provides RAG synthesis using Sonnet 4.5
type Synthesizer struct {
	client     *anthropic.Client
	retriever  *SemanticRetriever
	queryCache *QueryCache

	systemPrompt string
	maxTokens    int64
}

// SynthesizerConfig configures the synthesizer
type SynthesizerConfig struct {
	Client       *anthropic.Client
	Retriever    *SemanticRetriever
	QueryCache   *QueryCache
	SystemPrompt string
	MaxTokens    int
}

// NewSynthesizer creates a new synthesizer
func NewSynthesizer(cfg SynthesizerConfig) *Synthesizer {
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = SynthesisSystemPrompt
	}

	maxTokens := int64(cfg.MaxTokens)
	if maxTokens == 0 {
		maxTokens = DefaultMaxOutputTokens
	}

	return &Synthesizer{
		client:       cfg.Client,
		retriever:    cfg.Retriever,
		queryCache:   cfg.QueryCache,
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
	}
}

// SynthesisResponse is the response from synthesis
type SynthesisResponse struct {
	Answer     string              `json:"answer"`
	Source     string              `json:"source"` // "cache" or "synthesis"
	CacheHit   bool                `json:"cache_hit"`
	Context    []*RetrievalResult  `json:"context,omitempty"`
	TokensUsed int                 `json:"tokens_used,omitempty"`
	Latency    time.Duration       `json:"latency"`
}

// Answer processes a query through the full RAG pipeline
func (s *Synthesizer) Answer(ctx context.Context, query string, sessionID string, queryType QueryType) (*SynthesisResponse, error) {
	startTime := time.Now()

	// Step 1: Check query cache
	if s.queryCache != nil {
		if cached, ok := s.queryCache.Get(ctx, query, sessionID); ok {
			return &SynthesisResponse{
				Answer:   string(cached.Response),
				Source:   "cache",
				CacheHit: true,
				Latency:  time.Since(startTime),
			}, nil
		}
	}

	// Step 2: Retrieve relevant context
	opts := DefaultRetrievalOptions()
	opts.TopK = 10 // Get top 10 most relevant results

	contextResults, err := s.retriever.Retrieve(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("retrieval failed: %w", err)
	}

	// Step 3: Build prompt with context
	prompt := s.buildPrompt(query, contextResults)

	// Step 4: Run Sonnet for synthesis
	message, err := s.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(ModelSonnet45),
		MaxTokens: s.maxTokens,
		System: []anthropic.TextBlockParam{
			{Text: s.systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("synthesis failed: %w", err)
	}

	// Extract text content
	var answer string
	for _, block := range message.Content {
		if block.Type == "text" {
			answer += block.Text
		}
	}

	tokensUsed := int(message.Usage.InputTokens + message.Usage.OutputTokens)

	// Step 5: Cache the response
	if s.queryCache != nil {
		s.queryCache.Store(ctx, query, sessionID, []byte(answer), queryType)
	}

	return &SynthesisResponse{
		Answer:     answer,
		Source:     "synthesis",
		CacheHit:   false,
		Context:    contextResults,
		TokensUsed: tokensUsed,
		Latency:    time.Since(startTime),
	}, nil
}

// AnswerWithContext synthesizes an answer from pre-retrieved context
func (s *Synthesizer) AnswerWithContext(ctx context.Context, query string, contextResults []*RetrievalResult) (*SynthesisResponse, error) {
	startTime := time.Now()

	// Build prompt with provided context
	prompt := s.buildPrompt(query, contextResults)

	// Run Sonnet for synthesis
	message, err := s.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(ModelSonnet45),
		MaxTokens: s.maxTokens,
		System: []anthropic.TextBlockParam{
			{Text: s.systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("synthesis failed: %w", err)
	}

	// Extract text content
	var answer string
	for _, block := range message.Content {
		if block.Type == "text" {
			answer += block.Text
		}
	}

	tokensUsed := int(message.Usage.InputTokens + message.Usage.OutputTokens)

	return &SynthesisResponse{
		Answer:     answer,
		Source:     "synthesis",
		CacheHit:   false,
		Context:    contextResults,
		TokensUsed: tokensUsed,
		Latency:    time.Since(startTime),
	}, nil
}

// buildPrompt constructs the prompt for Sonnet
func (s *Synthesizer) buildPrompt(query string, context []*RetrievalResult) string {
	var sb strings.Builder

	// Add context section
	sb.WriteString("## Relevant Context\n\n")

	if len(context) == 0 {
		sb.WriteString("No relevant context found in the knowledge base.\n\n")
	} else {
		for i, c := range context {
			sb.WriteString(fmt.Sprintf("### [%d] %s (%s, relevance: %.2f)\n", i+1, c.Type, c.Category, c.Score))
			sb.WriteString(c.Content)
			sb.WriteString("\n\n")
		}
	}

	// Add query section
	sb.WriteString("## Query\n\n")
	sb.WriteString(query)
	sb.WriteString("\n\n")

	// Add instructions
	sb.WriteString("## Instructions\n\n")
	sb.WriteString("Based on the relevant context above, provide a concise, actionable response to the query. ")
	sb.WriteString("Focus on information that directly addresses the query. ")
	sb.WriteString("If the context doesn't contain relevant information, say so clearly. ")
	sb.WriteString("Format your response for machine parsing - use structured formats where appropriate.")

	return sb.String()
}

// SynthesizePatterns synthesizes a response about patterns
func (s *Synthesizer) SynthesizePatterns(ctx context.Context, patterns []*Pattern, query string) (*SynthesisResponse, error) {
	// Convert patterns to retrieval results
	context := make([]*RetrievalResult, len(patterns))
	for i, p := range patterns {
		context[i] = &RetrievalResult{
			ID:       p.ID,
			Content:  formatPatternContent(p),
			Score:    1.0,
			Source:   "memory",
			Category: p.Category,
			Type:     "pattern",
		}
	}

	return s.AnswerWithContext(ctx, query, context)
}

// SynthesizeFailures synthesizes a response about failures
func (s *Synthesizer) SynthesizeFailures(ctx context.Context, failures []*Failure, query string) (*SynthesisResponse, error) {
	// Convert failures to retrieval results
	context := make([]*RetrievalResult, len(failures))
	for i, f := range failures {
		context[i] = &RetrievalResult{
			ID:       f.ID,
			Content:  formatFailureContent(f),
			Score:    1.0,
			Source:   "memory",
			Category: "failure",
			Type:     "failure",
		}
	}

	return s.AnswerWithContext(ctx, query, context)
}

// SynthesizeBriefing synthesizes a briefing response
func (s *Synthesizer) SynthesizeBriefing(ctx context.Context, briefing *AgentBriefing, query string) (*SynthesisResponse, error) {
	// Build context from briefing
	var context []*RetrievalResult

	// Add resume state
	if briefing.ResumeState != nil {
		resumeJSON, _ := json.MarshalIndent(briefing.ResumeState, "", "  ")
		context = append(context, &RetrievalResult{
			ID:       "resume_state",
			Content:  string(resumeJSON),
			Score:    1.0,
			Source:   "memory",
			Category: "resume",
			Type:     "state",
		})
	}

	// Add modified files
	for _, f := range briefing.ModifiedFiles {
		context = append(context, &RetrievalResult{
			ID:       f.Path,
			Content:  formatFileContent(f),
			Score:    0.9,
			Source:   "memory",
			Category: "file",
			Type:     "file",
		})
	}

	// Add patterns
	for _, p := range briefing.Patterns {
		context = append(context, &RetrievalResult{
			ID:       p.ID,
			Content:  formatPatternContent(p),
			Score:    0.8,
			Source:   "memory",
			Category: p.Category,
			Type:     "pattern",
		})
	}

	// Add failures
	for _, f := range briefing.RecentFailures {
		context = append(context, &RetrievalResult{
			ID:       f.ID,
			Content:  formatFailureContent(f),
			Score:    0.7,
			Source:   "memory",
			Category: "failure",
			Type:     "failure",
		})
	}

	return s.AnswerWithContext(ctx, query, context)
}

// queryTypeKeywords maps query types to their identifying keywords
var queryTypeKeywords = []struct {
	queryType QueryType
	keywords  []string
}{
	{QueryTypePattern, []string{"pattern", "how should", "what's the way", "best practice", "convention"}},
	{QueryTypeFailure, []string{"error", "fail", "bug", "issue", "problem", "fix", "resolution"}},
	{QueryTypeFileState, []string{"file", "modified", "changed", "created", "read"}},
	{QueryTypeResumeState, []string{"task", "progress", "current", "next step", "blocker"}},
	{QueryTypeBriefing, []string{"briefing", "handoff", "summary", "status", "overview"}},
}

// ClassifyQuery classifies a query for appropriate handling
func ClassifyQuery(query string) QueryType {
	queryLower := strings.ToLower(query)
	for _, entry := range queryTypeKeywords {
		if matchesKeywords(queryLower, entry.keywords) {
			return entry.queryType
		}
	}
	return QueryTypeContext
}

func matchesKeywords(query string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(query, kw) {
			return true
		}
	}
	return false
}
