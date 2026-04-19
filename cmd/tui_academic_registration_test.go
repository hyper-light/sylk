package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/academic"
	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	csecurity "github.com/adalundhe/sylk/core/container/security"
	"github.com/adalundhe/sylk/core/handoff"
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

	factory, err := buildIdentityFactory(handoff.NewDescriptorRegistry(), "sess-academic-bootstrap")
	if err != nil {
		t.Fatalf("build identity factory: %v", err)
	}

	g, err := guide.NewWithClassifier(guide.NewRuleClassifierClient(), guide.Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "sess-academic-bootstrap",
		Factory:   factory,
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
		Factory:   factory,
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

	// Bound the pre-registration attempt with an explicit-cancel deadline.
	// RequestGuideRouteSync uses WithoutDeadlineCancellation internally, which
	// deliberately ignores parent DeadlineExceeded; only an explicit cancel
	// call propagates. A timer-driven cancel gives us that without waiting
	// for the inactivity timeout, which can be preempted by early stream
	// activity from the guide.
	preCtx, preCancel := context.WithCancel(context.Background())
	preTimer := time.AfterFunc(500*time.Millisecond, preCancel)
	_, err = agentshared.RequestGuideRouteSync(preCtx, agentshared.GuideRouteSyncRequest{
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
	preTimer.Stop()
	preCancel()
	if err == nil {
		t.Fatal("pre-registration-only sync route succeeded; want failure because academic guide channels are not subscribed yet")
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
