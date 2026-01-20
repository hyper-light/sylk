package vectorgraphdb

import (
	"encoding/json"
	"testing"
	"time"
)

// =============================================================================
// Domain Tests
// =============================================================================

func TestDomainConstants(t *testing.T) {
	tests := []struct {
		name     string
		domain   Domain
		expected string
	}{
		{"DomainCode", DomainCode, "code"},
		{"DomainHistory", DomainHistory, "history"},
		{"DomainAcademic", DomainAcademic, "academic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.domain.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.domain.String())
			}
		})
	}
}

func TestValidDomains(t *testing.T) {
	domains := ValidDomains()

	if len(domains) != 10 {
		t.Errorf("expected 10 domains, got %d", len(domains))
	}

	expectedDomains := map[Domain]bool{
		DomainCode:         false,
		DomainHistory:      false,
		DomainAcademic:     false,
		DomainArchitect:    false,
		DomainEngineer:     false,
		DomainDesigner:     false,
		DomainInspector:    false,
		DomainTester:       false,
		DomainOrchestrator: false,
		DomainGuide:        false,
	}

	for _, d := range domains {
		if _, exists := expectedDomains[d]; !exists {
			t.Errorf("unexpected domain in ValidDomains: %q", d)
		}
		expectedDomains[d] = true
	}

	for domain, found := range expectedDomains {
		if !found {
			t.Errorf("expected domain %q not found in ValidDomains", domain)
		}
	}
}

func TestDomainIsValid(t *testing.T) {
	tests := []struct {
		name     string
		domain   Domain
		expected bool
	}{
		{"valid code domain", DomainCode, true},
		{"valid history domain", DomainHistory, true},
		{"valid academic domain", DomainAcademic, true},
		{"valid architect domain", DomainArchitect, true},
		{"valid engineer domain", DomainEngineer, true},
		{"valid designer domain", DomainDesigner, true},
		{"valid inspector domain", DomainInspector, true},
		{"valid tester domain", DomainTester, true},
		{"valid orchestrator domain", DomainOrchestrator, true},
		{"valid guide domain", DomainGuide, true},
		{"invalid empty domain", Domain(-1), false},
		{"invalid arbitrary domain", Domain(99), false},
		{"invalid out of range domain", Domain(10), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.domain.IsValid(); got != tt.expected {
				t.Errorf("Domain(%q).IsValid() = %v, want %v", tt.domain, got, tt.expected)
			}
		})
	}
}

func TestDomainString(t *testing.T) {
	tests := []struct {
		domain   Domain
		expected string
	}{
		{DomainCode, "code"},
		{DomainHistory, "history"},
		{DomainAcademic, "academic"},
		{Domain(42), "domain(42)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.domain.String(); got != tt.expected {
				t.Errorf("Domain(%q).String() = %q, want %q", tt.domain, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// NodeType Tests
// =============================================================================

func TestNodeTypeConstants(t *testing.T) {
	// Code domain node types
	codeTypes := []struct {
		nodeType NodeType
		expected string
	}{
		{NodeTypeFile, "file"},
		{NodeTypePackage, "package"},
		{NodeTypeFunction, "function"},
		{NodeTypeMethod, "method"},
		{NodeTypeStruct, "struct"},
		{NodeTypeInterface, "interface"},
		{NodeTypeVariable, "variable"},
		{NodeTypeConstant, "constant"},
		{NodeTypeImport, "import"},
	}

	// History domain node types
	historyTypes := []struct {
		nodeType NodeType
		expected string
	}{
		{NodeTypeHistoryEntry, "history_entry"},
		{NodeTypeSession, "session"},
		{NodeTypeWorkflow, "workflow"},
		{NodeTypeOutcome, "outcome"},
		{NodeTypeDecision, "decision"},
	}

	// Academic domain node types
	academicTypes := []struct {
		nodeType NodeType
		expected string
	}{
		{NodeTypePaper, "paper"},
		{NodeTypeDocumentation, "documentation"},
		{NodeTypeBestPractice, "best_practice"},
		{NodeTypeRFC, "rfc"},
		{NodeTypeStackOverflow, "stackoverflow"},
		{NodeTypeBlogPost, "blog_post"},
		{NodeTypeTutorial, "tutorial"},
	}

	allTypes := append(codeTypes, append(historyTypes, academicTypes...)...)

	for _, tt := range allTypes {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.nodeType.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.nodeType.String())
			}
		})
	}
}

func TestValidNodeTypes(t *testing.T) {
	nodeTypes := ValidNodeTypes()

	if len(nodeTypes) != 42 {
		t.Errorf("expected 42 node types, got %d", len(nodeTypes))
	}

	for _, nt := range nodeTypes {
		if !nt.IsValid() {
			t.Errorf("ValidNodeTypes contains invalid type: %q", nt)
		}
	}
}

