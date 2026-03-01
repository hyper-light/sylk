package tdd

import (
	"testing"

	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/tester/shared"
)

func newTestFactory() *AgentFactory {
	return NewAgentFactory(AgentFactoryConfig{
		InspectorConfig: inspShared.DefaultPipelineInspectorConfig(),
		TesterConfig:    shared.PipelineTesterConfig{},
		EngineerConfig:  engineer.Config{},
		DesignerConfig:  designer.Config{},
	})
}

func TestAgentFactory_CreateInspector(t *testing.T) {
	f := newTestFactory()
	insp, err := f.CreateInspector(WorkerEngineer)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()
	// Verify the pipeline inspector was created successfully.
	if insp == nil {
		t.Fatal("expected non-nil inspector")
	}
}

func TestAgentFactory_CreateInspectorDesigner(t *testing.T) {
	f := newTestFactory()
	insp, err := f.CreateInspector(WorkerDesigner)
	if err != nil {
		t.Fatal(err)
	}
	defer insp.Close()
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
	w, err := f.CreateWorker(t.Context(),WorkerEngineer)
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
	w, err := f.CreateWorker(t.Context(),WorkerDesigner)
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
	_, err := f.CreateWorker(t.Context(),"unknown")
	if err == nil {
		t.Error("expected error for unknown worker type")
	}
}

func TestAgentFactory_CreateCoWorkers(t *testing.T) {
	f := newTestFactory()
	workers, err := f.CreateCoWorkers(t.Context(),[]WorkerType{WorkerEngineer})
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 {
		t.Fatalf("got %d workers, want 1", len(workers))
	}
	for _, w := range workers {
		w.Close()
	}
}

func TestAgentFactory_CreateCoWorkers_PartialFailure(t *testing.T) {
	f := newTestFactory()
	// Second type is invalid — first worker should be closed on failure.
	_, err := f.CreateCoWorkers(t.Context(),[]WorkerType{WorkerEngineer, "invalid"})
	if err == nil {
		t.Error("expected error for invalid co-worker type")
	}
}

func TestAgentFactory_CreateCoWorkers_Empty(t *testing.T) {
	f := newTestFactory()
	workers, err := f.CreateCoWorkers(t.Context(),nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 0 {
		t.Errorf("got %d workers, want 0", len(workers))
	}
}

func TestTaskPromptSetter(t *testing.T) {
	f := newTestFactory()
	w, err := f.CreateWorker(t.Context(),WorkerEngineer)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	setter, ok := w.(TaskPromptSetter)
	if !ok {
		t.Fatal("engineerWorker does not implement TaskPromptSetter")
	}
	setter.SetTaskPrompt("test prompt")

	ew := w.(*engineerWorker)
	if ew.taskPrompt != "test prompt" {
		t.Errorf("got %q, want %q", ew.taskPrompt, "test prompt")
	}
}

func TestPriorOutputSetter(t *testing.T) {
	f := newTestFactory()
	w, err := f.CreateWorker(t.Context(),WorkerEngineer)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	setter, ok := w.(PriorOutputSetter)
	if !ok {
		t.Fatal("engineerWorker does not implement PriorOutputSetter")
	}
	prior := &WorkerResult{ChangedFiles: []string{"test.go"}}
	setter.SetPriorOutput(prior)

	ew := w.(*engineerWorker)
	if ew.priorOutput == nil || len(ew.priorOutput.ChangedFiles) != 1 {
		t.Errorf("priorOutput not set correctly")
	}
}

func TestBuildEngineerPromptWithContext(t *testing.T) {
	criteria := &inspShared.InspectorCriteria{TaskID: "t1"}
	prompt := buildEngineerPromptWithContext("Do the thing", nil, criteria, nil, nil)
	if !contains(prompt, "Do the thing") {
		t.Error("expected task prompt in output")
	}
	if !contains(prompt, "t1") {
		t.Error("expected task ID in output")
	}
}

func TestBuildEngineerPromptWithContext_PriorOutput(t *testing.T) {
	criteria := &inspShared.InspectorCriteria{TaskID: "t1"}
	prior := &WorkerResult{ChangedFiles: []string{"main.go", "util.go"}}
	prompt := buildEngineerPromptWithContext("", prior, criteria, nil, nil)
	if !contains(prompt, "Prior Agent Output") {
		t.Error("expected prior output section")
	}
	if !contains(prompt, "main.go") {
		t.Error("expected file name in prior output section")
	}
}

func TestBuildDesignerPromptWithContext(t *testing.T) {
	criteria := &inspShared.InspectorCriteria{TaskID: "t2"}
	prompt := buildDesignerPromptWithContext("Design it", nil, criteria, nil, nil)
	if !contains(prompt, "Design it") {
		t.Error("expected task prompt in output")
	}
	if !contains(prompt, "t2") {
		t.Error("expected task ID in output")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
