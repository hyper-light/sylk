# Memory Pressure Defense System

**7 layers, tracked usage, responsive, anti-flap**

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                       MEMORY PRESSURE DEFENSE SYSTEM                                 │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌───────────────────────────────────────────────────────────────────────────────┐ │
│  │ LAYER 1: USAGE REGISTRY                                                       │ │
│  │          Track actual usage by category — no upfront reservation              │ │
│  └───────────────────────────────────────────────────────────────────────────────┘ │
│                                       │                                             │
│                                       ▼                                             │
│  ┌───────────────────────────────────────────────────────────────────────────────┐ │
│  │ LAYER 2: MEMORY MONITOR                                                       │ │
│  │          200ms sampling, trend detection, spike detection                     │ │
│  └───────────────────────────────────────────────────────────────────────────────┘ │
│                                       │                                             │
│                                       ▼                                             │
│  ┌───────────────────────────────────────────────────────────────────────────────┐ │
│  │ LAYER 3: PRESSURE STATE MACHINE                                               │ │
│  │          NORMAL → ELEVATED → HIGH → CRITICAL (hysteresis + cooldown)          │ │
│  └───────────────────────────────────────────────────────────────────────────────┘ │
│                                       │                                             │
│           ┌───────────────┬───────────┴───────────┬───────────────┐                │
│           ▼               ▼                       ▼               ▼                │
│  ┌─────────────┐ ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐      │
│  │  LAYER 4:   │ │    LAYER 5:     │ │    LAYER 6:     │ │    LAYER 7:     │      │
│  │  Admission  │ │  Cache Evictor  │ │    Context      │ │    Pipeline     │      │
│  │  Controller │ │                 │ │   Compactor     │ │    Suspender    │      │
│  │             │ │                 │ │                 │ │                 │      │
│  │ Gate new    │ │ LRU eviction    │ │ Trigger early   │ │ Pause/resume    │      │
│  │ pipelines   │ │ from caches     │ │ LLM compaction  │ │ by priority     │      │
│  └─────────────┘ └─────────────────┘ └─────────────────┘ └─────────────────┘      │
│                                                                                     │
│  RESPONSE MATRIX:                                                                   │
│  ───────────────────────────────────────────────────────────────────────────────── │
│  NORMAL (< 50%):     Resume admissions, resume pipelines                           │
│  ELEVATED (50-70%):  Stop admissions, GC                                           │
│  HIGH (70-85%):      + Evict caches 25%, compact contexts                          │
│  CRITICAL (> 85%):   + Evict caches 50%, pause pipelines                           │
│  SPIKE (+15%):       Immediate GC + evict (bypasses cooldown)                      │
│                                                                                     │
│  ANTI-FLAP:                                                                         │
│  ───────────────────────────────────────────────────────────────────────────────── │
│  Hysteresis: exit threshold = enter - 15%                                          │
│  Cooldown: 3s between state changes (spike bypasses)                               │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Layer 1: Usage Registry

**Track actual usage. No blocking. No reservation.**

