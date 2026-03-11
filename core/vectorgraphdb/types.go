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
// VectorGraphDB operates across multiple distinct domains that can be linked
// through cross-domain edges. The first three domains (Code, History, Academic)
// maintain backward compatibility with existing stored vectors.
type Domain int

const (
	DomainCode     Domain = 0
	DomainHistory  Domain = 1
	DomainAcademic Domain = 2

	DomainArchitect    Domain = 3
	DomainEngineer     Domain = 4
	DomainDesigner     Domain = 5
	DomainInspector    Domain = 6
	DomainTester       Domain = 7
	DomainOrchestrator Domain = 8
	DomainGuide        Domain = 9
)

func ValidDomains() []Domain {
	return cloneEnumSlice(validDomains)
}

func (d Domain) IsValid() bool {
	return d >= DomainCode && d <= DomainGuide
}

func (d Domain) String() string {
	return enumString(d, domainNames, "domain")
}

func ParseDomain(value string) (Domain, bool) {
	domain, ok := domainByName[value]
	return domain, ok
}

func KnowledgeDomains() []Domain {
	return cloneEnumSlice(knowledgeDomains)
}

func PipelineDomains() []Domain {
	return cloneEnumSlice(pipelineDomains)
}

func ControlDomains() []Domain {
	return cloneEnumSlice(controlDomains)
}

func (d Domain) IsKnowledge() bool {
	return containsEnum(knowledgeDomainSet, d)
}

func (d Domain) IsPipeline() bool {
	return containsEnum(pipelineDomainSet, d)
}

func (d Domain) IsControl() bool {
	return containsEnum(controlDomainSet, d)
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

const (
	NodeTypeArchitectureDecision NodeType = 300
	NodeTypeDesignPattern        NodeType = 301
	NodeTypeSystemDiagram        NodeType = 302
)

const (
	NodeTypeTask           NodeType = 400
	NodeTypeImplementation NodeType = 401
	NodeTypeCodeChange     NodeType = 402
)

const (
	NodeTypeUIComponent NodeType = 500
	NodeTypeStyleGuide  NodeType = 501
	NodeTypeDesignAsset NodeType = 502
)

const (
	NodeTypeInspection    NodeType = 600
	NodeTypeCodeReview    NodeType = 601
	NodeTypeQualityMetric NodeType = 602
)

const (
	NodeTypeTestCase   NodeType = 700
	NodeTypeTestSuite  NodeType = 701
	NodeTypeTestResult NodeType = 702
)

const (
	NodeTypeWorkflowDef NodeType = 800
	NodeTypeAgentConfig NodeType = 801
	NodeTypePipeline    NodeType = 802
)

const (
	NodeTypeRoutingRule   NodeType = 900
	NodeTypeIntentPattern NodeType = 901
	NodeTypeUserQuery     NodeType = 902
)

func ValidNodeTypes() []NodeType {
	return cloneEnumSlice(validNodeTypes)
}

func ValidNodeTypesForDomain(domain Domain) []NodeType {
	nodeTypes, ok := nodeTypesByDomain[domain]
	if !ok {
		return nil
	}
	return cloneEnumSlice(nodeTypes)
}

// IsValid returns true if the node type is a recognized value.
func (nt NodeType) IsValid() bool {
	return containsEnum(validNodeTypeSet, nt)
}

// IsValidForDomain returns true if the node type is valid for the given domain.
func (nt NodeType) IsValidForDomain(domain Domain) bool {
	validTypes, ok := nodeTypeDomainSets[domain]
	if !ok {
		return false
	}
	return containsEnum(validTypes, nt)
}

func (nt NodeType) String() string {
	return enumString(nt, nodeTypeNames, "node_type")
}

func ParseNodeType(value string) (NodeType, bool) {
	nodeType, ok := nodeTypeByName[value]
	return nodeType, ok
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
	return cloneEnumSlice(validEdgeTypes)
}

// StructuralEdgeTypes returns edge types that represent structural relationships.
func StructuralEdgeTypes() []EdgeType {
	return cloneEnumSlice(structuralEdgeTypes)
}

// TemporalEdgeTypes returns edge types that represent temporal relationships.
func TemporalEdgeTypes() []EdgeType {
	return cloneEnumSlice(temporalEdgeTypes)
}

// CrossDomainEdgeTypes returns edge types that link different domains.
func CrossDomainEdgeTypes() []EdgeType {
	return cloneEnumSlice(crossDomainEdgeTypes)
}

// IsValid returns true if the edge type is a recognized value.
func (et EdgeType) IsValid() bool {
	return containsEnum(validEdgeTypeSet, et)
}

// IsStructural returns true if this is a structural edge type.
func (et EdgeType) IsStructural() bool {
	return containsEnum(structuralEdgeTypeSet, et)
}

// IsTemporal returns true if this is a temporal edge type.
func (et EdgeType) IsTemporal() bool {
	return containsEnum(temporalEdgeTypeSet, et)
}

// IsCrossDomain returns true if this is a cross-domain edge type.
func (et EdgeType) IsCrossDomain() bool {
	return containsEnum(crossDomainEdgeTypeSet, et)
}

// String returns the string representation of the edge type.
func (et EdgeType) String() string {
	return enumString(et, edgeTypeNames, "edge_type")
}

func ParseEdgeType(value string) (EdgeType, bool) {
	edgeType, ok := edgeTypeByName[value]
	return edgeType, ok
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

	// IndexSize is the size of the vector index in bytes.
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
	return enumString(st, sourceTypeNames, "source_type")
}

func ParseSourceType(value string) (SourceType, bool) {
	sourceType, ok := sourceTypeByName[value]
	return sourceType, ok
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
	return enumString(ct, conflictTypeNames, "conflict_type")
}

func ParseConflictType(value string) (ConflictType, bool) {
	conflictType, ok := conflictTypeByName[value]
	return conflictType, ok
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
