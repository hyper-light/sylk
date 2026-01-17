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
			if string(tt.domain) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(tt.domain))
			}
		})
	}
}

func TestValidDomains(t *testing.T) {
	domains := ValidDomains()

	if len(domains) != 3 {
		t.Errorf("expected 3 domains, got %d", len(domains))
	}

	expectedDomains := map[Domain]bool{
		DomainCode:     false,
		DomainHistory:  false,
		DomainAcademic: false,
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
		{"invalid empty domain", Domain(""), false},
		{"invalid arbitrary domain", Domain("invalid"), false},
		{"invalid similar domain", Domain("codes"), false},
		{"invalid case domain", Domain("Code"), false},
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
		{Domain("custom"), "custom"},
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
		{NodeTypeFunction, "function"},
		{NodeTypeType, "type"},
		{NodeTypePackage, "package"},
		{NodeTypeImport, "import"},
	}

	// History domain node types
	historyTypes := []struct {
		nodeType NodeType
		expected string
	}{
		{NodeTypeSession, "session"},
		{NodeTypeDecision, "decision"},
		{NodeTypeFailure, "failure"},
		{NodeTypePattern, "pattern"},
		{NodeTypeWorkflow, "workflow"},
	}

	// Academic domain node types
	academicTypes := []struct {
		nodeType NodeType
		expected string
	}{
		{NodeTypeRepo, "repo"},
		{NodeTypeDoc, "doc"},
		{NodeTypeArticle, "article"},
		{NodeTypeConcept, "concept"},
		{NodeTypeBestPractice, "best_practice"},
	}

	allTypes := append(codeTypes, append(historyTypes, academicTypes...)...)

	for _, tt := range allTypes {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.nodeType) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(tt.nodeType))
			}
		})
	}
}

