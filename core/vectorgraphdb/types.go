package vectorgraphdb

import (
	"encoding/json"
	"fmt"
	"time"
)

// =============================================================================
// Domain Types
// =============================================================================

// Domain represents the knowledge domain a node belongs to.
// VectorGraphDB operates across three distinct domains that can be linked
// through cross-domain edges.
type Domain int

const (
	DomainCode     Domain = 0
	DomainHistory  Domain = 1
	DomainAcademic Domain = 2
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
	switch d {
	case DomainCode:
		return "code"
	case DomainHistory:
		return "history"
	case DomainAcademic:
		return "academic"
	default:
		return fmt.Sprintf("domain(%d)", d)
	}
}

func ParseDomain(value string) (Domain, bool) {
	switch value {
	case "code":
		return DomainCode, true
	case "history":
		return DomainHistory, true
	case "academic":
		return DomainAcademic, true
	default:
		return Domain(0), false
	}
}

func (d Domain) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Domain) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseDomain(asString); ok {
			*d = parsed
			return nil
		}
		return fmt.Errorf("invalid domain: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*d = Domain(asInt)
		return nil
	}

	return fmt.Errorf("invalid domain")
}

// =============================================================================
// Node Types
// =============================================================================

// NodeType represents the specific type of node within a domain.
// Each domain has its own set of valid node types.
type NodeType int

const (
	NodeTypeFile      NodeType = 0
	NodeTypePackage   NodeType = 1
	NodeTypeFunction  NodeType = 2
	NodeTypeMethod    NodeType = 3
	NodeTypeStruct    NodeType = 4
	NodeTypeInterface NodeType = 5
	NodeTypeVariable  NodeType = 6
	NodeTypeConstant  NodeType = 7
	NodeTypeImport    NodeType = 8
)

const (
	NodeTypeHistoryEntry NodeType = 100
	NodeTypeSession      NodeType = 101
	NodeTypeWorkflow     NodeType = 102
	NodeTypeOutcome      NodeType = 103
	NodeTypeDecision     NodeType = 104
)

const (
	NodeTypePaper         NodeType = 200
	NodeTypeDocumentation NodeType = 201
	NodeTypeBestPractice  NodeType = 202
	NodeTypeRFC           NodeType = 203
	NodeTypeStackOverflow NodeType = 204
	NodeTypeBlogPost      NodeType = 205
	NodeTypeTutorial      NodeType = 206
)

// ValidNodeTypes returns all valid NodeType values.
func ValidNodeTypes() []NodeType {
	return []NodeType{
		NodeTypeFile,
		NodeTypePackage,
		NodeTypeFunction,
		NodeTypeMethod,
		NodeTypeStruct,
		NodeTypeInterface,
		NodeTypeVariable,
		NodeTypeConstant,
		NodeTypeImport,
		NodeTypeHistoryEntry,
		NodeTypeSession,
		NodeTypeWorkflow,
		NodeTypeOutcome,
		NodeTypeDecision,
		NodeTypePaper,
		NodeTypeDocumentation,
		NodeTypeBestPractice,
		NodeTypeRFC,
		NodeTypeStackOverflow,
		NodeTypeBlogPost,
		NodeTypeTutorial,
	}
}

