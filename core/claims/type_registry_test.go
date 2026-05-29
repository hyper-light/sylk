package claims

import (
	"errors"
	"reflect"
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
	if err := registry.Register("synthetic_alias.v1", syntheticArtifactPayload{}, JSONArtifactCodec{}); !errors.Is(err, ErrArtifactTypeDuplicate) {
		t.Fatalf("duplicate Go type error = %v, want duplicate", err)
	}
	if err := registry.Register("synthetic_pointer.v1", &struct{ Pointer string }{}, JSONArtifactCodec{}); err != nil {
		t.Fatalf("pointer sample register: %v", err)
	}
	if _, err := registry.LookupArtifactTypeFor(reflect.TypeOf((*struct{ Pointer string })(nil))); err != nil {
		t.Fatalf("pointer lookup: %v", err)
	}
	if _, err := registry.LookupArtifactType("missing.v1"); !errors.Is(err, ErrArtifactTypeUnknown) {
		t.Fatalf("unknown error = %v", err)
	}
}

func TestTypeRegistryRejectsInvalidRegistrationInputs(t *testing.T) {
	registry := NewTypeRegistry()
	for _, tc := range []struct {
		name     string
		dataType string
		sample   any
		codec    ArtifactDataCodec
		want     error
	}{
		{name: "empty datatype", dataType: "", sample: syntheticArtifactPayload{}, codec: JSONArtifactCodec{}, want: ErrArtifactTypeInvalid},
		{name: "nil sample", dataType: "nil_sample.v1", sample: nil, codec: JSONArtifactCodec{}, want: ErrArtifactTypeInvalid},
		{name: "nil pointer sample", dataType: "nil_pointer_sample.v1", sample: (*syntheticArtifactPayload)(nil), codec: JSONArtifactCodec{}, want: ErrArtifactTypeInvalid},
		{name: "nil codec", dataType: "nil_codec.v1", sample: syntheticArtifactPayload{}, codec: nil, want: ErrArtifactCodecNil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := registry.Register(tc.dataType, tc.sample, tc.codec); !errors.Is(err, tc.want) {
				t.Fatalf("Register error = %v, want %v", err, tc.want)
			}
		})
	}
	var nilRegistry *TypeRegistry
	if err := nilRegistry.Register("x.v1", syntheticArtifactPayload{}, JSONArtifactCodec{}); !errors.Is(err, ErrArtifactTypeInvalid) {
		t.Fatalf("nil registry register error = %v, want invalid", err)
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
		ArtifactDataTypeKnowledgeReadiness,
		ArtifactDataTypeIdentityAllocation,
		ArtifactDataTypeIdentityLineage,
		ArtifactDataTypeActivationRecord,
		ArtifactDataTypeDAGOperation,
		ArtifactDataTypeVFSOperation,
		ArtifactDataTypeToolRuntimeExecution,
		ArtifactDataTypeKnowledgeOperation,
		ArtifactDataTypeMemoryContinuity,
		ArtifactDataTypeDocumentOperation,
		ArtifactDataTypeGuardianDecision,
		ArtifactDataTypeProviderGatewayCall,
		ArtifactDataTypeExternalAdapterEvent,
	} {
		if _, err := registry.LookupArtifactType(dataType); err != nil {
			t.Fatalf("builtin %s missing: %v", dataType, err)
		}
	}
	if err := RegisterBuiltinArtifactDataTypes(registry); !errors.Is(err, ErrArtifactTypeDuplicate) {
		t.Fatalf("duplicate builtin error = %v", err)
	}
}
