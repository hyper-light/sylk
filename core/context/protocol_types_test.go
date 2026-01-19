package context

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ContextPriority Tests
// =============================================================================

func TestContextPriority_String(t *testing.T) {
	tests := []struct {
		name     string
		priority ContextPriority
		want     string
	}{
		{
			name:     "low priority",
			priority: ContextPriorityLow,
			want:     "low",
		},
		{
			name:     "normal priority",
			priority: ContextPriorityNormal,
			want:     "normal",
		},
		{
			name:     "high priority",
			priority: ContextPriorityHigh,
			want:     "high",
		},
		{
			name:     "critical priority",
			priority: ContextPriorityCritical,
			want:     "critical",
		},
		{
			name:     "invalid priority (negative)",
			priority: ContextPriority(-1),
			want:     "unknown",
		},
		{
			name:     "invalid priority (out of range)",
			priority: ContextPriority(999),
			want:     "unknown",
		},
		{
			name:     "invalid priority (between values)",
			priority: ContextPriority(25),
			want:     "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.priority.String()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestContextPriority_Values(t *testing.T) {
	// Verify the actual integer values
	assert.Equal(t, ContextPriority(0), ContextPriorityLow)
	assert.Equal(t, ContextPriority(50), ContextPriorityNormal)
	assert.Equal(t, ContextPriority(75), ContextPriorityHigh)
	assert.Equal(t, ContextPriority(100), ContextPriorityCritical)
}

// =============================================================================
// ContextShareRequest Tests
// =============================================================================

func TestContextShareRequest_HappyPath(t *testing.T) {
	deadline := time.Now().Add(5 * time.Minute)
	req := ContextShareRequest{
		RequestID:    "req-123",
		FromAgent:    "agent-a",
		ToAgent:      "agent-b",
		SessionID:    "session-456",
		Query:        "find relevant code",
		MaxTokens:    1000,
		Priority:     ContextPriorityHigh,
		ContentTypes: []ContentType{ContentTypeCodeFile, ContentTypeAgentResponse},
		Deadline:     deadline,
	}

	assert.Equal(t, "req-123", req.RequestID)
	assert.Equal(t, "agent-a", req.FromAgent)
	assert.Equal(t, "agent-b", req.ToAgent)
	assert.Equal(t, "session-456", req.SessionID)
	assert.Equal(t, "find relevant code", req.Query)
	assert.Equal(t, 1000, req.MaxTokens)
	assert.Equal(t, ContextPriorityHigh, req.Priority)
	assert.Len(t, req.ContentTypes, 2)
	assert.Equal(t, deadline, req.Deadline)
}

func TestContextShareRequest_JSONRoundtrip(t *testing.T) {
	deadline := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	original := ContextShareRequest{
		RequestID:    "req-roundtrip",
		FromAgent:    "agent-source",
		ToAgent:      "agent-target",
		SessionID:    "session-rt",
		Query:        "test query with special chars: <>&\"'",
		MaxTokens:    2000,
		Priority:     ContextPriorityCritical,
		ContentTypes: []ContentType{ContentTypeUserPrompt, ContentTypeToolCall},
		Deadline:     deadline,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ContextShareRequest
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.RequestID, decoded.RequestID)
	assert.Equal(t, original.FromAgent, decoded.FromAgent)
	assert.Equal(t, original.ToAgent, decoded.ToAgent)
	assert.Equal(t, original.SessionID, decoded.SessionID)
	assert.Equal(t, original.Query, decoded.Query)
	assert.Equal(t, original.MaxTokens, decoded.MaxTokens)
	assert.Equal(t, original.Priority, decoded.Priority)
	assert.Equal(t, original.ContentTypes, decoded.ContentTypes)
	assert.True(t, original.Deadline.Equal(decoded.Deadline))
}

func TestContextShareRequest_EdgeCases(t *testing.T) {
	t.Run("empty strings", func(t *testing.T) {
		req := ContextShareRequest{
			RequestID: "",
			FromAgent: "",
			ToAgent:   "",
			SessionID: "",
			Query:     "",
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		var decoded ContextShareRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.RequestID)
		assert.Empty(t, decoded.FromAgent)
		assert.Empty(t, decoded.Query)
	})

	t.Run("nil content types", func(t *testing.T) {
		req := ContextShareRequest{
			RequestID:    "req-nil",
			ContentTypes: nil,
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		// Content types should be omitted when nil
		assert.NotContains(t, string(data), "content_types")
	})

	t.Run("empty content types slice", func(t *testing.T) {
		req := ContextShareRequest{
			RequestID:    "req-empty",
			ContentTypes: []ContentType{},
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		var decoded ContextShareRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		// Empty slice may serialize as null or [] depending on Go version
		assert.Empty(t, decoded.ContentTypes)
	})

	t.Run("zero time", func(t *testing.T) {
		req := ContextShareRequest{
			RequestID: "req-zero-time",
			Deadline:  time.Time{},
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		var decoded ContextShareRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.True(t, decoded.Deadline.IsZero())
	})

	t.Run("zero max tokens", func(t *testing.T) {
		req := ContextShareRequest{
			RequestID: "req-zero-tokens",
			MaxTokens: 0,
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		var decoded ContextShareRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Zero(t, decoded.MaxTokens)
	})
}

// =============================================================================
// ContextShareResponse Tests
// =============================================================================

func TestContextShareResponse_HappyPath(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "entry-1", Content: "content 1"},
		{ID: "entry-2", Content: "content 2"},
	}

	resp := ContextShareResponse{
		RequestID:   "req-123",
		Entries:     entries,
		TotalTokens: 500,
		Truncated:   true,
		Duration:    100 * time.Millisecond,
		Error:       "",
	}

	assert.Equal(t, "req-123", resp.RequestID)
	assert.Len(t, resp.Entries, 2)
	assert.Equal(t, 500, resp.TotalTokens)
	assert.True(t, resp.Truncated)
	assert.Equal(t, 100*time.Millisecond, resp.Duration)
	assert.Empty(t, resp.Error)
}

func TestContextShareResponse_JSONRoundtrip(t *testing.T) {
	original := ContextShareResponse{
		RequestID: "req-rt",
		Entries: []*ContentEntry{
			{
				ID:          "entry-test",
				SessionID:   "session-test",
				AgentID:     "agent-test",
				ContentType: ContentTypeCodeFile,
				Content:     "package main\n\nfunc main() {}",
				TokenCount:  10,
			},
		},
		TotalTokens: 10,
		Truncated:   false,
		Duration:    50 * time.Millisecond,
		Error:       "",
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ContextShareResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.RequestID, decoded.RequestID)
	require.Len(t, decoded.Entries, 1)
	assert.Equal(t, original.Entries[0].ID, decoded.Entries[0].ID)
	assert.Equal(t, original.TotalTokens, decoded.TotalTokens)
	assert.Equal(t, original.Truncated, decoded.Truncated)
	assert.Equal(t, original.Duration, decoded.Duration)
}

func TestContextShareResponse_WithError(t *testing.T) {
	resp := ContextShareResponse{
		RequestID: "req-error",
		Entries:   nil,
		Error:     "context retrieval failed: timeout",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded ContextShareResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "context retrieval failed: timeout", decoded.Error)
	assert.Nil(t, decoded.Entries)
}

func TestContextShareResponse_EdgeCases(t *testing.T) {
	t.Run("nil entries", func(t *testing.T) {
		resp := ContextShareResponse{
			RequestID: "req-nil-entries",
			Entries:   nil,
		}

		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var decoded ContextShareResponse
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.Entries)
	})

	t.Run("zero duration", func(t *testing.T) {
		resp := ContextShareResponse{
			RequestID: "req-zero-dur",
			Duration:  0,
		}

		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var decoded ContextShareResponse
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Zero(t, decoded.Duration)
	})
}

// =============================================================================
// ConsultationRequest Tests
// =============================================================================

func TestConsultationRequest_HappyPath(t *testing.T) {
	req := ConsultationRequest{
		RequestID: "consult-123",
		FromAgent: "agent-questioner",
		ToAgent:   "agent-expert",
		SessionID: "session-789",
		Query:     "How should I implement caching?",
		MaxTokens: 500,
		Priority:  ContextPriorityNormal,
		Timeout:   30 * time.Second,
	}

	assert.Equal(t, "consult-123", req.RequestID)
	assert.Equal(t, "agent-questioner", req.FromAgent)
	assert.Equal(t, "agent-expert", req.ToAgent)
	assert.Equal(t, "session-789", req.SessionID)
	assert.Equal(t, "How should I implement caching?", req.Query)
	assert.Equal(t, 500, req.MaxTokens)
	assert.Equal(t, ContextPriorityNormal, req.Priority)
	assert.Equal(t, 30*time.Second, req.Timeout)
}

func TestConsultationRequest_JSONRoundtrip(t *testing.T) {
	original := ConsultationRequest{
		RequestID: "consult-rt",
		FromAgent: "from-agent",
		ToAgent:   "to-agent",
		SessionID: "session-rt",
		Query:     "complex query with unicode: \u00e4\u00f6\u00fc\u00df",
		MaxTokens: 1000,
		Priority:  ContextPriorityLow,
		Timeout:   1 * time.Minute,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ConsultationRequest
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.RequestID, decoded.RequestID)
	assert.Equal(t, original.FromAgent, decoded.FromAgent)
	assert.Equal(t, original.ToAgent, decoded.ToAgent)
	assert.Equal(t, original.SessionID, decoded.SessionID)
	assert.Equal(t, original.Query, decoded.Query)
	assert.Equal(t, original.MaxTokens, decoded.MaxTokens)
	assert.Equal(t, original.Priority, decoded.Priority)
	assert.Equal(t, original.Timeout, decoded.Timeout)
}

func TestConsultationRequest_EdgeCases(t *testing.T) {
	t.Run("zero timeout", func(t *testing.T) {
		req := ConsultationRequest{
			RequestID: "consult-zero-timeout",
			Timeout:   0,
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		var decoded ConsultationRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Zero(t, decoded.Timeout)
	})

	t.Run("empty query", func(t *testing.T) {
		req := ConsultationRequest{
			RequestID: "consult-empty-query",
			Query:     "",
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		var decoded ConsultationRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.Query)
	})
}

// =============================================================================
// ConsultationResponse Tests
// =============================================================================

func TestConsultationResponse_HappyPath(t *testing.T) {
	resp := ConsultationResponse{
		RequestID:  "consult-123",
		Answer:     "You should use an LRU cache with TTL.",
		Sources:    []string{"doc-1", "doc-2", "doc-3"},
		Confidence: 0.85,
		Duration:   150 * time.Millisecond,
		Error:      "",
	}

	assert.Equal(t, "consult-123", resp.RequestID)
	assert.Equal(t, "You should use an LRU cache with TTL.", resp.Answer)
	assert.Len(t, resp.Sources, 3)
	assert.Equal(t, 0.85, resp.Confidence)
	assert.Equal(t, 150*time.Millisecond, resp.Duration)
	assert.Empty(t, resp.Error)
}

func TestConsultationResponse_JSONRoundtrip(t *testing.T) {
	original := ConsultationResponse{
		RequestID:  "consult-rt",
		Answer:     "Detailed answer here",
		Sources:    []string{"source-a", "source-b"},
		Confidence: 0.92,
		Duration:   200 * time.Millisecond,
		Error:      "",
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ConsultationResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.RequestID, decoded.RequestID)
	assert.Equal(t, original.Answer, decoded.Answer)
	assert.Equal(t, original.Sources, decoded.Sources)
	assert.Equal(t, original.Confidence, decoded.Confidence)
	assert.Equal(t, original.Duration, decoded.Duration)
}

func TestConsultationResponse_EdgeCases(t *testing.T) {
	t.Run("nil sources", func(t *testing.T) {
		resp := ConsultationResponse{
			RequestID: "consult-nil-sources",
			Sources:   nil,
		}

		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var decoded ConsultationResponse
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.Sources)
	})

	t.Run("zero confidence", func(t *testing.T) {
		resp := ConsultationResponse{
			RequestID:  "consult-zero-conf",
			Confidence: 0.0,
		}

		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var decoded ConsultationResponse
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Zero(t, decoded.Confidence)
	})

	t.Run("with error", func(t *testing.T) {
		resp := ConsultationResponse{
			RequestID: "consult-error",
			Error:     "agent unavailable",
		}

		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var decoded ConsultationResponse
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "agent unavailable", decoded.Error)
	})
}

// =============================================================================
// PrefetchSharePayload Tests
// =============================================================================

func TestPrefetchSharePayload_HappyPath(t *testing.T) {
	timestamp := time.Now()
	payload := PrefetchSharePayload{
		SessionID: "session-prefetch",
		Query:     "find authentication code",
		Excerpts: []Excerpt{
			{
				ID:         "excerpt-1",
				Content:    "func Authenticate() {}",
				Source:     "/path/to/auth.go",
				Confidence: 0.9,
				TokenCount: 5,
				LineRange:  [2]int{10, 15},
				Relevance:  0.95,
			},
		},
		Summaries: []Summary{
			{
				ID:         "summary-1",
				Text:       "JWT validation in middleware",
				Source:     "/path/to/middleware.go",
				Confidence: 0.75,
				TokenCount: 100,
			},
		},
		TokensUsed: 105,
		TierSource: TierHotCache,
		Timestamp:  timestamp,
	}

	assert.Equal(t, "session-prefetch", payload.SessionID)
	assert.Len(t, payload.Excerpts, 1)
	assert.Len(t, payload.Summaries, 1)
	assert.Equal(t, 105, payload.TokensUsed)
	assert.Equal(t, TierHotCache, payload.TierSource)
	assert.Equal(t, timestamp, payload.Timestamp)
}

func TestPrefetchSharePayload_JSONRoundtrip(t *testing.T) {
	timestamp := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	original := PrefetchSharePayload{
		SessionID: "session-rt",
		Query:     "test query",
		Excerpts: []Excerpt{
			{
				ID:         "exc-1",
				Content:    "test content",
				Source:     "test.go",
				Confidence: 0.8,
				TokenCount: 10,
				LineRange:  [2]int{1, 5},
				Relevance:  0.85,
			},
		},
		Summaries: []Summary{
			{
				ID:         "sum-1",
				Text:       "summary text",
				Source:     "test.go",
				Confidence: 0.7,
				TokenCount: 50,
			},
		},
		TokensUsed: 60,
		TierSource: TierWarmIndex,
		Timestamp:  timestamp,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded PrefetchSharePayload
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.SessionID, decoded.SessionID)
	assert.Equal(t, original.Query, decoded.Query)
	require.Len(t, decoded.Excerpts, 1)
	assert.Equal(t, original.Excerpts[0].ID, decoded.Excerpts[0].ID)
	assert.Equal(t, original.Excerpts[0].LineRange, decoded.Excerpts[0].LineRange)
	require.Len(t, decoded.Summaries, 1)
	assert.Equal(t, original.Summaries[0].ID, decoded.Summaries[0].ID)
	assert.Equal(t, original.TokensUsed, decoded.TokensUsed)
	assert.Equal(t, original.TierSource, decoded.TierSource)
	assert.True(t, original.Timestamp.Equal(decoded.Timestamp))
}

func TestPrefetchSharePayload_EdgeCases(t *testing.T) {
	t.Run("empty excerpts and summaries", func(t *testing.T) {
		payload := PrefetchSharePayload{
			SessionID:  "session-empty",
			Excerpts:   []Excerpt{},
			Summaries:  []Summary{},
			TokensUsed: 0,
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded PrefetchSharePayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.Excerpts)
		assert.Empty(t, decoded.Summaries)
	})

	t.Run("nil excerpts and summaries", func(t *testing.T) {
		payload := PrefetchSharePayload{
			SessionID: "session-nil",
			Excerpts:  nil,
			Summaries: nil,
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded PrefetchSharePayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.Excerpts)
		assert.Nil(t, decoded.Summaries)
	})

	t.Run("zero time", func(t *testing.T) {
		payload := PrefetchSharePayload{
			SessionID: "session-zero-time",
			Timestamp: time.Time{},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded PrefetchSharePayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.True(t, decoded.Timestamp.IsZero())
	})
}

// =============================================================================
// ConversationTurn Tests
// =============================================================================

func TestConversationTurn_HappyPath(t *testing.T) {
	timestamp := time.Now()
	turn := ConversationTurn{
		TurnNumber: 5,
		Role:       "assistant",
		Content:    "Here is my response...",
		TokenCount: 25,
		Timestamp:  timestamp,
	}

	assert.Equal(t, 5, turn.TurnNumber)
	assert.Equal(t, "assistant", turn.Role)
	assert.Equal(t, "Here is my response...", turn.Content)
	assert.Equal(t, 25, turn.TokenCount)
	assert.Equal(t, timestamp, turn.Timestamp)
}

func TestConversationTurn_JSONRoundtrip(t *testing.T) {
	timestamp := time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC)
	original := ConversationTurn{
		TurnNumber: 3,
		Role:       "user",
		Content:    "What is the meaning of life?",
		TokenCount: 8,
		Timestamp:  timestamp,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded ConversationTurn
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.TurnNumber, decoded.TurnNumber)
	assert.Equal(t, original.Role, decoded.Role)
	assert.Equal(t, original.Content, decoded.Content)
	assert.Equal(t, original.TokenCount, decoded.TokenCount)
	assert.True(t, original.Timestamp.Equal(decoded.Timestamp))
}

func TestConversationTurn_EdgeCases(t *testing.T) {
	t.Run("zero turn number", func(t *testing.T) {
		turn := ConversationTurn{
			TurnNumber: 0,
			Role:       "system",
			Content:    "System message",
		}

		data, err := json.Marshal(turn)
		require.NoError(t, err)

		var decoded ConversationTurn
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Zero(t, decoded.TurnNumber)
	})

	t.Run("empty content", func(t *testing.T) {
		turn := ConversationTurn{
			TurnNumber: 1,
			Role:       "user",
			Content:    "",
		}

		data, err := json.Marshal(turn)
		require.NoError(t, err)

		var decoded ConversationTurn
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.Content)
	})
}

// =============================================================================
// FileState Tests
// =============================================================================

func TestFileState_HappyPath(t *testing.T) {
	lastMod := time.Now()
	fs := FileState{
		Path:         "/path/to/file.go",
		LastModified: lastMod,
		TokenCount:   500,
		Relevance:    0.75,
	}

	assert.Equal(t, "/path/to/file.go", fs.Path)
	assert.Equal(t, lastMod, fs.LastModified)
	assert.Equal(t, 500, fs.TokenCount)
	assert.Equal(t, 0.75, fs.Relevance)
}

func TestFileState_JSONRoundtrip(t *testing.T) {
	lastMod := time.Date(2025, 1, 14, 10, 0, 0, 0, time.UTC)
	original := FileState{
		Path:         "/code/main.go",
		LastModified: lastMod,
		TokenCount:   1000,
		Relevance:    0.9,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded FileState
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Path, decoded.Path)
	assert.True(t, original.LastModified.Equal(decoded.LastModified))
	assert.Equal(t, original.TokenCount, decoded.TokenCount)
	assert.Equal(t, original.Relevance, decoded.Relevance)
}

func TestFileState_EdgeCases(t *testing.T) {
	t.Run("empty path", func(t *testing.T) {
		fs := FileState{Path: ""}

		data, err := json.Marshal(fs)
		require.NoError(t, err)

		var decoded FileState
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.Path)
	})

	t.Run("zero relevance", func(t *testing.T) {
		fs := FileState{
			Path:      "/test.go",
			Relevance: 0.0,
		}

		data, err := json.Marshal(fs)
		require.NoError(t, err)

		var decoded FileState
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Zero(t, decoded.Relevance)
	})
}

