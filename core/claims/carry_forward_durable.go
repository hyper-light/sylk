package claims

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

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
		db, err := OpenDurableBoard(ClaimsBoardConfig{
			BoardID:    "session-" + sessionID,
			SessionID:  sessionID,
			TaskID:     "session",
			SessionDir: filepath.Join(baseDir, sessionID),
		})
		if err != nil {
			return nil, nil, err
		}
		return db.Board(), func() { _ = db.Close() }, nil
	}
}
