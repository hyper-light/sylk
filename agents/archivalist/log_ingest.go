package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

const ingestRingCap = 1024

// LogIngestStore receives log entries broadcast by agents via the bus and
// maintains bounded per-agent ring buffers for cross-agent log querying.
type LogIngestStore struct {
	mu    sync.RWMutex
	rings map[string]*ingestRing // keyed by agent name
}

type ingestRing struct {
	entries [ingestRingCap]agentlog.JSONLEntry
	pos     int
	length  int
}

// NewLogIngestStore creates an empty ingest store.
func NewLogIngestStore() *LogIngestStore {
	return &LogIngestStore{
		rings: make(map[string]*ingestRing),
	}
}

// Ingest adds a log entry to the per-agent ring buffer.
func (s *LogIngestStore) Ingest(entry agentlog.JSONLEntry) {
	agent := entry.Agent
	if agent == "" {
		return
	}

	s.mu.Lock()
	r, ok := s.rings[agent]
	if !ok {
		r = &ingestRing{}
		s.rings[agent] = r
	}
	r.entries[r.pos%ingestRingCap] = entry
	r.pos++
	if r.length < ingestRingCap {
		r.length++
	}
	s.mu.Unlock()
}

// Query returns matching entries for a given agent, newest first.
func (s *LogIngestStore) Query(agent, corrID, level string, since time.Time, limit int) []agentlog.JSONLEntry {
	if limit <= 0 {
		limit = 50
	}

	s.mu.RLock()
	r, ok := s.rings[agent]
	if !ok {
		s.mu.RUnlock()
		return nil
	}

	// Snapshot the ring under lock.
	length := r.length
	pos := r.pos
	var snapshot [ingestRingCap]agentlog.JSONLEntry
	copy(snapshot[:], r.entries[:])
	s.mu.RUnlock()

	result := make([]agentlog.JSONLEntry, 0, min(limit, length))
	for i := range length {
		if len(result) >= limit {
			break
		}
		idx := (pos - 1 - i) % ingestRingCap
		if idx < 0 {
			idx += ingestRingCap
		}
		e := snapshot[idx]

		if !since.IsZero() && e.Timestamp.Before(since) {
			continue
		}
		if level != "" && e.Level != level {
			continue
		}
		if corrID != "" && e.CorrID != corrID {
			continue
		}
		result = append(result, e)
	}

	return result
}

// Stats returns per-agent entry counts.
func (s *LogIngestStore) Stats() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]int, len(s.rings))
	for agent, r := range s.rings {
		out[agent] = r.length
	}
	return out
}

// Agents returns the list of agent names with ingested entries.
func (s *LogIngestStore) Agents() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.rings))
	for agent := range s.rings {
		out = append(out, agent)
	}
	return out
}

// queryAgentLogsSkill creates the archivalist skill that allows any agent
// to query cross-agent log entries via the archivalist.
func queryAgentLogsSkill(store *LogIngestStore) *skills.Skill {
	return skills.NewSkill("query_agent_logs").
		Description("Query log entries from any agent via the archivalist's ingest store.").
		Domain("diagnostics").
		Keywords("logs", "agent_logs", "cross_agent", "errors", "diagnostics").
		Priority(90).
		TokenEstimate(400).
		StringParam("agent_name", "Name of the agent whose logs to query", true).
		StringParam("corr_id", "Filter by correlation ID", false).
		EnumParam("level", "Filter by log level", []string{"info", "warn", "error"}, false).
		StringParam("since", "Time window as Go duration (e.g. '5m', '1h')", false).
		IntParam("limit", "Maximum number of results (default 50)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				AgentName string `json:"agent_name"`
				CorrID    string `json:"corr_id"`
				Level     string `json:"level"`
				Since     string `json:"since"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("query_agent_logs: invalid input: %w", err)
			}
			if params.AgentName == "" {
				return nil, fmt.Errorf("query_agent_logs: agent_name is required")
			}

			var since time.Time
			if params.Since != "" {
				if d, err := time.ParseDuration(params.Since); err == nil {
					since = time.Now().Add(-d)
				}
			}

			entries := store.Query(params.AgentName, params.CorrID, params.Level, since, params.Limit)
			summary := agentlog.Summarize(entries)
			return map[string]any{
				"agent":   params.AgentName,
				"entries": entries,
				"summary": summary,
			}, nil
		}).
		Build()
}
