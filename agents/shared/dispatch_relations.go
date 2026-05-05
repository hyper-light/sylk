package shared

import (
	"context"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
)

// AttachCausedByFromContext appends a caused_by relation to the given
// slice when the context carries a parent claim ID
// (claims.ParentClaimIDFromContext). Returns the slice unchanged when
// no parent claim is in scope (e.g., a top-level cycle root).
//
// This is the canonical helper every dispatcher site uses to thread
// the cycle-tree edge through to the bridge. The bridge's cycle
// resolver walks caused_by to nest child claims under their parent
// (UI_DESIGN.md §4.5 + §5.3). Sites without an enclosing claim simply
// don't get the relation — that's the cycle-root case.
func AttachCausedByFromContext(ctx context.Context, relations []claims.Relation) []claims.Relation {
	if ctx == nil {
		return relations
	}
	// Handoff claims open a fresh top-level cycle on the successor
	// side; nesting them under the predecessor via caused_by would
	// pull the successor's cycle root back into the predecessor's
	// cycle and defeat ownership transfer (UI_DESIGN.md §2.2 + §5.2).
	// handoff_from is the structurally correct edge — and it's
	// mutually exclusive with caused_by. The post-claim chokepoint
	// (e.g., architectPostClaim) calls this universally, so this
	// guard is what keeps a handoff dispatch from acquiring both
	// relations when it goes through the same chokepoint.
	for _, r := range relations {
		if r.Relationship == claims.RelationshipHandoffFrom {
			return relations
		}
	}
	parent := strings.TrimSpace(claims.ParentClaimIDFromContext(ctx))
	if parent == "" {
		return relations
	}
	for _, r := range relations {
		if r.Relationship == claims.RelationshipCausedBy && r.Related == parent {
			return relations
		}
	}
	return append(relations, claims.Relation{
		Related:      parent,
		RelatedType:  claims.RelatedTypeClaim,
		Relationship: claims.RelationshipCausedBy,
	})
}

// AttachHandoffFrom appends a handoff_from relation pointing at the
// predecessor cycle's root claim ID. Used by handoff sites alongside
// ActionType=handoff so the bridge's cycle resolver can close the
// predecessor's cycle and open the successor's (UI_DESIGN.md §2.2 +
// §5.2). Empty predecessorID is a no-op — the caller is responsible
// for verifying this is a real handoff (not a fresh top-level cycle).
func AttachHandoffFrom(relations []claims.Relation, predecessorID string) []claims.Relation {
	predecessorID = strings.TrimSpace(predecessorID)
	if predecessorID == "" {
		return relations
	}
	return append(relations, claims.Relation{
		Related:      predecessorID,
		RelatedType:  claims.RelatedTypeClaim,
		Relationship: claims.RelationshipHandoffFrom,
	})
}

// predecessorCycleRootClaimID resolves the cycle root claim ID for the
// predecessor of a handoff. The caller's parent claim ID (carried on
// ctx via claims.ParentClaimIDFromContext) is the structural anchor:
// for an agent issuing a handoff, the parent claim of its
// currently-executing work IS its cycle root, since cycles open at
// the unparented top-level claim and the agent never spawns a new
// cycle while inside an existing one. UI_DESIGN.md §5.2.
func predecessorCycleRootClaimID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(claims.ParentClaimIDFromContext(ctx))
}
