package tdd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/inspector"
	"github.com/adalundhe/sylk/agents/tester"
)

// AgentFactory creates pipeline-scoped agent instances.
type AgentFactory struct {
	inspectorConfig inspector.InspectorConfig
	testerConfig    tester.TesterConfig
	engineerConfig  engineer.Config
	designerConfig  designer.Config
	logger          *slog.Logger
}

// AgentFactoryConfig holds the configs needed to build agents.
type AgentFactoryConfig struct {
	InspectorConfig inspector.InspectorConfig
	TesterConfig    tester.TesterConfig
	EngineerConfig  engineer.Config
	DesignerConfig  designer.Config
	Logger          *slog.Logger
}

// NewAgentFactory creates a factory with the given agent configurations.
func NewAgentFactory(cfg AgentFactoryConfig) *AgentFactory {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentFactory{
		inspectorConfig: cfg.InspectorConfig,
		testerConfig:    cfg.TesterConfig,
		engineerConfig:  cfg.EngineerConfig,
		designerConfig:  cfg.DesignerConfig,
		logger:          logger,
	}
}

// CreateInspector creates a pipeline-scoped Inspector in PipelineInternal mode.
func (f *AgentFactory) CreateInspector() (*inspector.Inspector, error) {
	cfg := f.inspectorConfig
	cfg.Mode = inspector.PipelineInternal
	return inspector.New(cfg)
}

// CreateTester creates a pipeline-scoped Tester.
func (f *AgentFactory) CreateTester() (*tester.Tester, error) {
	return tester.New(f.testerConfig)
}

// CreateWorker creates a WorkerAgent adapter for the given worker type.
func (f *AgentFactory) CreateWorker(wt WorkerType) (WorkerAgent, error) {
	switch wt {
	case WorkerEngineer:
		eng, err := engineer.New(f.engineerConfig)
		if err != nil {
			return nil, fmt.Errorf("create engineer: %w", err)
		}
		return &engineerWorker{eng: eng}, nil
	case WorkerDesigner:
		des, err := designer.New(f.designerConfig)
		if err != nil {
			return nil, fmt.Errorf("create designer: %w", err)
		}
		return &designerWorker{des: des}, nil
	default:
		return nil, fmt.Errorf("unknown worker type: %s", wt)
	}
}

// engineerWorker adapts the Engineer agent to the WorkerAgent interface.
type engineerWorker struct {
	eng *engineer.Engineer
}

func (w *engineerWorker) Execute(ctx context.Context, criteria *inspector.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error) {
	prompt := buildEngineerPrompt(criteria, inspFb, testFb)
	req := &engineer.EngineerRequest{
		Intent:    engineer.IntentComplete,
		TaskID:    criteria.TaskID,
		Prompt:    prompt,
		SessionID: "",
	}
	resp, err := w.eng.Handle(ctx, req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("engineer failed: %s", resp.Error)
	}
	return taskResultToWorkerResult(resp.Result), nil
}

func (w *engineerWorker) Close() error {
	return w.eng.Close()
}

// designerWorker adapts the Designer agent to the WorkerAgent interface.
type designerWorker struct {
	des *designer.Designer
}

func (w *designerWorker) Execute(ctx context.Context, criteria *inspector.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error) {
	prompt := buildDesignerPrompt(criteria, inspFb, testFb)
	req := &designer.DesignerRequest{
		Intent:    designer.IntentDesignComponent,
		TaskID:    criteria.TaskID,
		Prompt:    prompt,
		SessionID: "",
	}
	resp, err := w.des.Handle(ctx, req)
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("designer failed: %s", resp.Error)
	}
	return designResultToWorkerResult(resp.Result), nil
}

func (w *designerWorker) Close() error {
	return w.des.Close()
}

func buildEngineerPrompt(criteria *inspector.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) string {
	prompt := fmt.Sprintf("Implement task %s according to the defined criteria.", criteria.TaskID)
	if inspFb != nil && inspFb.Feedback != nil && !inspFb.Feedback.Passed {
		prompt += fmt.Sprintf(" Inspector feedback (loop %d): %d issues found.", inspFb.Feedback.Loop, len(inspFb.Feedback.Issues))
	}
	if testFb != nil && len(testFb.FailedTests) > 0 {
		prompt += fmt.Sprintf(" %d tests currently failing.", len(testFb.FailedTests))
	}
	return prompt
}

func buildDesignerPrompt(criteria *inspector.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) string {
	prompt := fmt.Sprintf("Design components for task %s according to the defined criteria.", criteria.TaskID)
	if inspFb != nil && inspFb.Feedback != nil && !inspFb.Feedback.Passed {
		prompt += fmt.Sprintf(" Inspector feedback (loop %d): %d issues found.", inspFb.Feedback.Loop, len(inspFb.Feedback.Issues))
	}
	if testFb != nil && len(testFb.FailedTests) > 0 {
		prompt += fmt.Sprintf(" %d tests currently failing.", len(testFb.FailedTests))
	}
	return prompt
}

func taskResultToWorkerResult(tr *engineer.TaskResult) *WorkerResult {
	if tr == nil {
		return &WorkerResult{}
	}
	files := make([]string, len(tr.FilesChanged))
	for i, fc := range tr.FilesChanged {
		files[i] = fc.Path
	}
	return &WorkerResult{
		TaskResult:   tr,
		ChangedFiles: files,
	}
}

func designResultToWorkerResult(dr *designer.DesignResult) *WorkerResult {
	if dr == nil {
		return &WorkerResult{}
	}
	files := make([]string, len(dr.FilesChanged))
	for i, fc := range dr.FilesChanged {
		files[i] = fc.Path
	}
	return &WorkerResult{
		TaskResult: &engineer.TaskResult{
			TaskID:  dr.TaskID,
			Success: dr.Success,
			Output:  dr.Output,
			Errors:  dr.Errors,
		},
		ChangedFiles: files,
	}
}
