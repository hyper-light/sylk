package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/academic"
	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	csecurity "github.com/adalundhe/sylk/core/container/security"
	"github.com/adalundhe/sylk/core/providers"
)

type fixedAcademicConsultProvider struct{}

func (p *fixedAcademicConsultProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{
		Content: "Use the project-scoped package manager to install the missing test tool, then rerun the validation command.",
	}, nil
}

func newAcademicBootstrapTestContainer(agent *academic.Academic) *container.Container {
	scope := concurrency.NewGoroutineScope(context.Background(), "academic-bootstrap-test", nil)
	secCtx := csecurity.NewSecurityContext(csecurity.SecurityContextConfig{
		ContainerID: "academic-bootstrap-test",
		AgentID:     agent.AgentID(),
		Role:        "worker",
	})
	return container.NewContainer(container.ContainerConfig{
		ID: container.ContainerID("academic-bootstrap-test"),
		Spec: container.ContainerSpec{
			Name:      "academic-bootstrap-test",
			AgentType: "academic",
		},
		Scope:  scope,
		SecCtx: secCtx,
		Agent:  agent,
	})
}

func TestRegisterPhase4AcademicCompletesGuideReturnPath(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := guide.NewWithClassifier(guide.NewRuleClassifierClient(), guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "sess-academic-bootstrap",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}
	if err := g.PreRegister(academic.AcademicRoutingInfo("academic")); err != nil {
		t.Fatalf("pre-register academic: %v", err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("guide start: %v", err)
	}
	defer func() { _ = g.Stop() }()
	g.MarkAgentReady("academic")

	academicAgent, err := academic.New(academic.Config{
		ID:        "academic",
		SessionID: "sess-academic-bootstrap",
	}, &fixedAcademicConsultProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer academicAgent.Close()
	if err := academicAgent.Start(bus); err != nil {
		t.Fatalf("academic start: %v", err)
	}

	phase1 := &bootstrapPhase1{
		containerReg: container.NewContainerRegistry(),
	}
	if err := phase1.containerReg.Register(newAcademicBootstrapTestContainer(academicAgent)); err != nil {
		t.Fatalf("register academic container: %v", err)
	}
	phase3 := bootstrapPhase3{guide: g}

	if channels := g.GetAgentChannels("academic"); channels != nil {
		t.Fatalf("academic channels before full registration = %#v, want nil", channels)
	}

	_, err = agentshared.RequestGuideRouteSync(context.Background(), agentshared.GuideRouteSyncRequest{
		Bus:               bus,
		ResponseTopic:     guide.TopicResponses("tester-sync", "tester-sync"),
		InactivityTimeout: 40 * time.Millisecond,
		Request: &guide.RouteRequest{
			SourceAgentID:   "tester-sync",
			SourceAgentName: "Tester Sync",
			TargetAgentID:   "academic",
			ExplicitTarget:  true,
			SessionID:       "sess-academic-bootstrap",
			Input:           "Research the missing test tool and return a concise install recommendation.",
		},
	})
	if err == nil || !strings.Contains(err.Error(), `guide route to "academic" timed out`) {
		t.Fatalf("pre-registration-only sync route error = %v, want academic timeout", err)
	}

	if err := registerPhase4Academic(phase1, phase3); err != nil {
		t.Fatalf("registerPhase4Academic: %v", err)
	}
	if channels := g.GetAgentChannels("academic"); channels == nil {
		t.Fatal("academic channels after full registration = nil, want subscribed channels")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := agentshared.RequestGuideRouteSync(ctx, agentshared.GuideRouteSyncRequest{
		Bus:           bus,
		ResponseTopic: guide.TopicResponses("tester-sync", "tester-sync"),
		Request: &guide.RouteRequest{
			SourceAgentID:   "tester-sync",
			SourceAgentName: "Tester Sync",
			TargetAgentID:   "academic",
			ExplicitTarget:  true,
			SessionID:       "sess-academic-bootstrap",
			Input:           "Research the missing test tool and return a concise install recommendation.",
		},
	})
	if err != nil {
		t.Fatalf("post-registration sync route: %v", err)
	}

	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		t.Fatalf("route response missing: %#v", msg)
	}
	if !resp.Success {
		t.Fatalf("route response success = false, error = %q", resp.Error)
	}
	if resp.RespondingAgentID != "academic" {
		t.Fatalf("responding_agent_id = %q, want %q", resp.RespondingAgentID, "academic")
	}
}