```go
type UsageCategory int

const (
    UsageCategoryCaches UsageCategory = iota
    UsageCategoryPipelines
    UsageCategoryLLMBuffers
    UsageCategoryWAL
)

type UsageRegistry struct {
    mu          sync.RWMutex
    byCategory  map[UsageCategory]int64
    byComponent map[string]*ComponentUsage
}

type ComponentUsage struct {
    ID        string
    Category  UsageCategory
    Bytes     int64
    UpdatedAt time.Time
}

func NewUsageRegistry() *UsageRegistry {
    return &UsageRegistry{
        byCategory:  make(map[UsageCategory]int64),
        byComponent: make(map[string]*ComponentUsage),
    }
}

// Report updates usage for a component (non-blocking, called by components)
func (r *UsageRegistry) Report(id string, category UsageCategory, bytes int64) {
    r.mu.Lock()
    defer r.mu.Unlock()

    // Remove old usage if exists
    if old, exists := r.byComponent[id]; exists {
        r.byCategory[old.Category] -= old.Bytes
    }

    // Record new usage
    r.byComponent[id] = &ComponentUsage{
        ID:        id,
        Category:  category,
        Bytes:     bytes,
        UpdatedAt: time.Now(),
    }
    r.byCategory[category] += bytes
}

// Release removes a component's usage (called when component is done)
func (r *UsageRegistry) Release(id string) {
    r.mu.Lock()
    defer r.mu.Unlock()

    if usage, exists := r.byComponent[id]; exists {
        r.byCategory[usage.Category] -= usage.Bytes
        delete(r.byComponent, id)
    }
}

// Total returns total tracked usage across all categories
func (r *UsageRegistry) Total() int64 {
    r.mu.RLock()
    defer r.mu.RUnlock()

    var total int64
    for _, bytes := range r.byCategory {
        total += bytes
    }
    return total
}

// ByCategory returns usage for a specific category
func (r *UsageRegistry) ByCategory(cat UsageCategory) int64 {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.byCategory[cat]
}

// LargestComponents returns top N components by usage (for targeted eviction)
func (r *UsageRegistry) LargestComponents(n int, category UsageCategory) []*ComponentUsage {
    r.mu.RLock()
    defer r.mu.RUnlock()

    var components []*ComponentUsage
    for _, c := range r.byComponent {
        if c.Category == category {
            components = append(components, c)
        }
    }

    sort.Slice(components, func(i, j int) bool {
        return components[i].Bytes > components[j].Bytes
    })

    if len(components) > n {
        return components[:n]
    }
    return components
}
```

---

## Layer 2: Memory Monitor

**Fast sampling + spike detection**

```go
type MemoryMonitor struct {
    registry *UsageRegistry
    limit    int64
    interval time.Duration

    // Ring buffer for trend detection
    samples   []Sample
    sampleIdx int

    // Callbacks
    onSample func(Sample)
    onSpike  func(Sample)
}

type Sample struct {
    Timestamp time.Time
    HeapAlloc uint64
    Usage     float64 // HeapAlloc / limit
    Trend     float64 // Rate of change from previous sample
}

func NewMemoryMonitor(registry *UsageRegistry) *MemoryMonitor {
    // Determine limit from available memory (OS-agnostic)
    var stats runtime.MemStats
    runtime.ReadMemStats(&stats)

    limit := int64(float64(stats.Sys) * 0.75)
    debug.SetMemoryLimit(limit)

    return &MemoryMonitor{
        registry: registry,
        limit:    limit,
        interval: 200 * time.Millisecond,
        samples:  make([]Sample, 30), // 6 seconds of history
    }
}

func (m *MemoryMonitor) Run(ctx context.Context) {
    ticker := time.NewTicker(m.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            sample := m.takeSample()

            if m.onSample != nil {
                m.onSample(sample)
            }

            if m.isSpike(sample) && m.onSpike != nil {
                m.onSpike(sample)
            }
        }
    }
}

func (m *MemoryMonitor) takeSample() Sample {
    var stats runtime.MemStats
    runtime.ReadMemStats(&stats)

    sample := Sample{
        Timestamp: time.Now(),
        HeapAlloc: stats.HeapAlloc,
        Usage:     float64(stats.HeapAlloc) / float64(m.limit),
    }

    // Calculate trend from previous sample
    prevIdx := (m.sampleIdx - 1 + len(m.samples)) % len(m.samples)
    if prev := m.samples[prevIdx]; prev.HeapAlloc > 0 {
        sample.Trend = float64(int64(sample.HeapAlloc)-int64(prev.HeapAlloc)) / float64(prev.HeapAlloc)
    }

    m.samples[m.sampleIdx] = sample
    m.sampleIdx = (m.sampleIdx + 1) % len(m.samples)

    return sample
}

func (m *MemoryMonitor) isSpike(s Sample) bool {
    return s.Trend > 0.15 // 15% increase in one interval
}

func (m *MemoryMonitor) Limit() int64 {
    return m.limit
}
```

---

## Layer 3: Pressure State Machine

**Hysteresis + cooldown = no flapping**