// =============================================================================
// TaskState Tests
// =============================================================================

func TestTaskState_HappyPath(t *testing.T) {
	ts := TaskState{
		TaskID:       "task-456",
		Status:       "in_progress",
		Progress:     65.5,
		CurrentStep:  "Running tests",
		Dependencies: []string{"task-123", "task-234"},
	}

	assert.Equal(t, "task-456", ts.TaskID)
	assert.Equal(t, "in_progress", ts.Status)
	assert.Equal(t, 65.5, ts.Progress)
	assert.Equal(t, "Running tests", ts.CurrentStep)
	assert.Len(t, ts.Dependencies, 2)
}

func TestTaskState_JSONRoundtrip(t *testing.T) {
	original := TaskState{
		TaskID:       "task-rt",
		Status:       "completed",
		Progress:     100.0,
		CurrentStep:  "Done",
		Dependencies: []string{"dep-1", "dep-2", "dep-3"},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded TaskState
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.TaskID, decoded.TaskID)
	assert.Equal(t, original.Status, decoded.Status)
	assert.Equal(t, original.Progress, decoded.Progress)
	assert.Equal(t, original.CurrentStep, decoded.CurrentStep)
	assert.Equal(t, original.Dependencies, decoded.Dependencies)
}

func TestTaskState_EdgeCases(t *testing.T) {
	t.Run("nil dependencies", func(t *testing.T) {
		ts := TaskState{
			TaskID:       "task-nil-deps",
			Dependencies: nil,
		}

		data, err := json.Marshal(ts)
		require.NoError(t, err)

		var decoded TaskState
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.Dependencies)
	})

	t.Run("zero progress", func(t *testing.T) {
		ts := TaskState{
			TaskID:   "task-zero-progress",
			Progress: 0.0,
		}

		data, err := json.Marshal(ts)
		require.NoError(t, err)

		var decoded TaskState
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Zero(t, decoded.Progress)
	})

	t.Run("empty dependencies slice", func(t *testing.T) {
		ts := TaskState{
			TaskID:       "task-empty-deps",
			Dependencies: []string{},
		}

		data, err := json.Marshal(ts)
		require.NoError(t, err)

		var decoded TaskState
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.Dependencies)
	})
}

