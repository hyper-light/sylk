package activation

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/handoff"
)

func TestCoolStore_StoreAndLoad(t *testing.T) {
	dir := t.TempDir()
	cs, err := NewCoolStore(dir, 10)
	if err != nil {
		t.Fatalf("NewCoolStore: %v", err)
	}

	spec := container.ContainerSpec{
		Name:      "engineer",
		AgentType: "engineer",
	}
	state := &handoff.ArchivableState{
		AgentID:   "agent-1",
		AgentType: "engineer",
		State:     map[string]string{"key": "value"},
		Timestamp: time.Now(),
	}

	if err := cs.Store("engineer", spec, state); err != nil {
		t.Fatalf("Store: %v", err)
	}

	entry, err := cs.Load("engineer")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if entry.Spec.AgentType != "engineer" {
		t.Fatalf("expected engineer, got %s", entry.Spec.AgentType)
	}
	if entry.State.AgentID != "agent-1" {
		t.Fatalf("expected agent-1, got %s", entry.State.AgentID)
	}
	if entry.State.State["key"] != "value" {
		t.Fatalf("expected value, got %s", entry.State.State["key"])
	}
}

func TestCoolStore_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	cs, err := NewCoolStore(dir, 10)
	if err != nil {
		t.Fatalf("NewCoolStore: %v", err)
	}

	_, err = cs.Load("nonexistent")
	if err != ErrCoolStoreNotFound {
		t.Fatalf("expected ErrCoolStoreNotFound, got %v", err)
	}
}

func TestCoolStore_Remove(t *testing.T) {
	dir := t.TempDir()
	cs, err := NewCoolStore(dir, 10)
	if err != nil {
		t.Fatalf("NewCoolStore: %v", err)
	}

	spec := container.ContainerSpec{Name: "x", AgentType: "x"}
	_ = cs.Store("x", spec, nil)

	if err := cs.Remove("x"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if cs.Contains("x") {
		t.Fatal("should not contain x after remove")
	}
}

func TestCoolStore_CapacityEnforced(t *testing.T) {
	dir := t.TempDir()
	cs, err := NewCoolStore(dir, 2)
	if err != nil {
		t.Fatalf("NewCoolStore: %v", err)
	}

	spec := container.ContainerSpec{Name: "a", AgentType: "a"}
	_ = cs.Store("a", spec, nil)
	spec.AgentType = "b"
	spec.Name = "b"
	_ = cs.Store("b", spec, nil)

	spec.AgentType = "c"
	spec.Name = "c"
	err = cs.Store("c", spec, nil)
	if err != ErrCoolStoreFull {
		t.Fatalf("expected ErrCoolStoreFull, got %v", err)
	}
}

func TestCoolStore_StoreNilState(t *testing.T) {
	dir := t.TempDir()
	cs, err := NewCoolStore(dir, 10)
	if err != nil {
		t.Fatalf("NewCoolStore: %v", err)
	}

	spec := container.ContainerSpec{Name: "x", AgentType: "x"}
	if err := cs.Store("x", spec, nil); err != nil {
		t.Fatalf("Store with nil state: %v", err)
	}

	entry, err := cs.Load("x")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if entry.State != nil {
		t.Fatal("expected nil state")
	}
}

func TestCoolStore_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	cs, err := NewCoolStore(dir, 2)
	if err != nil {
		t.Fatalf("NewCoolStore: %v", err)
	}

	spec := container.ContainerSpec{Name: "a", AgentType: "a"}
	_ = cs.Store("a", spec, nil)

	// Overwrite should succeed even at capacity.
	_ = cs.Store("b", container.ContainerSpec{Name: "b", AgentType: "b"}, nil)

	state := &handoff.ArchivableState{AgentID: "updated"}
	err = cs.Store("a", spec, state)
	if err != nil {
		t.Fatalf("overwrite should succeed: %v", err)
	}

	entry, _ := cs.Load("a")
	if entry.State.AgentID != "updated" {
		t.Fatal("expected updated state")
	}
}