```go
type PressureLevel int

const (
    PressureNormal   PressureLevel = iota // < 50%
    PressureElevated                       // 50-70%
    PressureHigh                           // 70-85%
    PressureCritical                       // > 85%
)

func (p PressureLevel) String() string {
    return [...]string{"NORMAL", "ELEVATED", "HIGH", "CRITICAL"}[p]
}

type PressureStateMachine struct {
    mu         sync.Mutex
    level      PressureLevel
    lastChange time.Time
    cooldown   time.Duration

    // Thresholds with hysteresis
    enter []float64 // Enter thresholds: [0.50, 0.70, 0.85]
    exit  []float64 // Exit thresholds:  [0.35, 0.55, 0.70]

    onTransition func(from, to PressureLevel, usage float64)
}

func NewPressureStateMachine() *PressureStateMachine {
    return &PressureStateMachine{
        level:    PressureNormal,
        cooldown: 3 * time.Second,
        enter:    []float64{0.50, 0.70, 0.85},
        exit:     []float64{0.35, 0.55, 0.70},
    }
}

func (sm *PressureStateMachine) Update(usage float64) (changed bool) {
    sm.mu.Lock()
    defer sm.mu.Unlock()

    // Respect cooldown
    if time.Since(sm.lastChange) < sm.cooldown {
        return false
    }

    newLevel := sm.compute(usage)
    if newLevel == sm.level {
        return false
    }

    from := sm.level
    sm.level = newLevel
    sm.lastChange = time.Now()

    if sm.onTransition != nil {
        go sm.onTransition(from, newLevel, usage)
    }

    return true
}

func (sm *PressureStateMachine) compute(usage float64) PressureLevel {
    // Check for upward transition
    for i := int(sm.level); i < len(sm.enter); i++ {
        if usage >= sm.enter[i] {
            return PressureLevel(i + 1)
        }
    }

    // Check for downward transition (hysteresis)
    for i := int(sm.level) - 1; i >= 0; i-- {
        if usage < sm.exit[i] {
            return PressureLevel(i)
        }
    }

    return sm.level
}

func (sm *PressureStateMachine) Level() PressureLevel {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    return sm.level
}

func (sm *PressureStateMachine) BypassCooldown() {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.lastChange = time.Time{}
}
```

---

## Layer 4: Admission Controller

**Gate new pipelines based on pressure**

```go
type AdmissionController struct {
    scheduler *PipelineScheduler

    mu        sync.RWMutex
    admitting bool
    queued    []*Pipeline
}

func NewAdmissionController(scheduler *PipelineScheduler) *AdmissionController {
    return &AdmissionController{
        scheduler: scheduler,
        admitting: true,
        queued:    make([]*Pipeline, 0),
    }
}

// TryAdmit attempts to admit a pipeline; queues it if not admitting
func (ac *AdmissionController) TryAdmit(p *Pipeline) bool {
    ac.mu.Lock()
    defer ac.mu.Unlock()

    if !ac.admitting {
        ac.queued = append(ac.queued, p)
        return false
    }

    ac.scheduler.Schedule(p)
    return true
}

// Stop stops admitting new pipelines
func (ac *AdmissionController) Stop() {
    ac.mu.Lock()
    defer ac.mu.Unlock()
    ac.admitting = false
}

// Resume resumes admitting and drains the queue
func (ac *AdmissionController) Resume() {
    ac.mu.Lock()
    defer ac.mu.Unlock()

    ac.admitting = true

    // Drain queued pipelines
    for _, p := range ac.queued {
        ac.scheduler.Schedule(p)
    }
    ac.queued = ac.queued[:0]
}

// QueueLength returns number of queued pipelines
func (ac *AdmissionController) QueueLength() int {
    ac.mu.RLock()
    defer ac.mu.RUnlock()
    return len(ac.queued)
}

// IsAdmitting returns current admission state
func (ac *AdmissionController) IsAdmitting() bool {
    ac.mu.RLock()
    defer ac.mu.RUnlock()
    return ac.admitting
}
```

---

## Layer 5: Cache Evictor

