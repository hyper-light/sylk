package forest

import (
	"context"
	"time"
)

type ForestTextHit struct {
	ID      string
	Title   string
	Summary string
	Score   float64
}

//go:generate mockery --name=TextSearcher --inpackage --output=. --filename=mock_text_searcher_test.go --outpkg=forest
type TextSearcher interface {
	SearchText(ctx context.Context, query string, limit int) ([]ForestTextHit, error)
}

type AgentTurn struct {
	SessionID  string
	AgentID    string
	Role       string
	Content    string
	OccurredAt time.Time
}

//go:generate mockery --name=AgentTurnProvider --inpackage --output=. --filename=mock_agent_turn_provider_test.go --outpkg=forest
type AgentTurnProvider interface {
	RecentAgentTurns(ctx context.Context, sessionID string, limit int) ([]AgentTurn, error)
}
