package tdd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/guide"
	inspPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	pipelinetester "github.com/adalundhe/sylk/agents/tester/pipeline"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/versioning"
)

// AgentFactory creates pipeline-scoped agent instances.
type AgentFactory struct {
	inspectorConfig  inspShared.PipelineInspectorConfig
	testerConfig     shared.PipelineTesterConfig
	engineerConfig   engineer.Config
	designerConfig   designer.Config
	inspectorFactory func(context.Context, inspShared.PipelineInspectorConfig) (*inspPipeline.PipelineInspector, error)
	testerFactory    func(context.Context, shared.PipelineTesterConfig) (*pipelinetester.PipelineTester, error)
	engineerFactory  func(context.Context, engineer.Config) (*engineer.Engineer, error)
	designerFactory  func(context.Context, designer.Config) (*designer.Designer, error)
	logger           *slog.Logger
}

// AgentFactoryConfig holds the configs needed to build agents.
type AgentFactoryConfig struct {
	InspectorConfig  inspShared.PipelineInspectorConfig
	TesterConfig     shared.PipelineTesterConfig
	EngineerConfig   engineer.Config
	DesignerConfig   designer.Config
	InspectorFactory func(context.Context, inspShared.PipelineInspectorConfig) (*inspPipeline.PipelineInspector, error)
	TesterFactory    func(context.Context, shared.PipelineTesterConfig) (*pipelinetester.PipelineTester, error)
	EngineerFactory  func(context.Context, engineer.Config) (*engineer.Engineer, error)
	DesignerFactory  func(context.Context, designer.Config) (*designer.Designer, error)
	Logger           *slog.Logger
}

// NewAgentFactory creates a factory with the given agent configurations.
func NewAgentFactory(cfg AgentFactoryConfig) *AgentFactory {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentFactory{
		inspectorConfig:  cfg.InspectorConfig,
		testerConfig:     cfg.TesterConfig,
		engineerConfig:   cfg.EngineerConfig,
		designerConfig:   cfg.DesignerConfig,
		inspectorFactory: cfg.InspectorFactory,
		testerFactory:    cfg.TesterFactory,
		engineerFactory:  cfg.EngineerFactory,
		designerFactory:  cfg.DesignerFactory,
		logger:           logger,
	}
}

// CreateInspector creates a pipeline-scoped PipelineInspector.
// The worker type is used to select the correct system prompt for design-aware validation.
func (f *AgentFactory) CreateInspector(ctx context.Context, cfg PipelineConfig) (*inspPipeline.PipelineInspector, error) {
	agentCfg := f.inspectorConfig
	agentCfg.SessionID = cfg.SessionID
	agentCfg.AgentID = pipelineAgentID(cfg.TaskID, "inspector-pipeline")
	var (
		insp *inspPipeline.PipelineInspector
		err  error
	)
	if f.inspectorFactory != nil {
		insp, err = f.inspectorFactory(ctx, agentCfg)
	} else {
		insp, err = inspPipeline.New(agentCfg, nil)
	}
	if err != nil {
		return nil, err
	}
	insp.SetWorkerType(string(cfg.WorkerType))
	if err := applyPipelineRuntime(insp, cfg); err != nil {
		_ = insp.Close()
		return nil, err
	}
	return insp, nil
}

// CreateTester creates a pipeline-scoped PipelineTester.
func (f *AgentFactory) CreateTester(ctx context.Context, cfg PipelineConfig) (*pipelinetester.PipelineTester, error) {
	agentCfg := f.testerConfig
	agentCfg.SessionID = cfg.SessionID
	agentCfg.AgentID = pipelineAgentID(cfg.TaskID, "tester-pipeline")
	var (
		testerAgent *pipelinetester.PipelineTester
		err         error
	)
	if f.testerFactory != nil {
		testerAgent, err = f.testerFactory(ctx, agentCfg)
	} else {
		testerAgent, err = pipelinetester.New(agentCfg, nil)
	}
	if err != nil {
		return nil, err
	}
	if err := applyPipelineRuntime(testerAgent, cfg); err != nil {
		_ = testerAgent.Close()
		return nil, err
	}
	return testerAgent, nil
}

// CreateWorker creates a WorkerAgent adapter for the given worker type.
func (f *AgentFactory) CreateWorker(ctx context.Context, wt WorkerType, cfg PipelineConfig) (WorkerAgent, error) {
	switch wt {
	case WorkerEngineer:
		agentCfg := f.engineerConfig
		agentCfg.SessionID = cfg.SessionID
		agentCfg.ID = pipelineAgentID(cfg.TaskID, string(wt))
		var (
			eng *engineer.Engineer
			err error
		)
		if f.engineerFactory != nil {
			eng, err = f.engineerFactory(ctx, agentCfg)
		} else {
			eng, err = engineer.New(agentCfg, nil)
		}
		if err != nil {
			return nil, fmt.Errorf("create engineer: %w", err)
		}
		if err := applyPipelineRuntime(eng, cfg); err != nil {
			_ = eng.Close()
			return nil, err
		}
		return &engineerWorker{eng: eng}, nil
	case WorkerDesigner:
		agentCfg := f.designerConfig
		agentCfg.SessionID = cfg.SessionID
		agentCfg.ID = pipelineAgentID(cfg.TaskID, string(wt))
		des, err := f.createDesigner(ctx, agentCfg)
		if err != nil {
			return nil, fmt.Errorf("create designer: %w", err)
		}
		if err := applyPipelineRuntime(des, cfg); err != nil {
			_ = des.Close()
			return nil, err
		}
		return &designerWorker{des: des}, nil
	default:
		return nil, fmt.Errorf("unknown worker type: %s", wt)
	}
}

