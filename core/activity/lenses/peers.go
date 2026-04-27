// Package lenses contains the typed cross-cutting lens implementations
// over the Activity Fabric. Each lens is a small, focused function that
// projects, filters, or aggregates the underlying activity stream into
// the answer an agent (or audit tool) actually wants.
//
// Lenses live outside the leaf core/activity package so they can
// depend on the persistence layer (activitystore) without creating an
// import cycle.
package lenses

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// PeerActivityQuery describes a request to peers.WhatAreTheyDoing.
type PeerActivityQuery struct {
	SessionID    activity.SessionID
	Scope        string                // PathPrefix scope filter
	Kinds        []activity.ActionKind // Empty matches any
	Since        time.Time
	ExcludeAgent string // Don't include the caller's own activities
	Limit        int    // Default 50
}

// PeerActivityResult is the result of peers.WhatAreTheyDoing.
type PeerActivityResult struct {
	Activities []activity.AgentActivity
}

// WhatAreTheyDoing returns recent activity by other agents in scope.
// Implements the "what are my peers up to?" lens that powers ambient
// context envelopes and the active query_peer_activity skill.
func WhatAreTheyDoing(ctx context.Context, src activity.Source, q PeerActivityQuery) (PeerActivityResult, error) {
	if src == nil {
		return PeerActivityResult{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	filter := activity.QueryFilter{
		SessionID:         q.SessionID,
		ActionKinds:       q.Kinds,
		SubjectPathPrefix: q.Scope,
		Since:             q.Since,
		Limit:             limit,
	}
	rows, err := src.FilterActivities(ctx, filter)
	if err != nil {
		return PeerActivityResult{}, err
	}
	out := PeerActivityResult{Activities: make([]activity.AgentActivity, 0, len(rows))}
	for _, a := range rows {
		if q.ExcludeAgent != "" && a.Actor.AgentID == q.ExcludeAgent {
			continue
		}
		out.Activities = append(out.Activities, a)
	}
	return out, nil
}

// CausalContextResult holds the walked causal DAG for a target activity.
type CausalContextResult struct {
	Target     activity.AgentActivity
	Ancestors  []activity.AgentActivity // Chain from root → immediate parent
	Children   []activity.AgentActivity // Direct Caused-by descendants
	Depth      int                       // Length of the ancestor chain
}

// CausalContext walks the cause/caused DAG anchored at activityID.
// Returns ancestors in oldest-first order, plus immediate descendants.
// Bounded depth to keep walks finite.
func CausalContext(ctx context.Context, src activity.Source, activityID activity.ActivityID, maxDepth int) (CausalContextResult, error) {
	if src == nil || activityID == "" {
		return CausalContextResult{}, nil
	}
	if maxDepth <= 0 {
		maxDepth = 32
	}

	target, err := src.GetActivity(ctx, activityID)
	if err != nil {
		return CausalContextResult{}, err
	}
	if target == nil {
		return CausalContextResult{}, nil
	}

	var ancestors []activity.AgentActivity
	current := target
	depth := 0
	for current.Caused != nil && *current.Caused != "" && depth < maxDepth {
		parent, err := src.GetActivity(ctx, *current.Caused)
		if err != nil {
			return CausalContextResult{}, err
		}
		if parent == nil {
			break
		}
		ancestors = append([]activity.AgentActivity{*parent}, ancestors...)
		current = parent
		depth++
	}

	children, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID: target.SessionID,
		Caused:    &activityID,
		Limit:     32,
	})
	if err != nil {
		return CausalContextResult{}, err
	}

	return CausalContextResult{
		Target:    *target,
		Ancestors: ancestors,
		Children:  children,
		Depth:     depth,
	}, nil
}

// OpenConflictsQuery describes a request to peers.ConflictsOpen.
type OpenConflictsQuery struct {
	SessionID activity.SessionID
	Scope     string // Empty matches any scope
	Limit     int
}

