package academic

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestAcademicStartUsesConfiguredAgentIDForChannels(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}
	defer func() { _ = a.Stop() }()

	channels := a.Channels()
	if channels == nil {
		t.Fatal("academic channels not initialized")
	}
	if channels.Requests != guide.TopicRequests("academic", "academic-custom") {
		t.Fatalf("requests channel = %q, want %q", channels.Requests, guide.TopicRequests("academic", "academic-custom"))
	}
	if channels.Responses != guide.TopicResponses("academic", "academic-custom") {
		t.Fatalf("responses channel = %q, want %q", channels.Responses, guide.TopicResponses("academic", "academic-custom"))
	}
	if channels.Errors != guide.TopicErrors("academic", "academic-custom") {
		t.Fatalf("errors channel = %q, want %q", channels.Errors, guide.TopicErrors("academic", "academic-custom"))
	}
}
