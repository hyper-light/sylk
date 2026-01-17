package vectorgraphdb

import (
	"time"
)

// =============================================================================
// Domain Types
// =============================================================================

// Domain represents the knowledge domain a node belongs to.
// VectorGraphDB operates across three distinct domains that can be linked
// through cross-domain edges.
type Domain string

const (
	// DomainCode represents the codebase domain managed by Librarian.
	// Contains files, functions, types, packages, and import relationships.
	DomainCode Domain = "code"

	// DomainHistory represents the historical context domain managed by Archivalist.
	// Contains sessions, decisions, failures, patterns, and workflows.
	DomainHistory Domain = "history"

	// DomainAcademic represents external knowledge domain managed by Academic.
	// Contains repos, docs, articles, concepts, and best practices.
	DomainAcademic Domain = "academic"
)

// ValidDomains returns all valid Domain values.
func ValidDomains() []Domain {
	return []Domain{
		DomainCode,
		DomainHistory,
		DomainAcademic,
	}
}

// IsValid returns true if the domain is a recognized value.
func (d Domain) IsValid() bool {
	switch d {
	case DomainCode, DomainHistory, DomainAcademic:
		return true
	default:
		return false
	}
}

// String returns the string representation of the domain.
func (d Domain) String() string {
	return string(d)
}

// =============================================================================
// Node Types
// =============================================================================

// NodeType represents the specific type of node within a domain.
// Each domain has its own set of valid node types.
type NodeType string

// Code domain node types (managed by Librarian)
const (
	// NodeTypeFile represents a source file in the codebase.
	NodeTypeFile NodeType = "file"

	// NodeTypeFunction represents a function or method.
	NodeTypeFunction NodeType = "function"

	// NodeTypeType represents a type definition (struct, interface, class, etc.).
	NodeTypeType NodeType = "type"

	// NodeTypePackage represents a package or module.
	NodeTypePackage NodeType = "package"

	// NodeTypeImport represents an import statement or dependency.
	NodeTypeImport NodeType = "import"
)

// History domain node types (managed by Archivalist)
const (
	// NodeTypeSession represents a user session with the system.
	NodeTypeSession NodeType = "session"

	// NodeTypeDecision represents a decision made during development.
	NodeTypeDecision NodeType = "decision"

	// NodeTypeFailure represents a failure or error that occurred.
	NodeTypeFailure NodeType = "failure"

	// NodeTypePattern represents a recognized pattern in development history.
	NodeTypePattern NodeType = "pattern"

	// NodeTypeWorkflow represents a workflow or process that was executed.
	NodeTypeWorkflow NodeType = "workflow"
)

// Academic domain node types (managed by Academic)
const (
	// NodeTypeRepo represents an external repository reference.
	NodeTypeRepo NodeType = "repo"

	// NodeTypeDoc represents documentation content.
	NodeTypeDoc NodeType = "doc"

	// NodeTypeArticle represents a technical article or paper.
	NodeTypeArticle NodeType = "article"

	// NodeTypeConcept represents an abstract concept or principle.
	NodeTypeConcept NodeType = "concept"

	// NodeTypeBestPractice represents a documented best practice.
	NodeTypeBestPractice NodeType = "best_practice"
)

// ValidNodeTypes returns all valid NodeType values.
func ValidNodeTypes() []NodeType {
	return []NodeType{
		// Code domain
		NodeTypeFile,
		NodeTypeFunction,
		NodeTypeType,
		NodeTypePackage,
		NodeTypeImport,
		// History domain
		NodeTypeSession,
		NodeTypeDecision,
		NodeTypeFailure,
		NodeTypePattern,
		NodeTypeWorkflow,
		// Academic domain
		NodeTypeRepo,
		NodeTypeDoc,
		NodeTypeArticle,
		NodeTypeConcept,
		NodeTypeBestPractice,
	}
}