func (f *AgentFactory) createDesigner(ctx context.Context, cfg designer.Config) (*designer.Designer, error) {
	if f.designerFactory != nil {
		return f.designerFactory(ctx, cfg)
	}
	googleCfg := providers.DefaultGoogleConfig()
	googleCfg.Model = string(providers.Gemini31Pro)
	googleCfg.MaxTokens = 16384
	provider, err := providers.NewGoogleProvider(ctx, googleCfg)
	if err != nil {
		return nil, fmt.Errorf("create designer google provider: %w", err)
	}
	return designer.New(cfg, provider)
}

// CreateCoWorkers creates execute-stage peer workers for each co-tenant type.
// On partial failure, closes all successfully created workers before returning.
func (f *AgentFactory) CreateCoWorkers(ctx context.Context, types []WorkerType, cfg PipelineConfig) ([]WorkerAgent, error) {
	workers := make([]WorkerAgent, 0, len(types))
	for _, wt := range types {
		w, err := f.CreateWorker(ctx, wt, cfg)
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

func pipelineAgentID(taskID, role string) string {
	taskID = strings.TrimSpace(taskID)
	role = strings.TrimSpace(role)
	if taskID == "" || role == "" {
		return ""
	}
	return taskID + "-" + role
}

func applyPipelineRuntime(agent any, cfg PipelineConfig) error {
	svfs := cfg.SessionVFS
	if svfs == nil || strings.TrimSpace(cfg.TaskID) == "" {
		return nil
	}

	workingDir := strings.TrimSpace(cfg.WorkingDir)
	if workingDir == "" {
		workingDir = svfs.WorkingDir()
	}
	if _, err := svfs.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: cfg.TaskID,
		SessionID:  versioning.SessionID(cfg.SessionID),
		WorkingDir: workingDir,
		AgentRole:  string(cfg.WorkerType),
		Files:      append([]string(nil), cfg.Files...),
	}); err != nil {
		return fmt.Errorf("begin session pipeline %s: %w", cfg.TaskID, err)
	}

	if setter, ok := agent.(interface{ SetFileAccess(versioning.FileAccess) }); ok {
		fileAccess, err := svfs.PipelineFileAccess(cfg.TaskID)
		if err != nil {
			return fmt.Errorf("pipeline file access for %s: %w", cfg.TaskID, err)
		}
		setter.SetFileAccess(fileAccess)
	}
	if setter, ok := agent.(interface {
		SetWorkspaceViews(versioning.WorkspaceViewAccess)
	}); ok {
		setter.SetWorkspaceViews(versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:       versioning.WorkspaceViewPipeline,
			DefaultPipelineID: cfg.TaskID,
			DefaultSessionID:  cfg.SessionID,
			WorkingDir:        workingDir,
			Session:           svfs,
			DiskFallback:      versioning.NewDiskFileAccess(workingDir, true),
		}))
	}
	return nil
}

// TaskPromptSetter is implemented by worker adapters that accept a task prompt.
type TaskPromptSetter interface {
	SetTaskPrompt(prompt string)
}

// PriorOutputSetter is implemented by worker adapters that accept prior output context.
type PriorOutputSetter interface {
	SetPriorOutput(result *WorkerResult)
}

// PipelineTaskSetter is implemented by worker adapters that accept the current
// structured pipeline task payload.
type PipelineTaskSetter interface {
	SetPipelineTask(task *agentShared.PipelineTaskInput)
}

// engineerWorker adapts the Engineer agent to the WorkerAgent interface.
type engineerWorker struct {
	eng          *engineer.Engineer
	taskPrompt   string
	priorOutput  *WorkerResult
	pipelineTask *agentShared.PipelineTaskInput
}

func (w *engineerWorker) SetTaskPrompt(prompt string)         { w.taskPrompt = prompt }
func (w *engineerWorker) SetPriorOutput(result *WorkerResult) { w.priorOutput = result }
func (w *engineerWorker) SetPipelineTask(task *agentShared.PipelineTaskInput) {
	w.pipelineTask = task
}

