package orchestrator

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
)

// WALEntry is a single orchestrator WAL record.
// Kept as the public type consumed by DAGBridge for recovery.
type WALEntry struct {
	Seq       uint64
	Timestamp time.Time
	DAGID     string
	NodeID    string
	Layer     int
	State     string
	Payload   string
	Error     string
}

// dagWALPayload is the JSON structure stored in each journal entry.
type dagWALPayload struct {
	DAGID   string `json:"dag_id"`
	NodeID  string `json:"node_id,omitempty"`
	Layer   int    `json:"layer,omitempty"`
	State   string `json:"state"`
	Payload string `json:"payload,omitempty"`
	Error   string `json:"err,omitempty"`
}

// OrchestratorJournal provides a segment-based write-ahead log for DAG operations.
// Delegates all storage to agentlog.AgentJournal with JSON payloads.
type OrchestratorJournal struct {
	journal *agentlog.AgentJournal
}

// OpenOrchestratorJournal opens or creates an orchestrator WAL journal.
// The dir is used directly as the journal directory (backward compatible path).
func OpenOrchestratorJournal(dir string) (*OrchestratorJournal, error) {
	j, err := agentlog.OpenJournal(agentlog.JournalConfig{
		WALName:    "dag",
		SegmentCap: 4096,
	})
	if err != nil {
		// Fall back: open with explicit dir by creating the journal dir structure.
		j, err = openJournalInDir(dir)
		if err != nil {
			return nil, fmt.Errorf("orchestrator journal: %w", err)
		}
	}
	return &OrchestratorJournal{journal: j}, nil
}

// OpenOrchestratorJournalSession opens a journal rooted in a session directory.
func OpenOrchestratorJournalSession(sessionDir string) (*OrchestratorJournal, error) {
	j, err := agentlog.OpenJournal(agentlog.JournalConfig{
		SessionDir: sessionDir,
		AgentName:  "orchestrator",
		WALName:    "dag",
		SegmentCap: 4096,
	})
	if err != nil {
		return nil, fmt.Errorf("orchestrator journal: %w", err)
	}
	return &OrchestratorJournal{journal: j}, nil
}

// openJournalInDir creates a journal directly in the specified directory.
// Used for backward compatibility with the existing OrchestratorWALPath.
func openJournalInDir(dir string) (*agentlog.AgentJournal, error) {
	return agentlog.OpenJournalDirect(dir, agentlog.JournalConfig{
		WALName:    "dag",
		SegmentCap: 4096,
	})
}

// --- Public Log Methods ---

// LogDAGStart records the start of a DAG execution.
func (j *OrchestratorJournal) LogDAGStart(dagID, dagJSON string) error {
	_, err := j.journal.AppendJSON(agentlog.EventDAGStarted, &dagWALPayload{
		DAGID:   dagID,
		State:   "running",
		Payload: dagJSON,
	})
	return err
}

// LogDAGComplete records successful or failed DAG completion.
func (j *OrchestratorJournal) LogDAGComplete(dagID, state string) error {
	_, err := j.journal.AppendJSON(agentlog.EventDAGCompleted, &dagWALPayload{
		DAGID: dagID,
		State: state,
	})
	return err
}

// LogDAGAbort records a DAG abort (crash recovery).
func (j *OrchestratorJournal) LogDAGAbort(dagID, errMsg string) error {
	_, err := j.journal.AppendJSON(agentlog.EventDAGAborted, &dagWALPayload{
		DAGID: dagID,
		State: "aborted",
		Error: errMsg,
	})
	return err
}

// LogNodeDispatch records a node being dispatched to a pipeline agent.
func (j *OrchestratorJournal) LogNodeDispatch(dagID, nodeID, agentID string) error {
	_, err := j.journal.AppendJSON(agentlog.EventNodeDispatched, &dagWALPayload{
		DAGID:   dagID,
		NodeID:  nodeID,
		State:   "dispatched",
		Payload: agentID,
	})
	return err
}

// LogNodeResult records a node execution result.
func (j *OrchestratorJournal) LogNodeResult(dagID, nodeID, state, resultJSON string) error {
	_, err := j.journal.AppendJSON(agentlog.EventNodeCompleted, &dagWALPayload{
		DAGID:   dagID,
		NodeID:  nodeID,
		State:   state,
		Payload: resultJSON,
	})
	return err
}

// LogDAGModify records a mid-flight DAG modification.
func (j *OrchestratorJournal) LogDAGModify(dagID string, revision int, diffJSON string) error {
	_, err := j.journal.AppendJSON(agentlog.EventDAGModified, &dagWALPayload{
		DAGID:   dagID,
		Layer:   revision,
		State:   "modified",
		Payload: diffJSON,
	})
	return err
}

// LogDAGCancel records a DAG cancellation.
func (j *OrchestratorJournal) LogDAGCancel(dagID, reason string) error {
	_, err := j.journal.AppendJSON(agentlog.EventDAGCancelled, &dagWALPayload{
		DAGID:   dagID,
		State:   "cancelled",
		Payload: reason,
	})
	return err
}

// FindIncompleteDAGs scans the most recent segments for unpaired DAGStart
// entries (no matching Complete/Abort/Cancel).
func (j *OrchestratorJournal) FindIncompleteDAGs() ([]WALEntry, error) {
	begins := make(map[string]*WALEntry)
	resolved := make(map[string]struct{})

	err := j.journal.Replay(0, func(e agentlog.Entry) error {
		if !e.EventType.IsOrchestrator() {
			return nil
		}

		var p dagWALPayload
		if err := json.Unmarshal(e.Data, &p); err != nil {
			return nil // skip malformed
		}

		switch e.EventType {
		case agentlog.EventDAGStarted:
			if _, ok := resolved[p.DAGID]; !ok {
				begins[p.DAGID] = &WALEntry{
					Seq:       e.Seq,
					Timestamp: e.Timestamp,
					DAGID:     p.DAGID,
					State:     p.State,
					Payload:   p.Payload,
				}
			}
		case agentlog.EventDAGCompleted, agentlog.EventDAGAborted, agentlog.EventDAGCancelled:
			delete(begins, p.DAGID)
			resolved[p.DAGID] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("orchestrator journal: replay: %w", err)
	}

	result := make([]WALEntry, 0, len(begins))
	for _, e := range begins {
		result = append(result, *e)
	}
	return result, nil
}

// GC removes segments whose entries are all older than the given time.
func (j *OrchestratorJournal) GC(before time.Time) error {
	return j.journal.GC(before)
}

// Close syncs and closes the journal.
func (j *OrchestratorJournal) Close() error {
	return j.journal.Close()
}
