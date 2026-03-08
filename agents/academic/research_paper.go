package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/google/uuid"
)

const (
	architectProposalAction     = "proposal"
	archivalistStorePaperAction = "store_research_paper"
	defaultPaperVersion         = 1
)

type authorResearchPaperParams struct {
	Topic              string   `json:"topic"`
	Context            string   `json:"context,omitempty"`
	Title              string   `json:"title,omitempty"`
	ResearchSlug       string   `json:"research_slug,omitempty"`
	Version            int      `json:"version,omitempty"`
	SessionID          string   `json:"session_id,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	Invariants         []string `json:"invariants,omitempty"`
	OpenQuestions      []string `json:"open_questions,omitempty"`
	RelatedTopics      []string `json:"related_topics,omitempty"`
	ArchitectSummary   string   `json:"architect_summary,omitempty"`
	HandoffToArchitect bool     `json:"handoff_to_architect,omitempty"`
	StoreInArchivalist bool     `json:"store_in_archivalist,omitempty"`
}

func authorResearchPaperSkill(a *Academic) *skills.Skill {
	return skills.NewSkill("author_research_paper").
		Description("Research a topic, distill it into an architect-grade proposal artifact, optionally store it in the Archivalist, and optionally hand it off to the Architect.").
		Domain("research").
		Keywords("research paper", "proposal", "architect", "handoff", "distill", "architecture").
		Priority(92).
		StringParam("topic", "Primary topic or problem to research", true).
		StringParam("context", "Additional constraints or codebase context", false).
		StringParam("title", "Optional explicit paper title", false).
		StringParam("research_slug", "Stable identifier for the proposal", false).
		IntParam("version", "Explicit version override; defaults to next available version", false).
		StringParam("session_id", "Session identifier for artifact placement", false).
		ArrayParam("constraints", "Hard constraints that must survive handoff", "string", false).
		ArrayParam("invariants", "Invariants the resulting design must preserve", "string", false).
		ArrayParam("open_questions", "Unresolved questions to carry into planning", "string", false).
		ArrayParam("related_topics", "Adjacent topics for future research", "string", false).
		StringParam("architect_summary", "Optional planning-specific summary override", false).
		BoolParam("handoff_to_architect", "Dispatch the resulting proposal to the Architect", false).
		BoolParam("store_in_archivalist", "Persist the proposal in the Archivalist", false).
		Usage("Use when research should end in a reusable architectural proposal artifact rather than a transient answer. This skill writes a versioned markdown research paper under the active Sylk session, can persist it to the Archivalist for later retrieval, and can hand the paper to the Architect as a formal proposal input.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params authorResearchPaperParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Topic) == "" {
				return nil, fmt.Errorf("topic is required")
			}
			return a.authorResearchPaper(ctx, &params)
		}).
		Build()
}

func (a *Academic) authorResearchPaper(ctx context.Context, params *authorResearchPaperParams) (map[string]any, error) {
	if params == nil {
		return nil, fmt.Errorf("research paper parameters are required")
	}

	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(a.config.SessionID)
	}
	if sessionID == "" {
		sessionID = "default"
	}

	queryText := strings.TrimSpace(params.Topic)
	if contextText := strings.TrimSpace(params.Context); contextText != "" {
		queryText = fmt.Sprintf("%s\n\nContext:\n%s", queryText, contextText)
	}

	result, err := a.Research(ctx, &ResearchQuery{
		Query:     queryText,
		Intent:    IntentRecall,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}

	librarianEvidence := a.bestEffortConsultation(
		ctx,
		"librarian",
		fmt.Sprintf("Assess codebase applicability and existing implementation patterns for: %s", params.Topic),
		params.Context,
		sessionID,
	)
	archivalEvidence := a.bestEffortConsultation(
		ctx,
		"archivalist",
		fmt.Sprintf("Recall prior decisions, issues, and historical constraints relevant to: %s", params.Topic),
		params.Context,
		sessionID,
	)

	paper, err := a.buildResearchPaper(params, result, librarianEvidence, archivalEvidence, sessionID)
	if err != nil {
		return nil, err
	}
	path, err := a.writeResearchPaperArtifact(paper)
	if err != nil {
		return nil, err
	}
	paper.PaperPath = path

	var warnings []string
	stored := false
	storeRequested := params.StoreInArchivalist || !params.HandoffToArchitect
	if storeRequested {
		if err := a.dispatchResearchPaperToArchivalist(ctx, paper); err != nil {
			warnings = append(warnings, "archivalist storage skipped: "+err.Error())
		} else {
			stored = true
		}
	}

	handoffDispatched := false
	if params.HandoffToArchitect {
		if err := a.dispatchResearchPaperToArchitect(ctx, paper); err != nil {
			warnings = append(warnings, "architect handoff skipped: "+err.Error())
		} else {
			handoffDispatched = true
		}
	}

	return map[string]any{
		"paper_id":                paper.ID,
		"research_slug":           paper.ResearchSlug,
		"version":                 paper.Version,
		"title":                   paper.Title,
		"paper_path":              paper.PaperPath,
		"stored_in_archivalist":   stored,
		"handoff_to_architect":    handoffDispatched,
		"recommended_option_id":   recommendedOptionID(paper),
		"topics_researched":       paper.TopicsResearched,
		"open_questions":          paper.OpenQuestions,
		"architect_handoff_ready": paper.ArchitectHandoff != nil,
		"warnings":                warnings,
	}, nil
}

func (a *Academic) bestEffortConsultation(
	ctx context.Context,
	target string,
	query string,
	scope string,
	sessionID string,
) *shared.ConsultationEvidence {
	if a.bus == nil || !a.running {
		return nil
	}
	evidence, err := a.requestConsultation(ctx, target, query, scope, sessionID)
	if err != nil {
		return failedConsultEvidence(target, query, scope, "", err)
	}
	return evidence
}

func (a *Academic) buildResearchPaper(
	params *authorResearchPaperParams,
	result *ResearchResult,
	librarianEvidence *shared.ConsultationEvidence,
	archivalEvidence *shared.ConsultationEvidence,
	sessionID string,
) (*AcademicResearchPaper, error) {
	if params == nil {
		return nil, fmt.Errorf("research paper parameters are required")
	}
	if result == nil {
		return nil, fmt.Errorf("research result is required")
	}

	researchSlug := normalizeResearchSlug(params.ResearchSlug, params.Title, params.Topic)
	version := params.Version
	if version <= 0 {
		nextVersion, err := a.nextResearchPaperVersion(sessionID, researchSlug)
		if err != nil {
			return nil, err
		}
		version = nextVersion
	}

	recommendations := make([]string, 0, len(result.Recommendations))
	for _, rec := range result.Recommendations {
		recommendations = append(recommendations, rec.Title)
	}
	if len(recommendations) == 0 {
		recommendations = extractBullets(summarizeFindings(result.Findings), 5)
	}

	options, rejected, recommendedID := buildOptionMatrix(result)
	architectSummary := strings.TrimSpace(params.ArchitectSummary)
	if architectSummary == "" {
		architectSummary = firstNonEmpty(
			consultationSummary(librarianEvidence),
			summarizeFindings(result.Findings),
			params.Context,
			params.Topic,
		)
	}

	paper := &AcademicResearchPaper{
		ID:               uuid.NewString(),
		Timestamp:        time.Now().UTC(),
		SessionID:        sessionID,
		ContextUsage:     0,
		ResearchSlug:     researchSlug,
		Version:          version,
		Title:            researchPaperTitle(params, researchSlug),
		Abstract:         firstNonEmpty(summarizeFindings(result.Findings), params.Context, params.Topic),
		ProblemFraming:   firstNonEmpty(strings.TrimSpace(params.Context), params.Topic),
		Constraints:      toResearchConstraints(params.Constraints),
		Invariants:       toResearchInvariants(params.Invariants),
		TopicsResearched: mergeUnique([]string{params.Topic}, params.RelatedTopics),
		KeyFindings:      append([]Finding(nil), result.Findings...),
		SourcesCited:     a.collectPaperSources(result),
		OptionMatrix:     options,
		RejectedOptions:  rejected,
		TradeOffs: mergeUnique(
			extractBullets(consultationSummary(librarianEvidence), 4),
			extractBullets(consultationSummary(archivalEvidence), 4),
		),
		CodebaseApplicability: buildCodebaseApplicability(librarianEvidence),
		Recommendations:       recommendations,
		MigrationConcerns:     extractBullets(consultationSummary(archivalEvidence), 4),
		OperationalConcerns:   extractBullets(consultationSummary(librarianEvidence), 4),
		OpenQuestions: mergeUnique(
			params.OpenQuestions,
			extractBullets(consultationSummary(archivalEvidence), 4),
		),
		RelatedTopics: params.RelatedTopics,
		ArchitectHandoff: &ArchitectHandoff{
			PlanningSummary:     architectSummary,
			RecommendedOptionID: recommendedID,
			RequiredDecisions:   mergeUnique(params.OpenQuestions, rejected),
			SuggestedTasks: mergeUnique(
				recommendations,
				requiredChanges(buildCodebaseApplicability(librarianEvidence)),
			),
			AcceptanceSignals: mergeUnique(
				extractBullets(architectSummary, 3),
				extractBullets(summarizeFindings(result.Findings), 3),
			),
		},
	}

	if len(paper.TopicsResearched) == 0 {
		paper.TopicsResearched = []string{params.Topic}
	}
	if paper.Abstract == "" {
		paper.Abstract = params.Topic
	}
	if paper.ArchitectHandoff != nil && strings.TrimSpace(paper.ArchitectHandoff.PlanningSummary) == "" {
		paper.ArchitectHandoff.PlanningSummary = paper.Abstract
	}

	return paper, nil
}

func (a *Academic) collectPaperSources(result *ResearchResult) []Source {
	if result == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(result.SourcesConsulted))
	for _, id := range result.SourcesConsulted {
		if strings.TrimSpace(id) != "" {
			seen[id] = struct{}{}
		}
	}
	for _, finding := range result.Findings {
		for _, id := range finding.SourceIDs {
			if strings.TrimSpace(id) != "" {
				seen[id] = struct{}{}
			}
		}
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	sources := make([]Source, 0, len(seen))
	for id := range seen {
		if src, ok := a.sourceIndex[id]; ok && src != nil {
			sources = append(sources, *src)
		}
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].Title < sources[j].Title
	})
	return sources
}

func (a *Academic) nextResearchPaperVersion(sessionID string, researchSlug string) (int, error) {
	dir, err := a.researchPaperDir(sessionID)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultPaperVersion, nil
		}
		return 0, err
	}
	prefix := researchSlug + "_v"
	maxVersion := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".md") {
			continue
		}
		versionText := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".md")
		var version int
		if _, err := fmt.Sscanf(versionText, "%d", &version); err == nil && version > maxVersion {
			maxVersion = version
		}
	}
	if maxVersion == 0 {
		return defaultPaperVersion, nil
	}
	return maxVersion + 1, nil
}

func (a *Academic) writeResearchPaperArtifact(paper *AcademicResearchPaper) (string, error) {
	if paper == nil {
		return "", fmt.Errorf("research paper is required")
	}
	dir, err := a.researchPaperDir(paper.SessionID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s_v%d.md", paper.ResearchSlug, paper.Version))
	if err := os.WriteFile(path, []byte(renderResearchPaperMarkdown(paper)), 0o644); err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return absPath, nil
}

func (a *Academic) researchPaperDir(sessionID string) (string, error) {
	rootDir := strings.TrimSpace(a.steering.SessionDir())
	if rootDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		rootDir = filepath.Join(cwd, ".sylk", "sessions", sessionID)
	} else if !filepath.IsAbs(rootDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		rootDir = filepath.Join(cwd, rootDir)
	}
	return filepath.Join(rootDir, "research"), nil
}

func renderResearchPaperMarkdown(paper *AcademicResearchPaper) string {
	if paper == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(strings.TrimSpace(paper.Title))
	b.WriteString("\n\n")
	b.WriteString("- Research slug: `")
	b.WriteString(strings.TrimSpace(paper.ResearchSlug))
	b.WriteString("`\n")
	b.WriteString("- Version: ")
	b.WriteString(fmt.Sprintf("%d", paper.Version))
	b.WriteString("\n")
	b.WriteString("- Session: `")
	b.WriteString(strings.TrimSpace(paper.SessionID))
	b.WriteString("`\n")
	b.WriteString("- Generated: ")
	b.WriteString(paper.Timestamp.Format(time.RFC3339))
	b.WriteString("\n\n")

	writeSection(&b, "Abstract", paper.Abstract)
	writeSection(&b, "Problem Framing", paper.ProblemFraming)
	writeConstraints(&b, paper.Constraints)
	writeInvariants(&b, paper.Invariants)
	writeFindings(&b, paper.KeyFindings)
	writeOptionMatrix(&b, paper.OptionMatrix)
	writeStringListSection(&b, "Rejected Options", paper.RejectedOptions)
	writeApplicability(&b, paper.CodebaseApplicability)
	writeStringListSection(&b, "Recommendations", paper.Recommendations)
	writeStringListSection(&b, "Trade-Offs", paper.TradeOffs)
	writeStringListSection(&b, "Migration Concerns", paper.MigrationConcerns)
	writeStringListSection(&b, "Operational Concerns", paper.OperationalConcerns)
	writeStringListSection(&b, "Open Questions", paper.OpenQuestions)
	writeStringListSection(&b, "Related Topics", paper.RelatedTopics)
	writeSources(&b, paper.SourcesCited)
	writeArchitectHandoff(&b, paper.ArchitectHandoff)

	return strings.TrimSpace(b.String()) + "\n"
}

