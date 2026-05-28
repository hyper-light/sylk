package claims

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type panicCodec struct{}

func (panicCodec) Marshal(any) ([]byte, error) {
	panic("marshal exploded")
}

func (panicCodec) Unmarshal([]byte, any) error {
	panic("unmarshal exploded")
}

type emptyPayloadCodec struct{}

func (emptyPayloadCodec) Marshal(any) ([]byte, error) {
	return nil, nil
}

func (emptyPayloadCodec) Unmarshal([]byte, any) error {
	return nil
}

type countingCodec struct {
	marshalCount   atomic.Int64
	unmarshalCount atomic.Int64
}

func (c *countingCodec) Marshal(value any) ([]byte, error) {
	c.marshalCount.Add(1)
	return json.Marshal(value)
}

func (c *countingCodec) Unmarshal(data []byte, target any) error {
	c.unmarshalCount.Add(1)
	return json.Unmarshal(data, target)
}

func TestSetAndGetArtifactData(t *testing.T) {
	artifact := &Artifact{ID: "a", Kind: ArtifactKindPlanMarkdown, Reference: "legacy", Metadata: map[string]any{"keep": true}}
	if err := SetArtifactData(artifact, PlanMarkdownArtifactData{Markdown: "# Plan", Title: "Plan"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if artifact.DataType != ArtifactDataTypePlanMarkdown || len(artifact.Data) == 0 || artifact.Size != int64(len(artifact.Data)) {
		t.Fatalf("bad artifact data fields: %+v", artifact)
	}
	if artifact.Reference != "legacy" || artifact.Metadata["keep"] != true {
		t.Fatalf("unrelated fields not preserved: %+v", artifact)
	}
	value, err := ArtifactData[PlanMarkdownArtifactData](artifact)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if value.Markdown != "# Plan" || value.Title != "Plan" {
		t.Fatalf("decoded value = %+v", value)
	}
	if got := MustArtifactData[PlanMarkdownArtifactData](artifact); got.Markdown != "# Plan" {
		t.Fatalf("test-only must helper = %+v", got)
	}
}

func TestArtifactDataFailuresAreDistinct(t *testing.T) {
	if _, err := ArtifactData[PlanMarkdownArtifactData](nil); !errors.Is(err, ErrArtifactDataNil) {
		t.Fatalf("nil artifact error = %v", err)
	}
	empty := &Artifact{ID: "empty", DataType: ArtifactDataTypePlanMarkdown}
	if _, err := ArtifactData[PlanMarkdownArtifactData](empty); !errors.Is(err, ErrArtifactDataEmpty) {
		t.Fatalf("empty data error = %v", err)
	}
	mismatch := &Artifact{ID: "mismatch"}
	if err := SetArtifactData(mismatch, PlanMarkdownArtifactData{Markdown: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArtifactData[PresentationEvidenceArtifactData](mismatch); !errors.Is(err, ErrArtifactDataMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	unknown := &Artifact{ID: "unknown", DataType: "unknown.v1", Data: []byte("{}")}
	if _, err := ArtifactData[syntheticArtifactPayload](unknown); !errors.Is(err, ErrArtifactTypeUnknown) {
		t.Fatalf("unknown codec error = %v", err)
	}
	missingHash := CloneArtifact(mismatch)
	missingHash.ContentHash = ""
	if _, err := ArtifactData[PlanMarkdownArtifactData](missingHash); !errors.Is(err, ErrArtifactDataHashMismatch) {
		t.Fatalf("missing hash error = %v", err)
	}
	mismatch.ContentHash = ArtifactContentHash([]byte("different"))
	if _, err := ArtifactData[PlanMarkdownArtifactData](mismatch); !errors.Is(err, ErrArtifactDataHashMismatch) {
		t.Fatalf("hash mismatch error = %v", err)
	}
	sizeMismatch := CloneArtifact(mismatch)
	sizeMismatch.ContentHash = ArtifactContentHash(sizeMismatch.Data)
	sizeMismatch.Size = int64(len(sizeMismatch.Data) + 1)
	if _, err := ArtifactData[PlanMarkdownArtifactData](sizeMismatch); !errors.Is(err, ErrArtifactDataSizeMismatch) {
		t.Fatalf("size mismatch error = %v", err)
	}
	var nilPayload *PlanMarkdownArtifactData
	if err := SetArtifactData(&Artifact{ID: "nil-payload"}, nilPayload); !errors.Is(err, ErrArtifactTypeInvalid) {
		t.Fatalf("nil typed payload error = %v, want invalid", err)
	}
}

func TestSetArtifactDataRejectsEmptyCodecPayload(t *testing.T) {
	registry := NewTypeRegistry()
	if err := registry.Register("empty.v1", syntheticArtifactPayload{}, emptyPayloadCodec{}); err != nil {
		t.Fatal(err)
	}
	if err := SetArtifactDataWithRegistry(registry, &Artifact{ID: "empty"}, syntheticArtifactPayload{Name: "x"}); !errors.Is(err, ErrArtifactDataEmpty) {
		t.Fatalf("empty codec payload error = %v, want empty data", err)
	}
}

func TestArtifactDataCodecPanicIsControlledError(t *testing.T) {
	registry := NewTypeRegistry()
	if err := registry.Register("panic.v1", syntheticArtifactPayload{}, panicCodec{}); err != nil {
		t.Fatal(err)
	}
	artifact := &Artifact{ID: "panic"}
	if err := SetArtifactDataWithRegistry(registry, artifact, syntheticArtifactPayload{Name: "x"}); !errors.Is(err, ErrArtifactCodecPanic) {
		t.Fatalf("marshal panic error = %v", err)
	}
	artifact.DataType = "panic.v1"
	artifact.Data = []byte(`{"name":"x"}`)
	artifact.ContentHash = ArtifactContentHash(artifact.Data)
	if _, err := ArtifactDataWithRegistry[syntheticArtifactPayload](registry, artifact); !errors.Is(err, ErrArtifactCodecPanic) {
		t.Fatalf("unmarshal panic error = %v", err)
	}
}

func TestArtifactDataHelpersCallCodecExactlyOnce(t *testing.T) {
	registry := NewTypeRegistry()
	codec := &countingCodec{}
	if err := registry.Register("counting.v1", syntheticArtifactPayload{}, codec); err != nil {
		t.Fatal(err)
	}
	artifact := &Artifact{ID: "counting"}
	if err := SetArtifactDataWithRegistry(registry, artifact, syntheticArtifactPayload{Name: "x"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if codec.marshalCount.Load() != 1 {
		t.Fatalf("marshal count = %d, want 1", codec.marshalCount.Load())
	}
	if _, err := ArtifactDataWithRegistry[syntheticArtifactPayload](registry, artifact); err != nil {
		t.Fatalf("get: %v", err)
	}
	if codec.unmarshalCount.Load() != 1 {
		t.Fatalf("unmarshal count = %d, want 1", codec.unmarshalCount.Load())
	}
}

func TestApplyBuiltinArtifactDataBridgePlanMarkdown(t *testing.T) {
	artifact := &Artifact{
		ID:        "legacy-plan",
		Kind:      ArtifactKindPlanMarkdown,
		Reference: "# Plan",
		Presentation: &Presentation{
			Title: "Plan",
		},
	}
	if err := ApplyBuiltinArtifactDataBridge(artifact); err != nil {
		t.Fatalf("bridge: %v", err)
	}
	value, err := ArtifactData[PlanMarkdownArtifactData](artifact)
	if err != nil {
		t.Fatalf("typed read after bridge: %v", err)
	}
	if value.Markdown != "# Plan" || value.Title != "Plan" {
		t.Fatalf("bridge value = %+v", value)
	}

	referenceOnly := fixtureMalformedLegacyArtifact()
	if _, err := ArtifactData[PlanMarkdownArtifactData](&referenceOnly); !errors.Is(err, ErrArtifactDataMismatch) {
		t.Fatalf("legacy artifact forced through typed decode: %v", err)
	}
}

func TestApplyBuiltinArtifactDataBridgeKnowledgeReadiness(t *testing.T) {
	artifact := &Artifact{
		ID:        "knowledge-ready",
		Kind:      ArtifactKindReadiness,
		Reference: "committed knowledge backend ready",
		Metadata: map[string]any{
			"component":   KnowledgeBackendAgentID,
			"quality_bar": KnowledgeBackendReadyQuality,
		},
	}
	if err := ApplyBuiltinArtifactDataBridge(artifact); err != nil {
		t.Fatalf("bridge: %v", err)
	}
	value, err := ArtifactData[KnowledgeReadinessArtifactData](artifact)
	if err != nil {
		t.Fatalf("typed read after bridge: %v", err)
	}
	if value.Component != KnowledgeBackendAgentID || value.QualityBar != KnowledgeBackendReadyQuality {
		t.Fatalf("bridge value = %+v", value)
	}
}

func TestArtifactDataConcurrentReadsUseDefensiveData(t *testing.T) {
	artifact := &Artifact{ID: "concurrent"}
	if err := SetArtifactData(artifact, PlanMarkdownArtifactData{Markdown: "# Plan"}); err != nil {
		t.Fatal(err)
	}
	clone := CloneArtifact(artifact)
	clone.Data[0] = 0
	if artifact.Data[0] == 0 {
		t.Fatal("CloneArtifact shared data")
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ArtifactData[PlanMarkdownArtifactData](artifact); err != nil {
				t.Errorf("read: %v", err)
			}
		}()
	}
	wg.Wait()
}
