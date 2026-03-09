package fetch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/container/security"
)

// InspectFunc is called by the pipeline to have the Guardian inspect
// quarantined content. Returns the verdict and any findings.
type InspectFunc func(ctx context.Context, entry *QuarantineEntry) (QuarantineVerdict, []InspectionFinding, error)

// IngestFunc is called to ingest approved content into the knowledge
// graph and document DB via JointDocIngestion.
type IngestFunc func(ctx context.Context, entry *QuarantineEntry, provenance *Provenance, extracted *ExtractResult) error

// Pipeline orchestrates the full fetch-to-ingest flow:
//
//	SecurityContext check → FetchPolicy → ConsentGate → HTTP fetch →
//	Quarantine → Guardian inspection → Ingest (if clean/approved)
type Pipeline struct {
	policy     *FetchPolicy
	consent    *ConsentGate
	quarantine *QuarantineBuffer
	client     *Client
	extractor  *ContentExtractor
	inspect    InspectFunc
	ingest     IngestFunc
	logger     *slog.Logger
}

// PipelineConfig configures the fetch pipeline.
type PipelineConfig struct {
	Policy     *FetchPolicy
	Consent    *ConsentGate
	Quarantine *QuarantineBuffer
	Client     *Client
	Extractor  *ContentExtractor
	Inspect    InspectFunc
	Ingest     IngestFunc
	Logger     *slog.Logger
}

// NewPipeline creates a fetch pipeline with all dependencies.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		policy:     cfg.Policy,
		consent:    cfg.Consent,
		quarantine: cfg.Quarantine,
		client:     cfg.Client,
		extractor:  cfg.Extractor,
		inspect:    cfg.Inspect,
		ingest:     cfg.Ingest,
		logger:     logger,
	}
}

// FetchRequest describes what to fetch and why.
type FetchRequest struct {
	URL         string                    // Target URL
	SourceAgent string                    // Agent requesting the fetch
	Reason      string                    // Human-readable reason for the fetch
	SessionID   string                    // Session context
	SecurityCtx *security.SecurityContext // Capability check
}

// FetchResponse carries the outcome of a pipeline execution.
type FetchResponse struct {
	Success         bool                `json:"success"`
	URL             string              `json:"url"`
	Verdict         QuarantineVerdict   `json:"verdict"`
	Findings        []InspectionFinding `json:"findings,omitempty"`
	Provenance      *Provenance         `json:"provenance,omitempty"`
	Ingested        bool                `json:"ingested"`
	Extracted       *ExtractResult      `json:"extracted,omitempty"`
	ExtractionError string              `json:"extraction_error,omitempty"`
	Error           string              `json:"error,omitempty"`
	Duration        time.Duration       `json:"duration"`
}