**LRU eviction from caches**

```go
type CacheEvictor struct {
    caches []EvictableCache
}

type EvictableCache interface {
    Name() string
    Size() int64
    EvictPercent(percent float64) int64 // Returns bytes freed
}

func NewCacheEvictor(caches ...EvictableCache) *CacheEvictor {
    return &CacheEvictor{caches: caches}
}

// Evict evicts the given percentage from all caches
func (e *CacheEvictor) Evict(percent float64) int64 {
    var totalFreed int64

    for _, cache := range e.caches {
        freed := cache.EvictPercent(percent)
        totalFreed += freed
    }

    return totalFreed
}

// EvictBytes evicts until at least targetBytes are freed
func (e *CacheEvictor) EvictBytes(targetBytes int64) int64 {
    var totalFreed int64

    // Sort caches by size descending (evict from largest first)
    sorted := make([]EvictableCache, len(e.caches))
    copy(sorted, e.caches)
    sort.Slice(sorted, func(i, j int) bool {
        return sorted[i].Size() > sorted[j].Size()
    })

    for _, cache := range sorted {
        if totalFreed >= targetBytes {
            break
        }

        // Evict 25% at a time from each cache
        freed := cache.EvictPercent(0.25)
        totalFreed += freed
    }

    return totalFreed
}
```

### Example Cache Implementation

```go
type QueryCache struct {
    mu      sync.RWMutex
    entries map[string]*CacheEntry
    size    int64

    registry *UsageRegistry
}

func (c *QueryCache) EvictPercent(percent float64) int64 {
    c.mu.Lock()
    defer c.mu.Unlock()

    target := int(float64(len(c.entries)) * percent)
    if target == 0 {
        return 0
    }

    // Sort by eviction score (from existing architecture)
    entries := c.sortedByEvictionScore()

    var freed int64
    for i := 0; i < target && i < len(entries); i++ {
        freed += entries[i].Size
        c.size -= entries[i].Size
        delete(c.entries, entries[i].Key)
    }

    // Update registry
    c.registry.Report("query-cache", UsageCategoryCaches, c.size)

    return freed
}
```

---

## Layer 6: Context Compactor

**Trigger early LLM context compaction**

```go
type ContextCompactor struct {
    signalBus *SignalBus
    registry  *UsageRegistry
}

func NewContextCompactor(signalBus *SignalBus, registry *UsageRegistry) *ContextCompactor {
    return &ContextCompactor{
        signalBus: signalBus,
        registry:  registry,
    }
}

// CompactAll signals all agents to compact their LLM contexts
func (c *ContextCompactor) CompactAll() {
    c.signalBus.Broadcast(SignalMessage{
        Signal: SignalCompactContexts,
        Reason: "Memory pressure",
    })
}

// CompactLargest compacts the N largest context holders
func (c *ContextCompactor) CompactLargest(n int) {
    largest := c.registry.LargestComponents(n, UsageCategoryLLMBuffers)

    for _, comp := range largest {
        c.signalBus.Broadcast(SignalMessage{
            Signal:   SignalCompactContexts,
            TargetID: comp.ID,
            Reason:   "Memory pressure - large context",
        })
    }
}
```

---

## Layer 7: Pipeline Suspender

**Pause/resume pipelines by priority**

