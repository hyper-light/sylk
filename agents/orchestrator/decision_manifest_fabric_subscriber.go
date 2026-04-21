// Phase 3 refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// DecisionManifestFabricSubscriber tails the Activity Fabric for
// ActionDecisionDeclared activities emitted by pipeline agents'
// declare_decision skill and upserts them into the manifest SQL
// store. This flips the write direction: the skill is now the
// authoritative source, the SQL store is a projection fed by the
// subscriber (plus any server-initiated declarations that still call
// DecisionManifestStore.Declare directly).
//
// To keep the two paths consistent:
//
//   - Skill-originated fabric events carry the actor identity in
//     Actor.AgentID / .AgentType / .PipelineID and the typed payload
//     in Payload (decoded here).
//   - Server-initiated declarations (tests, proactive promotions)
//     still go through Declare(); the amplifier continues to emit an
//     ActionDecisionDeclared activity after the SQL write. The
//     subscriber tags those activities via SourceTable = "decision_manifest"
//     and skips them to avoid double-persisting.
package orchestrator

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/activitystore"
	"github.com/adalundhe/sylk/core/manifest"
)

// DecisionManifestFabricSubscriber ingests ActionDecisionDeclared
// activities from the fabric and upserts them into the manifest store.
type DecisionManifestFabricSubscriber struct {
	store *DecisionManifestStore
}

// NewDecisionManifestFabricSubscriber wraps a store for late-binding
// with SubscribeToDefault at orchestrator startup.
func NewDecisionManifestFabricSubscriber(store *DecisionManifestStore) *DecisionManifestFabricSubscriber {
	return &DecisionManifestFabricSubscriber{store: store}
}

// Name implements activitystore.Subscriber.
func (s *DecisionManifestFabricSubscriber) Name() string {
	return "manifest.decision"
}

// Receive implements activitystore.Subscriber. Only ActionDecisionDeclared
// events originating from pipeline skills are processed; amplifier-
// origin events (SourceTable = "decision_manifest") are skipped so the
// SQL store is not written to twice.
func (s *DecisionManifestFabricSubscriber) Receive(ctx context.Context, a activity.AgentActivity) {
	if s == nil || s.store == nil {
		return
	}
	if a.Action != activity.ActionDecisionDeclared {
		return
	}
	if strings.TrimSpace(a.SourceTable) == "decision_manifest" {
		// Emitted by the amplifier as an echo of a prior SQL write —
		// skip to avoid double-persisting.
		return
	}
	payload := decodeDeclareDecisionPayload(a.Payload)
	if payload == nil {
		return
	}
	scope := make(manifest.Scope, len(a.Subject.Coordinates))
	for k, v := range a.Subject.Coordinates {
		// Supersedes is stashed as a coordinate by the skill; promote
		// it back to the typed field.
		if k == "supersedes" {
			continue
		}
		scope[manifest.Dimension(k)] = v
	}
	confidence := manifest.Confidence(strings.TrimSpace(string(a.Confidence)))
	if confidence == "" {
		confidence = manifest.ConfidenceTentative
	}
	in := manifest.DeclareDecisionInput{
		Domain:     payload.Domain,
		Scope:      scope,
		Value:      payload.Value,
		Evidence:   payload.Evidence,
		Confidence: confidence,
		Supersedes: a.Subject.Coordinates["supersedes"],
	}
	author := manifest.AgentRef{
		AgentID:   a.Actor.AgentID,
		AgentType: a.Actor.AgentType,
	}
	// Best-effort: failures are logged inside the store, never returned
	// to the fabric caller.
	_, _ = s.store.Declare(ctx, string(a.SessionID), author, in)
}

// decideDeclareDecisionPayload decodes the typed payload AutoPublishDecision
// writes: {"value","author","evidence","trigger_skill","domain"}. The
// Domain field is also on Subject.Domain; we read from the payload as
// the authoritative source because AutoPublishInput is the skill-side
// contract.
type declareDecisionPayload struct {
	Domain   string   `json:"domain"`
	Value    string   `json:"value"`
	Evidence []string `json:"evidence,omitempty"`
}

func decodeDeclareDecisionPayload(raw json.RawMessage) *declareDecisionPayload {
	if len(raw) == 0 {
		return nil
	}
	var p declareDecisionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	p.Domain = strings.TrimSpace(p.Domain)
	p.Value = strings.TrimSpace(p.Value)
	if p.Domain == "" || p.Value == "" {
		return nil
	}
	return &p
}

var _ activitystore.Subscriber = (*DecisionManifestFabricSubscriber)(nil)
