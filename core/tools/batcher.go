package tools

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBatchWindow  = 100 * time.Millisecond
	DefaultMaxBatchSize = 50
)

type BatchConfig struct {
	BatchCapable bool
	MaxFiles     int
	Separator    string
	BatchWindow  time.Duration
}

func DefaultBatchConfig() BatchConfig {
	return BatchConfig{
		BatchCapable: true,
		MaxFiles:     DefaultMaxBatchSize,
		Separator:    " ",
		BatchWindow:  DefaultBatchWindow,
	}
}

var DefaultBatchableTools = map[string]BatchConfig{
	"eslint":   {BatchCapable: true, MaxFiles: 50, Separator: " ", BatchWindow: DefaultBatchWindow},
	"prettier": {BatchCapable: true, MaxFiles: 50, Separator: " ", BatchWindow: DefaultBatchWindow},
	"go vet":   {BatchCapable: true, MaxFiles: 100, Separator: " ", BatchWindow: DefaultBatchWindow},
	"go build": {BatchCapable: true, MaxFiles: 100, Separator: " ", BatchWindow: DefaultBatchWindow},
}

type BatcherConfig struct {
	DefaultWindow  time.Duration
	ToolConfigs    map[string]BatchConfig
	FallbackOnFail bool
}

func DefaultBatcherConfig() BatcherConfig {
	return BatcherConfig{
		DefaultWindow:  DefaultBatchWindow,
		ToolConfigs:    copyToolConfigs(DefaultBatchableTools),
		FallbackOnFail: true,
	}
}

func copyToolConfigs(src map[string]BatchConfig) map[string]BatchConfig {
	dst := make(map[string]BatchConfig, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

type PendingInvocation struct {
	Invocation ToolInvocation
	ResultChan chan *BatchedResult
	AddedAt    time.Time
}

type BatchedResult struct {
	Result *ToolResult
	Error  error
}

type ToolBatcher struct {
	config   BatcherConfig
	executor *ToolExecutor

	mu          sync.Mutex
	pending     map[string][]*PendingInvocation
	batchTimers map[string]*time.Timer
	closed      bool
}

func NewToolBatcher(cfg BatcherConfig, executor *ToolExecutor) *ToolBatcher {
	cfg = normalizeBatcherConfig(cfg)

	return &ToolBatcher{
		config:      cfg,
		executor:    executor,
		pending:     make(map[string][]*PendingInvocation),
		batchTimers: make(map[string]*time.Timer),
	}
}

func normalizeBatcherConfig(cfg BatcherConfig) BatcherConfig {
	if cfg.DefaultWindow == 0 {
		cfg.DefaultWindow = DefaultBatchWindow
	}
	if cfg.ToolConfigs == nil {
		cfg.ToolConfigs = copyToolConfigs(DefaultBatchableTools)
	}
	return cfg
}

func (b *ToolBatcher) Submit(ctx context.Context, inv ToolInvocation) (*ToolResult, error) {
	if !b.isBatchable(inv.Command) {
		return b.executor.Execute(ctx, inv)
	}

	return b.submitToBatch(ctx, inv)
}

func (b *ToolBatcher) isBatchable(command string) bool {
	cfg, exists := b.config.ToolConfigs[command]
	return exists && cfg.BatchCapable
}

func (b *ToolBatcher) submitToBatch(ctx context.Context, inv ToolInvocation) (*ToolResult, error) {
	resultChan := make(chan *BatchedResult, 1)

	pending := &PendingInvocation{
		Invocation: inv,
		ResultChan: resultChan,
		AddedAt:    time.Now(),
	}

	b.addToPending(inv.Command, pending)

	return b.waitForResult(ctx, resultChan)
}

func (b *ToolBatcher) addToPending(command string, pending *PendingInvocation) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		pending.ResultChan <- &BatchedResult{Error: ErrExecutorClosed}
		return
	}

	b.pending[command] = append(b.pending[command], pending)
	b.scheduleFlushIfNeeded(command)
}

func (b *ToolBatcher) scheduleFlushIfNeeded(command string) {
	if _, exists := b.batchTimers[command]; exists {
		return
	}

	window := b.getBatchWindow(command)
	timer := time.AfterFunc(window, func() {
		b.flushBatch(command)
	})
	b.batchTimers[command] = timer
}

func (b *ToolBatcher) getBatchWindow(command string) time.Duration {
	if cfg, ok := b.config.ToolConfigs[command]; ok && cfg.BatchWindow > 0 {
		return cfg.BatchWindow
	}
	return b.config.DefaultWindow
}

