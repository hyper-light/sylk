package vectorgraphdb

import "fmt"

func cloneEnumSlice[T any](values []T) []T {
	return append([]T(nil), values...)
}

func buildEnumSet[T comparable](values []T) map[T]struct{} {
	set := make(map[T]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func buildEnumParseMap[T comparable](names map[T]string) map[string]T {
	parsed := make(map[string]T, len(names))
	for value, name := range names {
		parsed[name] = value
	}
	return parsed
}

func enumString[T ~int](value T, names map[T]string, kind string) string {
	if name, ok := names[value]; ok {
		return name
	}
	return fmt.Sprintf("%s(%d)", kind, value)
}

func containsEnum[T comparable](set map[T]struct{}, value T) bool {
	_, ok := set[value]
	return ok
}

func buildNodeTypeDomainSets(nodeTypesByDomain map[Domain][]NodeType) map[Domain]map[NodeType]struct{} {
	sets := make(map[Domain]map[NodeType]struct{}, len(nodeTypesByDomain))
	for domain, nodeTypes := range nodeTypesByDomain {
		sets[domain] = buildEnumSet(nodeTypes)
	}
	return sets
}

var (
	validDomains = []Domain{
		DomainCode,
		DomainHistory,
		DomainAcademic,
		DomainArchitect,
		DomainEngineer,
		DomainDesigner,
		DomainInspector,
		DomainTester,
		DomainOrchestrator,
		DomainGuide,
		DomainClaims,
	}
	domainNames = map[Domain]string{
		DomainCode:         "code",
		DomainHistory:      "history",
		DomainAcademic:     "academic",
		DomainArchitect:    "architect",
		DomainEngineer:     "engineer",
		DomainDesigner:     "designer",
		DomainInspector:    "inspector",
		DomainTester:       "tester",
		DomainOrchestrator: "orchestrator",
		DomainGuide:        "guide",
		DomainClaims:       "claims",
	}
	domainByName = buildEnumParseMap(domainNames)

	knowledgeDomains = []Domain{
		DomainCode,
		DomainHistory,
		DomainAcademic,
		DomainArchitect,
		DomainClaims,
	}
	pipelineDomains = []Domain{
		DomainEngineer,
		DomainDesigner,
		DomainInspector,
		DomainTester,
	}
	controlDomains = []Domain{
		DomainOrchestrator,
		DomainGuide,
	}
	knowledgeDomainSet = buildEnumSet(knowledgeDomains)
	pipelineDomainSet  = buildEnumSet(pipelineDomains)
	controlDomainSet   = buildEnumSet(controlDomains)

	validNodeTypes = []NodeType{
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
		NodeTypeArchitectureDecision,
		NodeTypeDesignPattern,
		NodeTypeSystemDiagram,
		NodeTypeTask,
		NodeTypeImplementation,
		NodeTypeCodeChange,
		NodeTypeUIComponent,
		NodeTypeStyleGuide,
		NodeTypeDesignAsset,
		NodeTypeInspection,
		NodeTypeCodeReview,
		NodeTypeQualityMetric,
		NodeTypeTestCase,
		NodeTypeTestSuite,
		NodeTypeTestResult,
		NodeTypeWorkflowDef,
		NodeTypeAgentConfig,
		NodeTypePipeline,
		NodeTypeRoutingRule,
		NodeTypeIntentPattern,
		NodeTypeUserQuery,
		NodeTypeClaim,
		NodeTypeTestament,
		NodeTypeArtifact,
		NodeTypeValidation,
	}
	nodeTypeNames = map[NodeType]string{
		NodeTypeFile:                 "file",
		NodeTypePackage:              "package",
		NodeTypeFunction:             "function",
		NodeTypeMethod:               "method",
		NodeTypeStruct:               "struct",
		NodeTypeInterface:            "interface",
		NodeTypeVariable:             "variable",
		NodeTypeConstant:             "constant",
		NodeTypeImport:               "import",
		NodeTypeHistoryEntry:         "history_entry",
		NodeTypeSession:              "session",
		NodeTypeWorkflow:             "workflow",
		NodeTypeOutcome:              "outcome",
		NodeTypeDecision:             "decision",
		NodeTypePaper:                "paper",
		NodeTypeDocumentation:        "documentation",
		NodeTypeBestPractice:         "best_practice",
		NodeTypeRFC:                  "rfc",
		NodeTypeStackOverflow:        "stackoverflow",
		NodeTypeBlogPost:             "blog_post",
		NodeTypeTutorial:             "tutorial",
		NodeTypeArchitectureDecision: "architecture_decision",
		NodeTypeDesignPattern:        "design_pattern",
		NodeTypeSystemDiagram:        "system_diagram",
		NodeTypeTask:                 "task",
		NodeTypeImplementation:       "implementation",
		NodeTypeCodeChange:           "code_change",
		NodeTypeUIComponent:          "ui_component",
		NodeTypeStyleGuide:           "style_guide",
		NodeTypeDesignAsset:          "design_asset",
		NodeTypeInspection:           "inspection",
		NodeTypeCodeReview:           "code_review",
		NodeTypeQualityMetric:        "quality_metric",
		NodeTypeTestCase:             "test_case",
		NodeTypeTestSuite:            "test_suite",
		NodeTypeTestResult:           "test_result",
		NodeTypeWorkflowDef:          "workflow_def",
		NodeTypeAgentConfig:          "agent_config",
		NodeTypePipeline:             "pipeline",
		NodeTypeRoutingRule:          "routing_rule",
		NodeTypeIntentPattern:        "intent_pattern",
		NodeTypeUserQuery:            "user_query",
		NodeTypeClaim:                "claim",
		NodeTypeTestament:            "testament",
		NodeTypeArtifact:             "artifact",
		NodeTypeValidation:           "validation",
	}
	nodeTypeByName    = buildEnumParseMap(nodeTypeNames)
	nodeTypesByDomain = map[Domain][]NodeType{
		DomainCode: {
			NodeTypeFile,
			NodeTypePackage,
			NodeTypeFunction,
			NodeTypeMethod,
			NodeTypeStruct,
			NodeTypeInterface,
			NodeTypeVariable,
			NodeTypeConstant,
			NodeTypeImport,
		},
		DomainHistory: {
			NodeTypeHistoryEntry,
			NodeTypeSession,
			NodeTypeWorkflow,
			NodeTypeOutcome,
			NodeTypeDecision,
		},
		DomainAcademic: {
			NodeTypePaper,
			NodeTypeDocumentation,
			NodeTypeBestPractice,
			NodeTypeRFC,
			NodeTypeStackOverflow,
			NodeTypeBlogPost,
			NodeTypeTutorial,
		},
		DomainArchitect: {
			NodeTypeArchitectureDecision,
			NodeTypeDesignPattern,
			NodeTypeSystemDiagram,
		},
		DomainEngineer: {
			NodeTypeTask,
			NodeTypeImplementation,
			NodeTypeCodeChange,
		},
		DomainDesigner: {
			NodeTypeUIComponent,
			NodeTypeStyleGuide,
			NodeTypeDesignAsset,
		},
		DomainInspector: {
			NodeTypeInspection,
			NodeTypeCodeReview,
			NodeTypeQualityMetric,
		},
		DomainTester: {
			NodeTypeTestCase,
			NodeTypeTestSuite,
			NodeTypeTestResult,
		},
		DomainOrchestrator: {
			NodeTypeWorkflowDef,
			NodeTypeAgentConfig,
			NodeTypePipeline,
		},
		DomainGuide: {
			NodeTypeRoutingRule,
			NodeTypeIntentPattern,
			NodeTypeUserQuery,
		},
		DomainClaims: {
			NodeTypeClaim,
			NodeTypeTestament,
			NodeTypeArtifact,
			NodeTypeValidation,
		},
	}
	validNodeTypeSet   = buildEnumSet(validNodeTypes)
	nodeTypeDomainSets = buildNodeTypeDomainSets(nodeTypesByDomain)

	validEdgeTypes = []EdgeType{
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
		EdgeTypeDependsOn,
		EdgeTypeCausedBy,
		EdgeTypeRefines,
		EdgeTypeConflictsWith,
		EdgeTypeTestifies,
		EdgeTypeProves,
	}
	structuralEdgeTypes = []EdgeType{
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
	temporalEdgeTypes = []EdgeType{
		EdgeTypeProducedBy,
		EdgeTypeResultedIn,
		EdgeTypeSimilarTo,
		EdgeTypeFollowedBy,
		EdgeTypeSupersedes,
	}
	crossDomainEdgeTypes = []EdgeType{
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
	claimsEdgeTypes = []EdgeType{
		EdgeTypeDependsOn,
		EdgeTypeCausedBy,
		EdgeTypeRefines,
		EdgeTypeConflictsWith,
		EdgeTypeTestifies,
		EdgeTypeProves,
	}
	validEdgeTypeSet       = buildEnumSet(validEdgeTypes)
	structuralEdgeTypeSet  = buildEnumSet(structuralEdgeTypes)
	temporalEdgeTypeSet    = buildEnumSet(temporalEdgeTypes)
	crossDomainEdgeTypeSet = buildEnumSet(crossDomainEdgeTypes)
	claimsEdgeTypeSet      = buildEnumSet(claimsEdgeTypes)
	edgeTypeNames          = map[EdgeType]string{
		EdgeTypeCalls:             "calls",
		EdgeTypeCalledBy:          "called_by",
		EdgeTypeImports:           "imports",
		EdgeTypeImportedBy:        "imported_by",
		EdgeTypeImplements:        "implements",
		EdgeTypeImplementedBy:     "implemented_by",
		EdgeTypeEmbeds:            "embeds",
		EdgeTypeHasField:          "has_field",
		EdgeTypeHasMethod:         "has_method",
		EdgeTypeDefines:           "defines",
		EdgeTypeDefinedIn:         "defined_in",
		EdgeTypeReturns:           "returns",
		EdgeTypeReceives:          "receives",
		EdgeTypeProducedBy:        "produced_by",
		EdgeTypeResultedIn:        "resulted_in",
		EdgeTypeSimilarTo:         "similar_to",
		EdgeTypeFollowedBy:        "followed_by",
		EdgeTypeSupersedes:        "supersedes",
		EdgeTypeModified:          "modified",
		EdgeTypeCreated:           "created",
		EdgeTypeDeleted:           "deleted",
		EdgeTypeBasedOn:           "based_on",
		EdgeTypeReferences:        "references",
		EdgeTypeValidatedBy:       "validated_by",
		EdgeTypeDocuments:         "documents",
		EdgeTypeUsesLibrary:       "uses_library",
		EdgeTypeImplementsPattern: "implements_pattern",
		EdgeTypeCites:             "cites",
		EdgeTypeRelatedTo:         "related_to",
		EdgeTypeDependsOn:         "depends_on",
		EdgeTypeCausedBy:          "caused_by",
		EdgeTypeRefines:           "refines",
		EdgeTypeConflictsWith:     "conflicts_with",
		EdgeTypeTestifies:         "testifies",
		EdgeTypeProves:            "proves",
	}
	edgeTypeByName = buildEnumParseMap(edgeTypeNames)

	sourceTypeNames = map[SourceType]string{
		SourceTypeCode:         "code",
		SourceTypeHistory:      "history",
		SourceTypeAcademic:     "academic",
		SourceTypeLLMInference: "llm_inference",
		SourceTypeUserProvided: "user_provided",
	}
	sourceTypeByName = buildEnumParseMap(sourceTypeNames)

	conflictTypeNames = map[ConflictType]string{
		ConflictTypeTemporal:       "temporal",
		ConflictTypeSourceMismatch: "source_mismatch",
		ConflictTypeSemantic:       "semantic",
	}
	conflictTypeByName = buildEnumParseMap(conflictTypeNames)
)
