package claims

import (
	"context"
	"path/filepath"
	"strings"
)

type claimsBoardContextKey struct{}

// WithClaimsBoardContext stores a claims board on the context.
func WithClaimsBoardContext(ctx context.Context, board *ClaimsBoard) context.Context {
	if board == nil {
		return ctx
	}
	return context.WithValue(ctx, claimsBoardContextKey{}, board)
}

// BoardFromContext retrieves the claims board from context, or nil.
func BoardFromContext(ctx context.Context) *ClaimsBoard {
	if ctx == nil {
		return nil
	}
	board, _ := ctx.Value(claimsBoardContextKey{}).(*ClaimsBoard)
	return board
}

// IsPathInClaimScope checks whether the given write path is within
// scope of any active claim held by agentID. Permissive: if no claims
// with file scope entries exist for the agent, all writes pass.
func IsPathInClaimScope(board *ClaimsBoard, agentID, path string) bool {
	if board == nil || strings.TrimSpace(agentID) == "" {
		return true // permissive when board unavailable
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return true
	}

	// Check direct file scope match.
	if ids := board.ClaimIDsWithScope("file", path); len(ids) > 0 {
		return hasAgentScopedClaim(board, ids, agentID)
	}

	// Check ancestor directories — a scope on "src/pkg" covers "src/pkg/foo.go".
	dir := path
	for {
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." || parent == "/" {
			break
		}
		dir = parent
		if ids := board.ClaimIDsWithScope("file", dir); len(ids) > 0 {
			if hasAgentScopedClaim(board, ids, agentID) {
				return true
			}
		}
	}

	// Permissive: if no file scope entries exist at all, all writes pass.
	return !agentHasFileScopedClaims(board, agentID)
}

// hasAgentScopedClaim checks if any of the given claim IDs are
// related to the agent as subject.
func hasAgentScopedClaim(board *ClaimsBoard, claimIDs []string, agentID string) bool {
	for _, id := range claimIDs {
		c, ok := board.ClaimByID(id)
		if !ok || c == nil {
			continue
		}
		if c.Status.IsTerminal() {
			continue
		}
		if HasRelation(c.Relations, RelationshipSubject, agentID) {
			return true
		}
		// Also allow if no specific subject (open claims).
		if SubjectAgentID(c.Relations) == "" {
			return true
		}
	}
	return false
}

// agentHasFileScopedClaims checks whether any non-terminal claim on
// the board with a file scope entry is directed at this agent.
func agentHasFileScopedClaims(board *ClaimsBoard, agentID string) bool {
	board.mu.RLock()
	defer board.mu.RUnlock()
	for _, c := range board.claims {
		if c.Status.IsTerminal() {
			continue
		}
		hasFile := false
		for _, s := range c.Scope {
			if s.Kind == "file" {
				hasFile = true
				break
			}
		}
		if !hasFile {
			continue
		}
		if HasRelation(c.Relations, RelationshipSubject, agentID) || SubjectAgentID(c.Relations) == "" {
			return true
		}
	}
	return false
}