func (b *ToolBatcher) waitForResult(ctx context.Context, resultChan <-chan *BatchedResult) (*ToolResult, error) {
	select {
	case result := <-resultChan:
		return result.Result, result.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *ToolBatcher) flushBatch(command string) {
	invocations := b.collectPendingInvocations(command)
	if len(invocations) == 0 {
		return
	}

	go b.executeBatch(command, invocations)
}

func (b *ToolBatcher) collectPendingInvocations(command string) []*PendingInvocation {
	b.mu.Lock()
	defer b.mu.Unlock()

	invocations := b.pending[command]
	delete(b.pending, command)

	if timer, exists := b.batchTimers[command]; exists {
		timer.Stop()
		delete(b.batchTimers, command)
	}

	return invocations
}

func (b *ToolBatcher) executeBatch(command string, invocations []*PendingInvocation) {
	cfg := b.getToolConfig(command)
	batches := b.splitIntoBatches(invocations, cfg.MaxFiles)

	for _, batch := range batches {
		b.executeSingleBatch(command, batch, cfg)
	}
}

func (b *ToolBatcher) getToolConfig(command string) BatchConfig {
	if cfg, ok := b.config.ToolConfigs[command]; ok {
		return cfg
	}
	return DefaultBatchConfig()
}

func (b *ToolBatcher) splitIntoBatches(invocations []*PendingInvocation, maxSize int) [][]*PendingInvocation {
	if maxSize <= 0 {
		maxSize = DefaultMaxBatchSize
	}

	var batches [][]*PendingInvocation
	for i := 0; i < len(invocations); i += maxSize {
		end := i + maxSize
		if end > len(invocations) {
			end = len(invocations)
		}
		batches = append(batches, invocations[i:end])
	}
	return batches
}

func (b *ToolBatcher) executeSingleBatch(command string, batch []*PendingInvocation, cfg BatchConfig) {
	if len(batch) == 1 {
		b.executeSingleInvocation(batch[0])
		return
	}

	batchedInv := b.combineBatchInvocations(command, batch, cfg)
	result, err := b.executor.Execute(context.Background(), batchedInv)

	if err != nil && b.config.FallbackOnFail {
		b.executeFallback(batch)
		return
	}

	b.distributeResults(batch, result, err)
}

func (b *ToolBatcher) executeSingleInvocation(pending *PendingInvocation) {
	result, err := b.executor.Execute(context.Background(), pending.Invocation)
	pending.ResultChan <- &BatchedResult{Result: result, Error: err}
}

func (b *ToolBatcher) combineBatchInvocations(command string, batch []*PendingInvocation, cfg BatchConfig) ToolInvocation {
	args := b.collectAllArgs(batch, cfg.Separator)
	workingDir := b.determineWorkingDir(batch)

	return ToolInvocation{
		Command:    command,
		Args:       args,
		WorkingDir: workingDir,
		Timeout:    b.calculateBatchTimeout(batch),
	}
}

func (b *ToolBatcher) collectAllArgs(batch []*PendingInvocation, separator string) []string {
	var allArgs []string
	for _, pending := range batch {
		allArgs = append(allArgs, pending.Invocation.Args...)
	}
	return allArgs
}

func (b *ToolBatcher) determineWorkingDir(batch []*PendingInvocation) string {
	if len(batch) > 0 {
		return batch[0].Invocation.WorkingDir
	}
	return ""
}

func (b *ToolBatcher) calculateBatchTimeout(batch []*PendingInvocation) time.Duration {
	var maxTimeout time.Duration
	for _, pending := range batch {
		if pending.Invocation.Timeout > maxTimeout {
			maxTimeout = pending.Invocation.Timeout
		}
	}
	return maxTimeout
}

func (b *ToolBatcher) executeFallback(batch []*PendingInvocation) {
	for _, pending := range batch {
		b.executeSingleInvocation(pending)
	}
}

func (b *ToolBatcher) distributeResults(batch []*PendingInvocation, result *ToolResult, err error) {
	for _, pending := range batch {
		pending.ResultChan <- &BatchedResult{Result: result, Error: err}
	}
}

func (b *ToolBatcher) SetToolConfig(command string, cfg BatchConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.config.ToolConfigs[command] = cfg
}

func (b *ToolBatcher) RemoveToolConfig(command string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.config.ToolConfigs, command)
}

func (b *ToolBatcher) IsBatchable(command string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.isBatchableUnlocked(command)
}

func (b *ToolBatcher) isBatchableUnlocked(command string) bool {
	cfg, exists := b.config.ToolConfigs[command]
	return exists && cfg.BatchCapable
}

func (b *ToolBatcher) PendingCount(command string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending[command])
}

func (b *ToolBatcher) TotalPending() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	total := 0
	for _, invocations := range b.pending {
		total += len(invocations)
	}
	return total
}

func (b *ToolBatcher) FlushAll() {
	commands := b.getPendingCommands()
	for _, command := range commands {
		b.flushBatch(command)
	}
}

func (b *ToolBatcher) getPendingCommands() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	commands := make([]string, 0, len(b.pending))
	for command := range b.pending {
		commands = append(commands, command)
	}
	return commands
}

func (b *ToolBatcher) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrExecutorClosed
	}
	b.closed = true

	b.stopAllTimers()
	b.cancelAllPending()
	b.mu.Unlock()

	return nil
}

func (b *ToolBatcher) stopAllTimers() {
	for _, timer := range b.batchTimers {
		timer.Stop()
	}
	b.batchTimers = make(map[string]*time.Timer)
}

func (b *ToolBatcher) cancelAllPending() {
	for _, invocations := range b.pending {
		for _, pending := range invocations {
			pending.ResultChan <- &BatchedResult{Error: ErrExecutorClosed}
		}
	}
	b.pending = make(map[string][]*PendingInvocation)
}

func (b *ToolBatcher) BatchableTools() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	tools := make([]string, 0, len(b.config.ToolConfigs))
	for tool, cfg := range b.config.ToolConfigs {
		if cfg.BatchCapable {
			tools = append(tools, tool)
		}
	}
	return tools
}

type BatchStats struct {
	TotalBatched     int64
	TotalFallbacks   int64
	AverageBatchSize float64
}

func ParseBatchedOutput(output []byte, files []string, separator string) map[string][]byte {
	results := make(map[string][]byte)
	content := string(output)

	for _, file := range files {
		results[file] = extractFileOutput(content, file)
	}

	return results
}

func extractFileOutput(content, file string) []byte {
	lines := strings.Split(content, "\n")
	var fileLines []string

	for _, line := range lines {
		if strings.Contains(line, file) {
			fileLines = append(fileLines, line)
		}
	}

	return []byte(strings.Join(fileLines, "\n"))
}