// =============================================================================
// Decision Tests
// =============================================================================

func TestDecision_HappyPath(t *testing.T) {
	timestamp := time.Now()
	d := Decision{
		ID:          "decision-789",
		Description: "Use microservices architecture",
		Rationale:   "Better scalability and team independence",
		Timestamp:   timestamp,
		AgentID:     "architect-agent",
	}

	assert.Equal(t, "decision-789", d.ID)
	assert.Equal(t, "Use microservices architecture", d.Description)
	assert.Equal(t, "Better scalability and team independence", d.Rationale)
	assert.Equal(t, timestamp, d.Timestamp)
	assert.Equal(t, "architect-agent", d.AgentID)
}

func TestDecision_JSONRoundtrip(t *testing.T) {
	timestamp := time.Date(2025, 1, 15, 16, 0, 0, 0, time.UTC)
	original := Decision{
		ID:          "decision-rt",
		Description: "Adopt Go for backend",
		Rationale:   "Strong concurrency support",
		Timestamp:   timestamp,
		AgentID:     "decision-maker",
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Decision
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.Description, decoded.Description)
	assert.Equal(t, original.Rationale, decoded.Rationale)
	assert.True(t, original.Timestamp.Equal(decoded.Timestamp))
	assert.Equal(t, original.AgentID, decoded.AgentID)
}