// ValidNodeTypesForDomain returns all valid NodeType values for a specific domain.
func ValidNodeTypesForDomain(domain Domain) []NodeType {
	switch domain {
	case DomainCode:
		return []NodeType{
			NodeTypeFile,
			NodeTypeFunction,
			NodeTypeType,
			NodeTypePackage,
			NodeTypeImport,
		}
	case DomainHistory:
		return []NodeType{
			NodeTypeSession,
			NodeTypeDecision,
			NodeTypeFailure,
			NodeTypePattern,
			NodeTypeWorkflow,
		}
	case DomainAcademic:
		return []NodeType{
			NodeTypeRepo,
			NodeTypeDoc,
			NodeTypeArticle,
			NodeTypeConcept,
			NodeTypeBestPractice,
		}
	default:
		return nil
	}
}

// IsValid returns true if the node type is a recognized value.
func (nt NodeType) IsValid() bool {
	for _, valid := range ValidNodeTypes() {
		if nt == valid {
			return true
		}
	}
	return false
}

// IsValidForDomain returns true if the node type is valid for the given domain.
func (nt NodeType) IsValidForDomain(domain Domain) bool {
	validTypes := ValidNodeTypesForDomain(domain)
	for _, valid := range validTypes {
		if nt == valid {
			return true
		}
	}
	return false
}

// String returns the string representation of the node type.
func (nt NodeType) String() string {
	return string(nt)
}

// =============================================================================
// Edge Types
// =============================================================================

// EdgeType represents the type of relationship between nodes.
// Edges can be structural (within domain), temporal (sequence-based),
// or cross-domain (linking different knowledge domains).
type EdgeType string

// Structural edge types (primarily within same domain)
const (
	// EdgeTypeCalls represents a function calling another function.
	EdgeTypeCalls EdgeType = "calls"

	// EdgeTypeImports represents a file or package importing another.
	EdgeTypeImports EdgeType = "imports"

	// EdgeTypeDefines represents a file defining a type or function.
	EdgeTypeDefines EdgeType = "defines"

	// EdgeTypeImplements represents a type implementing an interface.
	EdgeTypeImplements EdgeType = "implements"

	// EdgeTypeContains represents a containment relationship (package contains file, etc.).
	EdgeTypeContains EdgeType = "contains"
)

// Temporal edge types (sequence and causality)
const (
	// EdgeTypeFollows represents temporal sequence (A followed by B).
	EdgeTypeFollows EdgeType = "follows"

	// EdgeTypeCauses represents causal relationship (A caused B).
	EdgeTypeCauses EdgeType = "causes"

	// EdgeTypeResolves represents resolution relationship (A resolved B).
	EdgeTypeResolves EdgeType = "resolves"
)

// Cross-domain edge types (linking different knowledge domains)
const (
	// EdgeTypeReferences represents a reference from one domain to another.
	// Code ↔ Academic: code references documentation.
	EdgeTypeReferences EdgeType = "references"

	// EdgeTypeAppliesTo represents a best practice or concept applying to code.
	// Academic → Code: best practice applies to file.
	EdgeTypeAppliesTo EdgeType = "applies_to"

	// EdgeTypeDocuments represents documentation of code or patterns.
	// Academic → Code: article documents pattern.
	EdgeTypeDocuments EdgeType = "documents"

	// EdgeTypeModified represents modification relationship.
	// History → Code: session modified file.
	EdgeTypeModified EdgeType = "modified"
)

// ValidEdgeTypes returns all valid EdgeType values.
func ValidEdgeTypes() []EdgeType {
	return []EdgeType{
		// Structural
		EdgeTypeCalls,
		EdgeTypeImports,
		EdgeTypeDefines,
		EdgeTypeImplements,
		EdgeTypeContains,
		// Temporal
		EdgeTypeFollows,
		EdgeTypeCauses,
		EdgeTypeResolves,
		// Cross-domain
		EdgeTypeReferences,
		EdgeTypeAppliesTo,
		EdgeTypeDocuments,
		EdgeTypeModified,
	}
}

// StructuralEdgeTypes returns edge types that represent structural relationships.
func StructuralEdgeTypes() []EdgeType {
	return []EdgeType{
		EdgeTypeCalls,
		EdgeTypeImports,
		EdgeTypeDefines,
		EdgeTypeImplements,
		EdgeTypeContains,
	}
}

// TemporalEdgeTypes returns edge types that represent temporal relationships.
func TemporalEdgeTypes() []EdgeType {
	return []EdgeType{
		EdgeTypeFollows,
		EdgeTypeCauses,
		EdgeTypeResolves,
	}
}

