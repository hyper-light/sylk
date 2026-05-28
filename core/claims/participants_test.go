package claims

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParticipantUIDDerivationStableAcrossScopeKeyOrdering(t *testing.T) {
	left, err := DeriveParticipantUID(ParticipantCategoryService, "provider_gateway", map[string]string{"session": "default", "region": "local"})
	if err != nil {
		t.Fatalf("DeriveParticipantUID left: %v", err)
	}
	right, err := DeriveParticipantUID(ParticipantCategoryService, "provider_gateway", map[string]string{"region": "local", "session": "default"})
	if err != nil {
		t.Fatalf("DeriveParticipantUID right: %v", err)
	}
	if left != right {
		t.Fatalf("derived uid differs by map ordering: %s != %s", left, right)
	}
	const want = "participant:service:provider_gateway:3062309bab58979e6eee316058cb43ff853ac4e2454c1f1a58a0431c54acc0aa"
	if left != want {
		t.Fatalf("derived uid = %q, want stable golden %q", left, want)
	}
}

func TestParticipantUIDDerivationNormalizesScopeAndSanitizesTopicSegments(t *testing.T) {
	scope := map[string]string{" session ": " default ", "region": "local"}
	uid, err := DeriveParticipantUID(ParticipantCategoryService, " provider/gateway:alpha beta\tgamma\nv1.2 ", scope)
	if err != nil {
		t.Fatalf("DeriveParticipantUID: %v", err)
	}
	if strings.ContainsAny(uid, "./ \t\n\r") {
		t.Fatalf("derived uid %q contains a topic-unsafe separator", uid)
	}
	if scope[" session "] != " default " {
		t.Fatalf("DeriveParticipantUID mutated caller scope map: %#v", scope)
	}
	_, err = DeriveParticipantUID("", "provider_gateway", map[string]string{"session": "default"})
	if !errors.Is(err, ErrParticipantRegistrationInvalid) {
		t.Fatalf("empty category error = %v, want invalid", err)
	}
}

func TestParticipantRegistrationRejectsUnboundedMetadata(t *testing.T) {
	_, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "default"}, 0, 1, time.Second, []ActionType{ActionTypeTask})
	if !errors.Is(err, ErrParticipantRegistrationInvalid) {
		t.Fatalf("NewServiceParticipantRegistration error = %v, want invalid", err)
	}
	_, err = NewServiceParticipantRegistration("tool_runtime", nil, 1, 1, time.Second, []ActionType{ActionTypeTask})
	if !errors.Is(err, ErrParticipantRegistrationInvalid) {
		t.Fatalf("NewServiceParticipantRegistration no scope error = %v, want invalid", err)
	}
	_, err = NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "default"}, 1, 1, 0, []ActionType{ActionTypeTask})
	if !errors.Is(err, ErrParticipantRegistrationInvalid) {
		t.Fatalf("NewServiceParticipantRegistration no timeout error = %v, want invalid", err)
	}
	_, err = NewParticipantRegistration(ParticipantCategorySystem, "boot", nil, 1, 1, time.Second, HandlerDeterminismPure, []ActionType{ActionTypeBoot})
	if !errors.Is(err, ErrParticipantRegistrationInvalid) {
		t.Fatalf("system registration without scope error = %v, want invalid", err)
	}
}

func TestParticipantRegistrationNormalizesActionsAndReferences(t *testing.T) {
	participant, err := NewServiceParticipantRegistration(
		" tool_runtime ",
		map[string]string{" session ": " default "},
		4,
		2,
		time.Second,
		[]ActionType{ActionTypeTask, ActionTypeTask, ActionTypeConsultation},
	)
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	if want := []ActionType{ActionTypeConsultation, ActionTypeTask}; !reflect.DeepEqual(participant.Actions, want) {
		t.Fatalf("actions = %#v, want %#v", participant.Actions, want)
	}
	ref := participant.AgentRef()
	if ref.UID != participant.UID || ref.Type != "tool_runtime" || ref.Name != "tool_runtime" || ref.Category != string(ParticipantCategoryService) || ref.Generation != InitialParticipantGeneration {
		t.Fatalf("normalized ref = %+v, participant = %+v", ref, participant)
	}
	if ref.Labels["session"] != "default" {
		t.Fatalf("ref labels = %#v, want normalized scope labels", ref.Labels)
	}
}

func TestParticipantRegistrationRejectsMismatchedDeterministicUID(t *testing.T) {
	participant, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "default"}, 4, 2, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	participant.UID = participant.UID + ".wrong"
	if err := participant.Validate(); !errors.Is(err, ErrParticipantRegistrationInvalid) {
		t.Fatalf("mismatched uid error = %v, want invalid", err)
	}
}

func TestParticipantRegistryConcurrentRegistrationConverges(t *testing.T) {
	registry := NewParticipantRegistry()
	participant, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "default"}, 4, 2, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("participant registration: %v", err)
	}
	errs := make(chan error, participant.QueueCapacity)
	var wg sync.WaitGroup
	for range participant.QueueCapacity {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := registry.Register(participant)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Register concurrent error: %v", err)
		}
	}
	got, ok := registry.Lookup(participant.UID)
	if !ok {
		t.Fatalf("participant %s not registered", participant.UID)
	}
	if got.UID != participant.UID || got.RouteKey != participant.RouteKey {
		t.Fatalf("registered participant = %#v, want %#v", got, participant)
	}
}

func TestParticipantRegistryRejectsImmutableConflict(t *testing.T) {
	registry := NewParticipantRegistry()
	participant, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "default"}, 4, 2, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("participant registration: %v", err)
	}
	if _, err := registry.Register(participant); err != nil {
		t.Fatalf("Register: %v", err)
	}
	conflict := participant
	conflict.QueueCapacity = participant.QueueCapacity + 1
	_, err = registry.Register(conflict)
	if !errors.Is(err, ErrParticipantRegistrationConflict) {
		t.Fatalf("Register conflict error = %v, want conflict", err)
	}
}

func TestParticipantRegistryAllowsHigherGenerationForIdenticalMetadata(t *testing.T) {
	registry := NewParticipantRegistry()
	participant, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "default"}, 4, 2, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	if _, err := registry.Register(participant); err != nil {
		t.Fatalf("Register: %v", err)
	}
	next := participant
	next.Generation = participant.Generation + 1
	next.Ref.Generation = next.Generation
	got, err := registry.Register(next)
	if err != nil {
		t.Fatalf("Register higher generation: %v", err)
	}
	if got.Generation != next.Generation {
		t.Fatalf("generation = %d, want %d", got.Generation, next.Generation)
	}
	older := participant
	older.Generation = participant.Generation
	got, err = registry.Register(older)
	if err != nil {
		t.Fatalf("Register older generation: %v", err)
	}
	if got.Generation != next.Generation {
		t.Fatalf("older registration overwrote generation: got %d want %d", got.Generation, next.Generation)
	}
}
