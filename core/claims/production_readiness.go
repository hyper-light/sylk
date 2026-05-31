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
	ArtifactKindClaimsProductionReadiness = "claims_production_readiness"
	productionReadinessActorID            = "sys:claims_production_readiness"

	ReadinessEvidenceUnit          = "unit_tests"
	ReadinessEvidenceIntegration   = "integration_tests"
	ReadinessEvidenceE2E           = "e2e_tests"
	ReadinessEvidenceRace          = "race_tests"
	ReadinessEvidenceLint          = "lint"
	ReadinessEvidenceMockery       = "mockery_drift"
	ReadinessEvidenceRunbooks      = "runbooks"
	ReadinessEvidencePerformance   = "performance"
	ReadinessEvidenceRollout       = "rollout"
	ReadinessEvidenceRollback      = "rollback"
	ReadinessEvidenceTelemetry     = "telemetry"
	ReadinessEvidenceUIMigration   = "ui_migration"
	ReadinessEvidenceAgentAdoption = "agent_adoption"
)

const (
	ReadinessEvidenceUnitTests ClaimsOperationsReadinessEvidenceKind = ReadinessEvidenceUnit
)

var ErrProductionReadinessInvalid = errors.New("claims production readiness invalid")

type ClaimsOperationsReadinessEvidenceKind = string

type ClaimsOperationsReadinessEvidence struct {
	Kind       ClaimsOperationsReadinessEvidenceKind `json:"kind"`
	ArtifactID string                                `json:"artifact_id,omitempty"`
	Reference  string                                `json:"reference,omitempty"`
	Passed     bool                                  `json:"passed"`
	Details    string                                `json:"details,omitempty"`
}

type ProductionReadinessRequest struct {
	Board     *ClaimsBoard
	ActorID   string
	SubjectID string
	Phase     string
	Evidence  []ProductionReadinessEvidence
	Waivers   []ProductionReadinessWaiver
	OpenRisks []string
}

type ProductionReadinessReport struct {
	GeneratedAt time.Time                       `json:"generated_at"`
	Phase       string                          `json:"phase"`
	Data        ProductionReadinessArtifactData `json:"data"`
	Invalid     []string                        `json:"invalid,omitempty"`
}

type ProductionReadinessOptions = ProductionReadinessRequest

func RequiredProductionReadinessEvidence() []string {
	return []string{
		ReadinessEvidenceUnit,
		ReadinessEvidenceIntegration,
		ReadinessEvidenceE2E,
		ReadinessEvidenceRace,
		ReadinessEvidenceLint,
		ReadinessEvidenceMockery,
		ReadinessEvidenceRunbooks,
		ReadinessEvidencePerformance,
		ReadinessEvidenceRollout,
		ReadinessEvidenceRollback,
		ReadinessEvidenceTelemetry,
		ReadinessEvidenceUIMigration,
		ReadinessEvidenceAgentAdoption,
	}
}

func BuildProductionReadinessReport(req ProductionReadinessRequest) ProductionReadinessReport {
	evidence := normalizeProductionReadinessEvidence(req.Evidence)
	waivers, invalid := normalizeProductionReadinessWaivers(req.Waivers, time.Now().UTC())
	missing, failed := productionReadinessGaps(evidence, waivers)
	report := ProductionReadinessReport{
		GeneratedAt: time.Now().UTC(),
		Phase:       firstNonEmpty(strings.TrimSpace(req.Phase), "claims_operations_phase_8"),
		Invalid:     invalid,
	}
	report.Data = ProductionReadinessArtifactData{
		Ready:     len(missing) == 0 && len(failed) == 0 && len(invalid) == 0,
		Missing:   missing,
		Evidence:  evidence,
		Waivers:   waivers,
		OpenRisks: boundedStrings(req.OpenRisks, defaultTelemetryWarningLimit),
		Metadata: map[string]any{
			"phase":        report.Phase,
			"generated_at": report.GeneratedAt,
			"failed":       failed,
		},
	}
	return report
}

