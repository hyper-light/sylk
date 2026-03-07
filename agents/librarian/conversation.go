package librarian

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/google/uuid"
)

// processForwardedRequest handles the actual request processing.
// When LLM is enabled, builds a providers.Request and runs the tool loop.
// Falls back to direct intent-dispatch when LLM is disabled.
func (l *Librarian) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if l.config.EnableLLM && l.getProvider() != nil {
		return l.processViaLLM(ctx, fwd)
	}
	return l.processViaIntentDispatch(ctx, fwd)
}

// processViaLLM builds an LLM request with tools and runs the tool loop.
func (l *Librarian) processViaLLM(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	llmReq := l.buildLLMRequest(fwd)

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	result, err := l.executeToolLoop(ctx, llmReq, shared.SteeringLedgerFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("librarian search failed: %w", err)
	}

	// The tool loop must produce a non-empty response. An empty string
	// propagates as a silent no-output to the user, which violates the
	// librarian's contract of always providing a substantive answer.
	if strings.TrimSpace(result) == "" {
		return nil, fmt.Errorf("librarian: generated empty response for query %q", fwd.Input)
	}

	return result, nil
}

// buildLLMRequest constructs a providers.Request for the tool loop.
func (l *Librarian) buildLLMRequest(fwd *guide.ForwardedRequest) *providers.Request {
	l.prepareSkillsForInput(fwd.Input)
	req := &providers.Request{
		SystemPrompt: l.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: fwd.Input}},
		Tools:        l.buildToolDefinitions(),
		Model:        l.config.Model,
		MaxTokens:    l.config.MaxTokens,
	}
	l.applyConversationRuntimeProfile(req)
	return req
}

// processViaIntentDispatch is the legacy path that routes by intent without LLM.
func (l *Librarian) processViaIntentDispatch(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := l.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (l *Librarian) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentFind, guide.IntentSearch, guide.IntentLocate:
		return l.handleSearch, nil
	case guide.IntentFetch:
		return l.handleFetch, nil
	case guide.IntentRecall:
		return l.handleRecall, nil
	case guide.IntentCheck:
		return l.handleCheck, nil
	case guide.IntentHelp:
		return l.handleHelp, nil
	default:
		return nil, fmt.Errorf("unsupported intent: %s", intent)
	}
}

// handleFetch processes fetch/clone requests when LLM is disabled.
// Extracts a URL from the input and clones it. When LLM is enabled,
// this path is not used — the LLM drives clone_repository via tool loop.
func (l *Librarian) handleFetch(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	return l.executeClone(ctx, fwd.Input, "")
}

// =============================================================================
// Legacy Intent Handlers (used when LLM is disabled)
// =============================================================================

// handleSearch processes search requests (find, search, locate intents).
func (l *Librarian) handleSearch(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if l.searchHandler == nil {
		return nil, fmt.Errorf("search handler not configured")
	}

	req := &LibrarianRequest{
		ID:        uuid.New().String(),
		Intent:    IntentRecall,
		Domain:    DomainCode,
		Query:     fwd.Input,
		SessionID: "",
		Timestamp: time.Now(),
	}

	if fwd.Entities != nil {
		req.Params = make(map[string]any)
		if fwd.Entities.Limit > 0 {
			req.Params["limit"] = fwd.Entities.Limit
		}
		if len(fwd.Entities.FilePaths) > 0 {
			req.Params["path_prefix"] = fwd.Entities.FilePaths[0]
		}
	}

	return l.searchHandler.Handle(ctx, req)
}

// handleRecall processes recall requests (query past data).
func (l *Librarian) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	return l.handleSearch(ctx, fwd)
}

// handleCheck processes check/verification requests.
func (l *Librarian) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if l.searchHandler == nil {
		return nil, fmt.Errorf("search handler not configured")
	}

	req := &LibrarianRequest{
		ID:        uuid.New().String(),
		Intent:    IntentCheck,
		Domain:    DomainCode,
		Query:     fwd.Input,
		Params:    map[string]any{"limit": 1},
		Timestamp: time.Now(),
	}

	resp, err := l.searchHandler.Handle(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.Success {
		data, ok := resp.Data.(map[string]any)
		if ok {
			results, _ := data["results"].([]EnrichedResult)
			return map[string]any{
				"found": len(results) > 0,
				"count": len(results),
				"data":  results,
			}, nil
		}
	}

	return map[string]any{
		"found": false,
		"count": 0,
	}, nil
}

func (l *Librarian) handleHelp(_ context.Context, _ *guide.ForwardedRequest) (any, error) {
	return map[string]any{
		"agent":              "librarian",
		"description":        "Code and file search across the local workspace and cloned remote packages.",
		"supported_intents":  []guide.Intent{guide.IntentFind, guide.IntentSearch, guide.IntentLocate, guide.IntentFetch, guide.IntentRecall, guide.IntentCheck, guide.IntentHelp},
		"supported_domains":  []guide.Domain{guide.DomainCode},
		"recommended_routes": []string{"@librarian:find:code", "@librarian:search:code", "@librarian:locate:code", "@librarian:fetch:code"},
	}, nil
}
