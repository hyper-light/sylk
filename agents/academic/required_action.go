package academic

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type academicTurnActionType string

const (
	academicTurnActionResearchPaper academicTurnActionType = "author_research_paper"
)

type academicTurnState struct {
	mu             sync.RWMutex
	requiredAction academicTurnActionType
	requiredReason string
	terminalAction academicTurnActionType
}

type academicTurnStateKey struct{}

func WithAcademicTurnState(ctx context.Context, state *academicTurnState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, academicTurnStateKey{}, state)
}

func AcademicTurnStateFromContext(ctx context.Context) *academicTurnState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(academicTurnStateKey{}).(*academicTurnState)
	return state
}

func newAcademicTurnState(requiredAction academicTurnActionType, reason string) *academicTurnState {
	return &academicTurnState{
		requiredAction: requiredAction,
		requiredReason: strings.TrimSpace(reason),
	}
}

func (s *academicTurnState) RequiredAction() (academicTurnActionType, string) {
	if s == nil {
		return "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requiredAction, strings.TrimSpace(s.requiredReason)
}

func (s *academicTurnState) TerminalAction() academicTurnActionType {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.terminalAction
}

func (s *academicTurnState) setTerminalAction(action academicTurnActionType) error {
	if s == nil || strings.TrimSpace(string(action)) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requiredAction != "" && action != s.requiredAction {
		return fmt.Errorf("%s", requiredAcademicActionMessage(s.requiredAction, s.requiredReason))
	}
	s.terminalAction = action
	return nil
}

func (s *academicTurnState) setRequiredReason(reason string) {
	if s == nil {
		return
	}
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requiredReason = trimmed
}

func requiredAcademicActionMessage(action academicTurnActionType, reason string) string {
	required := strings.TrimSpace(string(action))
	message := fmt.Sprintf("Before ending this Academic research turn, you must invoke `%s`.", required)
	if required == string(academicTurnActionResearchPaper) {
		message = "Before ending this Academic research turn, you must invoke `author_research_paper` so the Architect receives a reusable research artifact instead of transient prose."
	}
	if strings.TrimSpace(reason) != "" {
		return message + " " + strings.TrimSpace(reason)
	}
	return message
}

func ValidateAcademicTurnCompletion(ctx context.Context) error {
	state := AcademicTurnStateFromContext(ctx)
	if state == nil {
		return nil
	}
	if action, reason := state.RequiredAction(); action != "" {
		if state.TerminalAction() != action {
			return fmt.Errorf("%s", requiredAcademicActionMessage(action, reason))
		}
	}
	return nil
}
