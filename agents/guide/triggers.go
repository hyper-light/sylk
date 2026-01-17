package guide

import (
	"strings"
)

// =============================================================================
// Trigger Detection
// =============================================================================

// TriggerResult contains the result of trigger detection
type TriggerResult struct {
	// Should this input be routed through the Guide?
	ShouldRoute bool `json:"should_route"`

	// Why we recommend routing (or not)
	Reason string `json:"reason"`

	// Detected trigger type
	TriggerType TriggerType `json:"trigger_type"`

	// Suggested target agent (if detectable without full classification)
	SuggestedTarget TargetAgent `json:"suggested_target,omitempty"`

	// Matched trigger phrase (for debugging)
	MatchedTrigger string `json:"matched_trigger,omitempty"`

	// Is this a DSL command?
	IsDSL bool `json:"is_dsl"`
}

// TriggerType indicates what kind of trigger was detected
type TriggerType string

const (
	TriggerTypeDSL           TriggerType = "dsl"           // Explicit DSL command
	TriggerTypeRetrospective TriggerType = "retrospective" // Past-focused query
	TriggerTypeGuide         TriggerType = "guide"         // Guide-specific query
	TriggerTypeNone          TriggerType = "none"          // No trigger detected
)

// =============================================================================
// Built-in Trigger Patterns
// =============================================================================
//
// These are the Guide's built-in triggers. Agent-specific triggers should be
// defined in each agent's routing.go file and registered via the RoutingAggregator.

// GuideTriggers are phrases that indicate a query for the Guide itself.
// These are built into the Guide and not agent-registered.
var GuideTriggers = []string{
	// Agent queries
	"what agents",
	"which agents",
	"registered agents",
	"available agents",
	"list agents",

	// Help queries
	"how do i route",
	"how do i query",
	"how do i store",
	"help with routing",
	"help with dsl",
	"dsl syntax",
	"dsl format",

	// Status queries
	"guide status",
	"system status",
	"routing status",
}

// =============================================================================
// Detection Functions
// =============================================================================

// TriggerDetector handles trigger detection with dynamic agent triggers
type TriggerDetector struct {
	routing *RoutingAggregator
}

// NewTriggerDetector creates a new trigger detector
func NewTriggerDetector(routing *RoutingAggregator) *TriggerDetector {
	return &TriggerDetector{routing: routing}
}

// Detect analyzes input and returns a routing recommendation.
// This is a fast, heuristic-based check that consuming agents can use
// to decide whether to invoke the Guide.
func (td *TriggerDetector) Detect(input string) *TriggerResult {
	input = strings.TrimSpace(input)
	inputLower := strings.ToLower(input)

	// Check for DSL first (fast path)
	if strings.HasPrefix(input, "@") {
		return td.detectDSLTrigger(input)
	}

	// Check for guide triggers (always checked)
	if trigger := matchTrigger(inputLower, GuideTriggers); trigger != "" {
		return &TriggerResult{
			ShouldRoute:     true,
			Reason:          "Guide query detected",
			TriggerType:     TriggerTypeGuide,
			SuggestedTarget: TargetGuide,
			MatchedTrigger:  trigger,
			IsDSL:           false,
		}
	}

	// Check registered agent triggers
	if td.routing != nil {
		for agentID, triggers := range td.routing.GetAllStrongTriggers() {
			if trigger := matchTrigger(inputLower, triggers); trigger != "" {
				return &TriggerResult{
					ShouldRoute:     true,
					Reason:          "Agent trigger detected - route to " + agentID,
					TriggerType:     TriggerTypeRetrospective,
					SuggestedTarget: TargetAgent(agentID),
					MatchedTrigger:  trigger,
					IsDSL:           false,
				}
			}
		}
	}

	// No trigger detected
	return &TriggerResult{
		ShouldRoute: false,
		Reason:      "No routing trigger detected",
		TriggerType: TriggerTypeNone,
		IsDSL:       false,
	}
}

// detectDSLTrigger handles DSL command detection
func (td *TriggerDetector) detectDSLTrigger(input string) *TriggerResult {
	result := newDSLTriggerResult()
	input = strings.TrimPrefix(input, "@")
	inputLower := strings.ToLower(input)

	if td.applyDSLPrefix(result, input, inputLower) {
		return result
	}
	if td.applyActionShortcut(result, inputLower) {
		return result
	}
	return td.applyFullDSL(result, input)
}

