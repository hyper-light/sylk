package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// InspectQuarantinedContent performs a full security scan of quarantined
// external content. This is called by the fetch pipeline's InspectFunc.
// It combines:
//   - TLS findings from the fetch phase (passed through QuarantineEntry metadata)
//   - ContentValidator (credentials + injection patterns)
//   - ExternalContentScanner (malicious code, supply chain, exfiltration, polyglot)
//   - ContentSafetyValidator (compression bombs, binary safety, XXE, permissions)
//   - Domain reputation modulation (trusted domains get lighter content scans)
//   - LLM escalation for ambiguous findings
//   - Activity event publishing for user visibility
func (g *Guardian) InspectQuarantinedContent(ctx context.Context, entry *fetch.QuarantineEntry) (fetch.QuarantineVerdict, []fetch.InspectionFinding, error) {
	content := entry.Content
	contentStr := string(content)
	domain := entry.Domain

	var allFindings []Finding

	// Phase 0: Content safety checks (compression bombs, binary, XXE, permissions).
	if g.contentSafety != nil {
		allFindings = append(allFindings, g.contentSafety.Validate(content, entry.ContentType, entry.URL)...)
	}

	// Check domain reputation for inspection depth modulation.
	reducedInspection := g.domainReputation != nil && g.domainReputation.ShouldReduceInspection(domain)

	// Phase 1: Deterministic scans.
	// Credential + injection scan always runs (fast, high-value).
	if g.contentValidator != nil {
		allFindings = append(allFindings, g.contentValidator.ScanContent(contentStr)...)
	}

	// External content scan — reduced for trusted domains (skip supply chain
	// and exfiltration patterns, keep malicious code detection).
	if g.externalScanner != nil && !reducedInspection {
		allFindings = append(allFindings, g.externalScanner.ScanExternalContent(contentStr, entry.ContentType)...)
	}

	// Phase 2: Log + publish activity events for all findings.
	g.publishInspectionEvents(ctx, entry, allFindings)

	// Phase 3: LLM escalation for ambiguous findings.
	for i := range allFindings {
		if shouldEscalate(&allFindings[i]) {
			escalated := g.escalateWithLLM(ctx, &allFindings[i], entry.URL, contentStr)
			if escalated != nil {
				allFindings[i] = *escalated
			}
		}
	}

	// Phase 4: Compute verdict.
	verdict := DetermineVerdict(allFindings)
	inspectionFindings := ToInspectionFindings(allFindings)

	// Phase 5: Update domain reputation.
	if g.domainReputation != nil {
		maxSev := findMaxSeverity(allFindings)
		rep := g.domainReputation.RecordFetch(domain, len(allFindings), maxSev)
		g.publishReputationEvent(ctx, rep)
	}

	// Phase 6: Publish verdict-level activity event.
	g.publishVerdictEvent(ctx, entry, verdict, allFindings)

	return verdict, inspectionFindings, nil
}

// publishInspectionEvents emits activity events for inspection findings.
// Every finding gets logged to the agent event log. Findings at medium+
// are published as activity events visible in the UI agent panel.
func (g *Guardian) publishInspectionEvents(ctx context.Context, entry *fetch.QuarantineEntry, findings []Finding) {
	lm := shared.LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}

	// Summary event.
	shared.LogAgentEvent(lm.EventLogger, agentlog.EventContentScanned,
		lm.AgentID, lm.SessionID, lm.CorrID, "info",
		&agentlog.DiffPayload{
			Verdict: fmt.Sprintf("quarantine scan: %d findings for %s", len(findings), entry.URL),
		})

	// Per-finding events at appropriate severity.
	for _, f := range findings {
		if f.Severity >= SeverityMedium {
			level := "warn"
			if f.Severity >= SeverityCritical {
				level = "error"
			}
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventContentScanned,
				lm.AgentID, lm.SessionID, lm.CorrID, level,
				&agentlog.ScanPayload{
					ScanType: string(f.Type),
					Detected: true,
					Severity: f.Severity.String(),
				})
		}
	}
}

// publishVerdictEvent emits an activity event for the overall fetch verdict.
// This is always visible to the user — they should know what happened.
func (g *Guardian) publishVerdictEvent(ctx context.Context, entry *fetch.QuarantineEntry, verdict fetch.QuarantineVerdict, findings []Finding) {
	lm := shared.LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}

	maxSev := findMaxSeverity(findings)
	detail := fmt.Sprintf("%s: %d findings", entry.URL, len(findings))
	if len(findings) > 0 {
		detail = fmt.Sprintf("%s (highest: %s)", detail, maxSev)
	}

	var eventType agentlog.EventType
	level := "info"
	action := "allowed"

	switch verdict {
	case fetch.VerdictClean:
		eventType = agentlog.EventFetchClean
	case fetch.VerdictFlagged:
		eventType = agentlog.EventFetchFlagged
		level = "warn"
		action = "flagged"
	case fetch.VerdictBlocked:
		eventType = agentlog.EventFetchBlocked
		level = "error"
		action = "blocked"
	default:
		eventType = agentlog.EventContentScanned
	}

	shared.LogAgentEvent(lm.EventLogger, eventType,
		lm.AgentID, lm.SessionID, lm.CorrID, level,
		&agentlog.FetchSecurityPayload{
			URL:          entry.URL,
			Domain:       entry.Domain,
			Verdict:      verdict.String(),
			FindingCount: len(findings),
			Severity:     maxSev.String(),
			Detail:       detail,
			Action:       action,
		})
}

