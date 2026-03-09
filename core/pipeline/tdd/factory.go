package tdd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	inspPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	pipelinetester "github.com/adalundhe/sylk/agents/tester/pipeline"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/providers"
)

// AgentFactory creates pipeline-scoped agent instances.
type AgentFactory struct {
	inspectorConfig inspShared.PipelineInspectorConfig
	testerConfig    shared.PipelineTesterConfig
	engineerConfig  engineer.Config
	designerConfig  designer.Config
	designerFactory func(context.Context, designer.Config) (*designer.Designer, error)
	logger          *slog.Logger
}

// AgentFactoryConfig holds the configs needed to build agents.
type AgentFactoryConfig struct {
	InspectorConfig inspShared.PipelineInspectorConfig
	TesterConfig    shared.PipelineTesterConfig
	EngineerConfig  engineer.Config
	DesignerConfig  designer.Config
	DesignerFactory func(context.Context, designer.Config) (*designer.Designer, error)
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
		designerFactory: cfg.DesignerFactory,
		logger:          logger,
	}
}

// CreateInspector creates a pipeline-scoped PipelineInspector.
// The worker type is used to select the correct system prompt for design-aware validation.
func (f *AgentFactory) CreateInspector(wt WorkerType) (*inspPipeline.PipelineInspector, error) {
	cfg := f.inspectorConfig
	insp, err := inspPipeline.New(cfg, nil)
	if err != nil {
		return nil, err
	}
	insp.SetWorkerType(string(wt))
	return insp, nil
}

// CreateTester creates a pipeline-scoped PipelineTester.
func (f *AgentFactory) CreateTester() (*pipelinetester.PipelineTester, error) {
	return pipelinetester.New(f.testerConfig, nil)
}

// CreateWorker creates a WorkerAgent adapter for the given worker type.
func (f *AgentFactory) CreateWorker(ctx context.Context, wt WorkerType) (WorkerAgent, error) {
	switch wt {
	case WorkerEngineer:
		eng, err := engineer.New(f.engineerConfig, nil)
		if err != nil {
			return nil, fmt.Errorf("create engineer: %w", err)
		}
		return &engineerWorker{eng: eng}, nil
	case WorkerDesigner:
		des, err := f.createDesigner(ctx)
		if err != nil {
			return nil, fmt.Errorf("create designer: %w", err)
		}
		return &designerWorker{des: des}, nil
	default:
		return nil, fmt.Errorf("unknown worker type: %s", wt)
	}
}

func (f *AgentFactory) createDesigner(ctx context.Context) (*designer.Designer, error) {
	if f.designerFactory != nil {
		return f.designerFactory(ctx, f.designerConfig)
	}
	googleCfg := providers.DefaultGoogleConfig()
	googleCfg.Model = string(providers.Gemini31Pro)
	googleCfg.MaxTokens = 16384
	provider, err := providers.NewGoogleProvider(ctx, googleCfg)
	if err != nil {
		return nil, fmt.Errorf("create designer google provider: %w", err)
	}
	return designer.New(f.designerConfig, provider)
}

// CreateCoWorkers creates worker agents for each co-tenant type.
// On partial failure, closes all successfully created workers before returning.
func (f *AgentFactory) CreateCoWorkers(ctx context.Context, types []WorkerType) ([]WorkerAgent, error) {
	workers := make([]WorkerAgent, 0, len(types))
	for _, wt := range types {
		w, err := f.CreateWorker(ctx, wt)
		if err != nil {
			for _, prev := range workers {
				prev.Close()
			}
			return nil, fmt.Errorf("create co-worker %s: %w", wt, err)
		}
		workers = append(workers, w)
	}
	return workers, nil
}

// TaskPromptSetter is implemented by worker adapters that accept a task prompt.
type TaskPromptSetter interface {
	SetTaskPrompt(prompt string)
}

// PriorOutputSetter is implemented by worker adapters that accept prior output context.
type PriorOutputSetter interface {
	SetPriorOutput(result *WorkerResult)
}

