package lenses

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// AmbientFor is the centerpiece lens of the awareness model. It
// computes the ambient_context envelope that gets attached to every
// tool result returned to an agent. The envelope is bounded, ordered
// by relevance × recency × hotness, and contains:
//
//   - in_flight_activities: peer activity in scope (capped)
//   - recent_peer_commitments: recently-promoted decisions in scope
//   - inbound_disputes: cross-pipeline challenges targeting this agent
//   - inbound_consults: cross-pipeline consults addressed to this agent
//   - outbound_pending: this agent's outstanding asks to peers
//   - advisories: knowledge-agent advisories targeting this agent / scope
//
// The envelope itself never blocks the agent's primary work — it's
// purely informational. See FABRIC.md §"Vector 3 — Ambient context
// envelope on every tool result."
func AmbientFor(ctx context.Context, src activity.Source, q AmbientQuery) (AmbientEnvelope, error) {
	if src == nil {
		return AmbientEnvelope{}, nil
	}

	q.normalize()

	envelope := AmbientEnvelope{
		Scope:           q.Scope,
		AgentID:         q.AgentID,
		AgentType:       q.AgentType,
		ComputedAt:      time.Now(),
		MaxInbound:      q.MaxInbound,
		MaxConsults:     q.MaxConsults,
		MaxPeers:        q.MaxPeers,
		HotnessWindow:   q.HotnessWindow,
	}

	since := time.Now().Add(-q.LookbackWindow)

	// In-flight peer activity in scope.
	peers, err := WhatAreTheyDoing(ctx, src, PeerActivityQuery{
		SessionID:    q.SessionID,
		Scope:        q.Scope,
		Since:        since,
		ExcludeAgent: q.AgentID,
		Limit:        q.MaxPeers,
	})
	if err != nil {
		return envelope, err
	}
	envelope.InFlightActivities = peers.Activities

	// Recent peer commitments (decisions in scope, recently promoted).
	commitments, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:         q.SessionID,
		ActionKinds:       []activity.ActionKind{activity.ActionDecisionPromoted, activity.ActionCharterRatified},
		SubjectPathPrefix: q.Scope,
		Since:             since,
		Limit:             q.MaxPeers,
	})
	if err != nil {
		return envelope, err
	}
	envelope.RecentPeerCommitments = commitments

	// Inbound disputes: challenges targeting this agent.
	if q.AgentID != "" {
		inbound, err := src.FilterActivities(ctx, activity.QueryFilter{
			SessionID:          q.SessionID,
			ActionKinds:        []activity.ActionKind{activity.ActionChallengeEmitted},
			States:             []activity.ActivityState{activity.StateInFlight},
			SubjectTargetAgent: q.AgentID,
			Since:              since,
			Limit:              q.MaxInbound,
		})
		if err != nil {
			return envelope, err
		}
		envelope.InboundDisputes = inbound

		// Inbound consults addressed to this agent.
		inboundConsults, err := src.FilterActivities(ctx, activity.QueryFilter{
			SessionID:          q.SessionID,
			ActionKinds:        []activity.ActionKind{activity.ActionConsultEmitted},
			States:             []activity.ActivityState{activity.StateInFlight},
			SubjectTargetAgent: q.AgentID,
			Since:              since,
			Limit:              q.MaxConsults,
		})
		if err != nil {
			return envelope, err
		}
		envelope.InboundConsults = inboundConsults
	}

	// Outbound pending: this agent's outstanding asks (Caller-emitted
	// challenges/consults still in_flight).
	if q.AgentID != "" {
		outbound, err := src.FilterActivities(ctx, activity.QueryFilter{
			SessionID:    q.SessionID,
			ActionKinds:  []activity.ActionKind{activity.ActionChallengeEmitted, activity.ActionConsultEmitted},
			States:       []activity.ActivityState{activity.StateInFlight},
			ActorAgentID: q.AgentID,
			Since:        since,
			Limit:        q.MaxInbound + q.MaxConsults,
		})
		if err != nil {
			return envelope, err
		}
		envelope.OutboundPending = outbound
	}

	// Advisories: knowledge-agent advisories or proactive advisories
	// targeting this agent or scope.
	advisories, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID: q.SessionID,
		ActionKinds: []activity.ActionKind{
			activity.ActionAdvisoryEmitted,
			activity.ActionProactiveAdvisory,
			activity.ActionNotificationEmitted,
		},
		Since: since,
		Limit: q.MaxPeers,
	})
	if err != nil {
		return envelope, err
	}
	for _, ad := range advisories {
		// Filter to advisories targeting this agent OR matching the
		// scope prefix.
		if ad.Subject.TargetAgent != "" && ad.Subject.TargetAgent != q.AgentID {
			continue
		}
		if ad.Subject.PathPrefix != "" && q.Scope != "" && !strings.HasPrefix(q.Scope, ad.Subject.PathPrefix) && !strings.HasPrefix(ad.Subject.PathPrefix, q.Scope) {
			continue
		}
		envelope.Advisories = append(envelope.Advisories, ad)
	}

	// Claims board digest: surface claim/testament activity for this agent.
	if q.AgentID != "" {
		digest, digestErr := computeClaimsBoardDigest(ctx, src, q, since)
		if digestErr == nil && !digest.isEmpty() {
			envelope.ClaimsBoardDigest = &digest
		}
	}

	// Hotness signal for the scope.
	hot, err := ScopeHotness(ctx, src, ScopeHotnessQuery{
		SessionID: q.SessionID,
		Scope:     q.Scope,
		Window:    q.HotnessWindow,
	})
	if err != nil {
		return envelope, err
	}
	envelope.Hotness = hot

	// Sort each slice by recency descending so the most-recent items
	// surface first within their cap.
	sortByRecencyDesc(envelope.InFlightActivities)
	sortByRecencyDesc(envelope.RecentPeerCommitments)
	sortByRecencyDesc(envelope.InboundDisputes)
	sortByRecencyDesc(envelope.InboundConsults)
	sortByRecencyDesc(envelope.OutboundPending)
	sortByRecencyDesc(envelope.Advisories)

	envelope.OverflowAdvisory = buildOverflowAdvisory(hot)

	return envelope, nil
}