// CrossDomainEdgeTypes returns edge types that link different domains.
func CrossDomainEdgeTypes() []EdgeType {
	return []EdgeType{
		EdgeTypeReferences,
		EdgeTypeAppliesTo,
		EdgeTypeDocuments,
		EdgeTypeModified,
	}
}

// IsValid returns true if the edge type is a recognized value.
func (et EdgeType) IsValid() bool {
	for _, valid := range ValidEdgeTypes() {
		if et == valid {
			return true
		}
	}
	return false
}

// IsStructural returns true if this is a structural edge type.
func (et EdgeType) IsStructural() bool {
	for _, structural := range StructuralEdgeTypes() {
		if et == structural {
			return true
		}
	}
	return false
}

// IsTemporal returns true if this is a temporal edge type.
func (et EdgeType) IsTemporal() bool {
	for _, temporal := range TemporalEdgeTypes() {
		if et == temporal {
			return true
		}
	}
	return false
}

// IsCrossDomain returns true if this is a cross-domain edge type.
func (et EdgeType) IsCrossDomain() bool {
	for _, crossDomain := range CrossDomainEdgeTypes() {
		if et == crossDomain {
			return true
		}
	}
	return false
}

// String returns the string representation of the edge type.
func (et EdgeType) String() string {
	return string(et)
}

// =============================================================================
// Core Data Structures
// =============================================================================

// GraphNode represents a node in the VectorGraphDB knowledge graph.
// Nodes belong to a specific domain and type, and can be connected
// to other nodes via edges.
type GraphNode struct {
	// ID is the unique identifier for this node.
	ID string `json:"id"`

	// Domain indicates which knowledge domain this node belongs to.
	Domain Domain `json:"domain"`

	// NodeType specifies the type of node within its domain.
	NodeType NodeType `json:"node_type"`

	// ContentHash is a hash of the node's content for deduplication and change detection.
	ContentHash string `json:"content_hash"`

	// Metadata contains domain-specific data for this node.
	// The schema varies by domain and node type.
	Metadata map[string]any `json:"metadata"`

	// CreatedAt is when this node was first created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when this node was last modified.
	UpdatedAt time.Time `json:"updated_at"`

	// AccessedAt is when this node was last accessed/queried.
	// Used for freshness scoring and cache eviction.
	AccessedAt time.Time `json:"accessed_at"`
}

// GraphEdge represents a directed edge between two nodes in the knowledge graph.
// Edges have a type that defines the nature of the relationship and an optional
// weight for scoring.
type GraphEdge struct {
	// ID is the unique identifier for this edge.
	ID string `json:"id"`

	// FromNodeID is the ID of the source node.
	FromNodeID string `json:"from_node_id"`

	// ToNodeID is the ID of the target node.
	ToNodeID string `json:"to_node_id"`

	// EdgeType specifies the type of relationship.
	EdgeType EdgeType `json:"edge_type"`

	// Weight is an optional score indicating relationship strength (0.0-1.0).
	// Higher weight indicates stronger relationship.
	Weight float64 `json:"weight"`

	// Metadata contains additional data about this relationship.
	Metadata map[string]any `json:"metadata,omitempty"`

	// CreatedAt is when this edge was created.
	CreatedAt time.Time `json:"created_at"`
}

// VectorData represents the embedding vector associated with a node.
// Stored separately from the node for efficient vector operations.
type VectorData struct {
	// ID is the unique identifier for this vector record.
	ID string `json:"id"`

	// NodeID is the ID of the associated node.
	NodeID string `json:"node_id"`

	// Embedding is the vector representation (typically 768 float32s).
	Embedding []float32 `json:"embedding"`

	// Magnitude is the pre-computed L2 norm for fast cosine similarity.
	Magnitude float64 `json:"magnitude"`

	// ModelVersion identifies which embedding model created this vector.
	// Used to detect when vectors need re-embedding after model updates.
	ModelVersion string `json:"model_version"`
}

