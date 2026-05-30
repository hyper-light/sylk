package claims

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ArtifactKindProductionReadiness = "production_readiness"

	ReadinessEvidenceUnit        = "unit"
	ReadinessEvidenceIntegration = "integration"
	ReadinessEvidenceE2E         = "e2e"
	ReadinessEvidenceRace        = "race"
	ReadinessEvidenceAnalyzer    = "analyzer"
	ReadinessEvidenceMockery     = "mockery"
	ReadinessEvidenceDocs        = "docs"
	ReadinessEvidencePerformance = "performance"
	ReadinessEvidenceRunbook     = "runbook"
	ReadinessEvidenceShadowDiff  = "shadow_diff"
	ReadinessEvidenceRollback    = "rollback"

	readinessActorID = "sys:claims_readiness"
)

var ErrProductionReadinessInvalid = errors.New("production readiness evidence invalid")

type ProductionReadinessRequest struct {
	Board          *ClaimsBoard
	SessionID      string
	Evidence       []ProductionReadinessEvidence
	Waivers        []ProductionReadinessWaiver
	OpenRisks      []string
	Metadata       map[string]any
	IdempotencyKey string
}

type ProductionReadinessReport struct {
	Data    ProductionReadinessArtifactData
	Invalid []string
}

func RequiredProductionReadinessEvidence() []string {
	return []string{
		ReadinessEvidenceUnit,
		ReadinessEvidenceIntegration,
		ReadinessEvidenceE2E,
		ReadinessEvidenceRace,
		ReadinessEvidenceAnalyzer,
		ReadinessEvidenceMockery,
		ReadinessEvidenceDocs,
		ReadinessEvidencePerformance,
		ReadinessEvidenceRunbook,
		ReadinessEvidenceShadowDiff,
		ReadinessEvidenceRollback,
	}
}

func BuildProductionReadinessReport(req ProductionReadinessRequest) ProductionReadinessReport {
	evidence := normalizeReadinessEvidence(req.Evidence)
	waivers, invalid := normalizeReadinessWaivers(req.Waivers)
	invalid = append(invalid, plannedInventoryReadinessFindings(OperationsInventory())...)
	missing := missingReadinessEvidence(evidence, waivers)
	data := ProductionReadinessArtifactData{
		Ready:     len(missing) == 0 && len(invalid) == 0,
		Missing:   missing,
		Evidence:  evidence,
		Waivers:   waivers,
		OpenRisks: normalizeStringList(req.OpenRisks),
		Metadata:  cloneAnyMap(req.Metadata),
	}
	return ProductionReadinessReport{Data: data, Invalid: invalid}
}

func BuildProductionReadinessArtifact(req ProductionReadinessRequest) (*Artifact, ProductionReadinessReport, error) {
	report := BuildProductionReadinessReport(req)
	if len(report.Invalid) != 0 {
		return nil, report, fmt.Errorf("%w: %s", ErrProductionReadinessInvalid, strings.Join(report.Invalid, "; "))
	}
	artifact := &Artifact{
		AgentID:      readinessActorID,
		ArtifactName: ArtifactKindProductionReadiness,
		Kind:         ArtifactKindProductionReadiness,
		Reference:    readinessReference(report.Data),
		Metadata:     map[string]any{"ready": report.Data.Ready, "missing": append([]string(nil), report.Data.Missing...)},
	}
	if err := SetArtifactData(artifact, report.Data); err != nil {
		return nil, report, err
	}
	return artifact, report, nil
}

func RecordProductionReadinessEvidence(ctx context.Context, req ProductionReadinessRequest) (SystemEvidenceResult, ProductionReadinessReport, error) {
	artifact, report, err := BuildProductionReadinessArtifact(req)
	if err != nil {
		return SystemEvidenceResult{}, report, err
	}
	result, err := RecordInfrastructureEvidence(ctx, InfrastructureEvidenceOptions{
		Board:          req.Board,
		ActorID:        readinessActorID,
		SubjectID:      readinessActorID,
		ParentClaimID:  "",
		Operation:      "production_readiness",
		Artifact:       artifact,
		IdempotencyKey: firstNonEmpty(strings.TrimSpace(req.IdempotencyKey), "production_readiness:"+readinessHash(report.Data)),
	})
	return result, report, err
}

