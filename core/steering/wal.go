package steering

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
)

// SteeringJournal wraps an AgentJournal for steering-specific WAL operations.
// Uses the same segment-based, CRC-protected format as all other agent WALs.
type SteeringJournal struct {
	journal *agentlog.AgentJournal
}

// OpenSteeringJournal opens or creates a steering WAL journal using the
// standard JournalConfig path resolution (SessionDir/AgentName based).
func OpenSteeringJournal(cfg agentlog.JournalConfig) (*SteeringJournal, error) {
	cfg.WALName = "steering"
	j, err := agentlog.OpenJournal(cfg)
	if err != nil {
		return nil, err
	}
	return &SteeringJournal{journal: j}, nil
}

// OpenSteeringJournalDirect opens a steering journal in an explicit directory.
// Used when the caller manages directory layout (typically the session-scoped
// SylkDir.SessionAgentWALPath).
func OpenSteeringJournalDirect(dir string) (*SteeringJournal, error) {
	j, err := agentlog.OpenJournalDirect(dir, agentlog.JournalConfig{
		WALName: "steering",
	})
	if err != nil {
		return nil, err
	}
	return &SteeringJournal{journal: j}, nil
}

// --- WAL payloads ---

type walBegin struct {
	CorrelationID string    `json:"correlation_id"`
	AgentID       string    `json:"agent_id"`
	SessionID     string    `json:"session_id"`
	Timestamp     time.Time `json:"timestamp"`
}

type walCheckpoint struct {
	CheckpointID  string `json:"checkpoint_id"`
	Turn          int    `json:"turn"`
	MessageCount  int    `json:"message_count"`
	Phase         string `json:"phase"`
	HasAgentState bool   `json:"has_agent_state"`
}

type walCommand struct {
	CommandType  string `json:"command_type"`
	TextLen      int    `json:"text_len"`
	CheckpointID string `json:"checkpoint_id,omitempty"`
}

type walComplete struct {
	CorrelationID    string `json:"correlation_id"`
	FinalTurn        int    `json:"final_turn"`
	TotalCheckpoints int    `json:"total_checkpoints"`
}

type walInterrupted struct {
	CorrelationID    string `json:"correlation_id"`
	LastCheckpointID string `json:"last_checkpoint_id"`
	LastTurn         int    `json:"last_turn"`
	LastPhase        string `json:"last_phase"`
}

// LogBegin writes an OpSteeringBegin bracket entry.
func (s *SteeringJournal) LogBegin(correlationID, agentID, sessionID string) {
	s.appendJSON(agentlog.EventSteeringBegin, walBegin{
		CorrelationID: correlationID,
		AgentID:       agentID,
		SessionID:     sessionID,
		Timestamp:     time.Now(),
	})
}

// LogCheckpoint writes a checkpoint WAL entry.
func (s *SteeringJournal) LogCheckpoint(cp Checkpoint) {
	s.appendJSON(agentlog.EventSteeringCheckpoint, walCheckpoint{
		CheckpointID:  cp.ID,
		Turn:          cp.Turn,
		MessageCount:  cp.MessageCount,
		Phase:         cp.Phase,
		HasAgentState: len(cp.AgentState) > 0,
	})
}

// LogCommand writes a steering command WAL entry.
func (s *SteeringJournal) LogCommand(cmd Command) {
	s.appendJSON(agentlog.EventSteeringCommand, walCommand{
		CommandType:  cmd.Type.String(),
		TextLen:      len(cmd.Text) + len(cmd.NewText),
		CheckpointID: cmd.CheckpointID,
	})
}

// LogComplete writes an OpSteeringComplete bracket-closing entry.
func (s *SteeringJournal) LogComplete(correlationID string, finalTurn, totalCheckpoints int) {
	s.appendJSON(agentlog.EventSteeringComplete, walComplete{
		CorrelationID:    correlationID,
		FinalTurn:        finalTurn,
		TotalCheckpoints: totalCheckpoints,
	})
}

// LogInterrupted writes an OpSteeringInterrupted bracket-closing entry.
func (s *SteeringJournal) LogInterrupted(correlationID, lastCheckpointID string, lastTurn int, lastPhase string) {
	s.appendJSON(agentlog.EventSteeringInterrupted, walInterrupted{
		CorrelationID:    correlationID,
		LastCheckpointID: lastCheckpointID,
		LastTurn:         lastTurn,
		LastPhase:        lastPhase,
	})
}

// IncompleteOperation describes an operation that was started but never
// completed or marked interrupted (indicates crash recovery needed).
type IncompleteOperation struct {
	CorrelationID string
	AgentID       string
	SessionID     string
	BeginSeq      uint64
}

// FindIncompleteOperations scans recent segments for unpaired Begin entries.
func (s *SteeringJournal) FindIncompleteOperations() []IncompleteOperation {
	open := make(map[string]*IncompleteOperation) // correlationID → op

	err := s.journal.Replay(0, func(e agentlog.Entry) error {
		switch e.EventType {
		case agentlog.EventSteeringBegin:
			var b walBegin
			if json.Unmarshal(e.Data, &b) == nil {
				open[b.CorrelationID] = &IncompleteOperation{
					CorrelationID: b.CorrelationID,
					AgentID:       b.AgentID,
					SessionID:     b.SessionID,
					BeginSeq:      e.Seq,
				}
			}
		case agentlog.EventSteeringComplete:
			var c walComplete
			if json.Unmarshal(e.Data, &c) == nil {
				delete(open, c.CorrelationID)
			}
		case agentlog.EventSteeringInterrupted:
			var i walInterrupted
			if json.Unmarshal(e.Data, &i) == nil {
				delete(open, i.CorrelationID)
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("steering: replay error during recovery scan", "err", err)
	}

	result := make([]IncompleteOperation, 0, len(open))
	for _, op := range open {
		result = append(result, *op)
	}
	return result
}

// GC removes old WAL segments.
func (s *SteeringJournal) GC(before time.Time) error {
	return s.journal.GC(before)
}

// Close flushes and closes the journal.
func (s *SteeringJournal) Close() error {
	return s.journal.Close()
}

func (s *SteeringJournal) appendJSON(eventType agentlog.EventType, v any) {
	if _, err := s.journal.AppendJSON(eventType, v); err != nil {
		slog.Warn("steering: WAL append failed", "event", eventType.String(), "err", err)
	}
}