func TestValidNodeTypesForDomain(t *testing.T) {
	tests := []struct {
		name          string
		domain        Domain
		expectedTypes []NodeType
	}{
		{
			name:   "code domain",
			domain: DomainCode,
			expectedTypes: []NodeType{
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
		},
		{
			name:   "history domain",
			domain: DomainHistory,
			expectedTypes: []NodeType{
				NodeTypeHistoryEntry,
				NodeTypeSession,
				NodeTypeWorkflow,
				NodeTypeOutcome,
				NodeTypeDecision,
			},
		},
		{
			name:   "academic domain",
			domain: DomainAcademic,
			expectedTypes: []NodeType{
				NodeTypePaper,
				NodeTypeDocumentation,
				NodeTypeBestPractice,
				NodeTypeRFC,
				NodeTypeStackOverflow,
				NodeTypeBlogPost,
				NodeTypeTutorial,
			},
		},
		{
			name:          "invalid domain",
			domain:        Domain(99),
			expectedTypes: nil,
		},
		{
			name:          "empty domain",
			domain:        Domain(-1),
			expectedTypes: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidNodeTypesForDomain(tt.domain)

			if tt.expectedTypes == nil {
				if got != nil {
					t.Errorf("ValidNodeTypesForDomain(%q) = %v, want nil", tt.domain, got)
				}
				return
			}

			if len(got) != len(tt.expectedTypes) {
				t.Errorf("ValidNodeTypesForDomain(%q) returned %d types, want %d",
					tt.domain, len(got), len(tt.expectedTypes))
				return
			}

			// Check that all expected types are present
			typeSet := make(map[NodeType]bool)
			for _, nt := range got {
				typeSet[nt] = true
			}

			for _, expected := range tt.expectedTypes {
				if !typeSet[expected] {
					t.Errorf("ValidNodeTypesForDomain(%q) missing expected type %q",
						tt.domain, expected)
				}
			}
		})
	}
}

func TestNodeTypeIsValid(t *testing.T) {
	// All valid node types
	validTypes := ValidNodeTypes()
	for _, nt := range validTypes {
		t.Run("valid_"+nt.String(), func(t *testing.T) {

			if !nt.IsValid() {
				t.Errorf("NodeType(%q).IsValid() = false, want true", nt)
			}
		})
	}

	// Invalid node types
	invalidTypes := []NodeType{
		NodeType(-1),
		NodeType(999),
		NodeType(42),
	}

	for _, nt := range invalidTypes {
		t.Run("invalid_"+nt.String(), func(t *testing.T) {

			if nt.IsValid() {
				t.Errorf("NodeType(%q).IsValid() = true, want false", nt)
			}
		})
	}
}

func TestNodeTypeIsValidForDomain(t *testing.T) {
	tests := []struct {
		name     string
		nodeType NodeType
		domain   Domain
		expected bool
	}{
		// Code domain - valid
		{"file in code", NodeTypeFile, DomainCode, true},
		{"package in code", NodeTypePackage, DomainCode, true},
		{"function in code", NodeTypeFunction, DomainCode, true},
		{"method in code", NodeTypeMethod, DomainCode, true},
		{"struct in code", NodeTypeStruct, DomainCode, true},
		{"interface in code", NodeTypeInterface, DomainCode, true},
		{"variable in code", NodeTypeVariable, DomainCode, true},
		{"constant in code", NodeTypeConstant, DomainCode, true},
		{"import in code", NodeTypeImport, DomainCode, true},

		// Code domain - invalid
		{"session in code", NodeTypeSession, DomainCode, false},
		{"paper in code", NodeTypePaper, DomainCode, false},

		// History domain - valid
		{"history_entry in history", NodeTypeHistoryEntry, DomainHistory, true},
		{"session in history", NodeTypeSession, DomainHistory, true},
		{"workflow in history", NodeTypeWorkflow, DomainHistory, true},
		{"outcome in history", NodeTypeOutcome, DomainHistory, true},
		{"decision in history", NodeTypeDecision, DomainHistory, true},

		// History domain - invalid
		{"file in history", NodeTypeFile, DomainHistory, false},
		{"documentation in history", NodeTypeDocumentation, DomainHistory, false},

		// Academic domain - valid
		{"paper in academic", NodeTypePaper, DomainAcademic, true},
		{"documentation in academic", NodeTypeDocumentation, DomainAcademic, true},
		{"best_practice in academic", NodeTypeBestPractice, DomainAcademic, true},
		{"rfc in academic", NodeTypeRFC, DomainAcademic, true},
		{"stackoverflow in academic", NodeTypeStackOverflow, DomainAcademic, true},
		{"blog_post in academic", NodeTypeBlogPost, DomainAcademic, true},
		{"tutorial in academic", NodeTypeTutorial, DomainAcademic, true},

		// Academic domain - invalid
		{"function in academic", NodeTypeFunction, DomainAcademic, false},
		{"session in academic", NodeTypeSession, DomainAcademic, false},

		// Invalid domain
		{"file in invalid domain", NodeTypeFile, Domain(99), false},
		{"invalid type in code", NodeType(999), DomainCode, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.nodeType.IsValidForDomain(tt.domain); got != tt.expected {
				t.Errorf("NodeType(%q).IsValidForDomain(%q) = %v, want %v",
					tt.nodeType, tt.domain, got, tt.expected)
			}
		})
	}
}