func newDSLTriggerResult() *TriggerResult {
	return &TriggerResult{
		ShouldRoute: true,
		Reason:      "DSL command detected",
		TriggerType: TriggerTypeDSL,
		IsDSL:       true,
	}
}

func (td *TriggerDetector) applyDSLPrefix(result *TriggerResult, input string, inputLower string) bool {
	switch {
	case strings.HasPrefix(inputLower, "guide"):
		result.Reason = "Intent-based routing via @guide"
		result.SuggestedTarget = TargetGuide
		return true
	case strings.HasPrefix(inputLower, "to:"):
		agent := td.parseDSLAgent(input, 3)
		result.Reason = "Direct routing via @to:" + agent
		result.SuggestedTarget = td.resolveTargetAgent(agent)
		return true
	case strings.HasPrefix(inputLower, "from:"):
		agent := td.parseDSLAgent(input, 5)
		result.Reason = "Response routing via @from:" + agent
		result.SuggestedTarget = td.resolveTargetAgent(agent)
		return true
	default:
		return false
	}
}

func (td *TriggerDetector) parseDSLAgent(input string, offset int) string {
	parts := strings.SplitN(input[offset:], " ", 2)
	return strings.ToLower(parts[0])
}

func (td *TriggerDetector) applyActionShortcut(result *TriggerResult, inputLower string) bool {
	if td.routing == nil {
		return false
	}

	action := td.extractAction(inputLower)
	if _, agentID, ok := td.routing.GetActionShortcut(action); ok {
		result.Reason = "Action shortcut via @" + action
		result.SuggestedTarget = TargetAgent(agentID)
		return true
	}
	return false
}

func (td *TriggerDetector) extractAction(inputLower string) string {
	actionEnd := strings.IndexAny(inputLower, " \t:")
	if actionEnd > 0 {
		return inputLower[:actionEnd]
	}
	return inputLower
}

func (td *TriggerDetector) applyFullDSL(result *TriggerResult, input string) *TriggerResult {
	parts := strings.SplitN(input, ":", 2)
	if len(parts) > 0 {
		agent := strings.ToLower(parts[0])
		result.SuggestedTarget = td.resolveTargetAgent(agent)
	}
	return result
}

// resolveTargetAgent resolves an agent name to a TargetAgent
func (td *TriggerDetector) resolveTargetAgent(agent string) TargetAgent {
	// Check routing aggregator first
	if td.routing != nil {
		if agentID, ok := td.routing.ResolveAgent(agent); ok {
			return TargetAgent(agentID)
		}
	}

	// Fallback to known agents
	switch agent {
	case "guide", "g":
		return TargetGuide
	default:
		return TargetUnknown
	}
}

// DetectTrigger is a convenience function that creates a detector without routing.
// For dynamic agent triggers, use NewTriggerDetector with a RoutingAggregator.
func DetectTrigger(input string) *TriggerResult {
	return NewTriggerDetector(nil).Detect(input)
}

// matchTrigger checks if input contains any of the trigger phrases
func matchTrigger(inputLower string, triggers []string) string {
	for _, trigger := range triggers {
		if strings.Contains(inputLower, trigger) {
			return trigger
		}
	}
	return ""
}

// =============================================================================
// Convenience Functions
// =============================================================================

// ShouldRouteToGuide is a simple helper that returns true if input should be routed
func ShouldRouteToGuide(input string) bool {
	return DetectTrigger(input).ShouldRoute
}

// ShouldRouteToGuideWithRouting checks if input should be routed, using registered agents
func ShouldRouteToGuideWithRouting(input string, routing *RoutingAggregator) bool {
	return NewTriggerDetector(routing).Detect(input).ShouldRoute
}

// IsDSLCommand checks if input is a DSL command
func IsDSLCommand(input string) bool {
	return strings.HasPrefix(strings.TrimSpace(input), "@")
}

// IsRetrospectiveQuery checks if input appears to be about the past
func IsRetrospectiveQuery(input string) bool {
	result := DetectTrigger(input)
	return result.TriggerType == TriggerTypeRetrospective
}