// ValidNodeTypesForDomain returns all valid NodeType values for a specific domain.
func ValidNodeTypesForDomain(domain Domain) []NodeType {
	switch domain {
	case DomainCode:
		return []NodeType{
			NodeTypeFile,
			NodeTypePackage,
			NodeTypeFunction,
			NodeTypeMethod,
			NodeTypeStruct,
			NodeTypeInterface,
			NodeTypeVariable,
			NodeTypeConstant,
			NodeTypeImport,
		}
	case DomainHistory:
		return []NodeType{
			NodeTypeHistoryEntry,
			NodeTypeSession,
			NodeTypeWorkflow,
			NodeTypeOutcome,
			NodeTypeDecision,
		}
	case DomainAcademic:
		return []NodeType{
			NodeTypePaper,
			NodeTypeDocumentation,
			NodeTypeBestPractice,
			NodeTypeRFC,
			NodeTypeStackOverflow,
			NodeTypeBlogPost,
			NodeTypeTutorial,
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
	switch nt {
	case NodeTypeFile:
		return "file"
	case NodeTypePackage:
		return "package"
	case NodeTypeFunction:
		return "function"
	case NodeTypeMethod:
		return "method"
	case NodeTypeStruct:
		return "struct"
	case NodeTypeInterface:
		return "interface"
	case NodeTypeVariable:
		return "variable"
	case NodeTypeConstant:
		return "constant"
	case NodeTypeImport:
		return "import"
	case NodeTypeHistoryEntry:
		return "history_entry"
	case NodeTypeSession:
		return "session"
	case NodeTypeWorkflow:
		return "workflow"
	case NodeTypeOutcome:
		return "outcome"
	case NodeTypeDecision:
		return "decision"
	case NodeTypePaper:
		return "paper"
	case NodeTypeDocumentation:
		return "documentation"
	case NodeTypeBestPractice:
		return "best_practice"
	case NodeTypeRFC:
		return "rfc"
	case NodeTypeStackOverflow:
		return "stackoverflow"
	case NodeTypeBlogPost:
		return "blog_post"
	case NodeTypeTutorial:
		return "tutorial"
	default:
		return fmt.Sprintf("node_type(%d)", nt)
	}
}

func ParseNodeType(value string) (NodeType, bool) {
	switch value {
	case "file":
		return NodeTypeFile, true
	case "package":
		return NodeTypePackage, true
	case "function":
		return NodeTypeFunction, true
	case "method":
		return NodeTypeMethod, true
	case "struct":
		return NodeTypeStruct, true
	case "interface":
		return NodeTypeInterface, true
	case "variable":
		return NodeTypeVariable, true
	case "constant":
		return NodeTypeConstant, true
	case "import":
		return NodeTypeImport, true
	case "history_entry":
		return NodeTypeHistoryEntry, true
	case "session":
		return NodeTypeSession, true
	case "workflow":
		return NodeTypeWorkflow, true
	case "outcome":
		return NodeTypeOutcome, true
	case "decision":
		return NodeTypeDecision, true
	case "paper":
		return NodeTypePaper, true
	case "documentation":
		return NodeTypeDocumentation, true
	case "best_practice":
		return NodeTypeBestPractice, true
	case "rfc":
		return NodeTypeRFC, true
	case "stackoverflow":
		return NodeTypeStackOverflow, true
	case "blog_post":
		return NodeTypeBlogPost, true
	case "tutorial":
		return NodeTypeTutorial, true
	default:
		return NodeType(0), false
	}
}

func (nt NodeType) MarshalJSON() ([]byte, error) {
	return json.Marshal(nt.String())
}

func (nt *NodeType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseNodeType(asString); ok {
			*nt = parsed
			return nil
		}
		return fmt.Errorf("invalid node type: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*nt = NodeType(asInt)
		return nil
	}

	return fmt.Errorf("invalid node type")
}

// =============================================================================
// Edge Types
// =============================================================================

// EdgeType represents the type of relationship between nodes.
// Edges can be structural (within domain), temporal (sequence-based),
// or cross-domain (linking different knowledge domains).
type EdgeType int

const (
	EdgeTypeCalls         EdgeType = 0
	EdgeTypeCalledBy      EdgeType = 1
	EdgeTypeImports       EdgeType = 2
	EdgeTypeImportedBy    EdgeType = 3
	EdgeTypeImplements    EdgeType = 4
	EdgeTypeImplementedBy EdgeType = 5
	EdgeTypeEmbeds        EdgeType = 6
	EdgeTypeHasField      EdgeType = 7
	EdgeTypeHasMethod     EdgeType = 8
	EdgeTypeDefines       EdgeType = 9
	EdgeTypeDefinedIn     EdgeType = 10
	EdgeTypeReturns       EdgeType = 11
	EdgeTypeReceives      EdgeType = 12
)

const (
	EdgeTypeProducedBy EdgeType = 50
	EdgeTypeResultedIn EdgeType = 51
	EdgeTypeSimilarTo  EdgeType = 52
	EdgeTypeFollowedBy EdgeType = 53
	EdgeTypeSupersedes EdgeType = 54
)

const (
	EdgeTypeModified          EdgeType = 100
	EdgeTypeCreated           EdgeType = 101
	EdgeTypeDeleted           EdgeType = 102
	EdgeTypeBasedOn           EdgeType = 103
	EdgeTypeReferences        EdgeType = 104
	EdgeTypeValidatedBy       EdgeType = 105
	EdgeTypeDocuments         EdgeType = 106
	EdgeTypeUsesLibrary       EdgeType = 107
	EdgeTypeImplementsPattern EdgeType = 108
)

const (
	EdgeTypeCites     EdgeType = 150
	EdgeTypeRelatedTo EdgeType = 151
)

// ValidEdgeTypes returns all valid EdgeType values.
func ValidEdgeTypes() []EdgeType {
	return []EdgeType{
		EdgeTypeCalls,
		EdgeTypeCalledBy,
		EdgeTypeImports,
		EdgeTypeImportedBy,
		EdgeTypeImplements,
		EdgeTypeImplementedBy,
		EdgeTypeEmbeds,
		EdgeTypeHasField,
		EdgeTypeHasMethod,
		EdgeTypeDefines,
		EdgeTypeDefinedIn,
		EdgeTypeReturns,
		EdgeTypeReceives,
		EdgeTypeProducedBy,
		EdgeTypeResultedIn,
		EdgeTypeSimilarTo,
		EdgeTypeFollowedBy,
		EdgeTypeSupersedes,
		EdgeTypeModified,
		EdgeTypeCreated,
		EdgeTypeDeleted,
		EdgeTypeBasedOn,
		EdgeTypeReferences,
		EdgeTypeValidatedBy,
		EdgeTypeDocuments,
		EdgeTypeUsesLibrary,
		EdgeTypeImplementsPattern,
		EdgeTypeCites,
		EdgeTypeRelatedTo,
	}
}

// StructuralEdgeTypes returns edge types that represent structural relationships.
func StructuralEdgeTypes() []EdgeType {
	return []EdgeType{
		EdgeTypeCalls,
		EdgeTypeCalledBy,
		EdgeTypeImports,
		EdgeTypeImportedBy,
		EdgeTypeImplements,
		EdgeTypeImplementedBy,
		EdgeTypeEmbeds,
		EdgeTypeHasField,
		EdgeTypeHasMethod,
		EdgeTypeDefines,
		EdgeTypeDefinedIn,
		EdgeTypeReturns,
		EdgeTypeReceives,
	}
}

// TemporalEdgeTypes returns edge types that represent temporal relationships.
func TemporalEdgeTypes() []EdgeType {
	return []EdgeType{
		EdgeTypeProducedBy,
		EdgeTypeResultedIn,
		EdgeTypeSimilarTo,
		EdgeTypeFollowedBy,
		EdgeTypeSupersedes,
	}
}

// CrossDomainEdgeTypes returns edge types that link different domains.
func CrossDomainEdgeTypes() []EdgeType {
	return []EdgeType{
		EdgeTypeModified,
		EdgeTypeCreated,
		EdgeTypeDeleted,
		EdgeTypeBasedOn,
		EdgeTypeReferences,
		EdgeTypeValidatedBy,
		EdgeTypeDocuments,
		EdgeTypeUsesLibrary,
		EdgeTypeImplementsPattern,
		EdgeTypeCites,
		EdgeTypeRelatedTo,
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
	switch et {
	case EdgeTypeCalls:
		return "calls"
	case EdgeTypeCalledBy:
		return "called_by"
	case EdgeTypeImports:
		return "imports"
	case EdgeTypeImportedBy:
		return "imported_by"
	case EdgeTypeImplements:
		return "implements"
	case EdgeTypeImplementedBy:
		return "implemented_by"
	case EdgeTypeEmbeds:
		return "embeds"
	case EdgeTypeHasField:
		return "has_field"
	case EdgeTypeHasMethod:
		return "has_method"
	case EdgeTypeDefines:
		return "defines"
	case EdgeTypeDefinedIn:
		return "defined_in"
	case EdgeTypeReturns:
		return "returns"
	case EdgeTypeReceives:
		return "receives"
	case EdgeTypeProducedBy:
		return "produced_by"
	case EdgeTypeResultedIn:
		return "resulted_in"
	case EdgeTypeSimilarTo:
		return "similar_to"
	case EdgeTypeFollowedBy:
		return "followed_by"
	case EdgeTypeSupersedes:
		return "supersedes"
	case EdgeTypeModified:
		return "modified"
	case EdgeTypeCreated:
		return "created"
	case EdgeTypeDeleted:
		return "deleted"
	case EdgeTypeBasedOn:
		return "based_on"
	case EdgeTypeReferences:
		return "references"
	case EdgeTypeValidatedBy:
		return "validated_by"
	case EdgeTypeDocuments:
		return "documents"
	case EdgeTypeUsesLibrary:
		return "uses_library"
	case EdgeTypeImplementsPattern:
		return "implements_pattern"
	case EdgeTypeCites:
		return "cites"
	case EdgeTypeRelatedTo:
		return "related_to"
	default:
		return fmt.Sprintf("edge_type(%d)", et)
	}
}

func ParseEdgeType(value string) (EdgeType, bool) {
	switch value {
	case "calls":
		return EdgeTypeCalls, true
	case "called_by":
		return EdgeTypeCalledBy, true
	case "imports":
		return EdgeTypeImports, true
	case "imported_by":
		return EdgeTypeImportedBy, true
	case "implements":
		return EdgeTypeImplements, true
	case "implemented_by":
		return EdgeTypeImplementedBy, true
	case "embeds":
		return EdgeTypeEmbeds, true
	case "has_field":
		return EdgeTypeHasField, true
	case "has_method":
		return EdgeTypeHasMethod, true
	case "defines":
		return EdgeTypeDefines, true
	case "defined_in":
		return EdgeTypeDefinedIn, true
	case "returns":
		return EdgeTypeReturns, true
	case "receives":
		return EdgeTypeReceives, true
	case "produced_by":
		return EdgeTypeProducedBy, true
	case "resulted_in":
		return EdgeTypeResultedIn, true
	case "similar_to":
		return EdgeTypeSimilarTo, true
	case "followed_by":
		return EdgeTypeFollowedBy, true
	case "supersedes":
		return EdgeTypeSupersedes, true
	case "modified":
		return EdgeTypeModified, true
	case "created":
		return EdgeTypeCreated, true
	case "deleted":
		return EdgeTypeDeleted, true
	case "based_on":
		return EdgeTypeBasedOn, true
	case "references":
		return EdgeTypeReferences, true
	case "validated_by":
		return EdgeTypeValidatedBy, true
	case "documents":
		return EdgeTypeDocuments, true
	case "uses_library":
		return EdgeTypeUsesLibrary, true
	case "implements_pattern":
		return EdgeTypeImplementsPattern, true
	case "cites":
		return EdgeTypeCites, true
	case "related_to":
		return EdgeTypeRelatedTo, true
	default:
		return EdgeType(0), false
	}
}

func (et EdgeType) MarshalJSON() ([]byte, error) {
	return json.Marshal(et.String())
}

func (et *EdgeType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseEdgeType(asString); ok {
			*et = parsed
			return nil
		}
		return fmt.Errorf("invalid edge type: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*et = EdgeType(asInt)
		return nil
	}

	return fmt.Errorf("invalid edge type")
}

// =============================================================================
// Core Data Structures
// =============================================================================

// GraphNode represents a node in the VectorGraphDB knowledge graph.
// Nodes belong to a specific domain and type, and can be connected
// to other nodes via edges.
type GraphNode struct {
	ID       string   `json:"id"`
	Domain   Domain   `json:"domain"`
	NodeType NodeType `json:"node_type"`
	Name     string   `json:"name"`

	Path      string `json:"path,omitempty"`
	Package   string `json:"package,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	Signature string `json:"signature,omitempty"`

	SessionID string    `json:"session_id,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Category  string    `json:"category,omitempty"`

	URL         string    `json:"url,omitempty"`
	Source      string    `json:"source,omitempty"`
	Authors     any       `json:"authors,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`

	Content     string         `json:"content,omitempty"`
	ContentHash string         `json:"content_hash,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`

	Verified         bool             `json:"verified"`
	VerificationType VerificationType `json:"verification_type,omitempty"`
	Confidence       float64          `json:"confidence,omitempty"`
	TrustLevel       TrustLevel       `json:"trust_level,omitempty"`

	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	SupersededBy string    `json:"superseded_by,omitempty"`

	// Version is used for optimistic concurrency control.
	// It is incremented on each update and checked during writes.
	Version uint64 `json:"version,omitempty"`
}

// GraphEdge represents a directed edge between two nodes in the knowledge graph.
// Edges have a type that defines the nature of the relationship and an optional
// weight for scoring.
type GraphEdge struct {
	ID        int64          `json:"id"`
	SourceID  string         `json:"source_id"`
	TargetID  string         `json:"target_id"`
	EdgeType  EdgeType       `json:"edge_type"`
	Weight    float64        `json:"weight"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// VectorData represents the embedding vector associated with a node.
// Stored separately from the node for efficient vector operations.
type VectorData struct {
	NodeID     string    `json:"node_id"`
	Embedding  []float32 `json:"embedding"`
	Magnitude  float64   `json:"magnitude"`
	Dimensions int       `json:"dimensions"`
	Domain     Domain    `json:"domain"`
	NodeType   NodeType  `json:"node_type"`
}

// Provenance tracks the source and verification status of a node.
// Critical for hallucination mitigation and trust scoring.
type Provenance struct {
	ID           int64      `json:"id"`
	NodeID       string     `json:"node_id"`
	SourceType   SourceType `json:"source_type"`
	SourceNodeID string     `json:"source_node_id,omitempty"`
	SourcePath   string     `json:"source_path,omitempty"`
	SourceURL    string     `json:"source_url,omitempty"`
	Confidence   float64    `json:"confidence"`
	VerifiedAt   time.Time  `json:"verified_at,omitempty"`
}

// Conflict represents a detected contradiction between two nodes.
// Used for hallucination detection and conflict resolution.
type Conflict struct {
	ID           int64        `json:"id"`
	ConflictType ConflictType `json:"conflict_type"`
	Subject      string       `json:"subject"`
	NodeAID      string       `json:"node_id_a"`
	NodeBID      string       `json:"node_id_b"`
	Description  string       `json:"description"`
	Resolution   string       `json:"resolution,omitempty"`
	Resolved     bool         `json:"resolved"`
	DetectedAt   time.Time    `json:"detected_at"`
	ResolvedAt   time.Time    `json:"resolved_at,omitempty"`
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

type VerificationType int

type SourceType int

type TrustLevel int

type ConflictType int

const (
	VerificationNone           VerificationType = 0
	VerificationAgainstCode    VerificationType = 1
	VerificationAgainstHistory VerificationType = 2
	VerificationByUser         VerificationType = 3
)

const (
	SourceTypeCode         SourceType = 0
	SourceTypeHistory      SourceType = 1
	SourceTypeAcademic     SourceType = 2
	SourceTypeLLMInference SourceType = 3
	SourceTypeUserProvided SourceType = 4
)

const (
	TrustLevelGround     TrustLevel = 100
	TrustLevelRecent     TrustLevel = 80
	TrustLevelStandard   TrustLevel = 70
	TrustLevelAcademic   TrustLevel = 60
	TrustLevelOldHistory TrustLevel = 40
	TrustLevelBlog       TrustLevel = 30
	TrustLevelLLM        TrustLevel = 20
)

const (
	ConflictTypeTemporal       ConflictType = 0
	ConflictTypeSourceMismatch ConflictType = 1
	ConflictTypeSemantic       ConflictType = 2
)

func (st SourceType) String() string {
	switch st {
	case SourceTypeCode:
		return "code"
	case SourceTypeHistory:
		return "history"
	case SourceTypeAcademic:
		return "academic"
	case SourceTypeLLMInference:
		return "llm_inference"
	case SourceTypeUserProvided:
		return "user_provided"
	default:
		return fmt.Sprintf("source_type(%d)", st)
	}
}

func ParseSourceType(value string) (SourceType, bool) {
	switch value {
	case "code":
		return SourceTypeCode, true
	case "history":
		return SourceTypeHistory, true
	case "academic":
		return SourceTypeAcademic, true
	case "llm_inference":
		return SourceTypeLLMInference, true
	case "user_provided":
		return SourceTypeUserProvided, true
	default:
		return SourceType(0), false
	}
}

func (st SourceType) MarshalJSON() ([]byte, error) {
	return json.Marshal(st.String())
}

func (st *SourceType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseSourceType(asString); ok {
			*st = parsed
			return nil
		}
		return fmt.Errorf("invalid source type: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*st = SourceType(asInt)
		return nil
	}

	return fmt.Errorf("invalid source type")
}

func (ct ConflictType) String() string {
	switch ct {
	case ConflictTypeTemporal:
		return "temporal"
	case ConflictTypeSourceMismatch:
		return "source_mismatch"
	case ConflictTypeSemantic:
		return "semantic"
	default:
		return fmt.Sprintf("conflict_type(%d)", ct)
	}
}

func ParseConflictType(value string) (ConflictType, bool) {
	switch value {
	case "temporal":
		return ConflictTypeTemporal, true
	case "source_mismatch":
		return ConflictTypeSourceMismatch, true
	case "semantic":
		return ConflictTypeSemantic, true
	default:
		return ConflictType(0), false
	}
}

func (ct ConflictType) MarshalJSON() ([]byte, error) {
	return json.Marshal(ct.String())
}

func (ct *ConflictType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseConflictType(asString); ok {
			*ct = parsed
			return nil
		}
		return fmt.Errorf("invalid conflict type: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*ct = ConflictType(asInt)
		return nil
	}

	return fmt.Errorf("invalid conflict type")
}