func TestNodeTypeString(t *testing.T) {
	tests := []struct {
		nodeType NodeType
		expected string
	}{
		{NodeTypeFile, "file"},
		{NodeTypeFunction, "function"},
		{NodeTypeSession, "session"},
		{NodeTypeDocumentation, "documentation"},
		{NodeType(999), "node_type(999)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.nodeType.String(); got != tt.expected {
				t.Errorf("NodeType(%q).String() = %q, want %q", tt.nodeType, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// EdgeType Tests
// =============================================================================

func TestEdgeTypeConstants(t *testing.T) {
	// Structural edge types
	structuralTypes := []struct {
		edgeType EdgeType
		expected string
	}{
		{EdgeTypeCalls, "calls"},
		{EdgeTypeImports, "imports"},
		{EdgeTypeDefines, "defines"},
		{EdgeTypeImplements, "implements"},
		{EdgeTypeEmbeds, "embeds"},
	}

	// Temporal edge types
	temporalTypes := []struct {
		edgeType EdgeType
		expected string
	}{
		{EdgeTypeProducedBy, "produced_by"},
		{EdgeTypeResultedIn, "resulted_in"},
		{EdgeTypeSimilarTo, "similar_to"},
	}

	// Cross-domain edge types
	crossDomainTypes := []struct {
		edgeType EdgeType
		expected string
	}{
		{EdgeTypeReferences, "references"},
		{EdgeTypeDocuments, "documents"},
		{EdgeTypeModified, "modified"},
		{EdgeTypeUsesLibrary, "uses_library"},
	}

	allTypes := append(structuralTypes, append(temporalTypes, crossDomainTypes...)...)

	for _, tt := range allTypes {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.edgeType.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.edgeType.String())
			}
		})
	}
}

func TestValidEdgeTypes(t *testing.T) {
	edgeTypes := ValidEdgeTypes()

	if len(edgeTypes) != 29 {
		t.Errorf("expected 29 edge types, got %d", len(edgeTypes))
	}

	expectedTypes := map[EdgeType]bool{
		EdgeTypeCalls:             false,
		EdgeTypeCalledBy:          false,
		EdgeTypeImports:           false,
		EdgeTypeImportedBy:        false,
		EdgeTypeImplements:        false,
		EdgeTypeImplementedBy:     false,
		EdgeTypeEmbeds:            false,
		EdgeTypeHasField:          false,
		EdgeTypeHasMethod:         false,
		EdgeTypeDefines:           false,
		EdgeTypeDefinedIn:         false,
		EdgeTypeReturns:           false,
		EdgeTypeReceives:          false,
		EdgeTypeProducedBy:        false,
		EdgeTypeResultedIn:        false,
		EdgeTypeSimilarTo:         false,
		EdgeTypeFollowedBy:        false,
		EdgeTypeSupersedes:        false,
		EdgeTypeModified:          false,
		EdgeTypeCreated:           false,
		EdgeTypeDeleted:           false,
		EdgeTypeBasedOn:           false,
		EdgeTypeReferences:        false,
		EdgeTypeValidatedBy:       false,
		EdgeTypeDocuments:         false,
		EdgeTypeUsesLibrary:       false,
		EdgeTypeImplementsPattern: false,
		EdgeTypeCites:             false,
		EdgeTypeRelatedTo:         false,
	}

	for _, et := range edgeTypes {
		if _, exists := expectedTypes[et]; !exists {
			t.Errorf("unexpected edge type in ValidEdgeTypes: %q", et)
		}
		expectedTypes[et] = true
	}

	for edgeType, found := range expectedTypes {
		if !found {
			t.Errorf("expected edge type %q not found in ValidEdgeTypes", edgeType)
		}
	}
}

