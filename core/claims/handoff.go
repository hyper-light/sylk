package claims

import (
	"errors"
	"fmt"
	"strings"
)

// HandoffNotEligibleError is the typed rejection returned by
// HandoffEligible when an agent attempts to issue a handoff while it
// still has open child work, is itself a child of a peer's open claim,
// or has tool/peer-interaction artifacts that started but haven't yet
// completed. Carries the offending IDs so callers (skill handlers and
// the board's PostAction guard) can surface a structured explanation.
//
// UI_DESIGN.md §4.3 — handoff precondition checker.
type HandoffNotEligibleError struct {
	AgentID                  string
	Reason                   string
	OpenChildClaims          []string // claims I issued that are still open
	OpenAsSubjectFromIssuers []string // claims peers issued to me that are still open
	InFlightStartedArtifacts []string // *_started artifacts on my testaments without matching *_completed
}

func (e *HandoffNotEligibleError) Error() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

// HandoffEligible reports nil when the agent identified by agentID may
// post a handoff action against the given board, or a populated
// *HandoffNotEligibleError otherwise. Pure read against a single
// projection snapshot — no mutation, no goroutines.
//
// Three independent checks (UI_DESIGN.md §4.3 + §4.4):
//  1. Open child work: any open claim where I am the issuer.
//  2. In-flight as child: any open claim where I am the subject AND
//     the issuer is another agent.
//  3. In-flight artifacts: any *_started artifact on my testaments
//     attached to an open claim, lacking a matching *_completed.
//
// The board-level guard in PostAction calls handoffEligibleLocked
// (the lock-aware variant of this function) for any action with
// ActionType=handoff so a misbehaving agent cannot bypass skill-side
// enforcement by posting the action directly.
func HandoffEligible(board *ClaimsBoard, agentID string) error {
	if board == nil {
		return errors.New("HandoffEligible: nil board")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errors.New("HandoffEligible: empty agentID")
	}

	board.mu.Lock()
	defer board.mu.Unlock()
	return board.handoffEligibleLocked(agentID)
}

// handoffEligibleLocked walks the board's internal maps directly and
// computes the same answer as HandoffEligible. Caller must hold b.mu.
// PostAction's handoff guard calls this directly so the eligibility
// check is atomic with the post — preventing TOCTOU between snapshot
// and stamp.
func (b *ClaimsBoard) handoffEligibleLocked(agentID string) error {
	return b.handoffEligibleExcludingLocked(agentID, nil)
}

func (b *ClaimsBoard) handoffEligibleForPostLocked(agentID string, claimID string) error {
	exclude := map[string]struct{}{strings.TrimSpace(claimID): {}}
	return b.handoffEligibleExcludingLocked(agentID, exclude)
}

func (b *ClaimsBoard) handoffEligibleExcludingLocked(agentID string, exclude map[string]struct{}) error {
	rejection := &HandoffNotEligibleError{AgentID: agentID}

	openClaimIDs := make(map[string]struct{}, len(b.claims))
	for id, c := range b.claims {
		if _, skipped := exclude[id]; skipped {
			continue
		}
		if c == nil || c.Status.IsTerminal() {
			continue
		}
		openClaimIDs[id] = struct{}{}

		issuer := IssuerAgentID(c.Relations)
		subject := SubjectAgentID(c.Relations)

		if issuer == agentID {
			rejection.OpenChildClaims = append(rejection.OpenChildClaims, id)
		}
		if subject == agentID && issuer != "" && issuer != agentID {
			rejection.OpenAsSubjectFromIssuers = append(rejection.OpenAsSubjectFromIssuers, id)
		}
	}

	completedStartedIDs := make(map[string]struct{})
	var startedArtifactIDs []string
	for _, t := range b.testaments {
		if t == nil || t.AgentID != agentID {
			continue
		}
		claimID := ClaimIDFromRelations(t.Relations)
		if _, open := openClaimIDs[claimID]; !open {
			continue
		}
		for _, a := range t.Artifacts {
			if a == nil {
				continue
			}
			switch {
			case isStartedArtifactKind(a.Kind):
				if a.ID != "" {
					startedArtifactIDs = append(startedArtifactIDs, a.ID)
				}
			case isCompletedArtifactKind(a.Kind):
				if rel := CompletesArtifactID(a.Relations); rel != "" {
					completedStartedIDs[rel] = struct{}{}
				}
			}
		}
	}
	for _, id := range startedArtifactIDs {
		if _, paired := completedStartedIDs[id]; !paired {
			rejection.InFlightStartedArtifacts = append(rejection.InFlightStartedArtifacts, id)
		}
	}

	if len(rejection.OpenChildClaims) == 0 &&
		len(rejection.OpenAsSubjectFromIssuers) == 0 &&
		len(rejection.InFlightStartedArtifacts) == 0 {
		return nil
	}
	rejection.Reason = formatHandoffRejection(rejection)
	return rejection
}

