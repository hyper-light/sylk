package claims

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type LegacySessionBoardError struct {
	SessionID  string
	SessionDir string
	BoardID    string
}

func (e *LegacySessionBoardError) Error() string {
	if e == nil {
		return "legacy session has no durable claims board WAL"
	}
	return fmt.Sprintf("legacy session %s has no durable claims board WAL at %s", e.SessionID, e.SessionDir)
}

func IsLegacySessionBoardError(err error) bool {
	var target *LegacySessionBoardError
	return errors.As(err, &target)
}

func DurableBoardWALExists(sessionDir, boardID string) bool {
	sessionDir = strings.TrimSpace(sessionDir)
	boardID = strings.TrimSpace(boardID)
	if sessionDir == "" || boardID == "" {
		return false
	}
	info, err := os.Stat(DurableBoardWALPath(sessionDir, boardID))
	return err == nil && !info.IsDir()
}

func DurableBoardWALPath(sessionDir, boardID string) string {
	return filepath.Join(sessionDir, "protocols", walNamespace, boardID, "wal", "events.wal.jsonl")
}

// DurableSessionBoardOpenerFromBoard builds a recall opener from the
// current session board's durable path. Session manager root boards live
// under <persistence>/<session_id>; a continuity session_cursor that names a
// previous_session_id can therefore be hydrated by opening the sibling durable
// board. Active sessions are resolved from the registry first to avoid taking a
// second WAL lock on an already-open board.
func DurableSessionBoardOpenerFromBoard(board *ClaimsBoard) SessionBoardOpener {
	if board == nil {
		return nil
	}
	currentDir := strings.TrimSpace(board.SessionDir())
	if currentDir == "" {
		return nil
	}
	baseDir := filepath.Dir(currentDir)
	if baseDir == "." || baseDir == "" {
		return nil
	}
	return func(_ context.Context, sessionID string) (*ClaimsBoard, func(), error) {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return nil, nil, fmt.Errorf("session_id is required")
		}
		if active := DefaultSessionBoardRegistry().Lookup(sessionID); active != nil {
			return active, nil, nil
		}
		sessionDir := filepath.Join(baseDir, sessionID)
		boardID := "session-" + sessionID
		if !DurableBoardWALExists(sessionDir, boardID) {
			return nil, nil, &LegacySessionBoardError{
				SessionID:  sessionID,
				SessionDir: sessionDir,
				BoardID:    boardID,
			}
		}
		db, err := OpenDurableBoard(ClaimsBoardConfig{
			BoardID:    boardID,
			SessionID:  sessionID,
			TaskID:     "session",
			SessionDir: sessionDir,
		})
		if err != nil {
			return nil, nil, err
		}
		return db.Board(), func() { _ = db.Close() }, nil
	}
}
