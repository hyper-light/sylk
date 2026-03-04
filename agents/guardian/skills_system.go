package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// system_status — Domain: system, Priority: 75
// ---------------------------------------------------------------------------

type systemStatusInput struct {
	Action string `json:"action"`
}

func systemStatusSkill(g *Guardian) *skills.Skill {
	type handler = func(context.Context, *systemStatusInput) (any, error)
	dispatch := map[string]handler{
		"resources": func(_ context.Context, _ *systemStatusInput) (any, error) {
			return systemResources()
		},
		"daemons": func(_ context.Context, _ *systemStatusInput) (any, error) {
			return systemDaemons(g)
		},
	}

	return skills.NewSkill("system_status").
		Description("Process-level metrics and system health.\n\n"+
			"Actions:\n"+
			"- resources: Memory stats, goroutine count, GC metrics\n"+
			"- daemons: Daemon set running/healthy state and restart counts").
		Domain("system").
		Keywords("system", "memory", "goroutines", "gc", "daemon", "process", "resources").
		Priority(75).
		TokenEstimate(400).
		EnumParam("action", "System action", []string{
			"resources", "daemons",
		}, true).
		Usage("Use system_status to check process-level resource consumption "+
			"or daemon health. action=resources shows memory and goroutine counts. "+
			"action=daemons shows daemon set running/healthy state.").
		BestPractice("Check goroutine count against GOMAXPROCS for concurrency pressure. "+
			"HeapAlloc above 500MB may indicate memory pressure.").
		Example(`{"action": "resources"}`).
		Example(`{"action": "daemons"}`).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params systemStatusInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			shared.LogAgentEvent(g.steering.EventLogger(),
				agentlog.EventSkillInvoked, g.id, "", "", "info",
				map[string]string{"skill": "system_status", "action": params.Action})
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown system_status action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func systemResources() (any, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return map[string]any{
		"heap_alloc_bytes":   m.HeapAlloc,
		"heap_sys_bytes":     m.HeapSys,
		"heap_alloc_mb":      m.HeapAlloc / (1024 * 1024),
		"heap_sys_mb":        m.HeapSys / (1024 * 1024),
		"stack_inuse_bytes":  m.StackInuse,
		"num_gc":             m.NumGC,
		"gc_pause_total_ns":  m.PauseTotalNs,
		"gc_pause_total_ms":  m.PauseTotalNs / 1_000_000,
		"num_goroutine":      runtime.NumGoroutine(),
		"gomaxprocs":         runtime.GOMAXPROCS(0),
		"num_cpu":            runtime.NumCPU(),
	}, nil
}

func systemDaemons(g *Guardian) (any, error) {
	if g.config.DaemonController == nil {
		return map[string]any{
			"available": false,
			"reason":    "daemon controller not wired",
		}, nil
	}

	statuses := g.config.DaemonController.Status()
	return map[string]any{
		"available":    true,
		"daemons":      statuses,
		"daemon_count": len(statuses),
	}, nil
}