func formatHandoffRejection(e *HandoffNotEligibleError) string {
	parts := make([]string, 0, 3)
	if n := len(e.OpenChildClaims); n > 0 {
		parts = append(parts, fmt.Sprintf("%d open child claim(s) issued by %q", n, e.AgentID))
	}
	if n := len(e.OpenAsSubjectFromIssuers); n > 0 {
		parts = append(parts, fmt.Sprintf("%d open claim(s) addressed to %q by peer issuers", n, e.AgentID))
	}
	if n := len(e.InFlightStartedArtifacts); n > 0 {
		parts = append(parts, fmt.Sprintf("%d in-flight tool/peer-interaction artifact(s)", n))
	}
	return "handoff blocked: " + strings.Join(parts, "; ")
}

// BuildHandoffClaim is the canonical constructor every dispatch site
// uses to issue an agent → agent cycle ownership transfer. The shape
// is fixed: ActionType=Handoff, Relations=[issuer, subject,
// handoff_from?]. The handoff_from relation pins this successor cycle
// to its predecessor cycle's root claim ID so the bridge's cycle
// resolver (UI_DESIGN.md §5.2) can close the predecessor and open a
// fresh top-level cycle on the successor side.
//
// predecessorClaimID may be empty when the issuer has no enclosing
// claim in scope (e.g., the guide's first-hop classification: the
// user prompt is not a claim). The bridge's cross-validator at
// ui/bridge/claims.go:259 surfaces the missing relation as a Warn,
// and the resolver still produces a correct (if uncoupled) successor
// cycle — preferable to mis-tagging the dispatch as ActionType=Task.
//
// Self-handoff is supported: when issuerAgent == targetAgent and
// predecessorClaimID is set (the scribe-driven context-exhaustion
// continuation, UI_DESIGN.md §2.2 + §5.2), the amplifier's
// self-claim filter (board_amplifier.go:382) explicitly allows the
// inbox delta through so the successor instance wakes. Without
// handoff_from, the same self-target shape would be filtered as an
// audit self-claim — that asymmetry is intentional.
func BuildHandoffClaim(
	title, description string,
	issuerAgent, targetAgent, predecessorClaimID string,
	scope []ClaimScopeEntry,
	validations []*Validation,
) Claim {
	relations := []Relation{
		{Related: issuerAgent, RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
		{Related: targetAgent, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
	}
	if predecessorID := strings.TrimSpace(predecessorClaimID); predecessorID != "" {
		relations = append(relations, Relation{
			Related:      predecessorID,
			RelatedType:  RelatedTypeClaim,
			Relationship: RelationshipHandoffFrom,
		})
	}
	return Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  ActionTypeHandoff,
		Relations:   relations,
		Validations: validations,
	}
}

// isStartedArtifactKind matches artifact-pair kinds still emitted by
// local tool and model runtime seams. Peer exchanges are lifecycle
// claims and are intentionally absent here.
func isStartedArtifactKind(kind string) bool {
	switch kind {
	case "tool_started", "llm_started":
		return true
	}
	return false
}

func isCompletedArtifactKind(kind string) bool {
	switch kind {
	case "tool_completed", "llm_completed":
		return true
	}
	return false
}
