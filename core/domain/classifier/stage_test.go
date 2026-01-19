package classifier

import (
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/domain"
)

func TestNewStageResult(t *testing.T) {
	r := NewStageResult()

	if r.Domains == nil {
		t.Error("Domains should not be nil")
	}
	if r.Confidences == nil {
		t.Error("Confidences should not be nil")
	}
	if r.Signals == nil {
		t.Error("Signals should not be nil")
	}
	if !r.ShouldContinue {
		t.Error("ShouldContinue should default to true")
	}
}

func TestStageResult_AddDomain(t *testing.T) {
	r := NewStageResult()

	r.AddDomain(domain.DomainLibrarian, 0.85, []string{"code", "function"})

	if r.DomainCount() != 1 {
		t.Errorf("DomainCount = %d, want 1", r.DomainCount())
	}
	if r.GetConfidence(domain.DomainLibrarian) != 0.85 {
		t.Errorf("Confidence = %f, want 0.85", r.GetConfidence(domain.DomainLibrarian))
	}
	if len(r.GetSignals(domain.DomainLibrarian)) != 2 {
		t.Errorf("Signals count = %d, want 2", len(r.GetSignals(domain.DomainLibrarian)))
	}
}

func TestStageResult_AddDomain_Duplicate(t *testing.T) {
	r := NewStageResult()

	r.AddDomain(domain.DomainLibrarian, 0.85, []string{"code"})
	r.AddDomain(domain.DomainLibrarian, 0.90, []string{"function"})

	if r.DomainCount() != 1 {
		t.Errorf("DomainCount = %d, want 1 (no duplicates)", r.DomainCount())
	}
	if r.GetConfidence(domain.DomainLibrarian) != 0.90 {
		t.Error("Confidence should be updated to 0.90")
	}
	if len(r.GetSignals(domain.DomainLibrarian)) != 2 {
		t.Error("Signals should be accumulated")
	}
}

func TestStageResult_HighestConfidence(t *testing.T) {
	r := NewStageResult()

	r.AddDomain(domain.DomainLibrarian, 0.70, nil)
	r.AddDomain(domain.DomainAcademic, 0.90, nil)
	r.AddDomain(domain.DomainArchivalist, 0.60, nil)

	d, conf := r.HighestConfidence()

	if d != domain.DomainAcademic {
		t.Errorf("HighestConfidence domain = %v, want %v", d, domain.DomainAcademic)
	}
	if conf != 0.90 {
		t.Errorf("HighestConfidence value = %f, want 0.90", conf)
	}
}

func TestStageResult_HighestConfidence_Empty(t *testing.T) {
	r := NewStageResult()

	_, conf := r.HighestConfidence()

	if conf != 0 {
		t.Errorf("HighestConfidence on empty should be 0, got %f", conf)
	}
}

func TestStageResult_IsTerminal(t *testing.T) {
	r := NewStageResult()
	r.AddDomain(domain.DomainLibrarian, 0.90, nil)

	if !r.IsTerminal(0.85) {
		t.Error("Should be terminal with single domain above threshold")
	}
	if r.IsTerminal(0.95) {
		t.Error("Should not be terminal below threshold")
	}
}

func TestStageResult_IsTerminal_MultipleDomains(t *testing.T) {
	r := NewStageResult()
	r.AddDomain(domain.DomainLibrarian, 0.90, nil)
	r.AddDomain(domain.DomainAcademic, 0.85, nil)

	if r.IsTerminal(0.80) {
		t.Error("Should not be terminal with multiple domains")
	}
}

func TestStageResult_SetMethod(t *testing.T) {
	r := NewStageResult()
	r.SetMethod("lexical")

	if r.Method != "lexical" {
		t.Errorf("Method = %s, want lexical", r.Method)
	}
}

func TestStageResult_MarkComplete(t *testing.T) {
	r := NewStageResult()
	r.MarkComplete()

	if r.ShouldContinue {
		t.Error("ShouldContinue should be false after MarkComplete")
	}
}

func TestStageResult_IsEmpty(t *testing.T) {
	r := NewStageResult()

	if !r.IsEmpty() {
		t.Error("New result should be empty")
	}

	r.AddDomain(domain.DomainLibrarian, 0.85, nil)

	if r.IsEmpty() {
		t.Error("Result with domain should not be empty")
	}
}

func TestStageResult_Clone(t *testing.T) {
	r := NewStageResult()
	r.AddDomain(domain.DomainLibrarian, 0.85, []string{"code", "function"})
	r.AddDomain(domain.DomainAcademic, 0.80, []string{"research"})
	r.SetMethod("lexical")
	r.MarkComplete()

	clone := r.Clone()

	if clone.Method != r.Method {
		t.Error("Clone method mismatch")
	}
	if clone.ShouldContinue != r.ShouldContinue {
		t.Error("Clone ShouldContinue mismatch")
	}
	if clone.DomainCount() != r.DomainCount() {
		t.Error("Clone domain count mismatch")
	}

	r.AddDomain(domain.DomainArchivalist, 0.75, nil)
	if clone.DomainCount() == r.DomainCount() {
		t.Error("Clone should not be affected by original modification")
	}
}

func TestStageResult_CloneNil(t *testing.T) {
	var r *StageResult
	clone := r.Clone()

	if clone != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestStageResult_Merge(t *testing.T) {
	r1 := NewStageResult()
	r1.AddDomain(domain.DomainLibrarian, 0.85, []string{"code"})

	r2 := NewStageResult()
	r2.AddDomain(domain.DomainAcademic, 0.90, []string{"research"})
	r2.AddDomain(domain.DomainLibrarian, 0.80, []string{"function"})

	r1.Merge(r2)

	if r1.DomainCount() != 2 {
		t.Errorf("Merged domain count = %d, want 2", r1.DomainCount())
	}

	if r1.GetConfidence(domain.DomainLibrarian) != 0.85 {
		t.Error("Higher confidence should be kept")
	}

	if len(r1.GetSignals(domain.DomainLibrarian)) != 2 {
		t.Error("Signals should be merged")
	}
}

func TestStageResult_MergeNil(t *testing.T) {
	r := NewStageResult()
	r.AddDomain(domain.DomainLibrarian, 0.85, nil)

	r.Merge(nil)

	if r.DomainCount() != 1 {
		t.Error("Merge with nil should not change result")
	}
}

func TestStageResult_GetConfidenceNotExist(t *testing.T) {
	r := NewStageResult()

	conf := r.GetConfidence(domain.DomainLibrarian)

	if conf != 0 {
		t.Errorf("GetConfidence for non-existent domain = %f, want 0", conf)
	}
}

func TestStageResult_GetSignalsNotExist(t *testing.T) {
	r := NewStageResult()

	signals := r.GetSignals(domain.DomainLibrarian)

	if signals != nil {
		t.Error("GetSignals for non-existent domain should be nil")
	}
}

func TestStageResult_JSONSerialization(t *testing.T) {
	r := NewStageResult()
	r.AddDomain(domain.DomainLibrarian, 0.85, []string{"code"})
	r.SetMethod("lexical")

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshaled StageResult
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if unmarshaled.Method != r.Method {
		t.Error("Method mismatch after JSON roundtrip")
	}
}

func TestStageResult_AddDomainNilSignals(t *testing.T) {
	r := NewStageResult()
	r.AddDomain(domain.DomainLibrarian, 0.85, nil)

	signals := r.GetSignals(domain.DomainLibrarian)
	if signals != nil {
		t.Error("Signals should be nil when added with nil")
	}
}