func normalizeReadinessEvidence(in []ProductionReadinessEvidence) []ProductionReadinessEvidence {
	best := make(map[string]ProductionReadinessEvidence, len(in))
	for _, ev := range in {
		ev.Category = strings.TrimSpace(ev.Category)
		ev.Reference = strings.TrimSpace(ev.Reference)
		ev.Status = strings.TrimSpace(ev.Status)
		ev.Metadata = cloneAnyMap(ev.Metadata)
		if ev.Category == "" || ev.Reference == "" {
			continue
		}
		if readinessEvidencePassing(ev) || best[ev.Category].Category == "" {
			best[ev.Category] = ev
		}
	}
	out := make([]ProductionReadinessEvidence, 0, len(best))
	for _, ev := range best {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}

func normalizeReadinessWaivers(in []ProductionReadinessWaiver) ([]ProductionReadinessWaiver, []string) {
	out := make([]ProductionReadinessWaiver, 0, len(in))
	var invalid []string
	for _, waiver := range in {
		normalized, err := normalizeReadinessWaiver(waiver)
		if err != "" {
			invalid = append(invalid, err)
			continue
		}
		out = append(out, normalized)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out, invalid
}

func normalizeReadinessWaiver(in ProductionReadinessWaiver) (ProductionReadinessWaiver, string) {
	in.Owner = strings.TrimSpace(in.Owner)
	in.Scope = strings.TrimSpace(in.Scope)
	in.Reason = strings.TrimSpace(in.Reason)
	in.CompensatingControl = strings.TrimSpace(in.CompensatingControl)
	if in.Owner == "" || in.Scope == "" || in.Reason == "" || in.CompensatingControl == "" || in.ExpiresAt.IsZero() {
		return ProductionReadinessWaiver{}, "waiver requires owner, scope, reason, expiry, and compensating control"
	}
	return in, ""
}

func missingReadinessEvidence(evidence []ProductionReadinessEvidence, waivers []ProductionReadinessWaiver) []string {
	present := readinessEvidenceByCategory(evidence)
	waived := readinessWaiverScopes(waivers)
	missing := make([]string, 0)
	for _, category := range RequiredProductionReadinessEvidence() {
		if readinessEvidencePassing(present[category]) || waived[category] {
			continue
		}
		missing = append(missing, category)
	}
	return missing
}

func readinessEvidenceByCategory(evidence []ProductionReadinessEvidence) map[string]ProductionReadinessEvidence {
	out := make(map[string]ProductionReadinessEvidence, len(evidence))
	for _, ev := range evidence {
		out[ev.Category] = ev
	}
	return out
}

func readinessWaiverScopes(waivers []ProductionReadinessWaiver) map[string]bool {
	out := make(map[string]bool, len(waivers))
	now := time.Now().UTC()
	for _, waiver := range waivers {
		if waiver.ExpiresAt.After(now) {
			out[waiver.Scope] = true
		}
	}
	return out
}

func readinessEvidencePassing(ev ProductionReadinessEvidence) bool {
	switch strings.TrimSpace(ev.Status) {
	case "pass", "passed", "ok", "success", "clean":
		return true
	default:
		return false
	}
}

func plannedInventoryReadinessFindings(entries []OperationsInventoryEntry) []string {
	if err := ValidateOperationsInventoryPlanning(entries, OperationsInventoryOwners()); err != nil {
		return []string{err.Error()}
	}
	return nil
}

func readinessReference(data ProductionReadinessArtifactData) string {
	if data.Ready {
		return "claims infrastructure production readiness complete"
	}
	return "claims infrastructure production readiness missing: " + strings.Join(data.Missing, ",")
}

func readinessHash(data ProductionReadinessArtifactData) string {
	artifact := &Artifact{}
	if err := SetArtifactData(artifact, data); err != nil {
		return "invalid"
	}
	return sanitizeSystemEvidenceSegment(artifact.ContentHash)
}