func TestDecision_EdgeCases(t *testing.T) {
	t.Run("empty strings", func(t *testing.T) {
		d := Decision{
			ID:          "",
			Description: "",
			Rationale:   "",
			AgentID:     "",
		}

		data, err := json.Marshal(d)
		require.NoError(t, err)

		var decoded Decision
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.ID)
		assert.Empty(t, decoded.Description)
		assert.Empty(t, decoded.Rationale)
		assert.Empty(t, decoded.AgentID)
	})
}

// =============================================================================
// HandoffContextPayload Tests
// =============================================================================

func TestHandoffContextPayload_HappyPath(t *testing.T) {
	timestamp := time.Now()
	payload := HandoffContextPayload{
		FromAgentID:         "agent-source",
		ToAgentID:           "agent-target",
		AgentType:           "developer",
		SessionID:           "session-handoff",
		PreparedContextSize: 5000,
		Summary:             "Completed initial feature implementation",
		RecentTurns: []ConversationTurn{
			{TurnNumber: 1, Role: "user", Content: "Implement feature X"},
			{TurnNumber: 2, Role: "assistant", Content: "Working on feature X..."},
		},
		ActiveFiles: map[string]FileState{
			"/main.go": {Path: "/main.go", TokenCount: 100, Relevance: 0.9},
		},
		TaskState: &TaskState{
			TaskID:   "task-main",
			Status:   "in_progress",
			Progress: 50.0,
		},
		KeyDecisions: []Decision{
			{ID: "dec-1", Description: "Use REST API"},
		},
		Timestamp: timestamp,
	}

	assert.Equal(t, "agent-source", payload.FromAgentID)
	assert.Equal(t, "agent-target", payload.ToAgentID)
	assert.Equal(t, "developer", payload.AgentType)
	assert.Equal(t, "session-handoff", payload.SessionID)
	assert.Equal(t, 5000, payload.PreparedContextSize)
	assert.Len(t, payload.RecentTurns, 2)
	assert.Len(t, payload.ActiveFiles, 1)
	assert.NotNil(t, payload.TaskState)
	assert.Len(t, payload.KeyDecisions, 1)
	assert.Equal(t, timestamp, payload.Timestamp)
}