// AmbientQuery describes a request to AmbientFor.
type AmbientQuery struct {
	SessionID activity.SessionID
	AgentID   string
	AgentType string
	Scope     string

	// LookbackWindow caps how far back the lens looks. Default 5 minutes.
	LookbackWindow time.Duration

	// HotnessWindow caps the hotness computation window. Default 5 min.
	HotnessWindow time.Duration

	// MaxPeers caps in-flight + commitments + advisories. Default 5.
	MaxPeers int

	// MaxInbound caps inbound disputes + outbound pending. Default 3.
	MaxInbound int

	// MaxConsults caps inbound consults. Default 5.
	MaxConsults int
}

func (q *AmbientQuery) normalize() {
	if q.LookbackWindow <= 0 {
		q.LookbackWindow = 5 * time.Minute
	}
	if q.HotnessWindow <= 0 {
		q.HotnessWindow = 5 * time.Minute
	}
	if q.MaxPeers <= 0 {
		q.MaxPeers = 5
	}
	if q.MaxInbound <= 0 {
		q.MaxInbound = 3
	}
	if q.MaxConsults <= 0 {
		q.MaxConsults = 5
	}
}

// AmbientEnvelope is the bounded, ordered context block returned to the
// agent on every tool result. Rendering happens in the agent's
// tool-loop response composer (see agents/shared/ambient_render.go).
type AmbientEnvelope struct {
	Scope                 string
	AgentID               string
	AgentType             string
	ComputedAt            time.Time
	MaxInbound            int
	MaxConsults           int
	MaxPeers              int
	HotnessWindow         time.Duration

	InFlightActivities    []activity.AgentActivity
	RecentPeerCommitments []activity.AgentActivity
	InboundDisputes       []activity.AgentActivity
	InboundConsults       []activity.AgentActivity
	OutboundPending       []activity.AgentActivity
	Advisories            []activity.AgentActivity
	Hotness               ScopeHotnessResult
	OverflowAdvisory      string

	// ClaimsBoardDigest surfaces claims board state: this agent's claims,
	// peer progress, recent testaments, blocked claims, and board phase.
	// Populated from claim_issued/testament_submitted/claim_accepted
	// activities in the Fabric stream.
	ClaimsBoardDigest *ClaimsBoardDigest
}