// engineerWorker adapts the Engineer agent to the WorkerAgent interface.
type engineerWorker struct {
	eng         *engineer.Engineer
	taskPrompt  string
	priorOutput *WorkerResult
}

func (w *engineerWorker) SetTaskPrompt(prompt string)         { w.taskPrompt = prompt }
func (w *engineerWorker) SetPriorOutput(result *WorkerResult) { w.priorOutput = result }

func (w *engineerWorker) Execute(ctx context.Context, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error) {
	prompt := buildEngineerPromptWithContext(w.taskPrompt, w.priorOutput, criteria, inspFb, testFb)
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
	des         *designer.Designer
	taskPrompt  string
	priorOutput *WorkerResult
}

func (w *designerWorker) SetTaskPrompt(prompt string)         { w.taskPrompt = prompt }
func (w *designerWorker) SetPriorOutput(result *WorkerResult) { w.priorOutput = result }

func (w *designerWorker) Execute(ctx context.Context, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error) {
	prompt := buildDesignerPromptWithContext(w.taskPrompt, w.priorOutput, criteria, inspFb, testFb)
	result, err := w.des.HandleRequest(ctx, prompt)
	if err != nil {
		return nil, err
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("designer returned unexpected result type")
	}
	response, _ := resultMap["response"].(string)
	return &WorkerResult{
		TaskResult: &engineer.TaskResult{
			TaskID:  criteria.TaskID,
			Success: true,
			Output:  response,
		},
	}, nil
}

func (w *designerWorker) Close() error {
	return w.des.Close()
}

func buildEngineerPrompt(criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) string {
	return buildEngineerPromptWithContext("", nil, criteria, inspFb, testFb)
}

func buildDesignerPrompt(criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) string {
	return buildDesignerPromptWithContext("", nil, criteria, inspFb, testFb)
}

func buildEngineerPromptWithContext(taskPrompt string, priorOutput *WorkerResult, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) string {
	var b strings.Builder
	if taskPrompt != "" {
		b.WriteString(taskPrompt)
		b.WriteString("\n\n")
	}
	writePriorOutputSection(&b, priorOutput)
	fmt.Fprintf(&b, "Implement task %s according to the defined criteria.", criteria.TaskID)
	writeFeedbackSuffix(&b, inspFb, testFb)
	return b.String()
}

func buildDesignerPromptWithContext(taskPrompt string, priorOutput *WorkerResult, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) string {
	var b strings.Builder
	if taskPrompt != "" {
		b.WriteString(taskPrompt)
		b.WriteString("\n\n")
	}
	writePriorOutputSection(&b, priorOutput)
	fmt.Fprintf(&b, "Design components for task %s according to the defined criteria.", criteria.TaskID)
	writeFeedbackSuffix(&b, inspFb, testFb)
	return b.String()
}

func writePriorOutputSection(b *strings.Builder, priorOutput *WorkerResult) {
	if priorOutput == nil || len(priorOutput.ChangedFiles) == 0 {
		return
	}
	b.WriteString("## Prior Agent Output\nThe primary agent has already modified these files:\n")
	for _, f := range priorOutput.ChangedFiles {
		fmt.Fprintf(b, "- %s\n", f)
	}
	b.WriteString("\nBuild upon their work. Do not re-implement what they have already done.\n\n")
}

func writeFeedbackSuffix(b *strings.Builder, inspFb *InspectorFeedback, testFb *TesterFeedback) {
	if inspFb != nil && inspFb.Feedback != nil && !inspFb.Feedback.Passed {
		fmt.Fprintf(b, " Inspector feedback (loop %d): %d issues found.", inspFb.Feedback.Loop, len(inspFb.Feedback.Issues))
	}
	if testFb != nil && len(testFb.FailedTests) > 0 {
		fmt.Fprintf(b, " %d tests currently failing.", len(testFb.FailedTests))
	}
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