func TestStructuralEdgeTypes(t *testing.T) {
	structuralTypes := StructuralEdgeTypes()

	if len(structuralTypes) != 13 {
		t.Errorf("expected 13 structural edge types, got %d", len(structuralTypes))
	}

	expectedTypes := []EdgeType{
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

	typeSet := make(map[EdgeType]bool)
	for _, et := range structuralTypes {
		typeSet[et] = true
	}

	for _, expected := range expectedTypes {
		if !typeSet[expected] {
			t.Errorf("StructuralEdgeTypes missing expected type %q", expected)
		}
	}

	// Verify non-structural types are not included
	nonStructural := []EdgeType{EdgeTypeProducedBy, EdgeTypeReferences, EdgeTypeModified}
	for _, et := range nonStructural {
		if typeSet[et] {
			t.Errorf("StructuralEdgeTypes incorrectly includes %q", et)
		}
	}
}

func TestTemporalEdgeTypes(t *testing.T) {
	temporalTypes := TemporalEdgeTypes()

	if len(temporalTypes) != 5 {
		t.Errorf("expected 5 temporal edge types, got %d", len(temporalTypes))
	}

	expectedTypes := []EdgeType{
		EdgeTypeProducedBy,
		EdgeTypeResultedIn,
		EdgeTypeSimilarTo,
		EdgeTypeFollowedBy,
		EdgeTypeSupersedes,
	}

	typeSet := make(map[EdgeType]bool)
	for _, et := range temporalTypes {
		typeSet[et] = true
	}

	for _, expected := range expectedTypes {
		if !typeSet[expected] {
			t.Errorf("TemporalEdgeTypes missing expected type %q", expected)
		}
	}

	nonTemporal := []EdgeType{EdgeTypeCalls, EdgeTypeReferences, EdgeTypeModified}
	for _, et := range nonTemporal {
		if typeSet[et] {
			t.Errorf("TemporalEdgeTypes incorrectly includes %q", et)
		}
	}
}

func TestCrossDomainEdgeTypes(t *testing.T) {
	crossDomainTypes := CrossDomainEdgeTypes()

	if len(crossDomainTypes) != 11 {
		t.Errorf("expected 11 cross-domain edge types, got %d", len(crossDomainTypes))
	}

	expectedTypes := []EdgeType{
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

	typeSet := make(map[EdgeType]bool)
	for _, et := range crossDomainTypes {
		typeSet[et] = true
	}

	for _, expected := range expectedTypes {
		if !typeSet[expected] {
			t.Errorf("CrossDomainEdgeTypes missing expected type %q", expected)
		}
	}

	// Verify non-cross-domain types are not included
	nonCrossDomain := []EdgeType{EdgeTypeCalls, EdgeTypeProducedBy, EdgeTypeDefines}
	for _, et := range nonCrossDomain {
		if typeSet[et] {
			t.Errorf("CrossDomainEdgeTypes incorrectly includes %q", et)
		}
	}
}

func TestEdgeTypeIsValid(t *testing.T) {
	// All valid edge types
	validTypes := ValidEdgeTypes()
	for _, et := range validTypes {
		t.Run("valid_"+et.String(), func(t *testing.T) {

			if !et.IsValid() {
				t.Errorf("EdgeType(%q).IsValid() = false, want true", et)
			}
		})
	}

	// Invalid edge types
	invalidTypes := []EdgeType{
		EdgeType(-1),
		EdgeType(999),
		EdgeType(42),
	}

	for _, et := range invalidTypes {
		t.Run("invalid_"+et.String(), func(t *testing.T) {

			if et.IsValid() {
				t.Errorf("EdgeType(%q).IsValid() = true, want false", et)
			}
		})
	}
}

func TestEdgeTypeIsStructural(t *testing.T) {
	tests := []struct {
		name     string
		edgeType EdgeType
		expected bool
	}{
		// Structural types
		{"calls is structural", EdgeTypeCalls, true},
		{"imports is structural", EdgeTypeImports, true},
		{"defines is structural", EdgeTypeDefines, true},
		{"implements is structural", EdgeTypeImplements, true},
		{"embeds is structural", EdgeTypeEmbeds, true},

		// Non-structural types
		{"produced_by is not structural", EdgeTypeProducedBy, false},
		{"references is not structural", EdgeTypeReferences, false},
		{"documents is not structural", EdgeTypeDocuments, false},
		{"modified is not structural", EdgeTypeModified, false},

		// Invalid types
		{"negative is not structural", EdgeType(-1), false},
		{"unknown is not structural", EdgeType(999), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.edgeType.IsStructural(); got != tt.expected {
				t.Errorf("EdgeType(%q).IsStructural() = %v, want %v",
					tt.edgeType, got, tt.expected)
			}
		})
	}
}

func TestEdgeTypeIsTemporal(t *testing.T) {
	tests := []struct {
		name     string
		edgeType EdgeType
		expected bool
	}{
		// Temporal types
		{"produced_by is temporal", EdgeTypeProducedBy, true},
		{"resulted_in is temporal", EdgeTypeResultedIn, true},
		{"similar_to is temporal", EdgeTypeSimilarTo, true},
		{"followed_by is temporal", EdgeTypeFollowedBy, true},
		{"supersedes is temporal", EdgeTypeSupersedes, true},

		// Non-temporal types
		{"calls is not temporal", EdgeTypeCalls, false},
		{"imports is not temporal", EdgeTypeImports, false},
		{"references is not temporal", EdgeTypeReferences, false},
		{"modified is not temporal", EdgeTypeModified, false},

		// Invalid types
		{"negative is not temporal", EdgeType(-1), false},
		{"unknown is not temporal", EdgeType(999), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.edgeType.IsTemporal(); got != tt.expected {
				t.Errorf("EdgeType(%q).IsTemporal() = %v, want %v",
					tt.edgeType, got, tt.expected)
			}
		})
	}
}