// ClaimsBoardDigest is the claims-specific section of the ambient
// context envelope. Computed from Fabric claim activities.
type ClaimsBoardDigest struct {
	// MyClaims are claims where this agent is the subject, currently
	// in_progress or pending.
	MyClaims []activity.AgentActivity

	// PeerClaimsInProgress shows what peers are actively working on.
	PeerClaimsInProgress []activity.AgentActivity

	// RecentTestaments shows testaments submitted in the lookback window.
	RecentTestaments []activity.AgentActivity

	// CompletedClaims shows recently accepted claims.
	CompletedClaims []activity.AgentActivity

	// BoardProgress is a compact summary string: "8/12 claims testified,
	// 2 accepted, 1 rejected"
	BoardProgress string
}

// IsEmpty reports whether the envelope has nothing to surface.
func (e AmbientEnvelope) IsEmpty() bool {
	return len(e.InFlightActivities) == 0 &&
		len(e.RecentPeerCommitments) == 0 &&
		len(e.InboundDisputes) == 0 &&
		len(e.InboundConsults) == 0 &&
		len(e.OutboundPending) == 0 &&
		len(e.Advisories) == 0 &&
		(e.ClaimsBoardDigest == nil || e.ClaimsBoardDigest.isEmpty())
}

// Render produces the user-facing string the agent sees attached to
// its tool result. Compact, structured, bounded.
func (e AmbientEnvelope) Render() string {
	if e.IsEmpty() && e.Hotness.HotnessScore == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<ambient_context>\n")
	if e.Scope != "" {
		fmt.Fprintf(&b, "  scope: %s\n", e.Scope)
	}
	if len(e.InFlightActivities) > 0 {
		fmt.Fprintf(&b, "  in_flight_activities: %d\n", len(e.InFlightActivities))
		for _, a := range e.InFlightActivities {
			fmt.Fprintf(&b, "    • %s by %s/%s (%s)\n", a.Action, a.Actor.AgentType, a.Actor.AgentID, summarizeAge(e.ComputedAt, a.Timestamp))
		}
	}
	if len(e.RecentPeerCommitments) > 0 {
		fmt.Fprintf(&b, "  recent_peer_commitments: %d\n", len(e.RecentPeerCommitments))
		for _, a := range e.RecentPeerCommitments {
			fmt.Fprintf(&b, "    • %s/%s by %s (%s)\n", a.Subject.Domain, a.Subject.PathPrefix, a.Actor.AgentID, summarizeAge(e.ComputedAt, a.Timestamp))
		}
	}
	if len(e.InboundDisputes) > 0 {
		fmt.Fprintf(&b, "  inbound_disputes: %d\n", len(e.InboundDisputes))
		for _, a := range e.InboundDisputes {
			fmt.Fprintf(&b, "    • %s challenged by %s (activity_id=%s)\n", a.Subject.TargetArtifact, a.Actor.AgentID, a.ID)
		}
	}
	if len(e.InboundConsults) > 0 {
		fmt.Fprintf(&b, "  inbound_consults: %d\n", len(e.InboundConsults))
		for _, a := range e.InboundConsults {
			fmt.Fprintf(&b, "    • %s asks (consult_id=%s)\n", a.Actor.AgentID, a.ID)
		}
	}
	if len(e.OutboundPending) > 0 {
		fmt.Fprintf(&b, "  outbound_pending: %d (your asks awaiting response)\n", len(e.OutboundPending))
	}
	if len(e.Advisories) > 0 {
		fmt.Fprintf(&b, "  advisories: %d\n", len(e.Advisories))
		for _, a := range e.Advisories {
			fmt.Fprintf(&b, "    • %s: %s\n", a.Actor.AgentType, a.Subject.PathPrefix)
		}
	}
	if e.ClaimsBoardDigest != nil && !e.ClaimsBoardDigest.isEmpty() {
		d := e.ClaimsBoardDigest
		b.WriteString("  claims_board:\n")
		if d.BoardProgress != "" {
			fmt.Fprintf(&b, "    progress: %s\n", d.BoardProgress)
		}
		if len(d.MyClaims) > 0 {
			fmt.Fprintf(&b, "    my_claims: %d\n", len(d.MyClaims))
			for _, a := range d.MyClaims {
				fmt.Fprintf(&b, "      - %s (claim_id=%s, %s)\n", a.Subject.TargetArtifact, a.ID, summarizeAge(e.ComputedAt, a.Timestamp))
			}
		}
		if len(d.PeerClaimsInProgress) > 0 {
			fmt.Fprintf(&b, "    peer_claims: %d in_progress\n", len(d.PeerClaimsInProgress))
			for _, a := range d.PeerClaimsInProgress {
				fmt.Fprintf(&b, "      - %s/%s: %s (%s)\n", a.Actor.AgentType, a.Actor.AgentID, a.Subject.TargetArtifact, summarizeAge(e.ComputedAt, a.Timestamp))
			}
		}
		if len(d.RecentTestaments) > 0 {
			fmt.Fprintf(&b, "    recent_testaments: %d\n", len(d.RecentTestaments))
			for _, a := range d.RecentTestaments {
				fmt.Fprintf(&b, "      - %s submitted testament (%s)\n", a.Actor.AgentID, summarizeAge(e.ComputedAt, a.Timestamp))
			}
		}
		if len(d.CompletedClaims) > 0 {
			fmt.Fprintf(&b, "    completed_claims: %d\n", len(d.CompletedClaims))
		}
	}
	if e.OverflowAdvisory != "" {
		fmt.Fprintf(&b, "  hotness_advisory: %s\n", e.OverflowAdvisory)
	}
	b.WriteString("</ambient_context>")
	return b.String()
}