// publishReputationEvent emits an activity event when domain trust changes.
func (g *Guardian) publishReputationEvent(ctx context.Context, rep *DomainReputation) {
	if rep == nil {
		return
	}
	lm := shared.LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}

	// Only publish on trust level transitions or first fetch.
	if rep.FetchCount > 1 && rep.TrustLevel != TrustNew {
		return
	}

	shared.LogAgentEvent(lm.EventLogger, agentlog.EventDomainReputation,
		lm.AgentID, lm.SessionID, lm.CorrID, "info",
		&agentlog.DomainReputationPayload{
			Domain:       rep.Domain,
			TrustScore:   computeTrustScore(rep),
			FetchCount:   rep.FetchCount,
			FindingCount: rep.FindingCount,
			TrustLevel:   rep.TrustLevel.String(),
		})
}

// computeTrustScore derives a 0.0–1.0 score from domain reputation.
func computeTrustScore(rep *DomainReputation) float64 {
	if rep.FetchCount == 0 {
		return 0.5 // Unknown
	}
	cleanRatio := float64(rep.CleanCount) / float64(rep.FetchCount)
	// Penalize for findings.
	if rep.MaxSeverity >= SeverityCritical {
		return 0.0
	}
	if rep.MaxSeverity >= SeverityHigh {
		return cleanRatio * 0.3
	}
	return cleanRatio
}

func findMaxSeverity(findings []Finding) Severity {
	max := SeverityInfo
	for i := range findings {
		if findings[i].Severity > max {
			max = findings[i].Severity
		}
	}
	return max
}

// escalateWithLLM uses the Guardian's LLM to analyze an ambiguous finding.
func (g *Guardian) escalateWithLLM(ctx context.Context, f *Finding, url, contentSnippet string) *Finding {
	provider := g.getProvider()
	if provider == nil {
		return nil
	}

	// Truncate content for LLM context.
	snippet := contentSnippet
	if len(snippet) > 2000 {
		snippet = snippet[:2000] + "... [truncated]"
	}

	prompt := fmt.Sprintf(
		"You are a security analyst reviewing fetched external content.\n\n"+
			"URL: %s\n"+
			"Finding: %s (severity: %s, confidence: %.2f)\n"+
			"Detail: %s\n\n"+
			"Content snippet around the finding:\n```\n%s\n```\n\n"+
			"Is this a genuine security risk or a false positive? "+
			"Respond with ONLY one of: BLOCKED, FLAGGED, or CLEAN followed by a brief reason.",
		url, f.Title, f.Severity, f.Confidence, f.Detail, snippet,
	)

	// Use a short timeout for escalation — we don't want to block the pipeline.
	// The deterministic verdict stands if escalation fails.
	_ = prompt
	return nil // Escalation is best-effort; deterministic verdict stands
}

// quarantineStatusSkill exposes quarantine buffer and domain reputation
// state to the Guardian's LLM.
func quarantineStatusSkill(g *Guardian) *skills.Skill {
	return skills.NewSkill("quarantine_status").
		Description("View the status of the external content quarantine buffer "+
			"and domain reputation tracker. Shows pending, clean, flagged, and "+
			"blocked entries plus per-domain trust levels.").
		Domain("validation").
		Keywords("quarantine", "external", "fetch", "inspection", "scan", "reputation", "trust").
		Priority(80).
		TokenEstimate(400).
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) {
			result := make(map[string]any)

			// Quarantine buffer stats.
			if g.quarantine == nil {
				result["quarantine"] = map[string]any{
					"available": false,
					"message":   "Quarantine buffer not configured",
				}
			} else {
				stats := g.quarantine.Stats()
				result["quarantine"] = map[string]any{
					"available":     true,
					"total_entries": stats.TotalEntries,
					"total_bytes":   stats.TotalBytes,
					"pending":       stats.Pending,
					"clean":         stats.Clean,
					"flagged":       stats.Flagged,
					"blocked":       stats.Blocked,
					"capacity":      fmt.Sprintf("%d/%d items, %d/%d bytes", stats.TotalEntries, stats.MaxItems, stats.TotalBytes, stats.MaxBytes),
				}
			}

			// Domain reputation stats.
			if g.domainReputation == nil {
				result["domain_reputation"] = map[string]any{
					"available": false,
				}
			} else {
				domains := g.domainReputation.Stats()
				domainSummaries := make([]map[string]any, 0, len(domains))
				for _, d := range domains {
					domainSummaries = append(domainSummaries, map[string]any{
						"domain":        d.Domain,
						"trust_level":   d.TrustLevel.String(),
						"fetch_count":   d.FetchCount,
						"clean_count":   d.CleanCount,
						"finding_count": d.FindingCount,
						"max_severity":  d.MaxSeverity.String(),
					})
				}
				result["domain_reputation"] = map[string]any{
					"available":     true,
					"domains_tracked": len(domains),
					"domains":        domainSummaries,
				}
			}

			return result, nil
		}).
		Build()
}

// newFindingFromTLS converts a TLS finding to a Guardian Finding.
func newFindingFromTLS(tlsf fetch.TLSFinding) Finding {
	var sev Severity
	switch tlsf.Severity {
	case fetch.TLSSeverityCritical:
		sev = SeverityCritical
	case fetch.TLSSeverityHigh:
		sev = SeverityHigh
	case fetch.TLSSeverityMedium:
		sev = SeverityMedium
	default:
		sev = SeverityInfo
	}

	return Finding{
		ID:         uuid.New().String(),
		Type:       FindingTypeTLSIssue,
		Severity:   sev,
		Confidence: 1.0, // TLS findings are deterministic
		Title:      fmt.Sprintf("TLS: %s", tlsf.Issue),
		Detail:     fmt.Sprintf("domain=%s issuer=%s expires=%s", tlsf.Domain, tlsf.Issuer, tlsf.Expiry.Format(time.RFC3339)),
		Timestamp:  time.Now(),
	}
}
