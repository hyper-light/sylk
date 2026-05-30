package guardian

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

func TestGuardianContentScanSkillUsesServiceClaimAndPreservesValidation(t *testing.T) {
	g := newTestGuardian(t, &stubGuardianProvider{})
	sessionID := "guardian-service-content"
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "guardian-service-board", SessionID: sessionID, TaskID: "task"})
	claims.DefaultSessionBoardRegistry().ReplaceForReason(sessionID, board, "guardian service test")
	defer claims.DefaultSessionBoardRegistry().Remove(sessionID)
	g.activeSessionID = sessionID

	invalid := g.skills.Invoke(context.Background(), "content_scan", []byte(`{"action":"scan_output"}`))
	if invalid.Success {
		t.Fatalf("invalid content scan succeeded: %+v", invalid)
	}
	if !strings.Contains(invalid.Error, "content is required for scan_output") {
		t.Fatalf("invalid error = %q, want content requirement", invalid.Error)
	}
	if got := board.Projection().TotalClaims; got != 0 {
		t.Fatalf("invalid content scan claims = %d, want none", got)
	}

	valid := g.skills.Invoke(context.Background(), "content_scan", []byte(`{"action":"scan_output","content":"ordinary build output"}`))
	if !valid.Success {
		t.Fatalf("valid content scan failed: %s", valid.Error)
	}
	proj := board.Projection()
	if proj.TotalClaims != 1 || proj.TotalTestaments != 1 {
		t.Fatalf("projection claims/testaments = %d/%d, want service claim and testament", proj.TotalClaims, proj.TotalTestaments)
	}
	claim := proj.Claims[0]
	if claims.SubjectAgentID(claim.Relations) != guardianServiceParticipantID || claim.LifecycleStatus != claims.ClaimLifecycleSatisfied {
		t.Fatalf("guardian service claim = %+v, want satisfied guardian claim", claim)
	}
	artifact := proj.Testaments[0].Artifacts[0]
	data, err := claims.ArtifactData[claims.GuardianDecisionArtifactData](artifact)
	if err != nil {
		t.Fatalf("guardian artifact data: %v", err)
	}
	if data.Operation != claims.GuardianToolContentScan || !data.Allowed || data.Status != claims.InfrastructureStatusOK {
		t.Fatalf("guardian service data = %+v, want allowed content scan evidence", data)
	}
}
