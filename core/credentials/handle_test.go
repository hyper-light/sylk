package credentials

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCredentialHandle_Creation(t *testing.T) {
	t.Parallel()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-secret")

	if handle.ID == "" {
		t.Error("handle should have ID")
	}
	if !strings.HasPrefix(handle.ID, "cred_") {
		t.Error("handle ID should have cred_ prefix")
	}
	if handle.Provider != "openai" {
		t.Error("wrong provider")
	}
	if handle.AgentID != "agent-1" {
		t.Error("wrong agent ID")
	}
	if handle.AgentType != "librarian" {
		t.Error("wrong agent type")
	}
	if handle.Used {
		t.Error("new handle should not be used")
	}
}

func TestCredentialHandle_Expiry(t *testing.T) {
	t.Parallel()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-secret")

	if handle.IsExpired() {
		t.Error("new handle should not be expired")
	}

	handle.ExpiresAt = time.Now().Add(-1 * time.Second)

	if !handle.IsExpired() {
		t.Error("handle should be expired")
	}
}

func TestCredentialHandle_Consume(t *testing.T) {
	t.Parallel()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-secret")

	value, err := handle.Consume()
	if err != nil {
		t.Fatalf("first consume should succeed: %v", err)
	}
	if value != "sk-secret" {
		t.Error("wrong value returned")
	}
	if !handle.Used {
		t.Error("handle should be marked as used")
	}

	_, err = handle.Consume()
	if err != ErrHandleUsed {
		t.Error("second consume should fail with ErrHandleUsed")
	}
}

func TestCredentialHandle_ConsumeExpired(t *testing.T) {
	t.Parallel()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-secret")
	handle.ExpiresAt = time.Now().Add(-1 * time.Second)

	_, err := handle.Consume()
	if err != ErrHandleExpired {
		t.Error("consume of expired handle should fail with ErrHandleExpired")
	}
}

func TestCredentialHandle_ValueNotSerialized(t *testing.T) {
	t.Parallel()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-super-secret-key")

	data, err := json.Marshal(handle)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, "sk-super-secret-key") {
		t.Error("credential value should not appear in JSON")
	}
	if strings.Contains(jsonStr, "value") {
		t.Error("value field should not appear in JSON")
	}
}

func TestHandleStore_StoreAndGet(t *testing.T) {
	t.Parallel()

	store := NewHandleStore()
	defer store.Stop()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-secret")
	store.Store(handle)

	retrieved, err := store.Get(handle.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if retrieved.Provider != "openai" {
		t.Error("retrieved wrong handle")
	}
}

func TestHandleStore_GetNotFound(t *testing.T) {
	t.Parallel()

	store := NewHandleStore()
	defer store.Stop()

	_, err := store.Get("nonexistent")
	if err != ErrHandleNotFound {
		t.Error("should return ErrHandleNotFound")
	}
}

func TestHandleStore_Resolve(t *testing.T) {
	t.Parallel()

	store := NewHandleStore()
	defer store.Stop()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-secret")
	store.Store(handle)

	value, err := store.Resolve(handle.ID)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if value != "sk-secret" {
		t.Error("wrong value resolved")
	}

	_, err = store.Resolve(handle.ID)
	if err != ErrHandleUsed {
		t.Error("second resolve should fail with ErrHandleUsed")
	}
}

func TestHandleStore_ResolveExpired(t *testing.T) {
	t.Parallel()

	store := NewHandleStore()
	defer store.Stop()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-secret")
	handle.ExpiresAt = time.Now().Add(-1 * time.Second)
	store.Store(handle)

	_, err := store.Resolve(handle.ID)
	if err != ErrHandleExpired {
		t.Error("resolve of expired handle should fail")
	}
}

func TestHandleStore_Revoke(t *testing.T) {
	t.Parallel()

	store := NewHandleStore()
	defer store.Stop()

	handle := NewCredentialHandle("openai", "agent-1", "librarian", "call-123", "sk-secret")
	store.Store(handle)

	if !store.Revoke(handle.ID) {
		t.Error("revoke should return true for existing handle")
	}

	_, err := store.Get(handle.ID)
	if err != ErrHandleNotFound {
		t.Error("revoked handle should not be found")
	}

	if store.Revoke("nonexistent") {
		t.Error("revoke should return false for nonexistent handle")
	}
}

func TestHandleStore_Count(t *testing.T) {
	t.Parallel()

	store := NewHandleStore()
	defer store.Stop()

	if store.Count() != 0 {
		t.Error("empty store should have count 0")
	}

	store.Store(NewCredentialHandle("openai", "a1", "l", "c1", "s1"))
	store.Store(NewCredentialHandle("anthropic", "a2", "l", "c2", "s2"))

	if store.Count() != 2 {
		t.Error("store should have count 2")
	}
}

func TestHandleStore_ActiveHandles(t *testing.T) {
	t.Parallel()

	store := NewHandleStore()
	defer store.Stop()

	h1 := NewCredentialHandle("openai", "a1", "l", "c1", "s1")
	h2 := NewCredentialHandle("anthropic", "a2", "l", "c2", "s2")
	h3 := NewCredentialHandle("google", "a3", "l", "c3", "s3")
	h3.ExpiresAt = time.Now().Add(-1 * time.Second)

	store.Store(h1)
	store.Store(h2)
	store.Store(h3)

	h1.Consume()

	active := store.ActiveHandles()

	if len(active) != 1 {
		t.Errorf("expected 1 active handle, got %d", len(active))
	}
}

func TestHandleStore_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	store := NewHandleStore()
	defer store.Stop()

	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)

		go func(n int) {
			defer wg.Done()
			h := NewCredentialHandle("openai", "agent", "lib", "call", "secret")
			store.Store(h)
		}(i)

		go func() {
			defer wg.Done()
			_ = store.Count()
		}()

		go func() {
			defer wg.Done()
			_ = store.ActiveHandles()
		}()
	}

	wg.Wait()
}

func TestGenerateHandleID_Unique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := generateHandleID()
		if seen[id] {
			t.Error("generated duplicate handle ID")
		}
		seen[id] = true
	}
}

func TestCredentialHandle_IsValid(t *testing.T) {
	t.Parallel()

	handle := NewCredentialHandle("openai", "a", "l", "c", "s")

	if !handle.IsValid() {
		t.Error("new handle should be valid")
	}

	handle.Used = true
	if handle.IsValid() {
		t.Error("used handle should not be valid")
	}

	handle.Used = false
	handle.ExpiresAt = time.Now().Add(-1 * time.Second)
	if handle.IsValid() {
		t.Error("expired handle should not be valid")
	}
}