func TestHandoffContextPayload_JSONRoundtrip(t *testing.T) {
	timestamp := time.Date(2025, 1, 15, 18, 0, 0, 0, time.UTC)
	turnTime := time.Date(2025, 1, 15, 17, 30, 0, 0, time.UTC)
	fileTime := time.Date(2025, 1, 15, 17, 0, 0, 0, time.UTC)
	decisionTime := time.Date(2025, 1, 15, 17, 15, 0, 0, time.UTC)

	original := HandoffContextPayload{
		FromAgentID:         "from-agent",
		ToAgentID:           "to-agent",
		AgentType:           "reviewer",
		SessionID:           "session-rt",
		PreparedContextSize: 3000,
		Summary:             "Review summary",
		RecentTurns: []ConversationTurn{
			{TurnNumber: 1, Role: "user", Content: "Review this", TokenCount: 3, Timestamp: turnTime},
		},
		ActiveFiles: map[string]FileState{
			"/code.go": {Path: "/code.go", LastModified: fileTime, TokenCount: 200, Relevance: 0.8},
		},
		TaskState: &TaskState{
			TaskID:       "task-review",
			Status:       "pending",
			Progress:     0.0,
			CurrentStep:  "Not started",
			Dependencies: []string{"task-pre"},
		},
		KeyDecisions: []Decision{
			{ID: "dec-review", Description: "Focus on security", Rationale: "Critical system", Timestamp: decisionTime, AgentID: "reviewer"},
		},
		Timestamp: timestamp,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded HandoffContextPayload
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.FromAgentID, decoded.FromAgentID)
	assert.Equal(t, original.ToAgentID, decoded.ToAgentID)
	assert.Equal(t, original.AgentType, decoded.AgentType)
	assert.Equal(t, original.SessionID, decoded.SessionID)
	assert.Equal(t, original.PreparedContextSize, decoded.PreparedContextSize)
	assert.Equal(t, original.Summary, decoded.Summary)

	require.Len(t, decoded.RecentTurns, 1)
	assert.Equal(t, original.RecentTurns[0].TurnNumber, decoded.RecentTurns[0].TurnNumber)
	assert.Equal(t, original.RecentTurns[0].Role, decoded.RecentTurns[0].Role)
	assert.Equal(t, original.RecentTurns[0].Content, decoded.RecentTurns[0].Content)
	assert.True(t, original.RecentTurns[0].Timestamp.Equal(decoded.RecentTurns[0].Timestamp))

	require.Len(t, decoded.ActiveFiles, 1)
	decodedFile := decoded.ActiveFiles["/code.go"]
	assert.Equal(t, original.ActiveFiles["/code.go"].Path, decodedFile.Path)
	assert.True(t, original.ActiveFiles["/code.go"].LastModified.Equal(decodedFile.LastModified))
	assert.Equal(t, original.ActiveFiles["/code.go"].TokenCount, decodedFile.TokenCount)
	assert.Equal(t, original.ActiveFiles["/code.go"].Relevance, decodedFile.Relevance)

	require.NotNil(t, decoded.TaskState)
	assert.Equal(t, original.TaskState.TaskID, decoded.TaskState.TaskID)
	assert.Equal(t, original.TaskState.Status, decoded.TaskState.Status)
	assert.Equal(t, original.TaskState.Progress, decoded.TaskState.Progress)
	assert.Equal(t, original.TaskState.CurrentStep, decoded.TaskState.CurrentStep)
	assert.Equal(t, original.TaskState.Dependencies, decoded.TaskState.Dependencies)

	require.Len(t, decoded.KeyDecisions, 1)
	assert.Equal(t, original.KeyDecisions[0].ID, decoded.KeyDecisions[0].ID)
	assert.Equal(t, original.KeyDecisions[0].Description, decoded.KeyDecisions[0].Description)
	assert.Equal(t, original.KeyDecisions[0].Rationale, decoded.KeyDecisions[0].Rationale)
	assert.True(t, original.KeyDecisions[0].Timestamp.Equal(decoded.KeyDecisions[0].Timestamp))
	assert.Equal(t, original.KeyDecisions[0].AgentID, decoded.KeyDecisions[0].AgentID)

	assert.True(t, original.Timestamp.Equal(decoded.Timestamp))
}

