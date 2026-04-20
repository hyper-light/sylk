package shared

import (
	"errors"
	"fmt"
	"strings"
)

// ProtocolError is the canonical typed error raised by protocol
// validators (pipeline_protocol.go, global_review_protocol.go,
// skill_contract_artifacts.go) when a rule is violated. Every field is
// structured so the LLM sees {RuleID, MissingArtifact, RecoveryAction,
// HumanMessage} instead of parsing English prose, the UI can render a
// recovery-hint inline, and logs carry a greppable RuleID for telemetry.
//
// Why this exists: the pre-typed-error era raised `fmt.Errorf("run_test_suite
// must produce a snapshot during this ...")` whose text changed with
// every refactor and referenced internal concepts the LLM had no
// mental model for. ProtocolError normalizes the shape: every
// validation failure is a row the LLM can switch on.
type ProtocolError struct {
	// RuleID is the stable string identifier of the rule violated
	// ("pipeline.finalize.requires_snapshot",
	// "pipeline.terminal.second_action_rejected", etc.). Greppable in
	// logs and telemetry. Every rule added to a validator MUST declare
	// an ID so rules are countable and regression-testable.
	RuleID string `json:"rule_id"`

	// Scope names the protocol tier the rule belongs to ("pipeline",
	// "global_review", "skill_contract"). Lets the UI route the error
	// to the right panel and telemetry bucket it correctly.
	Scope string `json:"scope"`

	// MissingArtifact, when non-empty, names the protocol-state artifact
	// whose absence triggered the rule. Populated for contract-style
	// violations where a specific artifact (test_snapshot,
	// pending_challenge, etc.) was required but missing.
	MissingArtifact string `json:"missing_artifact,omitempty"`

	// ActionName, when non-empty, names the skill or action the LLM
	// was attempting when the rule fired. Lets the UI surface "tried
	// to call X but Y was missing" without parsing prose.
	ActionName string `json:"action_name,omitempty"`

	// RecoveryAction is a short, concrete next step the LLM should
	// take to resolve the violation (e.g., "call run_test_suite
	// before retrying"). The LLM reads this as a string field and can
	// pattern-match on it instead of interpreting prose.
	RecoveryAction string `json:"recovery_action,omitempty"`

	// HumanMessage is the user-facing prose the UI shows in error
	// rows. Phrased for the user ("the tester needs to run the suite
	// before finalizing"), not the LLM.
	HumanMessage string `json:"human_message"`
}

// Error implements the error interface. Formats the typed fields into a
// compact prose string for callers that consume error.Error(). Typed
// consumers should type-assert and read fields directly.
//
// Convention: every RuleID carries its scope as a prefix
// ("pipeline.finalize.challenge_target_mismatch"). The Error()
// output prepends Scope only when RuleID does not already include
// it — otherwise the rendered string would double up
// ("pipeline.pipeline.finalize.challenge_target_mismatch") as a
// pure cosmetic artifact that confuses log consumers and LLM
// readers.
func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	if e.RuleID != "" {
		if e.Scope != "" && !strings.HasPrefix(e.RuleID, e.Scope+".") {
			b.WriteString(e.Scope)
			b.WriteString(".")
		}
		b.WriteString(e.RuleID)
	} else if e.Scope != "" {
		b.WriteString(e.Scope)
	}
	if e.HumanMessage != "" {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.HumanMessage)
	}
	if e.RecoveryAction != "" {
		b.WriteString(" — ")
		b.WriteString(e.RecoveryAction)
	}
	return b.String()
}

// Is lets callers use errors.Is to match on sentinel ProtocolError
// values (e.g., errors.Is(err, ErrProtocolViolation)) or on specific
// rule IDs via ProtocolErrorMatchingRule.
func (e *ProtocolError) Is(target error) bool {
	if target == nil {
		return false
	}
	if target == ErrProtocolViolation {
		return true
	}
	other, ok := target.(*ProtocolError)
	if !ok {
		return false
	}
	return e.RuleID == other.RuleID && e.Scope == other.Scope
}

// ErrProtocolViolation is the sentinel error every ProtocolError
// wraps via Is, so callers can `errors.Is(err, ErrProtocolViolation)`
// to branch on "any protocol rule failed" without enumerating IDs.
var ErrProtocolViolation = errors.New("protocol violation")

// ProtocolErrorMatchingRule returns a sentinel *ProtocolError with
// only the RuleID populated; use as an errors.Is target to match on a
// specific rule.
func ProtocolErrorMatchingRule(scope, ruleID string) *ProtocolError {
	return &ProtocolError{Scope: scope, RuleID: ruleID}
}

// NewPipelineProtocolError constructs a ProtocolError scoped to the
// pipeline tier. RuleID should be a stable "pipeline.<rule>" string.
func NewPipelineProtocolError(ruleID, actionName, humanMessage, recovery string) *ProtocolError {
	return &ProtocolError{
		Scope:          "pipeline",
		RuleID:         ruleID,
		ActionName:     strings.TrimSpace(actionName),
		HumanMessage:   strings.TrimSpace(humanMessage),
		RecoveryAction: strings.TrimSpace(recovery),
	}
}

// NewGlobalReviewProtocolError constructs a ProtocolError scoped to
// the global review tier.
func NewGlobalReviewProtocolError(ruleID, actionName, humanMessage, recovery string) *ProtocolError {
	return &ProtocolError{
		Scope:          "global_review",
		RuleID:         ruleID,
		ActionName:     strings.TrimSpace(actionName),
		HumanMessage:   strings.TrimSpace(humanMessage),
		RecoveryAction: strings.TrimSpace(recovery),
	}
}

// NewMissingArtifactProtocolError constructs a typed error for the
// common case where a rule fires because a canonical protocol-state
// artifact is missing. Wraps the ProtocolContractViolation shape used
// by the dispatcher so consumers can read both MissingArtifact and
// RecoveryAction from one struct.
func NewMissingArtifactProtocolError(scope, ruleID, actionName, missingArtifact string) *ProtocolError {
	return &ProtocolError{
		Scope:           scope,
		RuleID:          ruleID,
		ActionName:      strings.TrimSpace(actionName),
		MissingArtifact: strings.TrimSpace(missingArtifact),
		RecoveryAction:  recoveryForArtifact(missingArtifact),
		HumanMessage: fmt.Sprintf(
			"%s requires %s which is not present",
			actionName, missingArtifact,
		),
	}
}

// AsProtocolError returns the ProtocolError if err is one (or wraps
// one). Consumers use this instead of type-asserting so wrapped
// errors (fmt.Errorf with %w) still surface as typed.
func AsProtocolError(err error) (*ProtocolError, bool) {
	if err == nil {
		return nil, false
	}
	var pe *ProtocolError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}