func TestEdgeTypeIsCrossDomain(t *testing.T) {
	tests := []struct {
		name     string
		edgeType EdgeType
		expected bool
	}{
		// Cross-domain types
		{"references is cross-domain", EdgeTypeReferences, true},
		{"documents is cross-domain", EdgeTypeDocuments, true},
		{"modified is cross-domain", EdgeTypeModified, true},
		{"uses_library is cross-domain", EdgeTypeUsesLibrary, true},

		// Non-cross-domain types
		{"calls is not cross-domain", EdgeTypeCalls, false},
		{"produced_by is not cross-domain", EdgeTypeProducedBy, false},
		{"defines is not cross-domain", EdgeTypeDefines, false},

		// Invalid types
		{"negative is not cross-domain", EdgeType(-1), false},
		{"unknown is not cross-domain", EdgeType(999), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.edgeType.IsCrossDomain(); got != tt.expected {
				t.Errorf("EdgeType(%q).IsCrossDomain() = %v, want %v",
					tt.edgeType, got, tt.expected)
			}
		})
	}
}

func TestEdgeTypeString(t *testing.T) {
	tests := []struct {
		edgeType EdgeType
		expected string
	}{
		{EdgeTypeCalls, "calls"},
		{EdgeTypeProducedBy, "produced_by"},
		{EdgeTypeReferences, "references"},
		{EdgeType(999), "edge_type(999)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.edgeType.String(); got != tt.expected {
				t.Errorf("EdgeType(%q).String() = %q, want %q", tt.edgeType, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Edge Type Categories Are Mutually Exclusive
// =============================================================================

func TestEdgeTypeCategoriesMutuallyExclusive(t *testing.T) {
	// Each edge type should belong to exactly one category
	for _, et := range ValidEdgeTypes() {
		t.Run(et.String(), func(t *testing.T) {
			categoriesCount := 0
			if et.IsStructural() {
				categoriesCount++
			}
			if et.IsTemporal() {
				categoriesCount++
			}
			if et.IsCrossDomain() {
				categoriesCount++
			}

			if categoriesCount != 1 {
				t.Errorf("EdgeType(%q) belongs to %d categories, want exactly 1", et, categoriesCount)
			}
		})
	}
}

// =============================================================================
// JSON Marshaling Tests
// =============================================================================

func TestGraphNodeJSONMarshal(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	node := GraphNode{
		ID:          "node-123",
		Domain:      DomainCode,
		NodeType:    NodeTypeFile,
		ContentHash: "abc123",
		Metadata: map[string]any{
			"path": "/src/main.go",
			"size": 1024,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("failed to marshal GraphNode: %v", err)
	}

	var unmarshaled GraphNode
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal GraphNode: %v", err)
	}

	if unmarshaled.ID != node.ID {
		t.Errorf("ID mismatch: got %q, want %q", unmarshaled.ID, node.ID)
	}
	if unmarshaled.Domain != node.Domain {
		t.Errorf("Domain mismatch: got %q, want %q", unmarshaled.Domain, node.Domain)
	}
	if unmarshaled.NodeType != node.NodeType {
		t.Errorf("NodeType mismatch: got %q, want %q", unmarshaled.NodeType, node.NodeType)
	}
	if unmarshaled.ContentHash != node.ContentHash {
		t.Errorf("ContentHash mismatch: got %q, want %q", unmarshaled.ContentHash, node.ContentHash)
	}
	if unmarshaled.Metadata["path"] != node.Metadata["path"] {
		t.Errorf("Metadata[path] mismatch: got %v, want %v",
			unmarshaled.Metadata["path"], node.Metadata["path"])
	}
}

func TestGraphEdgeJSONMarshal(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	edge := GraphEdge{
		ID:       456,
		SourceID: "node-123",
		TargetID: "node-789",
		EdgeType: EdgeTypeCalls,
		Weight:   0.85,
		Metadata: map[string]any{
			"line": 42,
		},
		CreatedAt: now,
	}

	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatalf("failed to marshal GraphEdge: %v", err)
	}

	var unmarshaled GraphEdge
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal GraphEdge: %v", err)
	}

	if unmarshaled.ID != edge.ID {
		t.Errorf("ID mismatch: got %q, want %q", unmarshaled.ID, edge.ID)
	}
	if unmarshaled.SourceID != edge.SourceID {
		t.Errorf("SourceID mismatch: got %q, want %q", unmarshaled.SourceID, edge.SourceID)
	}
	if unmarshaled.TargetID != edge.TargetID {
		t.Errorf("TargetID mismatch: got %q, want %q", unmarshaled.TargetID, edge.TargetID)
	}
	if unmarshaled.EdgeType != edge.EdgeType {
		t.Errorf("EdgeType mismatch: got %q, want %q", unmarshaled.EdgeType, edge.EdgeType)
	}
	if unmarshaled.Weight != edge.Weight {
		t.Errorf("Weight mismatch: got %f, want %f", unmarshaled.Weight, edge.Weight)
	}
}

func TestVectorDataJSONMarshal(t *testing.T) {
	vectorData := VectorData{
		NodeID:     "node-123",
		Embedding:  []float32{0.1, 0.2, 0.3, 0.4, 0.5},
		Magnitude:  0.7416198487095663,
		Dimensions: 5,
		Domain:     DomainCode,
		NodeType:   NodeTypeFile,
	}

	data, err := json.Marshal(vectorData)
	if err != nil {
		t.Fatalf("failed to marshal VectorData: %v", err)
	}

	var unmarshaled VectorData
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal VectorData: %v", err)
	}

	if unmarshaled.NodeID != vectorData.NodeID {
		t.Errorf("NodeID mismatch: got %q, want %q", unmarshaled.NodeID, vectorData.NodeID)
	}
	if len(unmarshaled.Embedding) != len(vectorData.Embedding) {
		t.Errorf("Embedding length mismatch: got %d, want %d",
			len(unmarshaled.Embedding), len(vectorData.Embedding))
	}
	for i, v := range vectorData.Embedding {
		if unmarshaled.Embedding[i] != v {
			t.Errorf("Embedding[%d] mismatch: got %f, want %f", i, unmarshaled.Embedding[i], v)
		}
	}
	if unmarshaled.Magnitude != vectorData.Magnitude {
		t.Errorf("Magnitude mismatch: got %f, want %f", unmarshaled.Magnitude, vectorData.Magnitude)
	}
	if unmarshaled.Dimensions != vectorData.Dimensions {
		t.Errorf("Dimensions mismatch: got %d, want %d", unmarshaled.Dimensions, vectorData.Dimensions)
	}
	if unmarshaled.Domain != vectorData.Domain {
		t.Errorf("Domain mismatch: got %q, want %q", unmarshaled.Domain, vectorData.Domain)
	}
	if unmarshaled.NodeType != vectorData.NodeType {
		t.Errorf("NodeType mismatch: got %q, want %q", unmarshaled.NodeType, vectorData.NodeType)
	}
}

func TestDBStatsJSONMarshal(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	stats := DBStats{
		TotalNodes: 1000,
		NodesByDomain: map[Domain]int64{
			DomainCode:     500,
			DomainHistory:  300,
			DomainAcademic: 200,
		},
		NodesByType: map[NodeType]int64{
			NodeTypeFile:     100,
			NodeTypeFunction: 400,
		},
		TotalEdges: 5000,
		EdgesByType: map[EdgeType]int64{
			EdgeTypeCalls:   2000,
			EdgeTypeImports: 1500,
		},
		TotalVectors:        1000,
		IndexSize:           1024 * 1024,
		DBSizeBytes:         10 * 1024 * 1024,
		LastVacuumAt:        now,
		UnresolvedConflicts: 5,
		StaleNodes:          50,
	}

	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("failed to marshal DBStats: %v", err)
	}

	var unmarshaled DBStats
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal DBStats: %v", err)
	}

	if unmarshaled.TotalNodes != stats.TotalNodes {
		t.Errorf("TotalNodes mismatch: got %d, want %d", unmarshaled.TotalNodes, stats.TotalNodes)
	}
	if unmarshaled.TotalEdges != stats.TotalEdges {
		t.Errorf("TotalEdges mismatch: got %d, want %d", unmarshaled.TotalEdges, stats.TotalEdges)
	}
	if unmarshaled.NodesByDomain[DomainCode] != stats.NodesByDomain[DomainCode] {
		t.Errorf("NodesByDomain[code] mismatch: got %d, want %d",
			unmarshaled.NodesByDomain[DomainCode], stats.NodesByDomain[DomainCode])
	}
	if unmarshaled.EdgesByType[EdgeTypeCalls] != stats.EdgesByType[EdgeTypeCalls] {
		t.Errorf("EdgesByType[calls] mismatch: got %d, want %d",
			unmarshaled.EdgesByType[EdgeTypeCalls], stats.EdgesByType[EdgeTypeCalls])
	}
}

