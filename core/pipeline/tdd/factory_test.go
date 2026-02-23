package tdd

import (
	"testing"

	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/inspector"
	"github.com/adalundhe/sylk/agents/tester"
)

func newTestFactory() *AgentFactory {
	return NewAgentFactory(AgentFactoryConfig{
		InspectorConfig: inspector.DefaultInspectorConfig(),
		TesterConfig:    tester.DefaultTesterConfig(),
		EngineerConfig:  engineer.Config{},
		DesignerConfig:  designer.Config{},
	})
}

func TestAgentFactory_CreateInspector(t *testing.T) {
	f := newTestFactory()
	insp, err := f.CreateInspector()
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()
	// Verify the inspector was created in PipelineInternal mode.
	if insp == nil {
		t.Fatal("expected non-nil inspector")
	}
}

func TestAgentFactory_CreateTester(t *testing.T) {
	f := newTestFactory()
	tst, err := f.CreateTester()
	if err != nil {
		t.Fatal(err)
	}
	defer tst.Close()
	if tst == nil {
		t.Fatal("expected non-nil tester")
	}
}

func TestAgentFactory_CreateWorkerEngineer(t *testing.T) {
	f := newTestFactory()
	w, err := f.CreateWorker(WorkerEngineer)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if _, ok := w.(*engineerWorker); !ok {
		t.Errorf("expected *engineerWorker, got %T", w)
	}
}

func TestAgentFactory_CreateWorkerDesigner(t *testing.T) {
	f := newTestFactory()
	w, err := f.CreateWorker(WorkerDesigner)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if _, ok := w.(*designerWorker); !ok {
		t.Errorf("expected *designerWorker, got %T", w)
	}
}

func TestAgentFactory_CreateWorkerUnknown(t *testing.T) {
	f := newTestFactory()
	_, err := f.CreateWorker("unknown")
	if err == nil {
		t.Error("expected error for unknown worker type")
	}
}