// Provenance tracks the source and verification status of a node.
// Critical for hallucination mitigation and trust scoring.
type Provenance struct {
	// ID is the unique identifier for this provenance record.
	ID string `json:"id"`

	// NodeID is the ID of the node this provenance is for.
	NodeID string `json:"node_id"`

	// SourceType indicates the category of source (e.g., "git", "user", "llm", "web").
	SourceType string `json:"source_type"`

	// SourceID is the specific identifier within the source (e.g., commit hash, URL).
	SourceID string `json:"source_id"`

	// Confidence is the trust score for this source (0.0-1.0).
	// Git commits and direct observations have high confidence.
	// LLM-generated content has lower confidence until verified.
	Confidence float64 `json:"confidence"`

	// VerifiedAt is when this node was last verified against its source.
	VerifiedAt time.Time `json:"verified_at,omitempty"`

	// Verifier identifies what/who performed the verification.
	Verifier string `json:"verifier,omitempty"`
}

// Conflict represents a detected contradiction between two nodes.
// Used for hallucination detection and conflict resolution.
type Conflict struct {
	// ID is the unique identifier for this conflict record.
	ID string `json:"id"`

	// NodeAID is the ID of the first conflicting node.
	NodeAID string `json:"node_a_id"`

	// NodeBID is the ID of the second conflicting node.
	NodeBID string `json:"node_b_id"`

	// ConflictType categorizes the nature of the conflict.
	// Examples: "semantic_contradiction", "version_mismatch", "duplicate".
	ConflictType string `json:"conflict_type"`

	// DetectedAt is when the conflict was discovered.
	DetectedAt time.Time `json:"detected_at"`

	// Resolution describes how the conflict was resolved, if at all.
	Resolution string `json:"resolution,omitempty"`

	// ResolvedAt is when the conflict was resolved.
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}

// =============================================================================
// Database Statistics
// =============================================================================

// DBStats contains statistics about the VectorGraphDB state.
type DBStats struct {
	// TotalNodes is the count of all nodes across all domains.
	TotalNodes int64 `json:"total_nodes"`

	// NodesByDomain maps domain to node count.
	NodesByDomain map[Domain]int64 `json:"nodes_by_domain"`

	// NodesByType maps node type to count.
	NodesByType map[NodeType]int64 `json:"nodes_by_type"`

	// TotalEdges is the count of all edges.
	TotalEdges int64 `json:"total_edges"`

	// EdgesByType maps edge type to count.
	EdgesByType map[EdgeType]int64 `json:"edges_by_type"`

	// TotalVectors is the count of all vector embeddings.
	TotalVectors int64 `json:"total_vectors"`

	// IndexSize is the size of the HNSW index in bytes.
	IndexSize int64 `json:"index_size"`

	// DBSizeBytes is the total database file size.
	DBSizeBytes int64 `json:"db_size_bytes"`

	// LastVacuumAt is when the database was last compacted.
	LastVacuumAt time.Time `json:"last_vacuum_at,omitempty"`

	// UnresolvedConflicts is the count of conflicts without resolution.
	UnresolvedConflicts int64 `json:"unresolved_conflicts"`

	// StaleNodes is the count of nodes that haven't been accessed recently.
	StaleNodes int64 `json:"stale_nodes"`
}

// =============================================================================
// Source Types for Provenance
// =============================================================================

// SourceType constants for provenance tracking.
const (
	// SourceTypeGit indicates content from git repository.
	SourceTypeGit = "git"

	// SourceTypeUser indicates content from user input.
	SourceTypeUser = "user"

	// SourceTypeLLM indicates content generated by an LLM.
	SourceTypeLLM = "llm"

	// SourceTypeWeb indicates content from web sources.
	SourceTypeWeb = "web"

	// SourceTypeAPI indicates content from external APIs.
	SourceTypeAPI = "api"
)

// =============================================================================
// Conflict Types
// =============================================================================

// Conflict type constants.
const (
	// ConflictTypeSemanticContradiction indicates nodes with contradictory meanings.
	ConflictTypeSemanticContradiction = "semantic_contradiction"

	// ConflictTypeVersionMismatch indicates version inconsistencies.
	ConflictTypeVersionMismatch = "version_mismatch"

	// ConflictTypeDuplicate indicates duplicate nodes that should be merged.
	ConflictTypeDuplicate = "duplicate"

	// ConflictTypeStale indicates outdated information.
	ConflictTypeStale = "stale"
)
