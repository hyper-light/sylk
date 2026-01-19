package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewDomainContext(t *testing.T) {
	query := "test query"
	dc := NewDomainContext(query)

	if dc.OriginalQuery != query {
		t.Errorf("OriginalQuery = %s, want %s", dc.OriginalQuery, query)
	}
	if dc.DetectedDomains == nil {
		t.Error("DetectedDomains should not be nil")
	}
	if dc.DomainConfidences == nil {
		t.Error("DomainConfidences should not be nil")
	}
	if dc.Signals == nil {
		t.Error("Signals should not be nil")
	}
	if dc.ClassifiedAt.IsZero() {
		t.Error("ClassifiedAt should be set")
	}
}

func TestDomainContext_AddDomain(t *testing.T) {
	dc := NewDomainContext("test")

	dc.AddDomain(DomainLibrarian, 0.85, []string{"our code", "function"})

	if !dc.HasDomain(DomainLibrarian) {
		t.Error("Should have DomainLibrarian")
	}
	if dc.GetConfidence(DomainLibrarian) != 0.85 {
		t.Errorf("Confidence = %f, want 0.85", dc.GetConfidence(DomainLibrarian))
	}
	if len(dc.GetSignals(DomainLibrarian)) != 2 {
		t.Errorf("Signals count = %d, want 2", len(dc.GetSignals(DomainLibrarian)))
	}
	if dc.PrimaryDomain != DomainLibrarian {
		t.Errorf("PrimaryDomain = %v, want %v", dc.PrimaryDomain, DomainLibrarian)
	}
}

func TestDomainContext_AddMultipleDomains(t *testing.T) {
	dc := NewDomainContext("test")

	dc.AddDomain(DomainLibrarian, 0.85, []string{"code"})
	dc.AddDomain(DomainAcademic, 0.90, []string{"research"})
	dc.AddDomain(DomainArchivalist, 0.70, []string{"history"})

	if dc.DomainCount() != 3 {
		t.Errorf("DomainCount = %d, want 3", dc.DomainCount())
	}

	if dc.PrimaryDomain != DomainAcademic {
		t.Errorf("PrimaryDomain = %v, want %v (highest confidence)", dc.PrimaryDomain, DomainAcademic)
	}

	if len(dc.SecondaryDomains) != 2 {
		t.Errorf("SecondaryDomains count = %d, want 2", len(dc.SecondaryDomains))
	}
}

func TestDomainContext_AllowedDomains_SingleDomain(t *testing.T) {
	dc := NewDomainContext("test")
	dc.AddDomain(DomainLibrarian, 0.85, nil)
	dc.SetCrossDomain(false)

	allowed := dc.AllowedDomains()
	if len(allowed) != 1 {
		t.Errorf("AllowedDomains len = %d, want 1", len(allowed))
	}
	if allowed[0] != DomainLibrarian {
		t.Errorf("AllowedDomains[0] = %v, want %v", allowed[0], DomainLibrarian)
	}
}

func TestDomainContext_AllowedDomains_CrossDomain(t *testing.T) {
	dc := NewDomainContext("test")
	dc.AddDomain(DomainLibrarian, 0.85, nil)
	dc.AddDomain(DomainAcademic, 0.80, nil)
	dc.SetCrossDomain(true)

	allowed := dc.AllowedDomains()
	if len(allowed) != 2 {
		t.Errorf("AllowedDomains len = %d, want 2", len(allowed))
	}
}

func TestDomainContext_HighestConfidenceDomain(t *testing.T) {
	dc := NewDomainContext("test")
	dc.AddDomain(DomainLibrarian, 0.70, nil)
	dc.AddDomain(DomainAcademic, 0.95, nil)

	if dc.HighestConfidenceDomain() != DomainAcademic {
		t.Errorf("HighestConfidenceDomain = %v, want %v", dc.HighestConfidenceDomain(), DomainAcademic)
	}
}

func TestDomainContext_HasDomain(t *testing.T) {
	dc := NewDomainContext("test")
	dc.AddDomain(DomainLibrarian, 0.85, nil)

	if !dc.HasDomain(DomainLibrarian) {
		t.Error("Should have DomainLibrarian")
	}
	if dc.HasDomain(DomainAcademic) {
		t.Error("Should not have DomainAcademic")
	}
}