func TestHandoffContextPayload_EdgeCases(t *testing.T) {
	t.Run("nil task state", func(t *testing.T) {
		payload := HandoffContextPayload{
			FromAgentID: "agent-1",
			ToAgentID:   "agent-2",
			TaskState:   nil,
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		// TaskState should be omitted when nil
		assert.NotContains(t, string(data), "task_state")

		var decoded HandoffContextPayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.TaskState)
	})

	t.Run("empty recent turns", func(t *testing.T) {
		payload := HandoffContextPayload{
			FromAgentID: "agent-1",
			RecentTurns: []ConversationTurn{},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded HandoffContextPayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.RecentTurns)
	})

	t.Run("nil recent turns", func(t *testing.T) {
		payload := HandoffContextPayload{
			FromAgentID: "agent-1",
			RecentTurns: nil,
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded HandoffContextPayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.RecentTurns)
	})

	t.Run("empty active files", func(t *testing.T) {
		payload := HandoffContextPayload{
			FromAgentID: "agent-1",
			ActiveFiles: map[string]FileState{},
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded HandoffContextPayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.ActiveFiles)
	})

	t.Run("nil active files", func(t *testing.T) {
		payload := HandoffContextPayload{
			FromAgentID: "agent-1",
			ActiveFiles: nil,
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded HandoffContextPayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.ActiveFiles)
	})

	t.Run("nil key decisions", func(t *testing.T) {
		payload := HandoffContextPayload{
			FromAgentID:  "agent-1",
			KeyDecisions: nil,
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded HandoffContextPayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Nil(t, decoded.KeyDecisions)
	})

	t.Run("zero prepared context size", func(t *testing.T) {
		payload := HandoffContextPayload{
			FromAgentID:         "agent-1",
			PreparedContextSize: 0,
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded HandoffContextPayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Zero(t, decoded.PreparedContextSize)
	})

	t.Run("empty summary", func(t *testing.T) {
		payload := HandoffContextPayload{
			FromAgentID: "agent-1",
			Summary:     "",
		}

		data, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded HandoffContextPayload
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Empty(t, decoded.Summary)
	})
}

// =============================================================================
// JSON Field Verification Tests
// =============================================================================

func TestContextShareRequest_JSONFieldNames(t *testing.T) {
	req := ContextShareRequest{
		RequestID:    "test-id",
		FromAgent:    "from",
		ToAgent:      "to",
		SessionID:    "session",
		Query:        "query",
		MaxTokens:    100,
		Priority:     ContextPriorityHigh,
		ContentTypes: []ContentType{ContentTypeCodeFile},
		Deadline:     time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"request_id"`)
	assert.Contains(t, jsonStr, `"from_agent"`)
	assert.Contains(t, jsonStr, `"to_agent"`)
	assert.Contains(t, jsonStr, `"session_id"`)
	assert.Contains(t, jsonStr, `"query"`)
	assert.Contains(t, jsonStr, `"max_tokens"`)
	assert.Contains(t, jsonStr, `"priority"`)
	assert.Contains(t, jsonStr, `"content_types"`)
	assert.Contains(t, jsonStr, `"deadline"`)
}

func TestConsultationRequest_JSONFieldNames(t *testing.T) {
	req := ConsultationRequest{
		RequestID: "test-id",
		FromAgent: "from",
		ToAgent:   "to",
		SessionID: "session",
		Query:     "query",
		MaxTokens: 100,
		Priority:  ContextPriorityNormal,
		Timeout:   10 * time.Second,
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"request_id"`)
	assert.Contains(t, jsonStr, `"from_agent"`)
	assert.Contains(t, jsonStr, `"to_agent"`)
	assert.Contains(t, jsonStr, `"session_id"`)
	assert.Contains(t, jsonStr, `"query"`)
	assert.Contains(t, jsonStr, `"max_tokens"`)
	assert.Contains(t, jsonStr, `"priority"`)
	assert.Contains(t, jsonStr, `"timeout"`)
}

func TestHandoffContextPayload_JSONFieldNames(t *testing.T) {
	payload := HandoffContextPayload{
		FromAgentID:         "from",
		ToAgentID:           "to",
		AgentType:           "type",
		SessionID:           "session",
		PreparedContextSize: 100,
		Summary:             "summary",
		RecentTurns:         []ConversationTurn{},
		ActiveFiles:         map[string]FileState{},
		TaskState:           &TaskState{TaskID: "task"},
		KeyDecisions:        []Decision{},
		Timestamp:           time.Now(),
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"from_agent_id"`)
	assert.Contains(t, jsonStr, `"to_agent_id"`)
	assert.Contains(t, jsonStr, `"agent_type"`)
	assert.Contains(t, jsonStr, `"session_id"`)
	assert.Contains(t, jsonStr, `"prepared_context_size"`)
	assert.Contains(t, jsonStr, `"summary"`)
	assert.Contains(t, jsonStr, `"recent_turns"`)
	assert.Contains(t, jsonStr, `"active_files"`)
	assert.Contains(t, jsonStr, `"task_state"`)
	assert.Contains(t, jsonStr, `"key_decisions"`)
	assert.Contains(t, jsonStr, `"timestamp"`)
}

// =============================================================================
// Additional Type Tests
// =============================================================================

func TestConversationTurn_Roles(t *testing.T) {
	roles := []string{"user", "assistant", "system", "tool"}

	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			turn := ConversationTurn{
				TurnNumber: 1,
				Role:       role,
				Content:    "test content",
			}

			data, err := json.Marshal(turn)
			require.NoError(t, err)

			var decoded ConversationTurn
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, role, decoded.Role)
		})
	}
}

func TestTaskState_Statuses(t *testing.T) {
	statuses := []string{"pending", "in_progress", "completed", "failed", "cancelled"}

	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			ts := TaskState{
				TaskID: "task-1",
				Status: status,
			}

			data, err := json.Marshal(ts)
			require.NoError(t, err)

			var decoded TaskState
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, status, decoded.Status)
		})
	}
}

func TestPrefetchSharePayload_TierSources(t *testing.T) {
	tiers := []SearchTier{TierNone, TierHotCache, TierWarmIndex, TierFullSearch}

	for _, tier := range tiers {
		t.Run(tier.String(), func(t *testing.T) {
			payload := PrefetchSharePayload{
				SessionID:  "session",
				TierSource: tier,
			}

			data, err := json.Marshal(payload)
			require.NoError(t, err)

			var decoded PrefetchSharePayload
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tier, decoded.TierSource)
		})
	}
}
