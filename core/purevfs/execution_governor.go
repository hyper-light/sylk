package purevfs

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/memorybudget"
)

const (
	executionMemoryLimitEnv = "SYLK_EXECUTION_MEMORY_MAX_BYTES"
	executionRunLimitEnv    = "SYLK_EXECUTION_RUN_MAX_BYTES"
	executionReserveEnv     = "SYLK_EXECUTION_RESERVATION_BYTES"
	executionOutputLimitEnv = "SYLK_EXECUTION_OUTPUT_MAX_BYTES"

	defaultExecutionMemoryLimit = 512 << 20
	defaultExecutionRunLimit    = 256 << 20
	defaultExecutionReserve     = 16 << 20
	defaultExecutionOutputLimit = 8 << 20
)

var (
	defaultExecutionGovernorOnce  sync.Once
	defaultExecutionGovernorValue *executionGovernor
)

type executionGovernor struct {
	mu               sync.Mutex
	totalLimitBytes  int64
	runLimitBytes    int64
	reserveBytes     int64
	outputLimitBytes int64
	usedBytes        int64
}

type executionBudget struct {
	governor    *executionGovernor
	limit       int64
	used        int64
	reservation *memorybudget.Reservation
}

type brokerMemoryGuard struct {
	budget      *executionBudget
	outputLimit int64
	runLimit    int64
}

type executionCaptureBuffer struct {
	budget    *executionBudget
	limit     int64
	size      int64
	truncated bool
	buffer    bytes.Buffer
}

func currentExecutionGovernor() *executionGovernor {
	defaultExecutionGovernorOnce.Do(func() {
		defaultExecutionGovernorValue = newExecutionGovernorFromEnv()
	})
	return defaultExecutionGovernorValue
}

func newExecutionGovernorFromEnv() *executionGovernor {
	return newExecutionGovernor(
		envInt64OrDefault(executionMemoryLimitEnv, defaultExecutionMemoryLimit),
		envInt64OrDefault(executionRunLimitEnv, defaultExecutionRunLimit),
		envInt64OrDefault(executionReserveEnv, defaultExecutionReserve),
		envInt64OrDefault(executionOutputLimitEnv, defaultExecutionOutputLimit),
	)
}

func newExecutionGovernor(totalLimit, runLimit, reserve, outputLimit int64) *executionGovernor {
	totalLimit = normalizePositiveLimit(totalLimit, defaultExecutionMemoryLimit)
	runLimit = normalizePositiveLimit(runLimit, defaultExecutionRunLimit)
	reserve = normalizePositiveLimit(reserve, defaultExecutionReserve)
	outputLimit = normalizePositiveLimit(outputLimit, defaultExecutionOutputLimit)
	if totalLimit > 0 && runLimit > totalLimit {
		runLimit = totalLimit
	}
	if runLimit > 0 && reserve > runLimit {
		reserve = runLimit
	}
	if runLimit > 0 && outputLimit > runLimit {
		outputLimit = runLimit
	}
	return &executionGovernor{
		totalLimitBytes:  totalLimit,
		runLimitBytes:    runLimit,
		reserveBytes:     reserve,
		outputLimitBytes: outputLimit,
	}
}

func envInt64OrDefault(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func normalizePositiveLimit(value, fallback int64) int64 {
	if value <= 0 {
		return fallback
	}
	return value
}

func (g *executionGovernor) Admit() (*brokerMemoryGuard, error) {
	reservation, err := memorybudget.Current().Reserve(memorybudget.ScopeExecution, 0)
	if err != nil {
		return nil, ErrMemoryPressure
	}
	budget := &executionBudget{
		governor:    g,
		limit:       g.runLimitBytes,
		reservation: reservation,
	}
	if err := budget.Reserve(g.reserveBytes); err != nil {
		reservation.Release()
		return nil, err
	}
	return &brokerMemoryGuard{
		budget:      budget,
		outputLimit: g.outputLimitBytes,
		runLimit:    g.runLimitBytes,
	}, nil
}

func (g *executionGovernor) reserve(budget *executionBudget, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if budget.limit > 0 && budget.used+bytes > budget.limit {
		return ErrMemoryPressure
	}
	if g.totalLimitBytes > 0 && g.usedBytes+bytes > g.totalLimitBytes {
		return ErrMemoryPressure
	}
	if budget.reservation != nil {
		if err := budget.reservation.Resize(budget.used + bytes); err != nil {
			return ErrMemoryPressure
		}
	}
	budget.used += bytes
	g.usedBytes += bytes
	return nil
}

func (g *executionGovernor) release(budget *executionBudget, bytes int64) {
	if bytes <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if bytes > budget.used {
		bytes = budget.used
	}
	if bytes > g.usedBytes {
		bytes = g.usedBytes
	}
	budget.used -= bytes
	g.usedBytes -= bytes
	if budget.reservation != nil {
		_ = budget.reservation.Resize(budget.used)
	}
}

func (b *executionBudget) Reserve(bytes int64) error {
	if b == nil || b.governor == nil {
		return nil
	}
	return b.governor.reserve(b, bytes)
}

func (b *executionBudget) Release(bytes int64) {
	if b == nil || b.governor == nil {
		return
	}
	b.governor.release(b, bytes)
}

func (b *executionBudget) ReleaseAll() {
	if b == nil {
		return
	}
	b.Release(b.used)
	if b.reservation != nil {
		b.reservation.Release()
		b.reservation = nil
	}
}

func (g *brokerMemoryGuard) Release() {
	if g == nil || g.budget == nil {
		return
	}
	g.budget.ReleaseAll()
}

func newExecutionCaptureBuffer(budget *executionBudget, limit int64) *executionCaptureBuffer {
	return &executionCaptureBuffer{
		budget: budget,
		limit:  limit,
	}
}

func (b *executionCaptureBuffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	keep := b.keepBytes(len(p))
	if keep > 0 {
		if err := b.reserveBytes(int64(keep)); err == nil {
			_, _ = b.buffer.Write(p[:keep])
			b.size += int64(keep)
		} else {
			keep = 0
			b.truncated = true
		}
	}
	if keep < len(p) {
		b.truncated = true
	}
	return len(p), nil
}

func (b *executionCaptureBuffer) keepBytes(requested int) int {
	if b.limit <= 0 {
		return requested
	}
	remaining := int(b.limit - b.size)
	if remaining <= 0 {
		return 0
	}
	if requested < remaining {
		return requested
	}
	return remaining
}

func (b *executionCaptureBuffer) Bytes() []byte {
	return cloneBytes(b.buffer.Bytes())
}

func (b *executionCaptureBuffer) reserveBytes(bytes int64) error {
	if b.budget == nil {
		return nil
	}
	return b.budget.Reserve(bytes)
}