func writeSection(b *strings.Builder, title string, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(body)
	b.WriteString("\n\n")
}

func writeConstraints(b *strings.Builder, constraints []ResearchConstraint) {
	if len(constraints) == 0 {
		return
	}
	b.WriteString("## Constraints\n\n")
	for _, constraint := range constraints {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(constraint.Name))
		if desc := strings.TrimSpace(constraint.Description); desc != "" {
			b.WriteString(": ")
			b.WriteString(desc)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeInvariants(b *strings.Builder, invariants []ResearchInvariant) {
	if len(invariants) == 0 {
		return
	}
	b.WriteString("## Invariants\n\n")
	for _, invariant := range invariants {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(invariant.Name))
		if desc := strings.TrimSpace(invariant.Description); desc != "" {
			b.WriteString(": ")
			b.WriteString(desc)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeFindings(b *strings.Builder, findings []Finding) {
	if len(findings) == 0 {
		return
	}
	b.WriteString("## Key Findings\n\n")
	for _, finding := range findings {
		line := firstNonEmpty(finding.Summary, finding.Details, finding.Topic)
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString("- ")
		if topic := strings.TrimSpace(finding.Topic); topic != "" {
			b.WriteString(topic)
			b.WriteString(": ")
		}
		b.WriteString(strings.TrimSpace(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeOptionMatrix(b *strings.Builder, options []ArchitectureOption) {
	if len(options) == 0 {
		return
	}
	b.WriteString("## Option Matrix\n\n")
	for _, option := range options {
		b.WriteString("### ")
		b.WriteString(strings.TrimSpace(option.Name))
		b.WriteString("\n\n")
		if summary := strings.TrimSpace(option.Summary); summary != "" {
			b.WriteString(summary)
			b.WriteString("\n\n")
		}
		writeStringListSection(b, "Pros", option.Pros)
		writeStringListSection(b, "Cons", option.Cons)
		writeStringListSection(b, "Risks", option.Risks)
		if fit := strings.TrimSpace(option.Fit); fit != "" {
			writeSection(b, "Fit", fit)
		}
	}
}

func writeApplicability(b *strings.Builder, applicability *CodebaseApplicability) {
	if applicability == nil {
		return
	}
	writeSection(b, "Codebase Applicability", applicability.Summary)
	writeStringListSection(b, "Matching Patterns", applicability.MatchingPatterns)
	writeStringListSection(b, "Conflicts", applicability.Conflicts)
	writeStringListSection(b, "Required Changes", applicability.RequiredChanges)
}

func writeStringListSection(b *strings.Builder, title string, items []string) {
	items = filterNonEmpty(items)
	if len(items) == 0 {
		return
	}
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteString("\n\n")
	for _, item := range items {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeSources(b *strings.Builder, sources []Source) {
	if len(sources) == 0 {
		return
	}
	b.WriteString("## Sources\n\n")
	for _, source := range sources {
		b.WriteString("- ")
		if title := strings.TrimSpace(source.Title); title != "" {
			b.WriteString(title)
		} else {
			b.WriteString(source.ID)
		}
		if url := strings.TrimSpace(source.URL); url != "" {
			b.WriteString(" (")
			b.WriteString(url)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeArchitectHandoff(b *strings.Builder, handoff *ArchitectHandoff) {
	if handoff == nil {
		return
	}
	writeSection(b, "Architect Handoff Summary", handoff.PlanningSummary)
	writeStringListSection(b, "Required Decisions", handoff.RequiredDecisions)
	writeStringListSection(b, "Suggested Tasks", handoff.SuggestedTasks)
	writeStringListSection(b, "Acceptance Signals", handoff.AcceptanceSignals)
}

func (a *Academic) dispatchResearchPaperToArchitect(ctx context.Context, paper *AcademicResearchPaper) error {
	if paper == nil {
		return fmt.Errorf("research paper is required")
	}
	payload := map[string]any{
		"research_slug": paper.ResearchSlug,
		"paper_path":    paper.PaperPath,
		"version":       paper.Version,
		"summary":       architectPlanningSummary(paper),
		"session_id":    paper.SessionID,
	}
	return a.publishActionRequest(ctx, "architect", architectProposalAction, payload, true)
}

func (a *Academic) dispatchResearchPaperToArchivalist(ctx context.Context, paper *AcademicResearchPaper) error {
	if paper == nil {
		return fmt.Errorf("research paper is required")
	}
	payload := map[string]any{
		"id":                    paper.ID,
		"session_id":            paper.SessionID,
		"research_slug":         paper.ResearchSlug,
		"version":               paper.Version,
		"title":                 paper.Title,
		"abstract":              paper.Abstract,
		"problem_framing":       paper.ProblemFraming,
		"paper_path":            paper.PaperPath,
		"topics_researched":     paper.TopicsResearched,
		"recommendations":       paper.Recommendations,
		"open_questions":        paper.OpenQuestions,
		"related_topics":        paper.RelatedTopics,
		"architect_summary":     architectPlanningSummary(paper),
		"recommended_option_id": recommendedOptionID(paper),
	}
	return a.publishActionRequest(ctx, "archivalist", archivalistStorePaperAction, payload, true)
}

func (a *Academic) publishActionRequest(
	ctx context.Context,
	targetAgentType string,
	actionName string,
	data any,
	fireAndForget bool,
) error {
	if a.bus == nil {
		return fmt.Errorf("academic bus is unavailable")
	}
	agentID, channels := a.resolveAgentChannels(targetAgentType)
	if channels == nil {
		return fmt.Errorf("%s channels are unavailable", targetAgentType)
	}
	correlationID := "corr_" + uuid.NewString()
	parentCorrelationID := strings.TrimSpace(shared.LogMetaFromContext(ctx).CorrID)
	action := &guide.ActionRequest{
		CorrelationID:       correlationID,
		ParentCorrelationID: parentCorrelationID,
		SourceAgentID:       a.id,
		SourceAgentName:     "academic",
		TargetAgentID:       agentID,
		Action:              actionName,
		Data:                data,
		FireAndForget:       fireAndForget,
		Timestamp:           time.Now(),
	}
	msg := guide.NewActionMessage(generateMessageID(), action)
	return a.bus.Publish(channels.Requests, msg)
}

func (a *Academic) resolveAgentChannels(agentType string) (string, *guide.AgentChannels) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, ann := range a.knownAgents {
		if ann == nil {
			continue
		}
		if strings.EqualFold(ann.AgentType, agentType) ||
			strings.EqualFold(ann.AgentID, agentType) ||
			strings.EqualFold(ann.AgentName, agentType) {
			if ann.Channels != nil {
				return ann.AgentID, ann.Channels
			}
			return ann.AgentID, guide.NewAgentChannels(ann.AgentType, ann.AgentID)
		}
	}
	return agentType, guide.NewAgentChannels(agentType, agentType)
}

func buildOptionMatrix(result *ResearchResult) ([]ArchitectureOption, []string, string) {
	if result == nil {
		return nil, nil, ""
	}
	if len(result.Recommendations) == 0 {
		optionID := "recommended_direction"
		return []ArchitectureOption{{
			ID:         optionID,
			Name:       "Recommended direction",
			Summary:    summarizeFindings(result.Findings),
			Confidence: result.Confidence,
		}}, nil, optionID
	}

	options := make([]ArchitectureOption, 0, len(result.Recommendations))
	rejected := make([]string, 0, len(result.Recommendations))
	recommendedID := ""
	for i, rec := range result.Recommendations {
		optionID := rec.ID
		if strings.TrimSpace(optionID) == "" {
			optionID = fmt.Sprintf("option_%d", i+1)
		}
		if recommendedID == "" {
			recommendedID = optionID
		}
		options = append(options, ArchitectureOption{
			ID:         optionID,
			Name:       firstNonEmpty(rec.Title, fmt.Sprintf("Option %d", i+1)),
			Summary:    rec.Description,
			Pros:       extractBullets(rec.Rationale, 3),
			Cons:       extractBullets(strings.Join(rec.Alternatives, "\n"), 3),
			Fit:        rec.Applicability,
			Confidence: rec.Confidence,
			SourceIDs:  rec.SourceIDs,
		})
		for _, alt := range rec.Alternatives {
			alt = strings.TrimSpace(alt)
			if alt != "" {
				rejected = append(rejected, alt)
			}
		}
	}
	return options, filterNonEmpty(rejected), recommendedID
}

func buildCodebaseApplicability(evidence *shared.ConsultationEvidence) *CodebaseApplicability {
	summary := consultationSummary(evidence)
	if summary == "" {
		return nil
	}
	return &CodebaseApplicability{
		Summary:          summary,
		MatchingPatterns: extractBullets(summary, 4),
		RequiredChanges:  extractBullets(summary, 4),
		Confidence:       0.7,
	}
}

func requiredChanges(applicability *CodebaseApplicability) []string {
	if applicability == nil {
		return nil
	}
	return applicability.RequiredChanges
}

func architectPlanningSummary(paper *AcademicResearchPaper) string {
	if paper == nil {
		return ""
	}
	if paper.ArchitectHandoff != nil && strings.TrimSpace(paper.ArchitectHandoff.PlanningSummary) != "" {
		return strings.TrimSpace(paper.ArchitectHandoff.PlanningSummary)
	}
	return firstNonEmpty(paper.Abstract, paper.ProblemFraming, paper.Title)
}

func recommendedOptionID(paper *AcademicResearchPaper) string {
	if paper == nil || paper.ArchitectHandoff == nil {
		return ""
	}
	return strings.TrimSpace(paper.ArchitectHandoff.RecommendedOptionID)
}

func normalizeResearchSlug(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		return strings.ReplaceAll(storage.Slug(value), "_", "-")
	}
	return "research_proposal"
}

func researchPaperTitle(params *authorResearchPaperParams, researchSlug string) string {
	if params == nil {
		return "Research Proposal: " + humanizeResearchSlug(researchSlug)
	}
	if title := strings.TrimSpace(params.Title); title != "" {
		return title
	}
	return "Research Proposal: " + humanizeResearchSlug(researchSlug)
}

func toResearchConstraints(items []string) []ResearchConstraint {
	if len(items) == 0 {
		return nil
	}
	result := make([]ResearchConstraint, 0, len(items))
	for _, item := range filterNonEmpty(items) {
		result = append(result, ResearchConstraint{
			Name:        item,
			Description: item,
		})
	}
	return result
}

func toResearchInvariants(items []string) []ResearchInvariant {
	if len(items) == 0 {
		return nil
	}
	result := make([]ResearchInvariant, 0, len(items))
	for _, item := range filterNonEmpty(items) {
		result = append(result, ResearchInvariant{
			Name:        item,
			Description: item,
		})
	}
	return result
}

func consultationSummary(evidence *shared.ConsultationEvidence) string {
	if evidence == nil {
		return ""
	}
	return firstNonEmpty(summaryFromAny(evidence.Data), strings.TrimSpace(evidence.Error))
}

func summaryFromAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case map[string]any:
		if content := summaryFromAny(typed["content"]); content != "" {
			return content
		}
		if summary := summaryFromAny(typed["summary"]); summary != "" {
			return summary
		}
		if data := summaryFromAny(typed["data"]); data != "" {
			return data
		}
		if output := summaryFromAny(typed["output"]); output != "" {
			return output
		}
		encoded, _ := json.Marshal(typed)
		return strings.TrimSpace(string(encoded))
	default:
		encoded, _ := json.Marshal(typed)
		return strings.TrimSpace(string(encoded))
	}
}

func extractBullets(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	replacer := strings.NewReplacer("\r\n", "\n", "\r", "\n", "•", "\n", ";", "\n")
	text = replacer.Replace(text)
	lines := strings.Split(text, "\n")
	items := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		items = append(items, line)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func mergeUnique(groups ...[]string) []string {
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, group := range groups {
		for _, item := range group {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			merged = append(merged, item)
		}
	}
	return merged
}

func filterNonEmpty(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func humanizeResearchSlug(slug string) string {
	parts := strings.Fields(strings.ReplaceAll(strings.TrimSpace(slug), "-", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return strings.Join(parts, " ")
}