```go
type PipelineSuspender struct {
    scheduler *PipelineScheduler
    signalBus *SignalBus

    mu     sync.Mutex
    paused map[string]*PausedPipeline
}

type PausedPipeline struct {
    ID       string
    Priority int
    PausedAt time.Time
}

func NewPipelineSuspender(scheduler *PipelineScheduler, signalBus *SignalBus) *PipelineSuspender {
    return &PipelineSuspender{
        scheduler: scheduler,
        signalBus: signalBus,
        paused:    make(map[string]*PausedPipeline),
    }
}

// PauseLowestPriority pauses the lowest-priority active pipeline
func (s *PipelineSuspender) PauseLowestPriority() *PausedPipeline {
    s.mu.Lock()
    defer s.mu.Unlock()

    // Get active pipelines sorted by priority (lowest first)
    active := s.scheduler.GetActiveSortedByPriority()
    if len(active) == 0 {
        return nil
    }

    target := active[0] // Lowest priority

    // Send pause signal
    s.signalBus.Broadcast(SignalMessage{
        Signal:      SignalPausePipeline,
        TargetID:    target.ID,
        RequiresAck: true,
        Timeout:     5 * time.Second,
        Reason:      "Memory pressure",
    })

    paused := &PausedPipeline{
        ID:       target.ID,
        Priority: target.Priority.Priority,
        PausedAt: time.Now(),
    }
    s.paused[target.ID] = paused

    return paused
}

// ResumeAll resumes all paused pipelines
func (s *PipelineSuspender) ResumeAll() {
    s.mu.Lock()
    defer s.mu.Unlock()

    for id := range s.paused {
        s.signalBus.Broadcast(SignalMessage{
            Signal:   SignalResumePipeline,
            TargetID: id,
            Reason:   "Memory pressure relieved",
        })
    }

    s.paused = make(map[string]*PausedPipeline)
}

// ResumeHighestPriority resumes the highest-priority paused pipeline
func (s *PipelineSuspender) ResumeHighestPriority() *PausedPipeline {
    s.mu.Lock()
    defer s.mu.Unlock()

    if len(s.paused) == 0 {
        return nil
    }

    // Find highest priority
    var highest *PausedPipeline
    for _, p := range s.paused {
        if highest == nil || p.Priority > highest.Priority {
            highest = p
        }
    }

    s.signalBus.Broadcast(SignalMessage{
        Signal:   SignalResumePipeline,
        TargetID: highest.ID,
    })

    delete(s.paused, highest.ID)
    return highest
}

// PausedCount returns number of paused pipelines
func (s *PipelineSuspender) PausedCount() int {
    s.mu.Lock()
    defer s.mu.Unlock()
    return len(s.paused)
}
```

---

## Pressure Controller (Orchestrates All Layers)

```go
type PressureController struct {
    // Layers
    registry     *UsageRegistry
    monitor      *MemoryMonitor
    stateMachine *PressureStateMachine
    admission    *AdmissionController
    evictor      *CacheEvictor
    compactor    *ContextCompactor
    suspender    *PipelineSuspender

    signalBus *SignalBus
}

func NewPressureController(
    signalBus *SignalBus,
    scheduler *PipelineScheduler,
    caches []EvictableCache,
) *PressureController {
    registry := NewUsageRegistry()

    pc := &PressureController{
        registry:     registry,
        monitor:      NewMemoryMonitor(registry),
        stateMachine: NewPressureStateMachine(),
        admission:    NewAdmissionController(scheduler),
        evictor:      NewCacheEvictor(caches...),
        compactor:    NewContextCompactor(signalBus, registry),
        suspender:    NewPipelineSuspender(scheduler, signalBus),
        signalBus:    signalBus,
    }

    // Wire callbacks
    pc.monitor.onSample = pc.handleSample
    pc.monitor.onSpike = pc.handleSpike
    pc.stateMachine.onTransition = pc.handleTransition

    return pc
}

func (pc *PressureController) Run(ctx context.Context) {
    pc.monitor.Run(ctx)
}

// Registry returns the usage registry for components to report usage
func (pc *PressureController) Registry() *UsageRegistry {
    return pc.registry
}

// Admission returns the admission controller for pipeline creation
func (pc *PressureController) Admission() *AdmissionController {
    return pc.admission
}

func (pc *PressureController) handleSample(s Sample) {
    pc.stateMachine.Update(s.Usage)
}

func (pc *PressureController) handleSpike(s Sample) {
    // Spike bypasses state machine cooldown
    pc.stateMachine.BypassCooldown()

    // Immediate actions
    pc.triggerGC()

    if s.Usage > 0.60 {
        pc.evictor.Evict(0.25)
    }
    if s.Usage > 0.80 {
        pc.admission.Stop()
        pc.suspender.PauseLowestPriority()
    }
}

func (pc *PressureController) handleTransition(from, to PressureLevel, usage float64) {
    switch to {
    case PressureNormal:
        pc.admission.Resume()
        pc.suspender.ResumeAll()

    case PressureElevated:
        pc.admission.Stop()
        pc.triggerGC()

    case PressureHigh:
        pc.admission.Stop()
        pc.evictor.Evict(0.25)
        pc.compactor.CompactAll()
        pc.triggerGC()

    case PressureCritical:
        pc.admission.Stop()
        pc.evictor.Evict(0.50)
        pc.compactor.CompactAll()
        pc.suspender.PauseLowestPriority()
        pc.triggerGC()
    }

    // Emit state change event
    pc.signalBus.Broadcast(SignalMessage{
        Signal: SignalMemoryPressureChanged,
        Payload: map[string]any{
            "from":  from.String(),
            "to":    to.String(),
            "usage": usage,
        },
    })
}

func (pc *PressureController) triggerGC() {
    go func() {
        runtime.GC()
        debug.FreeOSMemory()
    }()
}
```