func BuildProductionReadinessArtifact(req ProductionReadinessRequest) (*Artifact, ProductionReadinessReport, error) {
	report := BuildProductionReadinessReport(req)
	if !report.Data.Ready {
		return nil, report, fmt.Errorf("%w: missing=%v invalid=%v failed=%v", ErrProductionReadinessInvalid, report.Data.Missing, report.Invalid, report.Data.Metadata["failed"])
	}
	artifact := &Artifact{
		ArtifactName: "claims_production_readiness",
		Kind:         ArtifactKindClaimsProductionReadiness,
		Reference:    "claims operations production readiness report",
		Metadata: map[string]any{
			"phase":          report.Phase,
			"evidence_count": len(report.Data.Evidence),
			"risk_count":     len(report.Data.OpenRisks),
		},
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
	if req.Board == nil {
		return SystemEvidenceResult{}, report, fmt.Errorf("%w: board is required", ErrProductionReadinessInvalid)
	}
	result, err := RecordInfrastructureEvidence(ctx, InfrastructureEvidenceOptions{
		Board:          req.Board,
		ActorID:        firstNonEmpty(req.ActorID, productionReadinessActorID),
		SubjectID:      firstNonEmpty(req.SubjectID, productionReadinessActorID),
		Operation:      "claims_production_readiness",
		IdempotencyKey: "claims_production_readiness:" + report.Phase,
		Artifact:       artifact,
	})
	return result, report, err
}

func normalizeProductionReadinessEvidence(in []ProductionReadinessEvidence) []ProductionReadinessEvidence {
	out := make([]ProductionReadinessEvidence, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item.Category = strings.TrimSpace(item.Category)
		if item.Category == "" {
			continue
		}
		if _, ok := seen[item.Category]; ok {
			continue
		}
		seen[item.Category] = struct{}{}
		item.Reference = boundedString(strings.TrimSpace(item.Reference), 256)
		item.Status = readinessStatus(item.Status)
		item.Metadata = boundedAnyMap(item.Metadata, defaultTelemetryWarningLimit)
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}

func normalizeProductionReadinessWaivers(in []ProductionReadinessWaiver, now time.Time) ([]ProductionReadinessWaiver, []string) {
	out := make([]ProductionReadinessWaiver, 0, len(in))
	invalid := make([]string, 0)
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item.Owner = boundedString(strings.TrimSpace(item.Owner), 128)
		item.Scope = strings.TrimSpace(item.Scope)
		item.Reason = boundedString(strings.TrimSpace(item.Reason), 512)
		item.CompensatingControl = boundedString(strings.TrimSpace(item.CompensatingControl), 512)
		if _, ok := seen[item.Scope]; ok {
			invalid = append(invalid, "duplicate waiver scope "+item.Scope)
			continue
		}
		seen[item.Scope] = struct{}{}
		if reason := productionReadinessWaiverInvalidReason(item, now); reason != "" {
			invalid = append(invalid, reason)
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out, invalid
}

func productionReadinessWaiverInvalidReason(item ProductionReadinessWaiver, now time.Time) string {
	switch {
	case item.Scope == "":
		return "waiver scope is required"
	case !productionReadinessRequired(item.Scope):
		return "waiver scope " + item.Scope + " is not required evidence"
	case item.Owner == "":
		return "waiver owner is required for " + item.Scope
	case item.Reason == "":
		return "waiver reason is required for " + item.Scope
	case item.CompensatingControl == "":
		return "waiver compensating control is required for " + item.Scope
	case item.ExpiresAt.IsZero() || !item.ExpiresAt.After(now):
		return "waiver expiration must be in the future for " + item.Scope
	default:
		return ""
	}
}

func productionReadinessGaps(evidence []ProductionReadinessEvidence, waivers []ProductionReadinessWaiver) ([]string, []string) {
	byCategory := make(map[string]ProductionReadinessEvidence, len(evidence))
	for _, item := range evidence {
		byCategory[item.Category] = item
	}
	waived := make(map[string]struct{}, len(waivers))
	for _, waiver := range waivers {
		waived[waiver.Scope] = struct{}{}
	}
	missing := make([]string, 0)
	failed := make([]string, 0)
	for _, category := range RequiredProductionReadinessEvidence() {
		if _, ok := waived[category]; ok {
			continue
		}
		item, ok := byCategory[category]
		if !ok {
			missing = append(missing, category)
			continue
		}
		if item.Status != "passed" {
			failed = append(failed, category)
		}
	}
	return missing, failed
}

func productionReadinessRequired(category string) bool {
	for _, required := range RequiredProductionReadinessEvidence() {
		if required == category {
			return true
		}
	}
	return false
}

func readinessStatus(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "pass", "passed", "success", "ok", "ready":
		return "passed"
	case "fail", "failed", "error", "errored":
		return "failed"
	default:
		if raw == "" {
			return "failed"
		}
		return boundedString(raw, 64)
	}
}

func boundedAnyMap(in map[string]any, limit int) map[string]any {
	if len(in) == 0 || limit <= 0 {
		return nil
	}
	out := make(map[string]any, min(len(in), limit))
	count := 0
	for key, value := range in {
		if count >= limit {
			break
		}
		key = boundedString(strings.TrimSpace(key), 96)
		if key == "" {
			continue
		}
		out[key] = value
		count++
	}
	return out
}

func ClaimsOperationsReadinessEvidenceAsProduction(in []ClaimsOperationsReadinessEvidence) []ProductionReadinessEvidence {
	out := make([]ProductionReadinessEvidence, 0, len(in))
	for _, item := range in {
		status := "failed"
		if item.Passed {
			status = "passed"
		}
		out = append(out, ProductionReadinessEvidence{
			Category:  strings.TrimSpace(item.Kind),
			Reference: firstNonEmpty(item.ArtifactID, item.Reference),
			Status:    status,
			Metadata: map[string]any{
				"details": boundedString(strings.TrimSpace(item.Details), 256),
			},
		})
	}
	return out
}