func TestProvenanceJSONMarshal(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	provenance := Provenance{
		ID:         1,
		NodeID:     "node-123",
		SourceType: SourceTypeCode,
		SourcePath: "abc123def456",
		Confidence: 0.95,
		VerifiedAt: now,
	}

	data, err := json.Marshal(provenance)
	if err != nil {
		t.Fatalf("failed to marshal Provenance: %v", err)
	}

	var unmarshaled Provenance
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal Provenance: %v", err)
	}

	if unmarshaled.ID != provenance.ID {
		t.Errorf("ID mismatch: got %q, want %q", unmarshaled.ID, provenance.ID)
	}
	if unmarshaled.SourceType != provenance.SourceType {
		t.Errorf("SourceType mismatch: got %q, want %q",
			unmarshaled.SourceType, provenance.SourceType)
	}
	if unmarshaled.Confidence != provenance.Confidence {
		t.Errorf("Confidence mismatch: got %f, want %f",
			unmarshaled.Confidence, provenance.Confidence)
	}
}

func TestConflictJSONMarshal(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	conflict := Conflict{
		ID:           1,
		NodeAID:      "node-123",
		NodeBID:      "node-456",
		ConflictType: ConflictTypeSemantic,
		DetectedAt:   now,
		Resolution:   "kept node-123 as source of truth",
		ResolvedAt:   now,
	}

	data, err := json.Marshal(conflict)
	if err != nil {
		t.Fatalf("failed to marshal Conflict: %v", err)
	}

	var unmarshaled Conflict
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal Conflict: %v", err)
	}

	if unmarshaled.ID != conflict.ID {
		t.Errorf("ID mismatch: got %d, want %d", unmarshaled.ID, conflict.ID)
	}
	if unmarshaled.ConflictType != conflict.ConflictType {
		t.Errorf("ConflictType mismatch: got %q, want %q",
			unmarshaled.ConflictType, conflict.ConflictType)
	}
	if unmarshaled.Resolution != conflict.Resolution {
		t.Errorf("Resolution mismatch: got %q, want %q",
			unmarshaled.Resolution, conflict.Resolution)
	}
}