---

## Integration with Existing Architecture

### New Signals

```go
const (
    // Existing signals...
    SignalPauseAll Signal = iota
    SignalResumeAll
    SignalPausePipeline
    SignalResumePipeline

    // Memory pressure signals (new)
    SignalEvictCaches
    SignalCompactContexts
    SignalMemoryPressureChanged
)
```

### Component Integration (Non-blocking Usage Reporting)

```go
// Cache reports usage after modifications
func (c *QueryCache) Set(key string, entry *CacheEntry) {
    c.mu.Lock()
    c.entries[key] = entry
    c.size += entry.Size
    size := c.size
    c.mu.Unlock()

    c.registry.Report("query-cache", UsageCategoryCaches, size)
}

// Pipeline reports usage at lifecycle points
func (p *Pipeline) Start() {
    p.registry.Report(p.ID, UsageCategoryPipelines, p.estimatedSize())
    // ... start work ...
}

func (p *Pipeline) Complete() {
    // ... cleanup ...
    p.registry.Release(p.ID)
}

// LLM buffer reports during streaming
func (b *StreamAccumulator) Accumulate(chunk StreamChunk) {
    b.mu.Lock()
    b.buffer = append(b.buffer, chunk)
    size := b.size()
    b.mu.Unlock()

    b.registry.Report(b.requestID, UsageCategoryLLMBuffers, size)
}
```

### Startup

```go
func main() {
    signalBus := NewSignalBus()
    scheduler := NewPipelineScheduler()

    caches := []EvictableCache{
        queryCache,
        librarianCache,
        archivalistCache,
    }

    pressureController := NewPressureController(signalBus, scheduler, caches)

    // Start monitoring
    go pressureController.Run(ctx)

    // Use admission controller for new pipelines
    admission := pressureController.Admission()

    // Use registry for usage reporting
    registry := pressureController.Registry()

    // ... rest of startup ...
}
```

---

## Summary

### Layer Responsibilities

| Layer | Role | Blocking? |
|-------|------|-----------|
| **1. Usage Registry** | Track actual usage by category/component | No |
| **2. Memory Monitor** | 200ms sampling, spike detection | No |
| **3. State Machine** | NORMAL→ELEVATED→HIGH→CRITICAL with hysteresis | No |
| **4. Admission Controller** | Gate new pipelines, queue when pressure exists | No |
| **5. Cache Evictor** | LRU eviction from caches | No |
| **6. Context Compactor** | Trigger early LLM context compaction | No |
| **7. Pipeline Suspender** | Pause/resume pipelines by priority | No |

### Properties

| Property | Mechanism |
|----------|-----------|
| **Responsive** | 200ms sampling, tracked usage (not reserved) |
| **Proactive** | Admission control stops new work before critical |
| **Graduated** | 4 levels with increasing severity |
| **Anti-flap** | 15% hysteresis + 3s cooldown |
| **Spike-aware** | Rate detection bypasses cooldown |
| **OS agnostic** | `runtime.MemStats`, `runtime.GC`, `debug.SetMemoryLimit` |
| **Non-blocking** | All components report, never wait |

