package mitigations

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// sanitizePromptInput removes potentially dangerous characters from prompt inputs.
// It removes control characters (except \t, \n, \r), null bytes, and normalizes whitespace.
func sanitizePromptInput(input string) string {
	if input == "" {
		return input
	}

	var sb strings.Builder
	sb.Grow(len(input))

	for _, r := range input {
		if shouldKeepRune(r) {
			sb.WriteRune(r)
		}
	}

	return sb.String()
}

func shouldKeepRune(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return true
	}
	if unicode.IsControl(r) || r == 0 {
		return false
	}
	return true
}

// escapeXMLContent escapes special XML characters in content.
func escapeXMLContent(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// LLMContextBuilder constructs structured context for LLM prompts.
type LLMContextBuilder struct {
	scorer     *ContextQualityScorer
	trust      *TrustHierarchy
	conflicts  *ConflictDetector
	provenance *ProvenanceTracker
	tokenizer  Tokenizer
}

// NewLLMContextBuilder creates a new LLMContextBuilder.
func NewLLMContextBuilder(
	scorer *ContextQualityScorer,
	trust *TrustHierarchy,
	conflicts *ConflictDetector,
	provenance *ProvenanceTracker,
) *LLMContextBuilder {
	return &LLMContextBuilder{
		scorer:     scorer,
		trust:      trust,
		conflicts:  conflicts,
		provenance: provenance,
		tokenizer:  &DefaultTokenizer{},
	}
}

// Build constructs an LLMContext from selected context items.
func (b *LLMContextBuilder) Build(items []*ContextItem) (*LLMContext, error) {
	ctx := &LLMContext{
		Preamble:    b.generatePreamble(),
		CodeContext: make([]*AnnotatedItem, 0),
		HistContext: make([]*AnnotatedItem, 0),
		AcadContext: make([]*AnnotatedItem, 0),
		Conflicts:   make([]*ConflictSummary, 0),
	}

	for _, item := range items {
		if !item.Selected {
			continue
		}

		annotated, err := b.annotateItem(item)
		if err != nil {
			continue
		}

		b.addToDomain(ctx, annotated, item.Node.Domain)
		ctx.TotalTokens += item.TokenCount
	}

	ctx.Conflicts = b.collectConflicts(items)
	return ctx, nil
}

func (b *LLMContextBuilder) generatePreamble() string {
	return `CONTEXT TRUST LEVELS:
- verified_code (1.0): Code directly from the codebase - authoritative
- recent_history (0.9): Decisions from last 24h - reflects current intent  
- official_docs (0.8): README, API docs, inline comments
- old_history (0.7): Decisions older than 24h - may be outdated
- external_article (0.5): Blogs, tutorials, StackOverflow
- llm_inference (0.3): Unverified LLM output - treat with caution

INSTRUCTIONS:
- Prefer higher trust sources when information conflicts
- Recent code changes override older documentation
- Flag any unresolved conflicts in your response
- Cite sources using [trust_level: source] format`
}

func (b *LLMContextBuilder) annotateItem(item *ContextItem) (*AnnotatedItem, error) {
	annotated := &AnnotatedItem{
		Content:   sanitizePromptInput(extractNodeContent(item.Node)),
		Domain:    item.Node.Domain,
		Freshness: item.Node.UpdatedAt,
	}

	b.addTrustInfo(annotated, item.Node.ID)
	b.addSourceInfo(annotated, item.Node)
	b.addConflictInfo(annotated, item.Node.ID)

	return annotated, nil
}

func (b *LLMContextBuilder) addTrustInfo(annotated *AnnotatedItem, nodeID string) {
	if b.trust == nil {
		annotated.TrustLevel = TrustUnknown
		annotated.TrustScore = 0.5
		return
	}

	info, err := b.trust.GetTrustInfo(nodeID)
	if err != nil {
		annotated.TrustLevel = TrustUnknown
		annotated.TrustScore = 0.5
		return
	}

	annotated.TrustLevel = info.TrustLevel
	annotated.TrustScore = info.TrustScore
}

func (b *LLMContextBuilder) addSourceInfo(annotated *AnnotatedItem, node *vectorgraphdb.GraphNode) {
	if path, ok := node.Metadata["path"].(string); ok {
		annotated.Source = sanitizePromptInput(path)
		return
	}
	if url, ok := node.Metadata["url"].(string); ok {
		annotated.Source = sanitizePromptInput(url)
		return
	}
	annotated.Source = sanitizePromptInput(node.ID)
}

func (b *LLMContextBuilder) addConflictInfo(annotated *AnnotatedItem, nodeID string) {
	if b.conflicts == nil {
		return
	}

	nodeConflicts := b.conflicts.GetConflictsForNode(nodeID)
	for _, c := range nodeConflicts {
		if c.Resolution == nil {
			annotated.Conflicts = append(annotated.Conflicts, c.ID)
		}
	}
}

func (b *LLMContextBuilder) addToDomain(ctx *LLMContext, item *AnnotatedItem, domain vectorgraphdb.Domain) {
	switch domain {
	case vectorgraphdb.DomainCode:
		ctx.CodeContext = append(ctx.CodeContext, item)
	case vectorgraphdb.DomainHistory:
		ctx.HistContext = append(ctx.HistContext, item)
	case vectorgraphdb.DomainAcademic:
		ctx.AcadContext = append(ctx.AcadContext, item)
	}
}

func (b *LLMContextBuilder) collectConflicts(items []*ContextItem) []*ConflictSummary {
	if b.conflicts == nil {
		return nil
	}

	seenConflicts := make(map[string]bool)
	summaries := make([]*ConflictSummary, 0)

	for _, item := range items {
		if !item.Selected {
			continue
		}
		b.collectNodeConflicts(item.Node.ID, seenConflicts, &summaries)
	}

	return summaries
}

func (b *LLMContextBuilder) collectNodeConflicts(nodeID string, seen map[string]bool, summaries *[]*ConflictSummary) {
	for _, c := range b.conflicts.GetConflictsForNode(nodeID) {
		if c.Resolution != nil || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		*summaries = append(*summaries, conflictToSummary(c))
	}
}

func conflictToSummary(c *Conflict) *ConflictSummary {
	return &ConflictSummary{
		ConflictID:  c.ID,
		Type:        c.ConflictType,
		ItemAID:     c.NodeAID,
		ItemBID:     c.NodeBID,
		Description: c.Details,
	}
}

// FormatAsMarkdown formats the context as Markdown.
func (ctx *LLMContext) FormatAsMarkdown() string {
	var sb strings.Builder

	sb.WriteString("## Context Information\n\n")
	sb.WriteString(ctx.Preamble)
	sb.WriteString("\n\n---\n\n")

	ctx.formatDomainMarkdown(&sb, "### Code Context", ctx.CodeContext)
	ctx.formatDomainMarkdown(&sb, "### Historical Context", ctx.HistContext)
	ctx.formatDomainMarkdown(&sb, "### Academic Context", ctx.AcadContext)

	if len(ctx.Conflicts) > 0 {
		sb.WriteString("### Unresolved Conflicts\n\n")
		for _, c := range ctx.Conflicts {
			sb.WriteString(fmt.Sprintf("- **%s**: %s vs %s - %s\n",
				c.Type, c.ItemAID, c.ItemBID, c.Description))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func (ctx *LLMContext) formatDomainMarkdown(sb *strings.Builder, header string, items []*AnnotatedItem) {
	if len(items) == 0 {
		return
	}

	sb.WriteString(header)
	sb.WriteString("\n\n")

	for _, item := range items {
		sb.WriteString(fmt.Sprintf("**[%s: %.0f%%]** %s\n",
			item.TrustLevel.String(), item.TrustScore*100, item.Source))
		sb.WriteString(fmt.Sprintf("_Updated: %s_\n\n",
			item.Freshness.Format("2006-01-02")))
		sb.WriteString("```\n")
		sb.WriteString(item.Content)
		sb.WriteString("\n```\n\n")

		if len(item.Conflicts) > 0 {
			sb.WriteString(fmt.Sprintf("⚠️ Conflicts with: %s\n\n",
				strings.Join(item.Conflicts, ", ")))
		}
	}
}

// FormatAsJSON formats the context as JSON.
func (ctx *LLMContext) FormatAsJSON() string {
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

// FormatAsXML formats the context as XML.
func (ctx *LLMContext) FormatAsXML() string {
	var sb strings.Builder

	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<context>\n")
	sb.WriteString("  <preamble><![CDATA[")
	sb.WriteString(ctx.Preamble)
	sb.WriteString("]]></preamble>\n")

	ctx.formatDomainXML(&sb, "code", ctx.CodeContext)
	ctx.formatDomainXML(&sb, "history", ctx.HistContext)
	ctx.formatDomainXML(&sb, "academic", ctx.AcadContext)

	if len(ctx.Conflicts) > 0 {
		sb.WriteString("  <conflicts>\n")
		for _, c := range ctx.Conflicts {
			sb.WriteString(fmt.Sprintf("    <conflict type=\"%s\" items=\"%s,%s\">%s</conflict>\n",
				escapeXMLContent(string(c.Type)),
				escapeXMLContent(c.ItemAID),
				escapeXMLContent(c.ItemBID),
				escapeXMLContent(c.Description)))
		}
		sb.WriteString("  </conflicts>\n")
	}

	sb.WriteString(fmt.Sprintf("  <total_tokens>%d</total_tokens>\n", ctx.TotalTokens))
	sb.WriteString("</context>")

	return sb.String()
}

func (ctx *LLMContext) formatDomainXML(sb *strings.Builder, domain string, items []*AnnotatedItem) {
	if len(items) == 0 {
		return
	}

	sb.WriteString(fmt.Sprintf("  <%s_context>\n", domain))
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("    <item trust=\"%s\" score=\"%.2f\" source=\"%s\">\n",
			escapeXMLContent(item.TrustLevel.String()),
			item.TrustScore,
			escapeXMLContent(item.Source)))
		sb.WriteString("      <content><![CDATA[")
		sb.WriteString(sanitizePromptInput(item.Content))
		sb.WriteString("]]></content>\n")
		sb.WriteString(fmt.Sprintf("      <freshness>%s</freshness>\n",
			item.Freshness.Format(time.RFC3339)))
		sb.WriteString("    </item>\n")
	}
	sb.WriteString(fmt.Sprintf("  </%s_context>\n", domain))
}

// FormatAsSystemPrompt formats the context as a system prompt fragment.
func (ctx *LLMContext) FormatAsSystemPrompt() string {
	var sb strings.Builder

	sb.WriteString("<system_context>\n")
	sb.WriteString(ctx.Preamble)
	sb.WriteString("\n\n")

	ctx.writeReferenceBlock(&sb, "code_reference", ctx.CodeContext)
	ctx.writeReferenceBlock(&sb, "history_reference", ctx.HistContext)
	ctx.writeReferenceBlock(&sb, "academic_reference", ctx.AcadContext)
	ctx.writeConflictsBlock(&sb)

	sb.WriteString("</system_context>")
	return sb.String()
}

func (ctx *LLMContext) writeReferenceBlock(sb *strings.Builder, tag string, items []*AnnotatedItem) {
	if len(items) == 0 {
		return
	}
	sb.WriteString(fmt.Sprintf("<%s>\n", tag))
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("[%s: %s]\n%s\n\n",
			item.TrustLevel.String(), item.Source, item.Content))
	}
	sb.WriteString(fmt.Sprintf("</%s>\n", tag))
}

func (ctx *LLMContext) writeConflictsBlock(sb *strings.Builder) {
	if len(ctx.Conflicts) == 0 {
		return
	}
	sb.WriteString("<conflicts>\n")
	for _, c := range ctx.Conflicts {
		sb.WriteString(fmt.Sprintf("WARNING: Conflict between %s and %s - %s\n",
			c.ItemAID, c.ItemBID, c.Description))
	}
	sb.WriteString("</conflicts>\n")
}

// EstimateTokens estimates the total token count for the context.
func (ctx *LLMContext) EstimateTokens() int {
	return ctx.TotalTokens
}

// GetItemCount returns the total number of context items.
func (ctx *LLMContext) GetItemCount() int {
	return len(ctx.CodeContext) + len(ctx.HistContext) + len(ctx.AcadContext)
}

// HasConflicts returns whether there are unresolved conflicts.
func (ctx *LLMContext) HasConflicts() bool {
	return len(ctx.Conflicts) > 0
}
