package guide

import (
	"context"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// =============================================================================
// Router
// =============================================================================

// Router handles intent-based routing of queries to agents.
// The router is stateless - it classifies and routes without maintaining state.
// Statistics and learning should be handled by the caller if needed.
type Router struct {
	// Configuration
	config RouterConfig

	// Components (stateless)
	parser     *Parser
	classifier *Classifier
}

// NewRouter creates a new intent-based router.
// The router is stateless - each Route call is independent.
func NewRouter(client ClassifierClient, config RouterConfig) *Router {
	if config.Model == "" {
		config = DefaultRouterConfig()
	}

	return &Router{
		config:     config,
		parser:     NewParser(config.DSLPrefix),
		classifier: NewClassifierWithClient(client, config),
	}
}

// NewRouterWithAnthropicClient creates a router with an Anthropic client
func NewRouterWithAnthropicClient(client *anthropic.Client, config RouterConfig) *Router {
	return NewRouter(NewRealClassifierClient(client), config)
}

// Route routes a request to the appropriate agent.
// This method is stateless - it performs classification and returns a result
// without maintaining any internal state.
//
// Flow: RequestingAgent -> Router -> TargetAgent (determined by result)
func (r *Router) Route(ctx context.Context, request *RouteRequest) (*RouteResult, error) {
	start := time.Now()

	input := strings.TrimSpace(request.Input)

	// Try DSL parsing first (fast path - 0 tokens, <1ms)
	if r.parser.IsDSL(input) {
		return r.routeDSL(ctx, input, start)
	}

	// Fall back to LLM classification (slow path)
	return r.routeNaturalLanguage(ctx, input, start)
}

// routeDSL routes a DSL command (fast path)
func (r *Router) routeDSL(ctx context.Context, input string, start time.Time) (*RouteResult, error) {
	// Handle multiple commands
	if strings.Contains(input, ";") {
		return r.routeMultipleDSL(ctx, input, start)
	}

	// Parse single command
	cmd, err := r.parser.Parse(input)
	if err != nil {
		// DSL parse error - fall back to NL classification
		return r.routeNaturalLanguage(ctx, input, start)
	}

	// Handle different command types
	switch cmd.Type {
	case DSLCommandTypeIntent:
		// @guide <query> - use the query for intent classification
		if cmd.Query != "" {
			return r.routeNaturalLanguage(ctx, cmd.Query, start)
		}
		// @guide with no query - return help
		return &RouteResult{
			Intent:               IntentHelp,
			Domain:               DomainSystem,
			TargetAgent:          TargetGuide,
			Confidence:           1.0,
			ClassificationMethod: "dsl",
			Action:               RouteActionExecute,
			ProcessingTime:       time.Since(start),
		}, nil

	case DSLCommandTypeDirect, DSLCommandTypeAction:
		// @to:<agent>, @from:<agent>, @archive - direct routing
		result := cmd.ToRouteResult()
		result.ProcessingTime = time.Since(start)
		return result, nil

	case DSLCommandTypeFull:
		// @agent:intent:domain - full DSL
		result := cmd.ToRouteResult()
		result.ProcessingTime = time.Since(start)
		return result, nil

	default:
		// Unknown type - fall back to NL
		return r.routeNaturalLanguage(ctx, input, start)
	}
}

// routeMultipleDSL routes multiple DSL commands
func (r *Router) routeMultipleDSL(ctx context.Context, input string, start time.Time) (*RouteResult, error) {
	commands, errors := r.parser.ParseMultiple(input)

	// If all failed, fall back to NL
	if len(commands) == 0 && len(errors) > 0 {
		return r.routeNaturalLanguage(ctx, input, start)
	}

	// Build combined result
	result := &RouteResult{
		Intent:               IntentUnknown, // Multi-intent
		Domain:               DomainUnknown,
		TargetAgent:          TargetUnknown,
		Confidence:           1.0,
		ClassificationMethod: "dsl",
		ProcessingTime:       time.Since(start),
		SubResults:           make([]*RouteResult, 0, len(commands)),
	}

	for _, cmd := range commands {
		subResult := cmd.ToRouteResult()
		result.SubResults = append(result.SubResults, subResult)

		// Use first command's target as primary
		if result.TargetAgent == TargetUnknown {
			result.TargetAgent = subResult.TargetAgent
			result.Intent = subResult.Intent
			result.Domain = subResult.Domain
		}
	}

	return result, nil
}

// routeNaturalLanguage routes using LLM classification (slow path)
func (r *Router) routeNaturalLanguage(ctx context.Context, input string, start time.Time) (*RouteResult, error) {
	// Fast path: check for search queries before LLM classification
	if isSearchQuery(input) {
		return buildSearchRouteResult(time.Since(start)), nil
	}

	// Classify using LLM
	classification, err := r.classifier.Classify(ctx, input)
	if err != nil {
		return nil, err
	}

	// Convert to route result
	result := classification.ToRouteResult(time.Since(start))

	return result, nil
}

// AddCorrection adds a correction to improve future classification.
// This is for few-shot learning in the LLM prompt.
func (r *Router) AddCorrection(correction CorrectionRecord) {
	r.classifier.AddCorrection(correction)
}

// =============================================================================
// Helper Methods
// =============================================================================

// IsDSL checks if input is a DSL command
func (r *Router) IsDSL(input string) bool {
	return r.parser.IsDSL(input)
}

// ParseDSL parses a DSL command without routing
func (r *Router) ParseDSL(input string) (*DSLCommand, error) {
	return r.parser.Parse(input)
}

// FormatAsDSL formats a route result as DSL
func (r *Router) FormatAsDSL(result *RouteResult) string {
	return FormatDSLCommand(result)
}

// =============================================================================
// Search Query Detection
// =============================================================================

// searchKeywords are keywords that indicate a search query for the Librarian
var searchKeywords = []string{
	"find",
	"search",
	"locate",
	"where is",
}

// isSearchQuery detects if the input contains search-related keywords.
// Returns true if any search keyword is found in the input (case-insensitive).
func isSearchQuery(input string) bool {
	lower := strings.ToLower(input)
	for _, keyword := range searchKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// buildSearchRouteResult creates a RouteResult for search queries routed to Librarian.
func buildSearchRouteResult(processingTime time.Duration) *RouteResult {
	return &RouteResult{
		Intent:               IntentRecall,
		Domain:               DomainCode,
		TargetAgent:          TargetLibrarian,
		TemporalFocus:        TemporalPresent,
		Confidence:           0.85,
		Action:               RouteActionExecute,
		ClassificationMethod: "keyword",
		ProcessingTime:       processingTime,
	}
}
