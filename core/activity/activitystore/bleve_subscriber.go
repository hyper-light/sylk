package activitystore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"

	"github.com/adalundhe/sylk/core/activity"
)

// BleveSubscriber indexes Medium and Coarse activities into a Bleve
// full-text index so the find_related_activity skill can serve real
// substring + tokenized queries (Tier 10 of the FABRIC.md plan).
//
// The subscriber's index is an in-memory Bleve index by default;
// production wiring can swap to a disk-backed path via
// NewBleveSubscriberAtPath. Atomic and Fine activities are skipped —
// indexing them at their volume would dominate write cost.
type BleveSubscriber struct {
	index   bleve.Index
	indexed atomic.Uint64

	mu sync.Mutex
}

// NewBleveSubscriber creates a subscriber backed by an in-memory
// Bleve index. Suitable for dev workloads and tests.
func NewBleveSubscriber() (*BleveSubscriber, error) {
	idx, err := bleve.NewMemOnly(activityIndexMapping())
	if err != nil {
		return nil, fmt.Errorf("bleve subscriber: open mem index: %w", err)
	}
	return &BleveSubscriber{index: idx}, nil
}

// NewBleveSubscriberAtPath creates a subscriber backed by an on-disk
// Bleve index at path. The path is opened if it exists, created
// otherwise.
func NewBleveSubscriberAtPath(path string) (*BleveSubscriber, error) {
	idx, err := bleve.Open(path)
	if err == nil {
		return &BleveSubscriber{index: idx}, nil
	}
	idx, err = bleve.New(path, activityIndexMapping())
	if err != nil {
		return nil, fmt.Errorf("bleve subscriber: create index at %s: %w", path, err)
	}
	return &BleveSubscriber{index: idx}, nil
}

func (b *BleveSubscriber) Name() string { return "fabric.bleve" }

// Receive indexes the activity if it warrants full-text indexing.
// Atomic and Fine activities are dropped to keep indexing cost
// proportional to semantic-tier volume.
func (b *BleveSubscriber) Receive(_ context.Context, a activity.AgentActivity) {
	if !a.Resolution.ShouldIndexFullText() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	doc := map[string]any{
		"session_id":              string(a.SessionID),
		"action_kind":             string(a.Action),
		"actor_agent_id":          a.Actor.AgentID,
		"actor_agent_type":        a.Actor.AgentType,
		"actor_pipeline_id":       a.Actor.PipelineID,
		"subject_path_prefix":     a.Subject.PathPrefix,
		"subject_target_agent":    a.Subject.TargetAgent,
		"subject_target_artifact": a.Subject.TargetArtifact,
		"subject_domain":          a.Subject.Domain,
		"payload":                 string(a.Payload),
		"timestamp":               a.Timestamp.Unix(),
		"confidence":              string(a.Confidence),
	}
	enrichClaimsFields(doc, a)
	if err := b.index.Index(string(a.ID), doc); err == nil {
		b.indexed.Add(1)
	}
}

// IndexedCount returns the running total of activities indexed.
func (b *BleveSubscriber) IndexedCount() uint64 {
	return b.indexed.Load()
}

// Search runs a free-text query against the activity index. Returns
// the matching activity IDs in score order.
func (b *BleveSubscriber) Search(query string, limit int) ([]activity.ActivityID, error) {
	if b == nil || b.index == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	q := bleve.NewQueryStringQuery(query)
	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	res, err := b.index.Search(req)
	if err != nil {
		return nil, fmt.Errorf("bleve subscriber: search: %w", err)
	}
	out := make([]activity.ActivityID, 0, len(res.Hits))
	for _, hit := range res.Hits {
		out = append(out, activity.ActivityID(hit.ID))
	}
	return out, nil
}

// Close releases the index.
func (b *BleveSubscriber) Close() error {
	if b == nil || b.index == nil {
		return nil
	}
	return b.index.Close()
}

// activityIndexMapping returns a default mapping suitable for the
// activity stream — keyword fields for exact-match (action_kind,
// actor IDs) and text fields for the payload / subject prose.
func activityIndexMapping() mapping.IndexMapping {
	idx := bleve.NewIndexMapping()
	doc := bleve.NewDocumentMapping()

	// Keyword (exact-match) fields.
	for _, name := range []string{
		"session_id",
		"action_kind",
		"actor_agent_id",
		"actor_agent_type",
		"actor_pipeline_id",
		"subject_target_agent",
		"subject_target_artifact",
		"subject_domain",
		"confidence",
		"entity_type",
		"claim_id",
		"testament_id",
		"artifact_kind",
		"claim_action_type",
		"validation_type",
		"board_phase",
	} {
		f := bleve.NewKeywordFieldMapping()
		doc.AddFieldMappingsAt(name, f)
	}
	// Text (tokenized) fields.
	for _, name := range []string{
		"subject_path_prefix",
		"payload",
		"claim_title",
		"claim_description",
		"testament_summary",
		"validation_description",
		"quality_bar",
		"artifact_reference",
	} {
		f := bleve.NewTextFieldMapping()
		doc.AddFieldMappingsAt(name, f)
	}
	// Numeric.
	ts := bleve.NewNumericFieldMapping()
	doc.AddFieldMappingsAt("timestamp", ts)

	idx.AddDocumentMapping("activity", doc)
	idx.DefaultMapping = doc
	return idx
}

// enrichClaimsFields extracts structured claims data from the activity
// payload and sets named fields for faceted search. No-op for non-claims
// activities.
func enrichClaimsFields(doc map[string]any, a activity.AgentActivity) {
	entityType := deriveEntityType(a.Action)
	if entityType == "" {
		return
	}
	doc["entity_type"] = entityType

	var payload map[string]any
	if err := json.Unmarshal(a.Payload, &payload); err != nil {
		return
	}
	setIfPresent(doc, payload, "title", "claim_title")
	setIfPresent(doc, payload, "description", "claim_description")
	setIfPresent(doc, payload, "summary", "testament_summary")
	setIfPresent(doc, payload, "action_type", "claim_action_type")
	setIfPresent(doc, payload, "kind", "artifact_kind")
	setIfPresent(doc, payload, "reference", "artifact_reference")
	setIfPresent(doc, payload, "type", "validation_type")
	setIfPresent(doc, payload, "phase", "board_phase")
	setIfPresent(doc, payload, "status", "validation_description")
}

func deriveEntityType(kind activity.ActionKind) string {
	switch kind {
	case activity.ActionClaimIssued, activity.ActionClaimUpdated,
		activity.ActionClaimAccepted, activity.ActionClaimRejected:
		return "claim"
	case activity.ActionTestamentSubmitted:
		return "testament"
	case activity.ActionClaimArtifactPublished:
		return "artifact"
	case activity.ActionClaimValidated:
		return "validation"
	case activity.ActionActionPosted:
		return "action"
	case activity.ActionBoardPhaseChanged, activity.ActionBoardComplete:
		return "board"
	default:
		return ""
	}
}

func setIfPresent(doc, payload map[string]any, srcKey, dstKey string) {
	if v, ok := payload[srcKey]; ok {
		if s, isStr := v.(string); isStr && s != "" {
			doc[dstKey] = s
		}
	}
}

var _ Subscriber = (*BleveSubscriber)(nil)
