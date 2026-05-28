package claims

import (
	"errors"
	"sync"
	"testing"
)

type syntheticArtifactPayload struct {
	Name string `json:"name"`
}

func TestTypeRegistryRegisterLookupListAndDuplicate(t *testing.T) {
	registry := NewTypeRegistry()
	if err := registry.Register("synthetic.v1", syntheticArtifactPayload{}, JSONArtifactCodec{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	entry, err := registry.LookupArtifactType("synthetic.v1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if entry.DataType != "synthetic.v1" || entry.Codec == nil {
		t.Fatalf("bad entry: %+v", entry)
	}
	if got := registry.ListArtifactTypes(); len(got) != 1 || got[0].DataType != "synthetic.v1" {
		t.Fatalf("list = %+v", got)
	}
	if err := registry.Register("synthetic.v1", struct{ Other string }{}, JSONArtifactCodec{}); !errors.Is(err, ErrArtifactTypeDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := registry.LookupArtifactType("missing.v1"); !errors.Is(err, ErrArtifactTypeUnknown) {
		t.Fatalf("unknown error = %v", err)
	}
}

func TestTypeRegistryJSONCodecDeterministicAndRejectsBadInput(t *testing.T) {
	codec := JSONArtifactCodec{}
	first, err := codec.Marshal(map[string]any{"b": 2, "a": 1})
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	second, err := codec.Marshal(map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(first) != string(second) || string(first) != `{"a":1,"b":2}` {
		t.Fatalf("nondeterministic JSON: %q %q", first, second)
	}
	if _, err := codec.Marshal(nil); err == nil {
		t.Fatal("expected nil payload error")
	}
	var out map[string]any
	if err := codec.Unmarshal(nil, &out); err == nil {
		t.Fatal("expected empty payload error")
	}
}

func TestTypeRegistryConcurrentLookupsAndRegistrations(t *testing.T) {
	registry := NewTypeRegistry()
	if err := registry.Register("seed.v1", syntheticArtifactPayload{}, JSONArtifactCodec{}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := registry.LookupArtifactType("seed.v1"); err != nil {
				t.Errorf("lookup: %v", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		type anotherPayload struct {
			Value int `json:"value"`
		}
		if err := registry.Register("another.v1", anotherPayload{}, JSONArtifactCodec{}); err != nil {
			t.Errorf("register another: %v", err)
		}
	}()
	wg.Wait()
}

func TestBuiltinArtifactDataCatalog(t *testing.T) {
	registry := NewTypeRegistry()
	if err := RegisterBuiltinArtifactDataTypes(registry); err != nil {
		t.Fatalf("register builtins: %v", err)
	}
	for _, dataType := range []string{
		ArtifactDataTypePlanMarkdown,
		ArtifactDataTypeExpectedToolInvocation,
		ArtifactDataTypeExpectedToolOutput,
		ArtifactDataTypeExpectedToolSkipped,
		ArtifactDataTypeCarryForwardWorkingContext,
		ArtifactDataTypeCarryForwardEvidenceDigest,
		ArtifactDataTypeCarryForwardSourceIndex,
		ArtifactDataTypeCarryForwardContinuity,
		ArtifactDataTypeCarryForwardSessionCursor,
		ArtifactDataTypePresentationEvidence,
	} {
		if _, err := registry.LookupArtifactType(dataType); err != nil {
			t.Fatalf("builtin %s missing: %v", dataType, err)
		}
	}
	if err := RegisterBuiltinArtifactDataTypes(registry); !errors.Is(err, ErrArtifactTypeDuplicate) {
		t.Fatalf("duplicate builtin error = %v", err)
	}
}