func sortByRecencyDesc(slice []activity.AgentActivity) {
	sort.SliceStable(slice, func(i, j int) bool {
		return slice[i].Timestamp.After(slice[j].Timestamp)
	})
}

func summarizeAge(now, then time.Time) string {
	if then.IsZero() {
		return "unknown"
	}
	d := now.Sub(then)
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

func (d ClaimsBoardDigest) isEmpty() bool {
	return len(d.MyClaims) == 0 &&
		len(d.PeerClaimsInProgress) == 0 &&
		len(d.RecentTestaments) == 0 &&
		len(d.CompletedClaims) == 0
}

func computeClaimsBoardDigest(ctx context.Context, src activity.Source, q AmbientQuery, since time.Time) (ClaimsBoardDigest, error) {
	var d ClaimsBoardDigest

	// My claims: claims where this agent is the subject (via TargetAgent).
	myClaims, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:          q.SessionID,
		ActionKinds:        []activity.ActionKind{activity.ActionClaimIssued, activity.ActionClaimUpdated},
		SubjectTargetAgent: q.AgentID,
		Since:              since,
		Limit:              5,
	})
	if err != nil {
		return d, err
	}
	d.MyClaims = myClaims

	// Peer claims in progress: claims where someone else is the subject.
	peerClaims, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:   q.SessionID,
		ActionKinds: []activity.ActionKind{activity.ActionClaimUpdated},
		Since:       since,
		Limit:       5,
	})
	if err != nil {
		return d, err
	}
	for _, a := range peerClaims {
		if a.Actor.AgentID != q.AgentID {
			d.PeerClaimsInProgress = append(d.PeerClaimsInProgress, a)
		}
	}

	// Recent testaments.
	testaments, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:   q.SessionID,
		ActionKinds: []activity.ActionKind{activity.ActionTestamentSubmitted},
		Since:       since,
		Limit:       5,
	})
	if err != nil {
		return d, err
	}
	d.RecentTestaments = testaments

	// Completed claims (accepted).
	completed, err := src.FilterActivities(ctx, activity.QueryFilter{
		SessionID:   q.SessionID,
		ActionKinds: []activity.ActionKind{activity.ActionClaimAccepted},
		Since:       since,
		Limit:       5,
	})
	if err != nil {
		return d, err
	}
	d.CompletedClaims = completed

	// Board progress summary.
	total := len(myClaims) + len(peerClaims)
	testified := len(testaments)
	accepted := len(completed)
	if total > 0 || testified > 0 || accepted > 0 {
		d.BoardProgress = fmt.Sprintf("%d claims visible, %d testaments, %d accepted", total, testified, accepted)
	}

	// Sort by recency.
	sortByRecencyDesc(d.MyClaims)
	sortByRecencyDesc(d.PeerClaimsInProgress)
	sortByRecencyDesc(d.RecentTestaments)
	sortByRecencyDesc(d.CompletedClaims)

	return d, nil
}

func buildOverflowAdvisory(h ScopeHotnessResult) string {
	if h.ChallengeCount >= 3 {
		return fmt.Sprintf("scope %s is contested (%d challenges in window) — consider adopting an existing thread", h.Scope, h.ChallengeCount)
	}
	if h.ActivityCount >= 50 {
		return fmt.Sprintf("scope %s is busy (%d activities in window) — query before declaring", h.Scope, h.ActivityCount)
	}
	return ""
}