// IsRetrospectiveQueryWithRouting checks if input appears to be about the past
func IsRetrospectiveQueryWithRouting(input string, routing *RoutingAggregator) bool {
	result := NewTriggerDetector(routing).Detect(input)
	return result.TriggerType == TriggerTypeRetrospective
}

// =============================================================================
// Trigger Registration (for extensibility)
// =============================================================================

// TriggerRegistry allows custom triggers to be registered.
// It wraps a RoutingAggregator for dynamic agent triggers plus custom additions.
type TriggerRegistry struct {
	routing *RoutingAggregator
	guide   []string
	custom  map[TargetAgent][]string
}

// NewTriggerRegistry creates a new trigger registry
func NewTriggerRegistry() *TriggerRegistry {
	return NewTriggerRegistryWithRouting(nil)
}

// NewTriggerRegistryWithRouting creates a trigger registry with routing aggregator
func NewTriggerRegistryWithRouting(routing *RoutingAggregator) *TriggerRegistry {
	return &TriggerRegistry{
		routing: routing,
		guide:   append([]string{}, GuideTriggers...),
		custom:  make(map[TargetAgent][]string),
	}
}

// SetRouting sets the routing aggregator
func (tr *TriggerRegistry) SetRouting(routing *RoutingAggregator) {
	tr.routing = routing
}

// AddGuideTrigger adds a custom guide trigger
func (tr *TriggerRegistry) AddGuideTrigger(trigger string) {
	tr.guide = append(tr.guide, strings.ToLower(trigger))
}

// AddCustomTrigger adds a trigger for a custom target agent
func (tr *TriggerRegistry) AddCustomTrigger(target TargetAgent, trigger string) {
	tr.custom[target] = append(tr.custom[target], strings.ToLower(trigger))
}

// Detect uses the registry to detect triggers
func (tr *TriggerRegistry) Detect(input string) *TriggerResult {
	input = strings.TrimSpace(input)
	inputLower := strings.ToLower(input)

	if strings.HasPrefix(input, "@") {
		return tr.detectDSL(input)
	}

	if result := tr.detectGuideTrigger(inputLower); result != nil {
		return result
	}

	if result := tr.detectRoutingTriggers(inputLower); result != nil {
		return result
	}

	if result := tr.detectCustomTrigger(inputLower); result != nil {
		return result
	}

	return &TriggerResult{
		ShouldRoute: false,
		Reason:      "No trigger detected",
		TriggerType: TriggerTypeNone,
	}
}

func (tr *TriggerRegistry) detectDSL(input string) *TriggerResult {
	detector := NewTriggerDetector(tr.routing)
	return detector.detectDSLTrigger(input)
}

func (tr *TriggerRegistry) detectGuideTrigger(inputLower string) *TriggerResult {
	trigger := matchTrigger(inputLower, tr.guide)
	if trigger == "" {
		return nil
	}
	return &TriggerResult{
		ShouldRoute:     true,
		Reason:          "Guide query detected",
		TriggerType:     TriggerTypeGuide,
		SuggestedTarget: TargetGuide,
		MatchedTrigger:  trigger,
	}
}

func (tr *TriggerRegistry) detectRoutingTriggers(inputLower string) *TriggerResult {
	if tr.routing == nil {
		return nil
	}

	for agentID, triggers := range tr.routing.GetAllStrongTriggers() {
		if trigger := matchTrigger(inputLower, triggers); trigger != "" {
			return &TriggerResult{
				ShouldRoute:     true,
				Reason:          "Agent trigger detected - route to " + agentID,
				TriggerType:     TriggerTypeRetrospective,
				SuggestedTarget: TargetAgent(agentID),
				MatchedTrigger:  trigger,
			}
		}
	}

	return nil
}

func (tr *TriggerRegistry) detectCustomTrigger(inputLower string) *TriggerResult {
	for target, triggers := range tr.custom {
		if trigger := matchTrigger(inputLower, triggers); trigger != "" {
			return &TriggerResult{
				ShouldRoute:     true,
				Reason:          "Custom trigger detected",
				TriggerType:     TriggerTypeRetrospective,
				SuggestedTarget: target,
				MatchedTrigger:  trigger,
			}
		}
	}
	return nil
}
