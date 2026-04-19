package archivalist

import (
	"context"
	"testing"
)

func TestArchivalistGetRoutingInfoUsesConfiguredAgentID(t *testing.T) {
	a, err := New(context.Background(), Config{ID: "archivalist-custom", Factory: newTestFactory(t)})
	if err != nil {
		t.Fatalf("new archivalist: %v", err)
	}

	info := a.GetRoutingInfo()
	if info == nil {
		t.Fatal("routing info is nil")
	}
	if info.ID != "archivalist-custom" {
		t.Fatalf("routing info id = %q, want archivalist-custom", info.ID)
	}
	if info.Registration == nil {
		t.Fatal("registration is nil")
	}
	if info.Registration.ID != "archivalist-custom" {
		t.Fatalf("registration id = %q, want archivalist-custom", info.Registration.ID)
	}
}

func TestArchivalistRoutingInfoDefaultIDRemainsStable(t *testing.T) {
	info := ArchivalistRoutingInfo()
	if info == nil {
		t.Fatal("routing info is nil")
	}
	if info.ID != "archivalist" {
		t.Fatalf("routing info id = %q, want archivalist", info.ID)
	}
	if info.Registration == nil {
		t.Fatal("registration is nil")
	}
	if info.Registration.ID != "archivalist" {
		t.Fatalf("registration id = %q, want archivalist", info.Registration.ID)
	}
}