func TestValidNodeTypes(t *testing.T) {
	nodeTypes := ValidNodeTypes()

	// Should have 5 code + 5 history + 5 academic = 15 total
	if len(nodeTypes) != 15 {
		t.Errorf("expected 15 node types, got %d", len(nodeTypes))
	}

	expectedTypes := map[NodeType]bool{
		// Code domain
		NodeTypeFile:     false,
		NodeTypeFunction: false,
		NodeTypeType:     false,
		NodeTypePackage:  false,
		NodeTypeImport:   false,
		// History domain
		NodeTypeSession:  false,
		NodeTypeDecision: false,
		NodeTypeFailure:  false,
		NodeTypePattern:  false,
		NodeTypeWorkflow: false,
		// Academic domain
		NodeTypeRepo:         false,
		NodeTypeDoc:          false,
		NodeTypeArticle:      false,
		NodeTypeConcept:      false,
		NodeTypeBestPractice: false,
	}

	for _, nt := range nodeTypes {
		if _, exists := expectedTypes[nt]; !exists {
			t.Errorf("unexpected node type in ValidNodeTypes: %q", nt)
		}
		expectedTypes[nt] = true
	}

	for nodeType, found := range expectedTypes {
		if !found {
			t.Errorf("expected node type %q not found in ValidNodeTypes", nodeType)
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
				NodeTypeFunction,
				NodeTypeType,
				NodeTypePackage,
				NodeTypeImport,
			},
		},
		{
			name:   "history domain",
			domain: DomainHistory,
			expectedTypes: []NodeType{
				NodeTypeSession,
				NodeTypeDecision,
				NodeTypeFailure,
				NodeTypePattern,
				NodeTypeWorkflow,
			},
		},
		{
			name:   "academic domain",
			domain: DomainAcademic,
			expectedTypes: []NodeType{
				NodeTypeRepo,
				NodeTypeDoc,
				NodeTypeArticle,
				NodeTypeConcept,
				NodeTypeBestPractice,
			},
		},
		{
			name:          "invalid domain",
			domain:        Domain("invalid"),
			expectedTypes: nil,
		},
		{
			name:          "empty domain",
			domain:        Domain(""),
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
		t.Run("valid_"+string(nt), func(t *testing.T) {
			if !nt.IsValid() {
				t.Errorf("NodeType(%q).IsValid() = false, want true", nt)
			}
		})
	}

	// Invalid node types
	invalidTypes := []NodeType{
		NodeType(""),
		NodeType("invalid"),
		NodeType("File"),   // wrong case
		NodeType("files"),  // wrong plural
		NodeType("method"), // similar but not valid
	}

	for _, nt := range invalidTypes {
		t.Run("invalid_"+string(nt), func(t *testing.T) {
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
		{"function in code", NodeTypeFunction, DomainCode, true},
		{"type in code", NodeTypeType, DomainCode, true},
		{"package in code", NodeTypePackage, DomainCode, true},
		{"import in code", NodeTypeImport, DomainCode, true},

		// Code domain - invalid
		{"session in code", NodeTypeSession, DomainCode, false},
		{"repo in code", NodeTypeRepo, DomainCode, false},

		// History domain - valid
		{"session in history", NodeTypeSession, DomainHistory, true},
		{"decision in history", NodeTypeDecision, DomainHistory, true},
		{"failure in history", NodeTypeFailure, DomainHistory, true},
		{"pattern in history", NodeTypePattern, DomainHistory, true},
		{"workflow in history", NodeTypeWorkflow, DomainHistory, true},

		// History domain - invalid
		{"file in history", NodeTypeFile, DomainHistory, false},
		{"doc in history", NodeTypeDoc, DomainHistory, false},

		// Academic domain - valid
		{"repo in academic", NodeTypeRepo, DomainAcademic, true},
		{"doc in academic", NodeTypeDoc, DomainAcademic, true},
		{"article in academic", NodeTypeArticle, DomainAcademic, true},
		{"concept in academic", NodeTypeConcept, DomainAcademic, true},
		{"best_practice in academic", NodeTypeBestPractice, DomainAcademic, true},

		// Academic domain - invalid
		{"function in academic", NodeTypeFunction, DomainAcademic, false},
		{"session in academic", NodeTypeSession, DomainAcademic, false},

		// Invalid domain
		{"file in invalid domain", NodeTypeFile, Domain("invalid"), false},
		{"invalid type in code", NodeType("invalid"), DomainCode, false},
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
		{NodeTypeRepo, "repo"},
		{NodeType("custom"), "custom"},
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
		{EdgeTypeContains, "contains"},
	}

	// Temporal edge types
	temporalTypes := []struct {
		edgeType EdgeType
		expected string
	}{
		{EdgeTypeFollows, "follows"},
		{EdgeTypeCauses, "causes"},
		{EdgeTypeResolves, "resolves"},
	}

	// Cross-domain edge types
	crossDomainTypes := []struct {
		edgeType EdgeType
		expected string
	}{
		{EdgeTypeReferences, "references"},
		{EdgeTypeAppliesTo, "applies_to"},
		{EdgeTypeDocuments, "documents"},
		{EdgeTypeModified, "modified"},
	}

	allTypes := append(structuralTypes, append(temporalTypes, crossDomainTypes...)...)

	for _, tt := range allTypes {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.edgeType) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(tt.edgeType))
			}
		})
	}
}