// OpenConflictsResult holds conflicts in progress.
type OpenConflictsResult struct {
	Challenges       []activity.AgentActivity // in_flight challenges
	UnresolvedConsults []activity.AgentActivity
	StalledHolds     []activity.AgentActivity
}

// ConflictsOpen returns activity that requires attention: open
// challenges, unresolved consults past their deadline, stalled
// validation holds. Inspector at audit time and pipeline agents
// looking at ambient context both consume this.
func ConflictsOpen(ctx context.Context, src activity.Source, q OpenConflictsQuery) (OpenConflictsResult, error) {
	if src == nil {
		return OpenConflictsResult{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}

	challenges, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:         q.SessionID,
		ActionKinds:       []activity.ActionKind{activity.ActionChallengeEmitted},
		States:            []activity.ActivityState{activity.StateInFlight},
		SubjectPathPrefix: q.Scope,
		Limit:             limit,
	})
	if err != nil {
		return OpenConflictsResult{}, err
	}

	unresolved, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:         q.SessionID,
		ActionKinds:       []activity.ActionKind{activity.ActionConsultUnanswered},
		SubjectPathPrefix: q.Scope,
		Limit:             limit,
	})
	if err != nil {
		return OpenConflictsResult{}, err
	}

	stalled, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:         q.SessionID,
		ActionKinds:       []activity.ActionKind{activity.ActionHoldAcquired},
		States:            []activity.ActivityState{activity.StateInFlight},
		SubjectPathPrefix: q.Scope,
		Limit:             limit,
	})
	if err != nil {
		return OpenConflictsResult{}, err
	}

	return OpenConflictsResult{
		Challenges:         challenges,
		UnresolvedConsults: unresolved,
		StalledHolds:       stalled,
	}, nil
}

// ScopeHotnessQuery describes a request to compute hotness for a scope.
type ScopeHotnessQuery struct {
	SessionID activity.SessionID
	Scope     string
	Window    time.Duration // How far back to count; default 5 min
}

// ScopeHotnessResult holds the hotness signal for a scope.
type ScopeHotnessResult struct {
	Scope             string
	ChallengeCount    int
	ResolutionCount   int
	ActivityCount     int
	HotnessScore      float64 // higher = hotter
	WindowStart       time.Time
}

// ScopeHotness computes the contention signal for a scope: rate of
// activity × challenge density × outcome variance. Simple version:
// score = challenges*5 + activities. Higher = hotter.
//
// Used by pre-emit advice ("this scope is hot — consider adopting an
// existing thread") and by ambient context envelope sorting.
func ScopeHotness(ctx context.Context, src activity.Source, q ScopeHotnessQuery) (ScopeHotnessResult, error) {
	if src == nil {
		return ScopeHotnessResult{Scope: q.Scope}, nil
	}
	window := q.Window
	if window <= 0 {
		window = 5 * time.Minute
	}
	since := time.Now().Add(-window)

	all, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:         q.SessionID,
		SubjectPathPrefix: q.Scope,
		Since:             since,
		Limit:             500,
	})
	if err != nil {
		return ScopeHotnessResult{}, err
	}

	res := ScopeHotnessResult{
		Scope:         q.Scope,
		ActivityCount: len(all),
		WindowStart:   since,
	}
	for _, a := range all {
		switch a.Action {
		case activity.ActionChallengeEmitted:
			res.ChallengeCount++
		case activity.ActionChallengeResponse, activity.ActionScopePartitioned, activity.ActionEscalationRequested:
			res.ResolutionCount++
		}
	}
	res.HotnessScore = float64(res.ChallengeCount*5 + res.ActivityCount)
	return res, nil
}

// SessionTimeline returns chronologically-ordered activities for a
// session, optionally filtered. Used by audit tooling and the TUI
// timeline view.
func SessionTimeline(ctx context.Context, src activity.Source, sessionID activity.SessionID, since time.Time, limit int) ([]activity.AgentActivity, error) {
	if src == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID: sessionID,
		Since:     since,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Timestamp.Before(rows[j].Timestamp)
	})
	return rows, nil
}