// =============================================================================
// Source Type Constants Tests
// =============================================================================

func TestSourceTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		value    SourceType
		expected string
	}{
		{"SourceTypeCode", SourceTypeCode, "code"},
		{"SourceTypeHistory", SourceTypeHistory, "history"},
		{"SourceTypeAcademic", SourceTypeAcademic, "academic"},
		{"SourceTypeLLMInference", SourceTypeLLMInference, "llm_inference"},
		{"SourceTypeUserProvided", SourceTypeUserProvided, "user_provided"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.value.String())
			}
		})
	}
}

// =============================================================================
// Conflict Type Constants Tests
// =============================================================================

func TestConflictTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		value    ConflictType
		expected string
	}{
		{"ConflictTypeTemporal", ConflictTypeTemporal, "temporal"},
		{"ConflictTypeSourceMismatch", ConflictTypeSourceMismatch, "source_mismatch"},
		{"ConflictTypeSemantic", ConflictTypeSemantic, "semantic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.value.String())
			}
		})
	}
}

// =============================================================================
// Edge Cases and Boundary Tests
// =============================================================================

func TestEmptyMetadata(t *testing.T) {
	node := GraphNode{
		ID:       "node-empty",
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Metadata: nil,
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("failed to marshal node with nil metadata: %v", err)
	}

	var unmarshaled GraphNode
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal node with nil metadata: %v", err)
	}

	// Metadata should be nil after unmarshaling null
	if unmarshaled.Metadata != nil {
		t.Errorf("expected nil metadata, got %v", unmarshaled.Metadata)
	}
}

func TestEmptyEmbedding(t *testing.T) {
	vectorData := VectorData{
		NodeID:     "node-123",
		Embedding:  []float32{},
		Magnitude:  0,
		Dimensions: 0,
		Domain:     DomainCode,
		NodeType:   NodeTypeFile,
	}

	data, err := json.Marshal(vectorData)
	if err != nil {
		t.Fatalf("failed to marshal VectorData with empty embedding: %v", err)
	}

	var unmarshaled VectorData
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal VectorData with empty embedding: %v", err)
	}

	if len(unmarshaled.Embedding) != 0 {
		t.Errorf("expected empty embedding, got length %d", len(unmarshaled.Embedding))
	}
}

func TestZeroValueDBStats(t *testing.T) {
	stats := DBStats{}

	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("failed to marshal zero-value DBStats: %v", err)
	}

	var unmarshaled DBStats
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal zero-value DBStats: %v", err)
	}

	if unmarshaled.TotalNodes != 0 {
		t.Errorf("expected TotalNodes 0, got %d", unmarshaled.TotalNodes)
	}
	if unmarshaled.TotalEdges != 0 {
		t.Errorf("expected TotalEdges 0, got %d", unmarshaled.TotalEdges)
	}
}

func TestGraphEdgeZeroWeight(t *testing.T) {
	edge := GraphEdge{
		ID:       0,
		SourceID: "node-1",
		TargetID: "node-2",
		EdgeType: EdgeTypeCalls,
		Weight:   0.0,
	}

	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatalf("failed to marshal edge with zero weight: %v", err)
	}

	var unmarshaled GraphEdge
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal edge with zero weight: %v", err)
	}

	if unmarshaled.Weight != 0.0 {
		t.Errorf("expected Weight 0.0, got %f", unmarshaled.Weight)
	}
}

// =============================================================================
// Domain to NodeType Mapping Completeness
// =============================================================================

func TestAllNodeTypesBelongToExactlyOneDomain(t *testing.T) {
	// Every node type should belong to exactly one domain
	allNodeTypes := ValidNodeTypes()
	domains := ValidDomains()

	for _, nt := range allNodeTypes {
		t.Run(nt.String(), func(t *testing.T) {
			domainCount := 0
			var belongsToDomains []Domain

			for _, d := range domains {
				if nt.IsValidForDomain(d) {
					domainCount++
					belongsToDomains = append(belongsToDomains, d)
				}
			}

			if domainCount != 1 {
				t.Errorf("NodeType(%q) belongs to %d domains (should be exactly 1): %v",
					nt, domainCount, belongsToDomains)
			}
		})
	}
}

