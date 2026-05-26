package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

// archivalistStoreCommentaryAction is the typed direct-communication
// action a scribe replica fires to persist its commentary on the
// archivalist. The scribe publishes a guide.ActionRequest with this
// Action name; the guide's forwardActionMessage republishes to the
// archivalist's request channel; the archivalist's handleActionMessage
// dispatches to handleStoreScribeCommentaryAction below.
//
// Why ActionRequest over RouteRequest: RouteRequest goes through the
// guide's full classification pipeline (LLM call) and posts a route
// claim that opens a handoff cycle in the activity panel. Scribe
// commentary is high-volume, deterministic system work — it has no
// peer to wait on, no validation to track, and no classification
// ambiguity. Routing it through ActionRequest is the existing direct
// agent-to-agent protocol (see academic.publishActionRequest at
// agents/academic/research_paper.go:839 for the canonical pattern).
const archivalistStoreCommentaryAction = "store_scribe_commentary"

// storeScribeCommentaryParams is the payload shape the scribe sends.
// Mirrors the metadata previously attached to the RouteRequest path so
// every traceability field is preserved on the persisted Entry.
type storeScribeCommentaryParams struct {
	Content           string         `json:"content"`
	SessionID         string         `json:"session_id,omitempty"`
	EntryID           string         `json:"entry_id,omitempty"`
	ParentAgent       string         `json:"parent_agent,omitempty"`
	ScribeID          string         `json:"scribe_id,omitempty"`
	ReplicaGeneration int            `json:"replica_generation,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

func (a *Archivalist) handleStoreScribeCommentaryAction(req *guide.ActionRequest) error {
	if req == nil {
		return nil
	}
	params, err := decodeStoreScribeCommentaryParams(req.Data)
	if err != nil {
		return a.publishActionFailure(req, err)
	}
	content := strings.TrimSpace(params.Content)
	if content == "" {
		return a.publishActionFailure(req, fmt.Errorf("scribe commentary content is empty"))
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		sessionID = a.GetDefaultSession()
	}
	metadata := map[string]any{
		"source_type": "scribe",
	}
	for k, v := range params.Metadata {
		metadata[k] = v
	}
	if params.ParentAgent != "" {
		metadata["parent_agent"] = params.ParentAgent
	}
	if params.ScribeID != "" {
		metadata["scribe_id"] = params.ScribeID
	}
	if params.ReplicaGeneration > 0 {
		metadata["replica_generation"] = params.ReplicaGeneration
	}
	if params.EntryID != "" {
		metadata["scribe_entry_id"] = params.EntryID
	}
	entry := &Entry{
		ID:        params.EntryID,
		Category:  CategoryTimeline,
		Content:   content,
		Source:    SourceModelArchivalist,
		SessionID: sessionID,
		CreatedAt: time.Now(),
		Metadata:  metadata,
	}
	result := a.StoreEntry(context.Background(), entry)
	if !result.Success {
		return a.publishActionFailure(req, result.Error)
	}
	if req.FireAndForget {
		return nil
	}
	return a.publishActionSuccess(req, map[string]any{
		"stored":   true,
		"entry_id": result.ID,
	})
}

func decodeStoreScribeCommentaryParams(data any) (*storeScribeCommentaryParams, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode scribe commentary payload: %w", err)
	}
	var params storeScribeCommentaryParams
	if err := json.Unmarshal(encoded, &params); err != nil {
		return nil, fmt.Errorf("decode scribe commentary payload: %w", err)
	}
	return &params, nil
}