// WorkSurfaceFor returns every activity touching a given file path or
// scope. Powers "who has been touching services/billing/auth.go?".
func WorkSurfaceFor(ctx context.Context, src activity.Source, sessionID activity.SessionID, path string, limit int) ([]activity.AgentActivity, error) {
	if src == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	return src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:         sessionID,
		SubjectPathPrefix: path,
		Limit:             limit,
	})
}

// IngestionContext is what knowledge agents use to ground their
// in-session responses. A structured digest of recent activity in
// scope, plus open conflicts and recent decisions.
type IngestionContext struct {
	RecentActivity  []activity.AgentActivity
	RecentDecisions []activity.AgentActivity
	OpenConflicts   OpenConflictsResult
}

// SessionIngestionContext builds the IngestionContext for a knowledge
// agent's consult response.
func SessionIngestionContext(ctx context.Context, src activity.Source, sessionID activity.SessionID, scope string) (IngestionContext, error) {
	if src == nil {
		return IngestionContext{}, nil
	}
	since := time.Now().Add(-30 * time.Minute)
	recent, err := WhatAreTheyDoing(ctx, src, PeerActivityQuery{
		SessionID: sessionID,
		Scope:     scope,
		Since:     since,
		Limit:     50,
	})
	if err != nil {
		return IngestionContext{}, err
	}
	decisions, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:         sessionID,
		ActionKinds:       []activity.ActionKind{activity.ActionDecisionDeclared, activity.ActionDecisionPromoted},
		SubjectPathPrefix: scope,
		Since:             since,
		Limit:             20,
	})
	if err != nil {
		return IngestionContext{}, err
	}
	conflicts, err := ConflictsOpen(ctx, src, OpenConflictsQuery{
		SessionID: sessionID,
		Scope:     scope,
	})
	if err != nil {
		return IngestionContext{}, err
	}
	return IngestionContext{
		RecentActivity:  recent.Activities,
		RecentDecisions: decisions,
		OpenConflicts:   conflicts,
	}, nil
}

// ClaimConflictsResult extends OpenConflictsResult with claims-aware
// conflict signals from the board.
type ClaimConflictsResult struct {
	OpenConflictsResult

	// RejectedClaims are claims that were rejected — conflicts with the
	// original plan.
	RejectedClaims []activity.AgentActivity

	// ValidationFailures are claims with at least one failed validation.
	ValidationFailures []activity.AgentActivity
}

// ClaimConflictsOpen extends ConflictsOpen with claims-specific conflict
// detection: rejected claims and validation failures from the Fabric
// activity stream. The existing ConflictsOpen result is embedded.
func ClaimConflictsOpen(ctx context.Context, src activity.Source, q OpenConflictsQuery) (ClaimConflictsResult, error) {
	base, err := ConflictsOpen(ctx, src, q)
	if err != nil {
		return ClaimConflictsResult{}, err
	}
	result := ClaimConflictsResult{OpenConflictsResult: base}
	if src == nil {
		return result, nil
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}

	rejected, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:   q.SessionID,
		ActionKinds: []activity.ActionKind{activity.ActionClaimRejected},
		Limit:       limit,
	})
	if err != nil {
		slog.Warn("claim_conflicts_rejected_query_failed", "error", err.Error())
	} else {
		result.RejectedClaims = rejected
	}

	validations, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:   q.SessionID,
		ActionKinds: []activity.ActionKind{activity.ActionClaimValidated},
		Limit:       limit,
	})
	if err != nil {
		slog.Warn("claim_conflicts_validation_query_failed", "error", err.Error())
	} else {
		for _, v := range validations {
			if isFailedValidationActivity(v) {
				result.ValidationFailures = append(result.ValidationFailures, v)
			}
		}
	}

	return result, nil
}

func isFailedValidationActivity(a activity.AgentActivity) bool {
	if len(a.Payload) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(a.Payload, &payload); err != nil {
		return false
	}
	status, _ := payload["status"].(string)
	return status == "failed"
}
