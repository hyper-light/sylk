package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

type WorkflowStore struct {
	archivalist *Archivalist
}

func NewWorkflowStore(archivalist *Archivalist) *WorkflowStore {
	return &WorkflowStore{archivalist: archivalist}
}

func (w *WorkflowStore) StoreDAG(ctx context.Context, sessionID string, d *dag.DAG) (*StoredDAG, error) {
	payload, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}

	stored := &StoredDAG{
		ID:         d.ID(),
		SessionID:  sessionID,
		Name:       d.Name(),
		Definition: payload,
		CreatedAt:  time.Now(),
	}

	entry := &Entry{
		ID:        fmt.Sprintf("workflow_%s", d.ID()),
		Category:  CategoryTimeline,
		Title:     d.Name(),
		Content:   string(payload),
		Source:    SourceModelArchivalist,
		SessionID: sessionID,
		Metadata: map[string]any{
			"kind":        "workflow_dag",
			"workflow_id": d.ID(),
		},
	}

	if _, err := w.archivalist.store.InsertEntryInSession(sessionID, entry); err != nil {
		return nil, err
	}

	return stored, nil
}

func (w *WorkflowStore) RecordRun(ctx context.Context, run DAGRun) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}

	entry := &Entry{
		ID:        fmt.Sprintf("workflow_run_%s", run.ID),
		Category:  CategoryTimeline,
		Title:     fmt.Sprintf("Workflow %s run", run.DAGID),
		Content:   string(payload),
		Source:    SourceModelArchivalist,
		SessionID: run.SessionID,
		Metadata: map[string]any{
			"kind":        "workflow_run",
			"workflow_id": run.DAGID,
			"run_id":      run.ID,
		},
	}

	_, err = w.archivalist.store.InsertEntryInSession(run.SessionID, entry)
	return err
}

func (w *WorkflowStore) QuerySimilarWorkflows(ctx context.Context, sessionID string, query string, limit int) ([]*Entry, error) {
	if w.archivalist.synthesizer == nil || w.archivalist.retriever == nil {
		return nil, fmt.Errorf("RAG not enabled")
	}

	opts := DefaultRetrievalOptions()
	opts.SessionID = sessionID
	opts.TopK = limit
	results, err := w.archivalist.retriever.Retrieve(ctx, query, opts)
	if err != nil {
		return nil, err
	}

	entries := make([]*Entry, 0, len(results))
	for _, result := range results {
		entries = append(entries, &Entry{
			ID:       result.ID,
			Content:  result.Content,
			Category: Category(result.Category),
			Source:   SourceModelArchivalist,
			Metadata: result.Metadata,
		})
	}

	return entries, nil
}

func (w *WorkflowStore) GetSessionHistory(ctx context.Context, query WorkflowQuery) ([]*Entry, error) {
	archiveQuery := ArchiveQuery{
		SessionIDs: []string{query.SessionID},
		Limit:      query.Limit,
	}
	return w.archivalist.store.Query(archiveQuery)
}
