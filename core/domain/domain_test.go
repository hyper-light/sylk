package domain

import (
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
)

func TestDomain_String(t *testing.T) {
	tests := []struct {
		domain   Domain
		expected string
	}{
		{DomainLibrarian, "librarian"},
		{DomainAcademic, "academic"},
		{DomainArchivalist, "archivalist"},
		{DomainArchitect, "architect"},
		{DomainEngineer, "engineer"},
		{DomainDesigner, "designer"},
		{DomainInspector, "inspector"},
		{DomainTester, "tester"},
		{DomainOrchestrator, "orchestrator"},
		{DomainGuide, "guide"},
		{Domain(999), "domain(999)"},
	}

	for _, tc := range tests {
		if got := tc.domain.String(); got != tc.expected {
			t.Errorf("Domain(%d).String() = %s, want %s", tc.domain, got, tc.expected)
		}
	}
}

func TestDomain_IsValid(t *testing.T) {
	for _, d := range ValidDomains() {
		if !d.IsValid() {
			t.Errorf("Domain %s should be valid", d)
		}
	}

	if Domain(999).IsValid() {
		t.Error("Domain(999) should not be valid")
	}
}

func TestParseDomain(t *testing.T) {
	tests := []struct {
		input    string
		expected Domain
		ok       bool
	}{
		{"librarian", DomainLibrarian, true},
		{"academic", DomainAcademic, true},
		{"archivalist", DomainArchivalist, true},
		{"architect", DomainArchitect, true},
		{"engineer", DomainEngineer, true},
		{"designer", DomainDesigner, true},
		{"inspector", DomainInspector, true},
		{"tester", DomainTester, true},
		{"orchestrator", DomainOrchestrator, true},
		{"guide", DomainGuide, true},
		{"invalid", Domain(0), false},
		{"", Domain(0), false},
	}

	for _, tc := range tests {
		got, ok := ParseDomain(tc.input)
		if ok != tc.ok {
			t.Errorf("ParseDomain(%q) ok = %v, want %v", tc.input, ok, tc.ok)
		}
		if ok && got != tc.expected {
			t.Errorf("ParseDomain(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestValidDomains(t *testing.T) {
	domains := ValidDomains()
	if len(domains) != 10 {
		t.Errorf("ValidDomains() returned %d domains, want 10", len(domains))
	}
}

func TestKnowledgeDomains(t *testing.T) {
	knowledge := KnowledgeDomains()
	if len(knowledge) != 4 {
		t.Errorf("KnowledgeDomains() returned %d domains, want 4", len(knowledge))
	}

	for _, d := range knowledge {
		if !d.IsKnowledge() {
			t.Errorf("%s should be a knowledge domain", d)
		}
	}
}

func TestPipelineDomains(t *testing.T) {
	pipeline := PipelineDomains()
	if len(pipeline) != 4 {
		t.Errorf("PipelineDomains() returned %d domains, want 4", len(pipeline))
	}

	for _, d := range pipeline {
		if !d.IsPipeline() {
			t.Errorf("%s should be a pipeline domain", d)
		}
	}
}

func TestStandaloneDomains(t *testing.T) {
	standalone := StandaloneDomains()
	if len(standalone) != 2 {
		t.Errorf("StandaloneDomains() returned %d domains, want 2", len(standalone))
	}

	for _, d := range standalone {
		if !d.IsStandalone() {
			t.Errorf("%s should be a standalone domain", d)
		}
	}
}

func TestDomain_IsKnowledge(t *testing.T) {
	knowledgeExpected := map[Domain]bool{
		DomainLibrarian:    true,
		DomainAcademic:     true,
		DomainArchivalist:  true,
		DomainArchitect:    true,
		DomainEngineer:     false,
		DomainDesigner:     false,
		DomainInspector:    false,
		DomainTester:       false,
		DomainOrchestrator: false,
		DomainGuide:        false,
	}

	for d, expected := range knowledgeExpected {
		if d.IsKnowledge() != expected {
			t.Errorf("%s.IsKnowledge() = %v, want %v", d, d.IsKnowledge(), expected)
		}
	}
}

func TestDomain_IsPipeline(t *testing.T) {
	pipelineExpected := map[Domain]bool{
		DomainLibrarian:    false,
		DomainAcademic:     false,
		DomainArchivalist:  false,
		DomainArchitect:    false,
		DomainEngineer:     true,
		DomainDesigner:     true,
		DomainInspector:    true,
		DomainTester:       true,
		DomainOrchestrator: false,
		DomainGuide:        false,
	}

	for d, expected := range pipelineExpected {
		if d.IsPipeline() != expected {
			t.Errorf("%s.IsPipeline() = %v, want %v", d, d.IsPipeline(), expected)
		}
	}
}

func TestDomain_IsStandalone(t *testing.T) {
	standaloneExpected := map[Domain]bool{
		DomainLibrarian:    false,
		DomainAcademic:     false,
		DomainArchivalist:  false,
		DomainArchitect:    false,
		DomainEngineer:     false,
		DomainDesigner:     false,
		DomainInspector:    false,
		DomainTester:       false,
		DomainOrchestrator: true,
		DomainGuide:        true,
	}

	for d, expected := range standaloneExpected {
		if d.IsStandalone() != expected {
			t.Errorf("%s.IsStandalone() = %v, want %v", d, d.IsStandalone(), expected)
		}
	}
}

func TestDomain_MarshalJSON(t *testing.T) {
	d := DomainLibrarian
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	expected := `"librarian"`
	if string(data) != expected {
		t.Errorf("json.Marshal(DomainLibrarian) = %s, want %s", data, expected)
	}
}

func TestDomain_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		input    string
		expected Domain
		wantErr  bool
	}{
		{`"librarian"`, DomainLibrarian, false},
		{`"academic"`, DomainAcademic, false},
		{`"invalid"`, Domain(0), true},
	}

	for _, tc := range tests {
		var d Domain
		err := json.Unmarshal([]byte(tc.input), &d)
		if (err != nil) != tc.wantErr {
			t.Errorf("json.Unmarshal(%s) error = %v, wantErr = %v", tc.input, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && d != tc.expected {
			t.Errorf("json.Unmarshal(%s) = %v, want %v", tc.input, d, tc.expected)
		}
	}
}

func TestAgentToDomain(t *testing.T) {
	tests := []struct {
		agent    concurrency.AgentType
		expected Domain
	}{
		{concurrency.AgentLibrarian, DomainLibrarian},
		{concurrency.AgentAcademic, DomainAcademic},
		{concurrency.AgentArchivalist, DomainArchivalist},
		{concurrency.AgentArchitect, DomainArchitect},
		{concurrency.AgentOrchestrator, DomainOrchestrator},
		{concurrency.AgentGuide, DomainGuide},
	}

	for _, tc := range tests {
		d, ok := DomainFromAgentType(tc.agent)
		if !ok {
			t.Errorf("DomainFromAgentType(%s) returned not ok", tc.agent)
			continue
		}
		if d != tc.expected {
			t.Errorf("DomainFromAgentType(%s) = %v, want %v", tc.agent, d, tc.expected)
		}
	}
}

func TestDomainToAgent(t *testing.T) {
	for _, d := range ValidDomains() {
		agent := d.ToAgentType()
		if agent == "" {
			t.Errorf("%s.ToAgentType() returned empty string", d)
		}
	}
}

func TestBidirectionalMapping(t *testing.T) {
	for agent, domain := range AgentToDomain {
		backAgent := domain.ToAgentType()
		if backAgent != agent {
			t.Errorf("Bidirectional mapping failed: %s -> %s -> %s", agent, domain, backAgent)
		}
	}
}

func TestDomainCategories_Exclusive(t *testing.T) {
	for _, d := range ValidDomains() {
		count := 0
		if d.IsKnowledge() {
			count++
		}
		if d.IsPipeline() {
			count++
		}
		if d.IsStandalone() {
			count++
		}

		if count != 1 {
			t.Errorf("Domain %s belongs to %d categories, want exactly 1", d, count)
		}
	}
}