### Thresholds

```
Usage %    State        Actions
────────────────────────────────────────────────────────────────
< 35%      NORMAL       Resume admissions, resume pipelines
35-50%     NORMAL       (hysteresis zone)
50-55%     ELEVATED     Stop admissions, GC
55-70%     ELEVATED     (hysteresis zone)
70-85%     HIGH         + Evict 25%, compact contexts
> 85%      CRITICAL     + Evict 50%, pause pipelines

SPIKE (+15% in one tick): Immediate GC + evict (bypasses cooldown)
```

---

## Integration with Existing Codebase

### What Already Exists

| Component | Location | Comparison to Design |
|-----------|----------|----------------------|
| **MemoryMonitor** | `core/resources/memory_monitor.go` | Has component tracking, pressure levels, thresholds |
| **SignalBus** | `core/signal/bus.go` | Has pause/resume signals, ack support |
| **PipelineScheduler** | `core/session/pipeline_scheduler.go` | Has quotas, queuing, but no memory-aware admission |
| **QueryCache** | `agents/archivalist/query_cache.go` | Has LRU eviction by count (not bytes) |

### Key Gaps

| Feature | Current State |
|---------|---------------|
| **Spike detection** | ❌ Not implemented |
| **Hysteresis** | ❌ Single thresholds (no enter/exit pairs) |
| **Cooldown** | ❌ No flap prevention |
| **Admission control** | ❌ Pipelines not gated by memory |
| **SignalPublisher** | ⚠️ `NoOpSignalPublisher` stub - not wired to SignalBus |
| **Tracked usage** | ⚠️ Budget-based (pre-allocated) vs tracked (observed) |
| **Context compaction** | ❌ No signal for agents to compact LLM contexts |
| **200ms sampling** | ⚠️ 1-second interval currently |

### Integration Strategy: Layer on Top

We **layer new components on top** of existing infrastructure rather than replacing:

1. **Preserves existing working code** - MemoryMonitor continues functioning
2. **Separation of concerns** - monitoring vs response orchestration
3. **Easier to test/debug** - each layer independently testable
4. **Backward compatible** - existing code can use MemoryMonitor directly

### Integration Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         INTEGRATION ARCHITECTURE                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │  NEW: PressureController (orchestrator)                               │   │
│  │       ├── Uses MemoryMonitor for raw HeapAlloc sampling               │   │
│  │       ├── Adds spike detection (rate-of-change)                       │   │
│  │       ├── Adds PressureStateMachine (hysteresis + cooldown)           │   │
│  │       └── Triggers response actions                                   │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                            │                                                 │
│        ┌───────────────────┼───────────────────┐                            │
│        ▼                   ▼                   ▼                            │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐  │
│  │EXISTING:    │    │NEW:         │    │NEW:         │    │EXISTING:    │  │
│  │SignalBus    │    │Admission    │    │Context      │    │Pipeline     │  │
│  │             │    │Controller   │    │Compactor    │    │Scheduler    │  │
│  │Add signals: │    │             │    │             │    │             │  │
│  │- EvictCache │    │Wraps        │    │Broadcasts   │    │Add methods: │  │
│  │- Compact    │    │Pipeline     │    │compact      │    │- PauseLow   │  │
│  │- Pressure   │    │Scheduler    │    │signals      │    │  Priority   │  │
│  │  Changed    │    │.Schedule()  │    │via SignalBus│    │- ResumeAll  │  │
│  └─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘  │
│        │                   │                   │                │          │
│        ▼                   ▼                   ▼                ▼          │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │  EXISTING: MemoryMonitor                                              │  │
│  │       └── Keep for component-level tracking (QueryCache, WAL, etc.)   │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │  EXISTING: QueryCache                                                 │  │
│  │       └── Add: EvictableCache interface, Size() method, EvictPercent()│  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Concrete Integration Points

#### 1. SignalBus Extension (`core/signal/types.go`)

Add new memory pressure signals:

