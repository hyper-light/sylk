package librarian

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/google/uuid"
)

// processForwardedRequest handles the actual request processing.
// When LLM is enabled, builds a providers.Request and runs the tool loop.
// Falls back to direct intent-dispatch when LLM is disabled.
func (l *Librarian) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest, bundle *librarianToolBundle) (any, error) {
	if l.config.EnableLLM && l.getProvider() != nil {
		return l.processViaLLM(ctx, fwd, bundle)
	}
	return l.processViaIntentDispatch(ctx, fwd)
}

// processViaLLM builds an LLM request with tools and runs the tool loop.
// Returns a typed *shared.ConsultResponsePayload so downstream consumers
// (chat UI, protocol logs) read declared fields rather than defensively
// parsing the LLM's raw text. See agents/shared/interagent_response.go.
func (l *Librarian) processViaLLM(ctx context.Context, fwd *guide.ForwardedRequest, bundle *librarianToolBundle) (any, error) {
	llmReq := l.buildLLMRequestWithBundle(fwd, bundle)

	// MEM-01: preload family-typed forest projections into the system
	// prompt before the tool loop starts. Best-effort: a preload error
	// degrades silently; memory is assist, not gate.
	ctx = l.injectForestPreload(ctx, llmReq, fwd)

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ctx, ledger, llmReq, func() (string, error) {
		return l.executeToolLoopWithBundle(ctx, llmReq, ledger, bundle)
	})
	if err != nil {
		return nil, fmt.Errorf("librarian search failed: %w", err)
	}

	// The tool loop must produce a non-empty response. An empty string
	// propagates as a silent no-output to the user, which violates the
	// librarian's contract of always providing a substantive answer.
	if strings.TrimSpace(result) == "" {
		return nil, fmt.Errorf("librarian: generated empty response for query %q", fwd.Input)
	}

	payload := shared.DecodeConsultResponsePayloadFromLLM(result)
	if payload != nil {
		payload.FreshnessHorizon = librarianConsultFreshnessHorizon
	}

	// Consultation response testament.
	summary := "Librarian responded (LLM path)"
	if payload != nil && payload.Summary != "" {
		summary = "Librarian: " + truncateLibrarian(payload.Summary, 100)
	}
	l.librarianSubmitTestament(ctx, l.librarianTestamentWithSubject(
		summary, "committed", fwd.SourceAgentID,
		[]*claims.Artifact{
			l.librarianJSONArtifact("response_payload", payload),
			l.librarianArtifact("source_agent", fwd.SourceAgentID),
			l.librarianArtifact("intent", string(fwd.Intent)),
		},
	))

	return payload, nil
}

// librarianConsultFreshnessHorizon is the validity window the
// librarian commits for cached re-use of its consultation answers.
// Derived from the librarian's data domain: librarian answers are
// grounded in workspace state (file existence, package layouts,
// import graphs). Workspace files can change at developer typing
// cadence, so this horizon is set short enough that an answer
// drafted before a meaningful edit doesn't get re-served stale,
// long enough that rapid back-to-back consults during a single
// reasoning pass benefit from cache reuse. 15 minutes matches the
// dev-tempo "active edit window" — the time scale over which an
// engineer typically completes a focused change before pausing.
//
// The architect's session cache uses this value as the upper bound
// on cache validity for librarian responses; entries past their
// StoredAt + horizon force a fresh consult.
const librarianConsultFreshnessHorizon = 15 * time.Minute

// buildLLMRequest constructs a providers.Request for the tool loop.
func (l *Librarian) buildLLMRequest(fwd *guide.ForwardedRequest) *providers.Request {
	return l.buildLLMRequestWithBundle(fwd, nil)
}

func (l *Librarian) buildLLMRequestWithBundle(fwd *guide.ForwardedRequest, bundle *librarianToolBundle) *providers.Request {
	if bundle != nil {
		bundle.prepareSkillsForInput(fwd.Input)
	} else {
		l.prepareSkillsForInput(fwd.Input)
	}
	req := &providers.Request{
		SystemPrompt: l.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: fwd.Input}},
		Tools:        l.buildToolDefinitionsWithBundle(bundle),
		Model:        l.CurrentModel(),
		MaxTokens:    l.config.MaxTokens,
	}
	l.applyConversationRuntimeProfile(req, fwd.SessionID)
	return req
}

// injectForestPreload pulls librarian-lane projections and prepends
// the rendered block to the existing system prompt. See
// agents/shared/forest_preload.go for the family selection rationale.
func (l *Librarian) injectForestPreload(ctx context.Context, req *providers.Request, fwd *guide.ForwardedRequest) context.Context {
	if req == nil || fwd == nil || l.config.Forest == nil {
		return ctx
	}
	ctx, _, err := shared.ApplyForestPreload(ctx, req, l.config.Forest, shared.ForestPreloadInput{
		AgentType: "librarian",
		Query:     fwd.Input,
		SessionID: fwd.SessionID,
	})
	if err != nil {
		return ctx
	}
	return ctx
}

// processViaIntentDispatch is the legacy path that routes by intent without LLM.
func (l *Librarian) processViaIntentDispatch(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := l.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	result, handlerErr := handler(ctx, fwd)

	// Intent dispatch response testament.
	if handlerErr != nil {
		l.librarianSubmitTestament(ctx, l.librarianTestament(
			"Librarian intent dispatch failed: "+handlerErr.Error(), "committed",
			[]*claims.Artifact{
				l.librarianArtifact("intent", string(fwd.Intent)),
				l.librarianArtifact("error", handlerErr.Error()),
			},
		))
	} else {
		l.librarianSubmitTestament(ctx, l.librarianTestamentWithSubject(
			"Librarian responded (intent: "+string(fwd.Intent)+")", "committed", fwd.SourceAgentID,
			[]*claims.Artifact{
				l.librarianArtifact("intent", string(fwd.Intent)),
				l.librarianArtifact("source_agent", fwd.SourceAgentID),
			},
		))
	}
	return result, handlerErr
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
		SessionID: fwd.SessionID,
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
		SessionID: fwd.SessionID,
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
