package domain

import (
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/concurrency"
)

type Domain int

const (
	DomainLibrarian Domain = iota
	DomainAcademic
	DomainArchivalist
	DomainArchitect
	DomainEngineer
	DomainDesigner
	DomainInspector
	DomainTester
	DomainOrchestrator
	DomainGuide
)

var domainNames = map[Domain]string{
	DomainLibrarian:    "librarian",
	DomainAcademic:     "academic",
	DomainArchivalist:  "archivalist",
	DomainArchitect:    "architect",
	DomainEngineer:     "engineer",
	DomainDesigner:     "designer",
	DomainInspector:    "inspector",
	DomainTester:       "tester",
	DomainOrchestrator: "orchestrator",
	DomainGuide:        "guide",
}

var nameToDoamin = map[string]Domain{
	"librarian":    DomainLibrarian,
	"academic":     DomainAcademic,
	"archivalist":  DomainArchivalist,
	"architect":    DomainArchitect,
	"engineer":     DomainEngineer,
	"designer":     DomainDesigner,
	"inspector":    DomainInspector,
	"tester":       DomainTester,
	"orchestrator": DomainOrchestrator,
	"guide":        DomainGuide,
}

var AgentToDomain = map[concurrency.AgentType]Domain{
	concurrency.AgentLibrarian:    DomainLibrarian,
	concurrency.AgentAcademic:     DomainAcademic,
	concurrency.AgentArchivalist:  DomainArchivalist,
	concurrency.AgentArchitect:    DomainArchitect,
	"engineer":                    DomainEngineer,
	"designer":                    DomainDesigner,
	"inspector":                   DomainInspector,
	"tester":                      DomainTester,
	concurrency.AgentOrchestrator: DomainOrchestrator,
	concurrency.AgentGuide:        DomainGuide,
}

var DomainToAgent = map[Domain]concurrency.AgentType{
	DomainLibrarian:    concurrency.AgentLibrarian,
	DomainAcademic:     concurrency.AgentAcademic,
	DomainArchivalist:  concurrency.AgentArchivalist,
	DomainArchitect:    concurrency.AgentArchitect,
	DomainEngineer:     "engineer",
	DomainDesigner:     "designer",
	DomainInspector:    "inspector",
	DomainTester:       "tester",
	DomainOrchestrator: concurrency.AgentOrchestrator,
	DomainGuide:        concurrency.AgentGuide,
}

func (d Domain) String() string {
	if name, ok := domainNames[d]; ok {
		return name
	}
	return fmt.Sprintf("domain(%d)", d)
}

func (d Domain) IsValid() bool {
	_, ok := domainNames[d]
	return ok
}

func ParseDomain(s string) (Domain, bool) {
	d, ok := nameToDoamin[s]
	return d, ok
}

func ValidDomains() []Domain {
	return []Domain{
		DomainLibrarian,
		DomainAcademic,
		DomainArchivalist,
		DomainArchitect,
		DomainEngineer,
		DomainDesigner,
		DomainInspector,
		DomainTester,
		DomainOrchestrator,
		DomainGuide,
	}
}

func KnowledgeDomains() []Domain {
	return []Domain{
		DomainLibrarian,
		DomainAcademic,
		DomainArchivalist,
		DomainArchitect,
	}
}

func PipelineDomains() []Domain {
	return []Domain{
		DomainEngineer,
		DomainDesigner,
		DomainInspector,
		DomainTester,
	}
}

func StandaloneDomains() []Domain {
	return []Domain{
		DomainOrchestrator,
		DomainGuide,
	}
}

func (d Domain) IsKnowledge() bool {
	switch d {
	case DomainLibrarian, DomainAcademic, DomainArchivalist, DomainArchitect:
		return true
	default:
		return false
	}
}

func (d Domain) IsPipeline() bool {
	switch d {
	case DomainEngineer, DomainDesigner, DomainInspector, DomainTester:
		return true
	default:
		return false
	}
}

func (d Domain) IsStandalone() bool {
	switch d {
	case DomainOrchestrator, DomainGuide:
		return true
	default:
		return false
	}
}

func (d Domain) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Domain) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	parsed, ok := ParseDomain(s)
	if !ok {
		return fmt.Errorf("invalid domain: %s", s)
	}

	*d = parsed
	return nil
}

func DomainFromAgentType(agentType concurrency.AgentType) (Domain, bool) {
	d, ok := AgentToDomain[agentType]
	return d, ok
}

func (d Domain) ToAgentType() concurrency.AgentType {
	return DomainToAgent[d]
}