```go
const (
    // Existing signals...
    PauseAll       Signal = "pause_all"
    ResumeAll      Signal = "resume_all"
    // ...

    // Memory pressure signals (NEW)
    EvictCaches           Signal = "evict_caches"
    CompactContexts       Signal = "compact_contexts"
    MemoryPressureChanged Signal = "memory_pressure_changed"
)
```

#### 2. MemoryMonitor Enhancement (`core/resources/memory_monitor.go`)

Wire `SignalPublisher` to real `SignalBus`:

```go
// SignalBusPublisher adapts SignalBus to SignalPublisher interface
type SignalBusPublisher struct {
    bus *signal.SignalBus
}

func (p *SignalBusPublisher) PublishMemorySignal(sig MemorySignal, payload MemorySignalPayload) error {
    return p.bus.Broadcast(signal.SignalMessage{
        ID:      uuid.New().String(),
        Signal:  signal.Signal(sig),
        Payload: payload,
        SentAt:  time.Now(),
    })
}
```

#### 3. PipelineScheduler Extension (`core/session/pipeline_scheduler.go`)

Add pause/resume by priority:

```go
// GetActiveSortedByPriority returns active pipelines sorted by priority (lowest first)
func (s *GlobalPipelineScheduler) GetActiveSortedByPriority() []*ScheduledPipelineInfo

// PauseLowestPriority pauses the lowest priority active pipeline
func (s *GlobalPipelineScheduler) PauseLowestPriority(sessionID string) error

// ResumeAllPaused resumes all paused pipelines
func (s *GlobalPipelineScheduler) ResumeAllPaused() error
```

#### 4. QueryCache Enhancement (`agents/archivalist/query_cache.go`)

Implement `EvictableCache` interface:

```go
// EvictableCache interface for memory pressure response
type EvictableCache interface {
    Name() string
    Size() int64
    EvictPercent(percent float64) int64 // Returns bytes freed
}

// Size returns estimated memory usage in bytes
func (qc *QueryCache) Size() int64 {
    qc.mu.RLock()
    defer qc.mu.RUnlock()
    // Estimate: ~1KB per response + embedding size
    return int64(len(qc.responses)*1024) + int64(len(qc.embeddings)*256*4)
}

// EvictPercent evicts given percentage of entries, returns bytes freed
func (qc *QueryCache) EvictPercent(percent float64) int64 {
    qc.mu.Lock()
    defer qc.mu.Unlock()

    beforeSize := qc.Size()
    target := int(float64(len(qc.responses)) * percent)
    qc.evictOldestLocked() // Reuse existing eviction
    for i := 1; i < target/10 && len(qc.responses) > 0; i++ {
        qc.evictOldestLocked()
    }
    return beforeSize - qc.Size()
}
```

#### 5. New Files to Create

| File | Purpose |
|------|---------|
| `core/resources/pressure_controller.go` | Main orchestrator |
| `core/resources/pressure_state.go` | PressureStateMachine with hysteresis |
| `core/resources/admission_controller.go` | Pipeline admission gate |
| `core/resources/cache_evictor.go` | Cache eviction coordinator |
| `core/resources/context_compactor.go` | LLM context compaction signals |
| `core/resources/pipeline_suspender.go` | Pipeline pause/resume by priority |

#### 6. Startup Integration (`cmd/root.go` or application bootstrap)

```go
// Initialize pressure controller with all components
func initPressureController(
    signalBus *signal.SignalBus,
    scheduler *session.GlobalPipelineScheduler,
    queryCache *archivalist.QueryCache,
    // ... other caches
) *resources.PressureController {

    // Wire SignalPublisher to real bus
    publisher := &resources.SignalBusPublisher{Bus: signalBus}

    // Configure monitor with real publisher
    monitorConfig := resources.DefaultMemoryMonitorConfig()
    monitorConfig.SignalPublisher = publisher
    monitorConfig.MonitorInterval = 200 * time.Millisecond // Faster sampling

    // Build evictable cache list
    caches := []resources.EvictableCache{queryCache}

    return resources.NewPressureController(signalBus, scheduler, caches)
}