func (w *engineerWorker) Execute(ctx context.Context, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error) {
	prompt := buildEngineerPromptWithContext(w.taskPrompt, w.priorOutput, criteria, inspFb, testFb)
	task := w.pipelineTask
	if task == nil {
		task = &agentShared.PipelineTaskInput{
			TaskID:    criteria.TaskID,
			AgentType: string(WorkerEngineer),
			Prompt:    prompt,
			Context:   map[string]any{"agent_type": string(WorkerEngineer)},
		}
	}
	if strings.TrimSpace(task.Prompt) == "" {
		task.Prompt = prompt
	}
	if bus := w.eng.Bus(); bus != nil {
		payload, err := json.Marshal(task)
		if err != nil {
			return nil, fmt.Errorf("encode engineer pipeline task: %w", err)
		}
		resp, err := requestPipelineAgentTurn(ctx, bus, w.eng.ID(), task, payload)
		if err != nil {
			return nil, err
		}
		var result engineer.EngineerResponse
		if err := decodeRouteResponseData(resp.Data, &result); err != nil {
			return nil, fmt.Errorf("decode engineer route response: %w", err)
		}
		if !result.Success || result.Result == nil {
			return nil, fmt.Errorf("engineer failed: %s", result.Error)
		}
		return taskResultToWorkerResult(result.Result), nil
	}

	req := &engineer.EngineerRequest{
		Intent:       engineer.IntentComplete,
		TaskID:       criteria.TaskID,
		Prompt:       task.Prompt,
		SessionID:    "",
		PipelineTask: task,
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
	des          *designer.Designer
	taskPrompt   string
	priorOutput  *WorkerResult
	pipelineTask *agentShared.PipelineTaskInput
}

func (w *designerWorker) SetTaskPrompt(prompt string)         { w.taskPrompt = prompt }
func (w *designerWorker) SetPriorOutput(result *WorkerResult) { w.priorOutput = result }
func (w *designerWorker) SetPipelineTask(task *agentShared.PipelineTaskInput) {
	w.pipelineTask = task
}

func (w *designerWorker) Execute(ctx context.Context, criteria *inspShared.InspectorCriteria, inspFb *InspectorFeedback, testFb *TesterFeedback) (*WorkerResult, error) {
	input := w.pipelineTask
	if input == nil {
		input = &agentShared.PipelineTaskInput{
			TaskID:    criteria.TaskID,
			AgentType: "designer",
			Prompt:    buildDesignerPromptWithContext(w.taskPrompt, w.priorOutput, criteria, inspFb, testFb),
			Context:   map[string]any{"agent_type": "designer"},
		}
	}
	if strings.TrimSpace(input.Prompt) == "" {
		input.Prompt = buildDesignerPromptWithContext(w.taskPrompt, w.priorOutput, criteria, inspFb, testFb)
	}
	if bus := w.des.Bus(); bus != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("encode designer pipeline task: %w", err)
		}
		resp, err := requestPipelineAgentTurn(ctx, bus, w.des.ID(), input, payload)
		if err != nil {
			return nil, err
		}
		var result struct {
			Response string `json:"response"`
		}
		if err := decodeRouteResponseData(resp.Data, &result); err != nil {
			return nil, fmt.Errorf("decode designer route response: %w", err)
		}
		return &WorkerResult{
			TaskResult: &engineer.TaskResult{
				TaskID:  criteria.TaskID,
				Success: true,
				Output:  result.Response,
			},
		}, nil
	}

	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode designer pipeline task: %w", err)
	}
	result, err := w.des.HandleRequest(ctx, string(payload))
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

func requestPipelineAgentTurn(
	ctx context.Context,
	bus guide.EventBus,
	targetAgentID string,
	task *agentShared.PipelineTaskInput,
	payload []byte,
) (*guide.RouteResponse, error) {
	if bus == nil {
		return nil, fmt.Errorf("pipeline agent bus is unavailable")
	}
	if task == nil {
		return nil, fmt.Errorf("pipeline task is required")
	}
	msg, err := agentShared.RequestGuideRouteSync(ctx, agentShared.GuideRouteSyncRequest{
		Bus:           bus,
		ResponseTopic: guide.TopicResponses("tui", "tui"),
		Request: &guide.RouteRequest{
			Input:           string(payload),
			TargetAgentID:   targetAgentID,
			ExplicitTarget:  true,
			SourceAgentID:   "tui",
			SourceAgentName: "pipeline",
			SessionID:       strings.TrimSpace(task.SessionID),
			Metadata: map[string]any{
				"task_id":   strings.TrimSpace(task.TaskID),
				"task_slug": taskContextValue(task.Context, "task_slug"),
				"task_name": taskContextValue(task.Context, "task_name"),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if errText, ok := msg.GetError(); ok && strings.TrimSpace(errText) != "" {
		return nil, fmt.Errorf("%s", errText)
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		return nil, fmt.Errorf("pipeline agent %s returned an invalid response", targetAgentID)
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

func decodeRouteResponseData(data any, out any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, out)
}

func taskContextValue(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx[key].(string)
	return strings.TrimSpace(value)
}