func TestValidNodeTypesForDomainCoversAllNodeTypes(t *testing.T) {
	// The union of all domain node types should equal all node types
	allNodeTypes := ValidNodeTypes()
	domains := ValidDomains()

	coveredTypes := make(map[NodeType]bool)

	for _, d := range domains {
		for _, nt := range ValidNodeTypesForDomain(d) {
			coveredTypes[nt] = true
		}
	}

	for _, nt := range allNodeTypes {
		if !coveredTypes[nt] {
			t.Errorf("NodeType(%q) is not covered by any domain", nt)
		}
	}

	if len(coveredTypes) != len(allNodeTypes) {
		t.Errorf("covered types count (%d) doesn't match all node types count (%d)",
			len(coveredTypes), len(allNodeTypes))
	}
}

// =============================================================================
// Edge Type Categories Cover All Edge Types
// =============================================================================

func TestEdgeTypeCategoriesCoverAllEdgeTypes(t *testing.T) {
	allEdgeTypes := ValidEdgeTypes()

	structural := make(map[EdgeType]bool)
	for _, et := range StructuralEdgeTypes() {
		structural[et] = true
	}

	temporal := make(map[EdgeType]bool)
	for _, et := range TemporalEdgeTypes() {
		temporal[et] = true
	}

	crossDomain := make(map[EdgeType]bool)
	for _, et := range CrossDomainEdgeTypes() {
		crossDomain[et] = true
	}

	for _, et := range allEdgeTypes {
		inStructural := structural[et]
		inTemporal := temporal[et]
		inCrossDomain := crossDomain[et]

		if !inStructural && !inTemporal && !inCrossDomain {
			t.Errorf("EdgeType(%q) is not in any category", et)
		}
	}
}

// =============================================================================
// JSON Field Name Verification
// =============================================================================

func TestGraphNodeJSONFieldNames(t *testing.T) {
	node := GraphNode{
		ID:          "test-id",
		Domain:      DomainCode,
		NodeType:    NodeTypeFile,
		ContentHash: "hash",
		Metadata:    map[string]any{"key": "value"},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	data, _ := json.Marshal(node)
	jsonStr := string(data)

	expectedFields := []string{
		`"id"`,
		`"domain"`,
		`"node_type"`,
		`"content_hash"`,
		`"metadata"`,
		`"created_at"`,
		`"updated_at"`,
	}

	for _, field := range expectedFields {
		if !containsSubstr(jsonStr, field) {
			t.Errorf("GraphNode JSON missing expected field: %s", field)
		}
	}
}

func TestGraphEdgeJSONFieldNames(t *testing.T) {
	edge := GraphEdge{
		ID:        1,
		SourceID:  "from",
		TargetID:  "to",
		EdgeType:  EdgeTypeCalls,
		Weight:    0.5,
		Metadata:  map[string]any{"key": "value"},
		CreatedAt: time.Now(),
	}

	data, _ := json.Marshal(edge)
	jsonStr := string(data)

	expectedFields := []string{
		`"id"`,
		`"source_id"`,
		`"target_id"`,
		`"edge_type"`,
		`"weight"`,
		`"metadata"`,
		`"created_at"`,
	}

	for _, field := range expectedFields {
		if !containsSubstr(jsonStr, field) {
			t.Errorf("GraphEdge JSON missing expected field: %s", field)
		}
	}
}

func TestVectorDataJSONFieldNames(t *testing.T) {
	vec := VectorData{
		NodeID:     "node-id",
		Embedding:  []float32{0.1},
		Magnitude:  0.1,
		Dimensions: 1,
		Domain:     DomainCode,
		NodeType:   NodeTypeFile,
	}

	data, _ := json.Marshal(vec)
	jsonStr := string(data)

	expectedFields := []string{
		`"node_id"`,
		`"embedding"`,
		`"magnitude"`,
		`"dimensions"`,
		`"domain"`,
		`"node_type"`,
	}

	for _, field := range expectedFields {
		if !containsSubstr(jsonStr, field) {
			t.Errorf("VectorData JSON missing expected field: %s", field)
		}
	}
}

func TestDBStatsJSONFieldNames(t *testing.T) {
	stats := DBStats{
		TotalNodes:          1,
		NodesByDomain:       map[Domain]int64{DomainCode: 1},
		NodesByType:         map[NodeType]int64{NodeTypeFile: 1},
		TotalEdges:          1,
		EdgesByType:         map[EdgeType]int64{EdgeTypeCalls: 1},
		TotalVectors:        1,
		IndexSize:           1,
		DBSizeBytes:         1,
		LastVacuumAt:        time.Now(),
		UnresolvedConflicts: 1,
		StaleNodes:          1,
	}

	data, _ := json.Marshal(stats)
	jsonStr := string(data)

	expectedFields := []string{
		`"total_nodes"`,
		`"nodes_by_domain"`,
		`"nodes_by_type"`,
		`"total_edges"`,
		`"edges_by_type"`,
		`"total_vectors"`,
		`"index_size"`,
		`"db_size_bytes"`,
		`"last_vacuum_at"`,
		`"unresolved_conflicts"`,
		`"stale_nodes"`,
	}

	for _, field := range expectedFields {
		if !containsSubstr(jsonStr, field) {
			t.Errorf("DBStats JSON missing expected field: %s", field)
		}
	}
}

// Helper function
func containsSubstr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstrHelper(s, substr))
}

func containsSubstrHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