func TestValidEdgeTypes(t *testing.T) {
	edgeTypes := ValidEdgeTypes()

	// Should have 5 structural + 3 temporal + 4 cross-domain = 12 total
	if len(edgeTypes) != 12 {
		t.Errorf("expected 12 edge types, got %d", len(edgeTypes))
	}

	expectedTypes := map[EdgeType]bool{
		// Structural
		EdgeTypeCalls:      false,
		EdgeTypeImports:    false,
		EdgeTypeDefines:    false,
		EdgeTypeImplements: false,
		EdgeTypeContains:   false,
		// Temporal
		EdgeTypeFollows:  false,
		EdgeTypeCauses:   false,
		EdgeTypeResolves: false,
		// Cross-domain
		EdgeTypeReferences: false,
		EdgeTypeAppliesTo:  false,
		EdgeTypeDocuments:  false,
		EdgeTypeModified:   false,
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

	if len(structuralTypes) != 5 {
		t.Errorf("expected 5 structural edge types, got %d", len(structuralTypes))
	}

	expectedTypes := []EdgeType{
		EdgeTypeCalls,
		EdgeTypeImports,
		EdgeTypeDefines,
		EdgeTypeImplements,
		EdgeTypeContains,
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
	nonStructural := []EdgeType{EdgeTypeFollows, EdgeTypeReferences, EdgeTypeModified}
	for _, et := range nonStructural {
		if typeSet[et] {
			t.Errorf("StructuralEdgeTypes incorrectly includes %q", et)
		}
	}
}

func TestTemporalEdgeTypes(t *testing.T) {
	temporalTypes := TemporalEdgeTypes()

	if len(temporalTypes) != 3 {
		t.Errorf("expected 3 temporal edge types, got %d", len(temporalTypes))
	}

	expectedTypes := []EdgeType{
		EdgeTypeFollows,
		EdgeTypeCauses,
		EdgeTypeResolves,
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

	// Verify non-temporal types are not included
	nonTemporal := []EdgeType{EdgeTypeCalls, EdgeTypeReferences, EdgeTypeModified}
	for _, et := range nonTemporal {
		if typeSet[et] {
			t.Errorf("TemporalEdgeTypes incorrectly includes %q", et)
		}
	}
}

func TestCrossDomainEdgeTypes(t *testing.T) {
	crossDomainTypes := CrossDomainEdgeTypes()

	if len(crossDomainTypes) != 4 {
		t.Errorf("expected 4 cross-domain edge types, got %d", len(crossDomainTypes))
	}

	expectedTypes := []EdgeType{
		EdgeTypeReferences,
		EdgeTypeAppliesTo,
		EdgeTypeDocuments,
		EdgeTypeModified,
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
	nonCrossDomain := []EdgeType{EdgeTypeCalls, EdgeTypeFollows, EdgeTypeDefines}
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
		t.Run("valid_"+string(et), func(t *testing.T) {
			if !et.IsValid() {
				t.Errorf("EdgeType(%q).IsValid() = false, want true", et)
			}
		})
	}

	// Invalid edge types
	invalidTypes := []EdgeType{
		EdgeType(""),
		EdgeType("invalid"),
		EdgeType("Calls"),   // wrong case
		EdgeType("calling"), // similar but not valid
		EdgeType("call"),    // similar but not valid
	}

	for _, et := range invalidTypes {
		t.Run("invalid_"+string(et), func(t *testing.T) {
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
		{"contains is structural", EdgeTypeContains, true},

		// Non-structural types
		{"follows is not structural", EdgeTypeFollows, false},
		{"causes is not structural", EdgeTypeCauses, false},
		{"resolves is not structural", EdgeTypeResolves, false},
		{"references is not structural", EdgeTypeReferences, false},
		{"applies_to is not structural", EdgeTypeAppliesTo, false},
		{"documents is not structural", EdgeTypeDocuments, false},
		{"modified is not structural", EdgeTypeModified, false},

		// Invalid types
		{"empty is not structural", EdgeType(""), false},
		{"invalid is not structural", EdgeType("invalid"), false},
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
		{"follows is temporal", EdgeTypeFollows, true},
		{"causes is temporal", EdgeTypeCauses, true},
		{"resolves is temporal", EdgeTypeResolves, true},

		// Non-temporal types
		{"calls is not temporal", EdgeTypeCalls, false},
		{"imports is not temporal", EdgeTypeImports, false},
		{"references is not temporal", EdgeTypeReferences, false},
		{"modified is not temporal", EdgeTypeModified, false},

		// Invalid types
		{"empty is not temporal", EdgeType(""), false},
		{"invalid is not temporal", EdgeType("invalid"), false},
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
		{"applies_to is cross-domain", EdgeTypeAppliesTo, true},
		{"documents is cross-domain", EdgeTypeDocuments, true},
		{"modified is cross-domain", EdgeTypeModified, true},

		// Non-cross-domain types
		{"calls is not cross-domain", EdgeTypeCalls, false},
		{"follows is not cross-domain", EdgeTypeFollows, false},
		{"defines is not cross-domain", EdgeTypeDefines, false},

		// Invalid types
		{"empty is not cross-domain", EdgeType(""), false},
		{"invalid is not cross-domain", EdgeType("invalid"), false},
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
		{EdgeTypeFollows, "follows"},
		{EdgeTypeReferences, "references"},
		{EdgeType("custom"), "custom"},
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
		t.Run(string(et), func(t *testing.T) {
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
		CreatedAt:  now,
		UpdatedAt:  now,
		AccessedAt: now,
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
		ID:         "edge-456",
		FromNodeID: "node-123",
		ToNodeID:   "node-789",
		EdgeType:   EdgeTypeCalls,
		Weight:     0.85,
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
	if unmarshaled.FromNodeID != edge.FromNodeID {
		t.Errorf("FromNodeID mismatch: got %q, want %q", unmarshaled.FromNodeID, edge.FromNodeID)
	}
	if unmarshaled.ToNodeID != edge.ToNodeID {
		t.Errorf("ToNodeID mismatch: got %q, want %q", unmarshaled.ToNodeID, edge.ToNodeID)
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
		ID:           "vec-001",
		NodeID:       "node-123",
		Embedding:    []float32{0.1, 0.2, 0.3, 0.4, 0.5},
		Magnitude:    0.7416198487095663,
		ModelVersion: "v1.0.0",
	}

	data, err := json.Marshal(vectorData)
	if err != nil {
		t.Fatalf("failed to marshal VectorData: %v", err)
	}

	var unmarshaled VectorData
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal VectorData: %v", err)
	}

	if unmarshaled.ID != vectorData.ID {
		t.Errorf("ID mismatch: got %q, want %q", unmarshaled.ID, vectorData.ID)
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
	if unmarshaled.ModelVersion != vectorData.ModelVersion {
		t.Errorf("ModelVersion mismatch: got %q, want %q",
			unmarshaled.ModelVersion, vectorData.ModelVersion)
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
		ID:         "prov-001",
		NodeID:     "node-123",
		SourceType: SourceTypeGit,
		SourceID:   "abc123def456",
		Confidence: 0.95,
		VerifiedAt: now,
		Verifier:   "git-sync",
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
		ID:           "conflict-001",
		NodeAID:      "node-123",
		NodeBID:      "node-456",
		ConflictType: ConflictTypeSemanticContradiction,
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
		t.Errorf("ID mismatch: got %q, want %q", unmarshaled.ID, conflict.ID)
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
		value    string
		expected string
	}{
		{"SourceTypeGit", SourceTypeGit, "git"},
		{"SourceTypeUser", SourceTypeUser, "user"},
		{"SourceTypeLLM", SourceTypeLLM, "llm"},
		{"SourceTypeWeb", SourceTypeWeb, "web"},
		{"SourceTypeAPI", SourceTypeAPI, "api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.value)
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
		value    string
		expected string
	}{
		{"ConflictTypeSemanticContradiction", ConflictTypeSemanticContradiction, "semantic_contradiction"},
		{"ConflictTypeVersionMismatch", ConflictTypeVersionMismatch, "version_mismatch"},
		{"ConflictTypeDuplicate", ConflictTypeDuplicate, "duplicate"},
		{"ConflictTypeStale", ConflictTypeStale, "stale"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.value)
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
		ID:        "vec-empty",
		NodeID:    "node-123",
		Embedding: []float32{},
		Magnitude: 0,
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
		ID:         "edge-zero",
		FromNodeID: "node-1",
		ToNodeID:   "node-2",
		EdgeType:   EdgeTypeCalls,
		Weight:     0.0,
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
		t.Run(string(nt), func(t *testing.T) {
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
		AccessedAt:  time.Now(),
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
		`"accessed_at"`,
	}

	for _, field := range expectedFields {
		if !contains(jsonStr, field) {
			t.Errorf("GraphNode JSON missing expected field: %s", field)
		}
	}
}

func TestGraphEdgeJSONFieldNames(t *testing.T) {
	edge := GraphEdge{
		ID:         "test-id",
		FromNodeID: "from",
		ToNodeID:   "to",
		EdgeType:   EdgeTypeCalls,
		Weight:     0.5,
		Metadata:   map[string]any{"key": "value"},
		CreatedAt:  time.Now(),
	}

	data, _ := json.Marshal(edge)
	jsonStr := string(data)

	expectedFields := []string{
		`"id"`,
		`"from_node_id"`,
		`"to_node_id"`,
		`"edge_type"`,
		`"weight"`,
		`"metadata"`,
		`"created_at"`,
	}

	for _, field := range expectedFields {
		if !contains(jsonStr, field) {
			t.Errorf("GraphEdge JSON missing expected field: %s", field)
		}
	}
}

func TestVectorDataJSONFieldNames(t *testing.T) {
	vec := VectorData{
		ID:           "test-id",
		NodeID:       "node-id",
		Embedding:    []float32{0.1},
		Magnitude:    0.1,
		ModelVersion: "v1",
	}

	data, _ := json.Marshal(vec)
	jsonStr := string(data)

	expectedFields := []string{
		`"id"`,
		`"node_id"`,
		`"embedding"`,
		`"magnitude"`,
		`"model_version"`,
	}

	for _, field := range expectedFields {
		if !contains(jsonStr, field) {
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
		if !contains(jsonStr, field) {
			t.Errorf("DBStats JSON missing expected field: %s", field)
		}
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