// Execute runs the full pipeline for a single URL. Returns a FetchResponse
// regardless of outcome — check Success and Error fields.
func (p *Pipeline) Execute(ctx context.Context, req *FetchRequest) *FetchResponse {
	start := time.Now()
	resp := &FetchResponse{URL: req.URL}

	defer func() {
		resp.Duration = time.Since(start)
	}()

	// Step 1: SecurityContext capability check.
	if req.SecurityCtx != nil {
		domain := extractDomain(req.URL)
		authResult := req.SecurityCtx.Authorize(ctx, security.AuthorizationRequest{
			Action: security.ActionNetworkEgress,
			Target: domain,
		})
		if !authResult.Allowed {
			resp.Error = fmt.Sprintf("network egress denied by security context: %s", authResult.Reason)
			p.logger.Warn("fetch denied by security context",
				"url", req.URL, "agent", req.SourceAgent, "reason", authResult.Reason)
			return resp
		}
	}

	// Step 2: FetchPolicy evaluation.
	policyResult := p.policy.Evaluate(req.URL)
	if policyResult.Verdict == VerdictDeny {
		resp.Error = fmt.Sprintf("fetch denied by policy: %s", policyResult.Reason)
		p.logger.Warn("fetch denied by policy",
			"url", req.URL, "agent", req.SourceAgent, "reason", policyResult.Reason)
		return resp
	}

	// Step 3: Consent gate.
	consentID := ""
	decision := p.consent.Check(policyResult.Domain)
	switch decision {
	case ConsentGranted:
		consentID = "auto"
	case ConsentDenied, ConsentRevoked:
		resp.Error = "fetch denied: user consent revoked for domain " + policyResult.Domain
		return resp
	case ConsentRequired:
		proposal := &FetchProposal{
			ID:             "fp_" + policyResult.Domain + "_" + fmt.Sprintf("%d", time.Now().UnixMilli()),
			URL:            req.URL,
			Domain:         policyResult.Domain,
			SourceAgent:    req.SourceAgent,
			Reason:         req.Reason,
			RiskAssessment: "policy: passed",
			Timestamp:      time.Now(),
		}
		consentResult, err := p.consent.RequestConsent(ctx, proposal)
		if err != nil {
			resp.Error = fmt.Sprintf("consent request failed: %v", err)
			return resp
		}
		if !consentResult.Granted {
			resp.Error = "fetch denied by user: " + consentResult.Reason
			return resp
		}
		consentID = proposal.ID
	}

	// Step 4: HTTP fetch.
	fetchResult, err := p.client.Fetch(ctx, req.URL)
	if err != nil {
		resp.Error = fmt.Sprintf("fetch failed: %v", err)
		p.logger.Warn("fetch failed", "url", req.URL, "error", err)
		return resp
	}

	// Step 5: Quarantine.
	entry := fetchResult.ToQuarantineEntry(req.SourceAgent, consentID)
	if err := p.quarantine.Hold(entry); err != nil {
		resp.Error = fmt.Sprintf("quarantine failed: %v", err)
		return resp
	}

	// Step 6: Guardian inspection.
	verdict, findings, err := p.inspect(ctx, entry)
	if err != nil {
		p.quarantine.Release(entry.ID)
		resp.Error = fmt.Sprintf("inspection failed: %v", err)
		return resp
	}

	p.quarantine.SetVerdict(entry.ID, verdict, findings, "guardian")
	resp.Verdict = verdict
	resp.Findings = findings

	// Step 7: Act on verdict.
	switch verdict {
	case VerdictBlocked:
		p.quarantine.Release(entry.ID)
		resp.Error = "content blocked by Guardian inspection"
		p.logger.Warn("content blocked",
			"url", req.URL, "findings", len(findings))
		return resp

	case VerdictFlagged:
		// Flagged content stays in quarantine for user review.
		// The Academic will receive a response indicating findings
		// and must surface them to the user.
		resp.Success = false
		resp.Error = fmt.Sprintf("content flagged: %d findings require user review", len(findings))
		return resp

	case VerdictClean:
		// Release from quarantine and ingest.
		released, _ := p.quarantine.Release(entry.ID)
		if released == nil {
			resp.Error = "quarantine release failed"
			return resp
		}

		provenance := &Provenance{
			FetchURL:     req.URL,
			FetchedAt:    fetchResult.FetchedAt,
			ContentHash:  fetchResult.ContentHash,
			ConsentID:    consentID,
			InspectionID: entry.ID,
			Verdict:      "clean",
			FindingCount: len(findings),
			IngestedAt:   time.Now(),
			SourceAgent:  req.SourceAgent,
			ApprovedBy:   approvedByFromConsent(consentID),
		}

		extracted, extractErr := p.extract(released)
		if extractErr != nil {
			resp.ExtractionError = extractErr.Error()
		} else {
			resp.Extracted = extracted
		}

		if p.ingest != nil {
			if err := p.ingest(ctx, released, provenance, extracted); err != nil {
				resp.Error = fmt.Sprintf("ingestion failed: %v", err)
				return resp
			}
			resp.Ingested = true
		}

		resp.Success = true
		resp.Provenance = provenance
		p.logger.Info("content ingested",
			"url", req.URL, "size", fetchResult.BytesRead,
			"domain", policyResult.Domain)
		return resp
	}

	resp.Error = "unexpected verdict"
	return resp
}

func (p *Pipeline) extract(entry *QuarantineEntry) (*ExtractResult, error) {
	if entry == nil {
		return nil, fmt.Errorf("quarantine entry is required")
	}
	extractor := p.extractor
	if extractor == nil {
		extractor = NewContentExtractor()
	}
	return extractor.Extract(entry.Content, entry.ContentType)
}

func extractDomain(rawURL string) string {
	// Quick extraction without full URL parse — policy.Evaluate does the real parse.
	after := rawURL
	if idx := indexOf(after, "://"); idx >= 0 {
		after = after[idx+3:]
	}
	if idx := indexOf(after, "/"); idx >= 0 {
		after = after[:idx]
	}
	if idx := indexOf(after, ":"); idx >= 0 {
		after = after[:idx]
	}
	return after
}

func indexOf(s, substr string) int {
	for i := range len(s) - len(substr) + 1 {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func approvedByFromConsent(consentID string) string {
	if consentID == "auto" {
		return "config"
	}
	if consentID == "" {
		return "auto"
	}
	return "user"
}
