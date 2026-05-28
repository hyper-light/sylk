package claims

import (
	"errors"
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
