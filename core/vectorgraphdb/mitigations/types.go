package mitigations

import (
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// SourceType represents the origin category of stored information.
type SourceType string

const (
	// SourceTypeCode indicates content directly from verified codebase.
	SourceTypeCode SourceType = "verified_code"

	// SourceTypeUser indicates explicit user input.
	SourceTypeUser SourceType = "user_input"

	// SourceTypeAcademic indicates documentation or articles.
	SourceTypeAcademic SourceType = "academic_source"

	// SourceTypeCrossRef indicates inference from multiple sources.
	SourceTypeCrossRef SourceType = "cross_reference"

	// SourceTypeLLM indicates LLM-generated content requiring verification.
	SourceTypeLLM SourceType = "llm_inference"
)

var sourceBaseTrust = map[SourceType]float64{
	SourceTypeCode:     1.0,
	SourceTypeUser:     0.95,
	SourceTypeAcademic: 0.85,
	SourceTypeCrossRef: 0.75,
	SourceTypeLLM:      0.5,
}

func (s SourceType) BaseTrust() float64 {
	if trust, ok := sourceBaseTrust[s]; ok {
		return trust
	}
	return 0.1
}

// TrustLevel represents the trust tier for information.
type TrustLevel int

const (
	TrustUnknown TrustLevel = iota
	TrustLLMInference
	TrustExternalArticle
	TrustOldHistory
	TrustOfficialDocs
	TrustRecentHistory
	TrustVerifiedCode
)

var trustScores = map[TrustLevel]float64{
	TrustVerifiedCode:    1.0,
	TrustRecentHistory:   0.9,
	TrustOfficialDocs:    0.8,
	TrustOldHistory:      0.7,
	TrustExternalArticle: 0.5,
	TrustLLMInference:    0.3,
	TrustUnknown:         0.1,
}

var trustNames = map[TrustLevel]string{
	TrustVerifiedCode:    "verified_code",
	TrustRecentHistory:   "recent_history",
	TrustOfficialDocs:    "official_docs",
	TrustOldHistory:      "old_history",
	TrustExternalArticle: "external_article",
	TrustLLMInference:    "llm_inference",
	TrustUnknown:         "unknown",
}

func (t TrustLevel) Score() float64 {
	if score, ok := trustScores[t]; ok {
		return score
	}
	return 0.1
}

func (t TrustLevel) String() string {
	if name, ok := trustNames[t]; ok {
		return name
	}
	return "unknown"
}

// ConflictType categorizes the nature of detected conflicts.
type ConflictType string

const (
	// ConflictSemantic indicates similar embeddings with contradictory content.
	ConflictSemantic ConflictType = "semantic"

	// ConflictTemporal indicates same topic with different answers over time.
	ConflictTemporal ConflictType = "temporal"

	// ConflictStructural indicates graph edges that shouldn't coexist.
	ConflictStructural ConflictType = "structural"

	// ConflictProvenance indicates same source with different claims.
	ConflictProvenance ConflictType = "provenance"
)

// Resolution represents how a conflict was resolved.
type Resolution string

const (
	ResolutionKeepNewer     Resolution = "keep_newer"
	ResolutionKeepTrusted   Resolution = "keep_trusted"
	ResolutionKeepBoth      Resolution = "keep_both"
	ResolutionMarkBothStale Resolution = "mark_both_stale"
	ResolutionHumanReview   Resolution = "human_review"
)

// Provenance tracks the source and verification chain for stored information.
type Provenance struct {
	ID         string     `json:"id"`
	NodeID     string     `json:"node_id"`
	SourceType SourceType `json:"source_type"`
	SourceID   string     `json:"source_id"`
	Confidence float64    `json:"confidence"`
	VerifiedAt time.Time  `json:"verified_at"`
	Verifier   string     `json:"verifier"`
	VerifiedBy []string   `json:"verified_by,omitempty"`
}

// ProvenanceChain represents the verification path for a node.
type ProvenanceChain struct {
	Node             *vectorgraphdb.GraphNode
	DirectProvenance *Provenance
	Chain            []*Provenance
	TransitiveConf   float64
}

// FreshnessInfo contains freshness metrics for a node.
type FreshnessInfo struct {
	NodeID         string    `json:"node_id"`
	LastVerified   time.Time `json:"last_verified"`
	SourceModified time.Time `json:"source_modified"`
	IsStale        bool      `json:"is_stale"`
	FreshnessScore float64   `json:"freshness_score"`
	DecayRate      float64   `json:"decay_rate"`
	NextScanAt     time.Time `json:"next_scan_at"`
}

// TrustInfo contains trust metrics for a node.
type TrustInfo struct {
	NodeID         string     `json:"node_id"`
	TrustLevel     TrustLevel `json:"trust_level"`
	TrustScore     float64    `json:"trust_score"`
	BaseScore      float64    `json:"base_score"`
	FreshnessBoost float64    `json:"freshness_boost"`
	VerifyBoost    float64    `json:"verify_boost"`
	CrossRefBoost  float64    `json:"cross_ref_boost"`
	EffectiveScore float64    `json:"effective_score"`
}

// Conflict represents a detected contradiction between nodes.
type Conflict struct {
	ID           string       `json:"id"`
	NodeAID      string       `json:"node_a_id"`
	NodeBID      string       `json:"node_b_id"`
	ConflictType ConflictType `json:"conflict_type"`
	Similarity   float64      `json:"similarity"`
	Details      string       `json:"details"`
	DetectedAt   time.Time    `json:"detected_at"`
	Resolution   *Resolution  `json:"resolution,omitempty"`
	ResolvedAt   *time.Time   `json:"resolved_at,omitempty"`
	ResolvedBy   string       `json:"resolved_by,omitempty"`
}

// ConflictStats contains aggregate conflict metrics.
type ConflictStats struct {
	TotalConflicts    int64                  `json:"total_conflicts"`
	UnresolvedCount   int64                  `json:"unresolved_count"`
	ByType            map[ConflictType]int64 `json:"by_type"`
	ByResolution      map[Resolution]int64   `json:"by_resolution"`
	AvgResolutionTime time.Duration          `json:"avg_resolution_time"`
}

// VerificationCheck represents a single verification operation result.
type VerificationCheck struct {
	CheckType  string  `json:"check_type"`
	Target     string  `json:"target"`
	Passed     bool    `json:"passed"`
	Confidence float64 `json:"confidence"`
	Details    string  `json:"details,omitempty"`
}

// VerificationResult contains the outcome of verifying a node.
type VerificationResult struct {
	Verified     bool                `json:"verified"`
	Confidence   float64             `json:"confidence"`
	Checks       []VerificationCheck `json:"checks"`
	FailedChecks []string            `json:"failed_checks,omitempty"`
	ShouldQueue  bool                `json:"should_queue"`
	QueueReason  string              `json:"queue_reason,omitempty"`
}

// ReviewItem represents an item queued for human review.
type ReviewItem struct {
	ID         string                   `json:"id"`
	Node       *vectorgraphdb.GraphNode `json:"node"`
	Reason     string                   `json:"reason"`
	Confidence float64                  `json:"confidence"`
	QueuedAt   time.Time                `json:"queued_at"`
	ExpiresAt  time.Time                `json:"expires_at"`
	ReviewedAt *time.Time               `json:"reviewed_at,omitempty"`
	ReviewedBy string                   `json:"reviewed_by,omitempty"`
	Approved   *bool                    `json:"approved,omitempty"`
}

// QualityWeights configures the weights for context quality scoring.
type QualityWeights struct {
	Relevance  float64 `json:"relevance"`
	Freshness  float64 `json:"freshness"`
	Trust      float64 `json:"trust"`
	Density    float64 `json:"density"`
	Redundancy float64 `json:"redundancy"`
}

// DefaultQualityWeights returns the default quality scoring weights.
func DefaultQualityWeights() QualityWeights {
	return QualityWeights{
		Relevance:  0.35,
		Freshness:  0.20,
		Trust:      0.25,
		Density:    0.15,
		Redundancy: 0.05,
	}
}

// QualityComponents contains the individual quality metric values.
type QualityComponents struct {
	Relevance  float64 `json:"relevance"`
	Freshness  float64 `json:"freshness"`
	Trust      float64 `json:"trust"`
	Density    float64 `json:"density"`
	Redundancy float64 `json:"redundancy"`
}

// ContextItem represents a scored context item for LLM prompts.
type ContextItem struct {
	Node          *vectorgraphdb.GraphNode `json:"node"`
	QualityScore  float64                  `json:"quality_score"`
	TokenCount    int                      `json:"token_count"`
	ScorePerToken float64                  `json:"score_per_token"`
	Components    QualityComponents        `json:"components"`
	Selected      bool                     `json:"selected"`
	Reason        string                   `json:"reason,omitempty"`
}

// AnnotatedItem represents a context item with trust and conflict annotations.
type AnnotatedItem struct {
	Content    string               `json:"content"`
	Domain     vectorgraphdb.Domain `json:"domain"`
	TrustLevel TrustLevel           `json:"trust_level"`
	TrustScore float64              `json:"trust_score"`
	Freshness  time.Time            `json:"freshness"`
	Source     string               `json:"source"`
	Conflicts  []string             `json:"conflicts,omitempty"`
}

// ConflictSummary provides a brief summary of a conflict for LLM context.
type ConflictSummary struct {
	ConflictID  string       `json:"conflict_id"`
	Type        ConflictType `json:"type"`
	ItemAID     string       `json:"item_a_id"`
	ItemBID     string       `json:"item_b_id"`
	Description string       `json:"description"`
}

// LLMContext represents the structured context for LLM prompts.
type LLMContext struct {
	Preamble    string             `json:"preamble"`
	CodeContext []*AnnotatedItem   `json:"code_context,omitempty"`
	HistContext []*AnnotatedItem   `json:"hist_context,omitempty"`
	AcadContext []*AnnotatedItem   `json:"acad_context,omitempty"`
	Conflicts   []*ConflictSummary `json:"conflicts,omitempty"`
	TotalTokens int                `json:"total_tokens"`
}

// DecayConfig configures decay rates per domain.
type DecayConfig struct {
	CodeDecayRate     float64 `json:"code_decay_rate"`
	HistoryDecayRate  float64 `json:"history_decay_rate"`
	AcademicDecayRate float64 `json:"academic_decay_rate"`
}

// DefaultDecayConfig returns the default decay configuration.
func DefaultDecayConfig() DecayConfig {
	return DecayConfig{
		CodeDecayRate:     0.01,
		HistoryDecayRate:  0.05,
		AcademicDecayRate: 0.001,
	}
}

// TrustAuditEntry records a trust change event.
type TrustAuditEntry struct {
	ID        string     `json:"id"`
	NodeID    string     `json:"node_id"`
	Action    string     `json:"action"`
	OldLevel  TrustLevel `json:"old_level"`
	NewLevel  TrustLevel `json:"new_level"`
	Reason    string     `json:"reason"`
	Actor     string     `json:"actor"`
	Timestamp time.Time  `json:"timestamp"`
}

// CacheConfig configures bounded cache behavior.
type CacheConfig struct {
	MaxSize int
}

// DefaultCacheConfig returns the default cache configuration.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		MaxSize: 10000,
	}
}
