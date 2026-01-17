package vectorgraphdb

import "time"

const (
	SchemaVersion = 2

	DefaultM           = 16
	DefaultEfConstruct = 200
	DefaultEfSearch    = 50
	DefaultLevelMult   = 0.36067977499789996

	EmbeddingDimension = 768

	DefaultMaxNodes          = 100000
	DefaultStaleThreshold    = 30 * 24 * time.Hour
	DefaultVacuumInterval    = 24 * time.Hour
	DefaultBatchSize         = 1000
	DefaultQueryTimeout      = 5 * time.Second
	DefaultInsertTimeout     = 1 * time.Second
	DefaultTraversalMaxDepth = 10
	DefaultTraversalMaxNodes = 1000
	DefaultHybridSearchAlpha = 0.7
	DefaultMinSimilarity     = 0.5
	DefaultMaxSearchResults  = 100
	DefaultCacheSize         = 10000
	DefaultCacheTTL          = 5 * time.Minute
)

var ValidSourceTypes = []SourceType{
	SourceTypeCode,
	SourceTypeHistory,
	SourceTypeAcademic,
	SourceTypeLLMInference,
	SourceTypeUserProvided,
}

var ValidConflictTypes = []ConflictType{
	ConflictTypeTemporal,
	ConflictTypeSourceMismatch,
	ConflictTypeSemantic,
}

type CrossDomainEdgeRule struct {
	EdgeType   EdgeType
	FromDomain Domain
	ToDomain   Domain
}

var CrossDomainEdgeRules = []CrossDomainEdgeRule{
	{EdgeTypeReferences, DomainCode, DomainAcademic},
	{EdgeTypeReferences, DomainAcademic, DomainCode},
	{EdgeTypeBasedOn, DomainHistory, DomainAcademic},
	{EdgeTypeDocuments, DomainAcademic, DomainCode},
	{EdgeTypeModified, DomainHistory, DomainCode},
	{EdgeTypeCreated, DomainHistory, DomainCode},
	{EdgeTypeDeleted, DomainHistory, DomainCode},
	{EdgeTypeValidatedBy, DomainHistory, DomainAcademic},
	{EdgeTypeUsesLibrary, DomainCode, DomainAcademic},
	{EdgeTypeImplementsPattern, DomainCode, DomainAcademic},
}

func IsValidCrossDomainEdge(edgeType EdgeType, fromDomain, toDomain Domain) bool {
	for _, rule := range CrossDomainEdgeRules {
		if rule.EdgeType == edgeType && rule.FromDomain == fromDomain && rule.ToDomain == toDomain {
			return true
		}
	}
	return false
}

func IsValidSourceType(sourceType SourceType) bool {
	for _, valid := range ValidSourceTypes {
		if sourceType == valid {
			return true
		}
	}
	return false
}

func IsValidConflictType(conflictType ConflictType) bool {
	for _, valid := range ValidConflictTypes {
		if conflictType == valid {
			return true
		}
	}
	return false
}