func TestDomainContext_Clone(t *testing.T) {
	dc := NewDomainContext("test query")
	dc.AddDomain(DomainLibrarian, 0.85, []string{"code", "function"})
	dc.AddDomain(DomainAcademic, 0.80, []string{"research"})
	dc.SetCrossDomain(true)
	dc.SetClassificationMethod("lexical")
	dc.SetOverallConfidence(0.82)
	dc.MarkCacheHit("key123")

	clone := dc.Clone()

	if clone.OriginalQuery != dc.OriginalQuery {
		t.Error("Clone OriginalQuery mismatch")
	}
	if clone.IsCrossDomain != dc.IsCrossDomain {
		t.Error("Clone IsCrossDomain mismatch")
	}
	if clone.PrimaryDomain != dc.PrimaryDomain {
		t.Error("Clone PrimaryDomain mismatch")
	}
	if clone.CacheHit != dc.CacheHit {
		t.Error("Clone CacheHit mismatch")
	}

	dc.AddDomain(DomainArchivalist, 0.75, nil)
	if clone.HasDomain(DomainArchivalist) {
		t.Error("Clone should not have DomainArchivalist after modifying original")
	}

	dc.Signals[DomainLibrarian] = append(dc.Signals[DomainLibrarian], "modified")
	if len(clone.GetSignals(DomainLibrarian)) != 2 {
		t.Error("Clone signals should not be modified")
	}
}

func TestDomainContext_CloneNil(t *testing.T) {
	var dc *DomainContext
	clone := dc.Clone()
	if clone != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestDomainContext_IsEmpty(t *testing.T) {
	dc := NewDomainContext("test")
	if !dc.IsEmpty() {
		t.Error("New context should be empty")
	}

	dc.AddDomain(DomainLibrarian, 0.85, nil)
	if dc.IsEmpty() {
		t.Error("Context with domain should not be empty")
	}
}

func TestDomainContext_SetMethods(t *testing.T) {
	dc := NewDomainContext("test")

	dc.SetCrossDomain(true)
	if !dc.IsCrossDomain {
		t.Error("IsCrossDomain should be true")
	}

	dc.SetClassificationMethod("embedding")
	if dc.ClassificationMethod != "embedding" {
		t.Errorf("ClassificationMethod = %s, want embedding", dc.ClassificationMethod)
	}

	dc.SetOverallConfidence(0.92)
	if dc.Confidence != 0.92 {
		t.Errorf("Confidence = %f, want 0.92", dc.Confidence)
	}

	dc.MarkCacheHit("test-key")
	if !dc.CacheHit {
		t.Error("CacheHit should be true")
	}
	if dc.CacheKey != "test-key" {
		t.Errorf("CacheKey = %s, want test-key", dc.CacheKey)
	}
}

func TestDomainContext_DuplicateDomain(t *testing.T) {
	dc := NewDomainContext("test")

	dc.AddDomain(DomainLibrarian, 0.85, []string{"code"})
	dc.AddDomain(DomainLibrarian, 0.90, []string{"function"})

	if dc.DomainCount() != 1 {
		t.Errorf("DomainCount = %d, want 1 (no duplicates)", dc.DomainCount())
	}
	if dc.GetConfidence(DomainLibrarian) != 0.90 {
		t.Error("Confidence should be updated to 0.90")
	}
	if len(dc.GetSignals(DomainLibrarian)) != 2 {
		t.Error("Signals should be accumulated")
	}
}

func TestDomainContext_JSONSerialization(t *testing.T) {
	dc := NewDomainContext("test query")
	dc.AddDomain(DomainLibrarian, 0.85, []string{"code"})
	dc.SetCrossDomain(false)
	dc.SetClassificationMethod("lexical")
	dc.SetOverallConfidence(0.85)

	data, err := json.Marshal(dc)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshaled DomainContext
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if unmarshaled.OriginalQuery != dc.OriginalQuery {
		t.Error("OriginalQuery mismatch after JSON roundtrip")
	}
	if unmarshaled.PrimaryDomain != dc.PrimaryDomain {
		t.Error("PrimaryDomain mismatch after JSON roundtrip")
	}
}

func TestDomainContext_GetConfidenceNotExist(t *testing.T) {
	dc := NewDomainContext("test")
	conf := dc.GetConfidence(DomainLibrarian)
	if conf != 0 {
		t.Errorf("GetConfidence for non-existent domain = %f, want 0", conf)
	}
}

func TestDomainContext_GetSignalsNotExist(t *testing.T) {
	dc := NewDomainContext("test")
	signals := dc.GetSignals(DomainLibrarian)
	if signals != nil {
		t.Error("GetSignals for non-existent domain should be nil")
	}
}

func TestDomainContext_UpdatePrimarySecondaryEmpty(t *testing.T) {
	dc := NewDomainContext("test")
	dc.updatePrimarySecondary()
}

func TestDomainContext_ClassifiedAtSet(t *testing.T) {
	before := time.Now()
	dc := NewDomainContext("test")
	after := time.Now()

	if dc.ClassifiedAt.Before(before) || dc.ClassifiedAt.After(after) {
		t.Error("ClassifiedAt should be between before and after")
	}
}
