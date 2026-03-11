# Dynamic Agent Handoff Architecture

## Overview

Sylk implements **GP-based dynamic handoff** for managing agent context exhaustion. Each **agent instance** has a fixed (AgentType, Model) assignment and learns its own performance degradation curve via Gaussian Process observations.

When degradation is detected, the action depends on agent category:

- **Knowledge Agents**: Gradual tiered eviction (HOT → WARM → COLD → [CTX-REF-xxx])
- **Standalone/Pipeline Agents**: Same-type handoff to new instance

**Core Principles**:
1. PreparedContext is maintained continuously during normal operation
2. Handoff is a pointer swap + trim (~1-2ms), not an expensive rebuild
3. **ALL parameters are learned distributions** - no hardcoded values
4. **Hierarchical Bayesian pooling** handles cold start automatically

---

## Agent Model Assignments

Each agent has a **fixed, specific model assignment**:

| Agent | Model | Context Window | Rationale |
|-------|-------|----------------|-----------|
| **Librarian** | Sonnet 4.5 | **1M tokens** | Fast code search, massive context for codebase knowledge |
| **Archivalist** | Sonnet 4.5 | **1M tokens** | Pattern matching, history - needs full session history |
| **Academic** | Opus 4.5 | 200K tokens | Complex research, synthesis - reasoning critical |
| **Architect** | Opus 4.5 | 200K tokens | Complex planning, decomposition |
| **Guide** | Haiku 4.5 | 200K tokens | Fast routing, lightweight |
| **Orchestrator** | Haiku 4.5 | 200K tokens | Status queries, pipeline monitoring |
| **Engineer** | Opus 4.5 | 200K tokens | Complex implementation |
| **Designer** | Sonnet 4.5 | 200K tokens | UI/UX implementation |
| **Inspector** (standalone) | Sonnet 4.5 | 200K tokens | Code review, validation |
| **Inspector** (pipeline) | Haiku 4.5 | 200K tokens | Fast criteria checking in TDD loop |
| **Tester** (standalone) | Sonnet 4.5 | 200K tokens | Test strategy, coverage analysis |
| **Tester** (pipeline) | Haiku 4.5 | 200K tokens | Fast test writing in TDD loop |

**Key Insight**: Librarian and Archivalist use Sonnet 4.5's **1M token context window**, fundamentally changing their degradation curves compared to 200K context agents.

---

## Agent Categories and Memory Management

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    AGENT CATEGORIES AND MEMORY MANAGEMENT                            │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  KNOWLEDGE AGENTS → GRADUAL TIERED EVICTION                                         │
│  ═══════════════════════════════════════════                                        │
│  Librarian (Sonnet-1M), Archivalist (Sonnet-1M), Academic (Opus), Architect (Opus) │
│                                                                                     │
│  • Own goroutine (long-lived, standalone)                                           │
│  • GP detects degradation → tiered eviction (HOT → WARM → COLD → [CTX-REF-xxx])    │
│  • Agent CONTINUES (same instance, reduced context)                                 │
│  • Evicted content retrievable on demand via CONTEXT.md tiered system              │
│  • Librarian/Archivalist: 1M context means MUCH longer before eviction needed      │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────    │
│                                                                                     │
│  STANDALONE AGENTS → SAME-TYPE HANDOFF                                              │
│  ════════════════════════════════════════                                           │
│  Guide (Haiku), Orchestrator (Haiku), Inspector-Standalone (Sonnet),               │
│  Tester-Standalone (Sonnet)                                                         │
│                                                                                     │
│  • Own goroutine (long-lived, standalone)                                           │
│  • GP detects degradation → handoff to new instance of SAME (AgentType, Model)     │
│  • PreparedContext trimmed to OptimalPreparedSize (learned for this agent+model)   │
│  • Old instance TERMINATES, new instance continues                                  │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────    │
│                                                                                     │
│  PIPELINE AGENTS → SAME-TYPE HANDOFF (within pipeline)                              │
│  ═════════════════════════════════════════════════════                              │
│  Engineer (Opus), Designer (Sonnet), Inspector-Pipeline (Haiku),                   │
│  Tester-Pipeline (Haiku)                                                            │
│                                                                                     │
│  • Execute sequentially within pipeline goroutine                                   │
│  • GP detects degradation → handoff to new instance of SAME (AgentType, Model)     │
│  • Pipeline continues with swapped agent reference                                  │
│  • PreparedContext trimmed to OptimalPreparedSize (learned for this agent+model)   │
│                                                                                     │
│  ───────────────────────────────────────────────────────────────────────────────    │
│                                                                                     │
│  TRIGGER: GP-detected performance degradation (NOT fixed % threshold)               │
│  PREPARED CONTEXT: Maintained continuously during normal operation                  │
│  HANDOFF LATENCY: ~1-2ms (pointer swap + trim)                                      │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Agent-Specific Performance Curves

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│              AGENT INSTANCE PERFORMANCE CURVES (LEARNED VIA GP)                      │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  Performance                                                                        │
│       │                                                                             │
│       │  Librarian (Sonnet-1M)     Engineer (Opus-200K)      Guide (Haiku-200K)    │
│       │  Codebase knowledge        Complex refactor          Routing               │
│       │                                                                             │
│  1.0 ─┤        ╭────────────────────────────╮                                       │
│       │       ╱                              ╲    ╭───────╮      ╭──╮               │
│       │      ╱                                ╲  ╱         ╲    ╱    ╲              │
│  0.8 ─┤     ╱          PEAK ZONE              ╲╱           ╲  ╱      ╲             │
│       │    ╱           (MUCH LONGER)                        ╲╱        ╲            │
│       │   ╱                                                            ╲           │
│  0.6 ─┤  ╱                                                              ╲          │
│       │ ╱                                                                ╲         │
│       │╱                                                                  ╲        │
│  0.4 ─┼─────┬─────┬─────┬─────┬─────┬─────┬─────┬─────┬─────┬─────┬─────►          │
│       0   100K  200K  300K  400K  500K  600K  700K  800K  900K   1M                │
│                                                                                     │
│       │←─ BURN-IN ─→│←────────────── PEAK ZONE ───────────────→│←─ DEGRADATION ─→│ │
│                                                                                     │
│  CONTEXT WINDOW DRAMATICALLY AFFECTS CURVE SHAPE:                                   │
│  • Librarian/Archivalist (1M): Peak zone can span 200K-800K+ tokens                │
│  • Engineer/Architect (200K Opus): Peak zone spans 30K-120K tokens                 │
│  • Guide/Orchestrator (200K Haiku): Peak zone spans 10K-60K tokens                 │
│                                                                                     │
│  EACH AGENT INSTANCE LEARNS ITS OWN CURVE FROM OBSERVATIONS                        │
│  No assumed shape - GP learns actual curve from quality observations               │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Hierarchical Parameter Learning

**ALL parameters are distributions.** Hierarchical Bayesian pooling handles cold start automatically.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    HIERARCHICAL PARAMETER LEARNING                                   │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  LEVEL 4: GLOBAL PRIOR (weakly informative)                                         │
│       │   All agents, all models                                                    │
│       │   Confidence: always available                                              │
│       │                                                                             │
│       ▼                                                                             │
│  LEVEL 3: MODEL PRIOR                                                               │
│       │   Sonnet-1M, Sonnet-200K, Opus-200K, Haiku-200K, Codex-200K, ...           │
│       │   Confidence: based on observations across all agent types on this model   │
│       │                                                                             │
│       ▼                                                                             │
│  LEVEL 2: (AGENT TYPE, MODEL) PRIOR                                                 │
│       │   Librarian-Sonnet-1M, Engineer-Opus-200K, Guide-Haiku-200K, ...           │
│       │   Confidence: based on observations across all instances of this combo     │
│       │                                                                             │
│       ▼                                                                             │
│  LEVEL 1: AGENT INSTANCE POSTERIOR                                                  │
│           This specific Librarian-Sonnet-1M in this specific session               │
│           Confidence: based on this instance's observations                         │
│                                                                                     │
│  ═══════════════════════════════════════════════════════════════════════════════   │
│                                                                                     │
│  COLD START RESOLUTION (automatic):                                                 │
│                                                                                     │
│  GetEffectiveParam(explore bool) {                                                  │
│      // Blend across hierarchy weighted by confidence                               │
│      instanceConf := instance.Confidence()      // 0.0 if new                      │
│      agentModelConf := agentModel.Confidence()  // may have data                   │
│      modelConf := model.Confidence()            // likely has data                 │
│      globalConf := global.Confidence()          // always has data                 │
│                                                                                     │
│      totalConf := instanceConf + agentModelConf + modelConf + globalConf           │
│                                                                                     │
│      // Weighted blend of distributions                                             │
│      effectiveAlpha := (instance.Alpha * instanceConf +                            │
│                         agentModel.Alpha * agentModelConf +                        │
│                         model.Alpha * modelConf +                                  │
│                         global.Alpha * globalConf) / totalConf                     │
│                                                                                     │
│      // Same for Beta...                                                            │
│      // Result: smooth blend, no hardcoded fallbacks                               │
│  }                                                                                  │
│                                                                                     │
│  NEW AGENT INSTANCE (zero observations):                                            │
│  • instanceConf = 0 → contributes nothing                                          │
│  • Effective param = blend of (AgentType,Model), Model, and Global priors          │
│  • As observations accumulate, instance posterior dominates                         │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## What Must Be Learned Per Agent Instance

**Every single parameter is a distribution.** No hardcoded values anywhere.

| Parameter | Distribution | Description |
|-----------|--------------|-------------|
| **GP Hyperparameters** | | |
| LengthScale | Beta (scaled) | RBF kernel length scale |
| OutputVariance | Beta | Signal variance |
| NoiseVariance | Beta | Observation noise |
| PriorBaseline | Beta | Prior mean function baseline |
| PriorDecay | Beta | Prior mean function decay rate |
| **Profile Points** | | |
| BurnInEnd | Gamma | Context size where burn-in ends |
| PeakZoneStart | Gamma | Context size where peak begins |
| PeakZoneEnd | Gamma | Context size where degradation begins |
| OptimalPreparedSize | Gamma (derived) | Target injection size for handoff |
| **Decision Thresholds** | | |
| PeakQualityRatio | Beta | What fraction of max counts as "peak" |
| DegradationThreshold | Beta | Quality drop that triggers action |
| LookaheadTokens | Gamma | How far ahead to predict |
| UncertaintyThreshold | Beta | GP uncertainty that triggers concern |
| **Continuity Parameters** | | |
| RecentTurnsNeeded | Poisson-Gamma | Verbatim turns for continuity |
| CompressionTolerance | Beta | How well agent handles summaries |
| **Budget Allocation** | | |
| SummaryBudgetWeight | Beta | Fraction of budget for summary |
| FileBudgetWeight | Beta | Fraction of budget for files |
| DecisionBudgetWeight | Beta | Fraction of budget for decisions |
| MaxDecisionsToKeep | Poisson-Gamma | How many decisions to retain |

---

## Core Data Structures

### Learned Distributions

```go
// core/handoff/learned_params.go

// LearnedWeight - Beta distribution for weights/thresholds in [0,1]
type LearnedWeight struct {
    Alpha            float64 `json:"alpha"`
    Beta             float64 `json:"beta"`
    EffectiveSamples float64 `json:"effective_samples"`
    PriorAlpha       float64 `json:"prior_alpha"`
    PriorBeta        float64 `json:"prior_beta"`
}

func (w *LearnedWeight) Mean() float64 {
    return w.Alpha / (w.Alpha + w.Beta)
}

func (w *LearnedWeight) Variance() float64 {
    sum := w.Alpha + w.Beta
    return (w.Alpha * w.Beta) / (sum * sum * (sum + 1))
}

func (w *LearnedWeight) Sample() float64 {
    return betaSample(w.Alpha, w.Beta)
}

func (w *LearnedWeight) Confidence() float64 {
    return 1.0 - 1.0/(1.0 + w.EffectiveSamples/10.0)
}

func (w *LearnedWeight) CredibleInterval() (low, high float64) {
    return betaQuantile(0.025, w.Alpha, w.Beta),
           betaQuantile(0.975, w.Alpha, w.Beta)
}

func (w *LearnedWeight) Update(observation float64, config *UpdateConfig) {
    // Exponential decay for recency
    w.Alpha *= config.DecayFactor
    w.Beta *= config.DecayFactor
    w.EffectiveSamples *= config.DecayFactor

    // Bayesian update
    w.Alpha += observation
    w.Beta += (1 - observation)
    w.EffectiveSamples++

    // Drift protection - blend toward prior if too confident
    if w.EffectiveSamples > config.MaxEffectiveSamples {
        w.Alpha = w.Alpha*config.DriftRate + w.PriorAlpha*(1-config.DriftRate)
        w.Beta = w.Beta*config.DriftRate + w.PriorBeta*(1-config.DriftRate)
    }
}

// LearnedContextSize - Gamma distribution for positive context sizes
type LearnedContextSize struct {
    Alpha            float64 `json:"alpha"`
    Beta             float64 `json:"beta"`
    EffectiveSamples float64 `json:"effective_samples"`
    PriorAlpha       float64 `json:"prior_alpha"`
    PriorBeta        float64 `json:"prior_beta"`
}

func (l *LearnedContextSize) Mean() int {
    return int(l.Alpha / l.Beta)
}

func (l *LearnedContextSize) Sample() int {
    return int(gammaSample(l.Alpha, l.Beta))
}

func (l *LearnedContextSize) Confidence() float64 {
    return 1.0 - 1.0/(1.0 + l.EffectiveSamples/20.0)
}

func (l *LearnedContextSize) Update(observed int, config *UpdateConfig) {
    l.Alpha *= config.DecayFactor
    l.Beta *= config.DecayFactor
    l.EffectiveSamples *= config.DecayFactor

    l.Alpha += 1
    l.Beta += 1.0 / float64(observed)
    l.EffectiveSamples++

    if l.EffectiveSamples > config.MaxEffectiveSamples {
        l.Alpha = l.Alpha*config.DriftRate + l.PriorAlpha*(1-config.DriftRate)
        l.Beta = l.Beta*config.DriftRate + l.PriorBeta*(1-config.DriftRate)
    }
}

// LearnedCount - Poisson-Gamma for count data
type LearnedCount struct {
    Alpha            float64 `json:"alpha"`
    Beta             float64 `json:"beta"`
    EffectiveSamples float64 `json:"effective_samples"`
    PriorAlpha       float64 `json:"prior_alpha"`
    PriorBeta        float64 `json:"prior_beta"`
}

func (c *LearnedCount) Mean() int {
    return int(math.Round(c.Alpha / c.Beta))
}

func (c *LearnedCount) Sample() int {
    lambda := gammaSample(c.Alpha, c.Beta)
    return poissonSample(lambda)
}

func (c *LearnedCount) Confidence() float64 {
    return 1.0 - 1.0/(1.0 + c.EffectiveSamples/15.0)
}
```

### Hierarchical Blending

```go
// core/handoff/hierarchical.go

type leveledParam struct {
    param      interface{}  // *LearnedWeight, *LearnedContextSize, or *LearnedCount
    confidence float64
}

// blendWeight combines Beta distributions across hierarchy
func blendWeight(levels []leveledParam, explore bool) float64 {
    totalConf := 0.0
    for _, l := range levels {
        totalConf += l.confidence
    }
    if totalConf == 0 {
        totalConf = 1
    }

    var effectiveAlpha, effectiveBeta float64
    for _, l := range levels {
        w := l.param.(*LearnedWeight)
        weight := l.confidence / totalConf
        effectiveAlpha += w.Alpha * weight
        effectiveBeta += w.Beta * weight
    }

    effective := &LearnedWeight{Alpha: effectiveAlpha, Beta: effectiveBeta}

    if explore {
        return effective.Sample()
    }
    return effective.Mean()
}

// blendContextSize combines Gamma distributions across hierarchy
func blendContextSize(levels []leveledParam, explore bool) int {
    totalConf := 0.0
    for _, l := range levels {
        totalConf += l.confidence
    }
    if totalConf == 0 {
        totalConf = 1
    }

    var effectiveAlpha, effectiveBeta float64
    for _, l := range levels {
        cs := l.param.(*LearnedContextSize)
        weight := l.confidence / totalConf
        effectiveAlpha += cs.Alpha * weight
        effectiveBeta += cs.Beta * weight
    }

    effective := &LearnedContextSize{Alpha: effectiveAlpha, Beta: effectiveBeta}

    if explore {
        return effective.Sample()
    }
    return effective.Mean()
}

// blendCount combines Poisson-Gamma distributions across hierarchy
func blendCount(levels []leveledParam, explore bool) int {
    totalConf := 0.0
    for _, l := range levels {
        totalConf += l.confidence
    }
    if totalConf == 0 {
        totalConf = 1
    }

    var effectiveAlpha, effectiveBeta float64
    for _, l := range levels {
        c := l.param.(*LearnedCount)
        weight := l.confidence / totalConf
        effectiveAlpha += c.Alpha * weight
        effectiveBeta += c.Beta * weight
    }

    effective := &LearnedCount{Alpha: effectiveAlpha, Beta: effectiveBeta}

    if explore {
        return effective.Sample()
    }
    return effective.Mean()
}
```

### Agent Handoff Profile

```go
// core/handoff/agent_profile.go

// AgentHandoffProfile contains ALL learned characteristics for a specific agent instance.
// Every parameter is a distribution with hierarchical priors for cold start.
type AgentHandoffProfile struct {
    // Identity (fixed at agent creation)
    AgentID       AgentID
    AgentType     AgentType  // Engineer, Librarian, Guide, etc.
    ModelID       ModelID    // sonnet-4.5-1m, opus-4.5-200k, haiku-4.5-200k
    ContextWindow int        // 1_000_000 or 200_000
    SessionID     SessionID

    // Learned GP for quality prediction
    QualityGP *AgentGaussianProcess

    // Derived profile points (all distributions)
    BurnInEnd           *LearnedContextSize
    PeakZoneStart       *LearnedContextSize
    PeakZoneEnd         *LearnedContextSize
    OptimalPreparedSize *LearnedContextSize

    // Decision thresholds (all distributions)
    PeakQualityRatio     *LearnedWeight
    DegradationThreshold *LearnedWeight
    LookaheadTokens      *LearnedContextSize
    UncertaintyThreshold *LearnedWeight

    // Continuity parameters (all distributions)
    RecentTurnsNeeded    *LearnedCount
    CompressionTolerance *LearnedWeight

    // Budget allocation (all distributions)
    SummaryBudgetWeight  *LearnedWeight
    FileBudgetWeight     *LearnedWeight
    DecisionBudgetWeight *LearnedWeight
    MaxDecisionsToKeep   *LearnedCount

    // Hierarchical priors for cold start
    AgentModelPrior *AgentHandoffProfile  // (AgentType, Model) shared
    ModelPrior      *AgentHandoffProfile  // Model-level shared
    GlobalPrior     *AgentHandoffProfile  // Global fallback
}

// GetEffectiveBurnInEnd returns burn-in with hierarchical blending
func (p *AgentHandoffProfile) GetEffectiveBurnInEnd(explore bool) int {
    return blendContextSize(
        []leveledParam{
            {p.BurnInEnd, p.BurnInEnd.Confidence()},
            {p.AgentModelPrior.BurnInEnd, p.AgentModelPrior.BurnInEnd.Confidence()},
            {p.ModelPrior.BurnInEnd, p.ModelPrior.BurnInEnd.Confidence()},
            {p.GlobalPrior.BurnInEnd, p.GlobalPrior.BurnInEnd.Confidence()},
        },
        explore,
    )
}

// GetEffectivePeakZoneEnd returns peak zone end with hierarchical blending
func (p *AgentHandoffProfile) GetEffectivePeakZoneEnd(explore bool) int {
    return blendContextSize(
        []leveledParam{
            {p.PeakZoneEnd, p.PeakZoneEnd.Confidence()},
            {p.AgentModelPrior.PeakZoneEnd, p.AgentModelPrior.PeakZoneEnd.Confidence()},
            {p.ModelPrior.PeakZoneEnd, p.ModelPrior.PeakZoneEnd.Confidence()},
            {p.GlobalPrior.PeakZoneEnd, p.GlobalPrior.PeakZoneEnd.Confidence()},
        },
        explore,
    )
}

// GetEffectivePeakQualityRatio returns threshold with hierarchical blending
func (p *AgentHandoffProfile) GetEffectivePeakQualityRatio(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {p.PeakQualityRatio, p.PeakQualityRatio.Confidence()},
            {p.AgentModelPrior.PeakQualityRatio, p.AgentModelPrior.PeakQualityRatio.Confidence()},
            {p.ModelPrior.PeakQualityRatio, p.ModelPrior.PeakQualityRatio.Confidence()},
            {p.GlobalPrior.PeakQualityRatio, p.GlobalPrior.PeakQualityRatio.Confidence()},
        },
        explore,
    )
}

// GetEffectiveRecentTurnsNeeded returns count with hierarchical blending
func (p *AgentHandoffProfile) GetEffectiveRecentTurnsNeeded(explore bool) int {
    return blendCount(
        []leveledParam{
            {p.RecentTurnsNeeded, p.RecentTurnsNeeded.Confidence()},
            {p.AgentModelPrior.RecentTurnsNeeded, p.AgentModelPrior.RecentTurnsNeeded.Confidence()},
            {p.ModelPrior.RecentTurnsNeeded, p.ModelPrior.RecentTurnsNeeded.Confidence()},
            {p.GlobalPrior.RecentTurnsNeeded, p.GlobalPrior.RecentTurnsNeeded.Confidence()},
        },
        explore,
    )
}

// GetEffectiveLookaheadTokens returns lookahead with hierarchical blending
func (p *AgentHandoffProfile) GetEffectiveLookaheadTokens(explore bool) int {
    return blendContextSize(
        []leveledParam{
            {p.LookaheadTokens, p.LookaheadTokens.Confidence()},
            {p.AgentModelPrior.LookaheadTokens, p.AgentModelPrior.LookaheadTokens.Confidence()},
            {p.ModelPrior.LookaheadTokens, p.ModelPrior.LookaheadTokens.Confidence()},
            {p.GlobalPrior.LookaheadTokens, p.GlobalPrior.LookaheadTokens.Confidence()},
        },
        explore,
    )
}

// GetEffectiveUncertaintyThreshold returns uncertainty threshold with hierarchical blending
func (p *AgentHandoffProfile) GetEffectiveUncertaintyThreshold(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {p.UncertaintyThreshold, p.UncertaintyThreshold.Confidence()},
            {p.AgentModelPrior.UncertaintyThreshold, p.AgentModelPrior.UncertaintyThreshold.Confidence()},
            {p.ModelPrior.UncertaintyThreshold, p.ModelPrior.UncertaintyThreshold.Confidence()},
            {p.GlobalPrior.UncertaintyThreshold, p.GlobalPrior.UncertaintyThreshold.Confidence()},
        },
        explore,
    )
}

// GetEffectiveDegradationThreshold returns degradation threshold with hierarchical blending
func (p *AgentHandoffProfile) GetEffectiveDegradationThreshold(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {p.DegradationThreshold, p.DegradationThreshold.Confidence()},
            {p.AgentModelPrior.DegradationThreshold, p.AgentModelPrior.DegradationThreshold.Confidence()},
            {p.ModelPrior.DegradationThreshold, p.ModelPrior.DegradationThreshold.Confidence()},
            {p.GlobalPrior.DegradationThreshold, p.GlobalPrior.DegradationThreshold.Confidence()},
        },
        explore,
    )
}

// GetEffectivePeakZoneStart returns peak zone start with hierarchical blending
func (p *AgentHandoffProfile) GetEffectivePeakZoneStart(explore bool) int {
    return blendContextSize(
        []leveledParam{
            {p.PeakZoneStart, p.PeakZoneStart.Confidence()},
            {p.AgentModelPrior.PeakZoneStart, p.AgentModelPrior.PeakZoneStart.Confidence()},
            {p.ModelPrior.PeakZoneStart, p.ModelPrior.PeakZoneStart.Confidence()},
            {p.GlobalPrior.PeakZoneStart, p.GlobalPrior.PeakZoneStart.Confidence()},
        },
        explore,
    )
}

// IsKnowledgeAgent returns true if this agent uses eviction instead of handoff
func (p *AgentHandoffProfile) IsKnowledgeAgent() bool {
    switch p.AgentType {
    case AgentTypeLibrarian, AgentTypeArchivalist, AgentTypeAcademic, AgentTypeArchitect:
        return true
    default:
        return false
    }
}
```

---

## Gaussian Process for Quality Prediction

```go
// core/handoff/agent_gp.go

// AgentGaussianProcess learns quality degradation for a specific agent instance.
// ALL hyperparameters are learned distributions, not hardcoded.
type AgentGaussianProcess struct {
    AgentID       AgentID
    ModelID       ModelID
    ContextWindow int

    // Observations (bounded, recency-weighted)
    Observations    []GPObservation
    MaxObservations int

    // GP Hyperparameters - ALL LEARNED
    LengthScale    *LearnedWeight  // Scaled by context window
    OutputVariance *LearnedWeight
    NoiseVariance  *LearnedWeight
    PriorBaseline  *LearnedWeight
    PriorDecay     *LearnedWeight

    // Sparse inducing points
    InducingPoints []GPObservation
    MaxInducing    int

    // Hierarchical priors
    AgentModelPrior *GPHyperparams
    ModelPrior      *GPHyperparams
    GlobalPrior     *GPHyperparams

    // Cached Cholesky
    choleskyL     *mat.Cholesky
    choleskyValid bool
}

type GPObservation struct {
    ContextSize int
    Quality     float64
    Timestamp   time.Time
    TurnNumber  int
    Weight      float64
}

type GPHyperparams struct {
    LengthScale    *LearnedWeight
    OutputVariance *LearnedWeight
    NoiseVariance  *LearnedWeight
    PriorBaseline  *LearnedWeight
    PriorDecay     *LearnedWeight
}

// PredictAtContextSize returns (mean, variance) using hierarchically-blended hyperparams
func (gp *AgentGaussianProcess) PredictAtContextSize(ctx int, explore bool) (mean, variance float64) {
    if len(gp.Observations) == 0 {
        return gp.priorMean(ctx, explore), gp.priorVariance(explore)
    }

    // Get effective hyperparams via hierarchical blending
    lengthScale := gp.getEffectiveLengthScale(explore) * float64(gp.ContextWindow)
    outputVar := gp.getEffectiveOutputVariance(explore)
    noiseVar := gp.getEffectiveNoiseVariance(explore)

    // Standard GP prediction with Matern 2.5 kernel
    kStar := gp.computeKernelVector(ctx, lengthScale, outputVar)
    kStarStar := gp.matern25(ctx, ctx, lengthScale, outputVar)

    gp.ensureCholesky(lengthScale, outputVar, noiseVar)

    y := gp.getObservationResiduals(explore)
    alpha := gp.solveCholesky(y)

    mean = gp.priorMean(ctx, explore) + dotProduct(kStar, alpha)

    v := gp.solveCholeskyLower(kStar)
    variance = kStarStar - dotProduct(v, v)
    variance = math.Max(variance, 1e-9)

    return mean, variance
}

func (gp *AgentGaussianProcess) getEffectiveLengthScale(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {gp.LengthScale, gp.LengthScale.Confidence()},
            {gp.AgentModelPrior.LengthScale, gp.AgentModelPrior.LengthScale.Confidence()},
            {gp.ModelPrior.LengthScale, gp.ModelPrior.LengthScale.Confidence()},
            {gp.GlobalPrior.LengthScale, gp.GlobalPrior.LengthScale.Confidence()},
        },
        explore,
    )
}

func (gp *AgentGaussianProcess) getEffectiveOutputVariance(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {gp.OutputVariance, gp.OutputVariance.Confidence()},
            {gp.AgentModelPrior.OutputVariance, gp.AgentModelPrior.OutputVariance.Confidence()},
            {gp.ModelPrior.OutputVariance, gp.ModelPrior.OutputVariance.Confidence()},
            {gp.GlobalPrior.OutputVariance, gp.GlobalPrior.OutputVariance.Confidence()},
        },
        explore,
    )
}

func (gp *AgentGaussianProcess) getEffectiveNoiseVariance(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {gp.NoiseVariance, gp.NoiseVariance.Confidence()},
            {gp.AgentModelPrior.NoiseVariance, gp.AgentModelPrior.NoiseVariance.Confidence()},
            {gp.ModelPrior.NoiseVariance, gp.ModelPrior.NoiseVariance.Confidence()},
            {gp.GlobalPrior.NoiseVariance, gp.GlobalPrior.NoiseVariance.Confidence()},
        },
        explore,
    )
}

func (gp *AgentGaussianProcess) getEffectiveBaseline(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {gp.PriorBaseline, gp.PriorBaseline.Confidence()},
            {gp.AgentModelPrior.PriorBaseline, gp.AgentModelPrior.PriorBaseline.Confidence()},
            {gp.ModelPrior.PriorBaseline, gp.ModelPrior.PriorBaseline.Confidence()},
            {gp.GlobalPrior.PriorBaseline, gp.GlobalPrior.PriorBaseline.Confidence()},
        },
        explore,
    )
}

func (gp *AgentGaussianProcess) getEffectiveDecay(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {gp.PriorDecay, gp.PriorDecay.Confidence()},
            {gp.AgentModelPrior.PriorDecay, gp.AgentModelPrior.PriorDecay.Confidence()},
            {gp.ModelPrior.PriorDecay, gp.ModelPrior.PriorDecay.Confidence()},
            {gp.GlobalPrior.PriorDecay, gp.GlobalPrior.PriorDecay.Confidence()},
        },
        explore,
    )
}

func (gp *AgentGaussianProcess) priorMean(ctx int, explore bool) float64 {
    baseline := gp.getEffectiveBaseline(explore)
    decay := gp.getEffectiveDecay(explore) / float64(gp.ContextWindow)
    return baseline - decay*float64(ctx)
}

func (gp *AgentGaussianProcess) priorVariance(explore bool) float64 {
    return gp.getEffectiveOutputVariance(explore)
}

// Matern 2.5 kernel (better for smooth degradation curves than RBF)
func (gp *AgentGaussianProcess) matern25(x1, x2 int, lengthScale, outputVar float64) float64 {
    r := math.Abs(float64(x1-x2)) / lengthScale
    sqrt5 := math.Sqrt(5)
    return outputVar * (1 + sqrt5*r + 5*r*r/3) * math.Exp(-sqrt5*r)
}

func (gp *AgentGaussianProcess) computeKernelVector(ctx int, lengthScale, outputVar float64) []float64 {
    kStar := make([]float64, len(gp.Observations))
    for i, obs := range gp.Observations {
        kStar[i] = gp.matern25(ctx, obs.ContextSize, lengthScale, outputVar)
    }
    return kStar
}

func (gp *AgentGaussianProcess) getObservationResiduals(explore bool) []float64 {
    y := make([]float64, len(gp.Observations))
    for i, obs := range gp.Observations {
        y[i] = obs.Quality - gp.priorMean(obs.ContextSize, explore)
    }
    return y
}

func (gp *AgentGaussianProcess) ensureCholesky(lengthScale, outputVar, noiseVar float64) {
    if gp.choleskyValid {
        return
    }

    n := len(gp.Observations)
    K := mat.NewSymDense(n, nil)

    for i := 0; i < n; i++ {
        for j := i; j < n; j++ {
            k := gp.matern25(gp.Observations[i].ContextSize, gp.Observations[j].ContextSize, lengthScale, outputVar)
            K.SetSym(i, j, k)
        }
        // Add noise variance to diagonal
        K.SetSym(i, i, K.At(i, i)+noiseVar)
    }

    // Add jitter for numerical stability
    jitter := 1e-6
    for {
        var chol mat.Cholesky
        if chol.Factorize(K) {
            gp.choleskyL = &chol
            gp.choleskyValid = true
            return
        }
        // Add more jitter if factorization fails
        for i := 0; i < n; i++ {
            K.SetSym(i, i, K.At(i, i)+jitter)
        }
        jitter *= 10
        if jitter > 1e-2 {
            // Fall back to identity + noise
            gp.choleskyL = nil
            gp.choleskyValid = true
            return
        }
    }
}

func (gp *AgentGaussianProcess) solveCholesky(y []float64) []float64 {
    if gp.choleskyL == nil {
        // Fallback: return zeros
        return make([]float64, len(y))
    }
    yVec := mat.NewVecDense(len(y), y)
    alpha := mat.NewVecDense(len(y), nil)
    gp.choleskyL.SolveVecTo(alpha, yVec)
    return alpha.RawVector().Data
}

func (gp *AgentGaussianProcess) solveCholeskyLower(kStar []float64) []float64 {
    if gp.choleskyL == nil {
        return make([]float64, len(kStar))
    }
    kVec := mat.NewVecDense(len(kStar), kStar)
    v := mat.NewVecDense(len(kStar), nil)
    // Solve L v = k*
    gp.choleskyL.LTo(nil).SolveVecTo(v, kVec)
    return v.RawVector().Data
}

// AddObservation adds a quality observation and invalidates cache
func (gp *AgentGaussianProcess) AddObservation(obs GPObservation) {
    gp.Observations = append(gp.Observations, obs)

    // Bound observations
    if len(gp.Observations) > gp.MaxObservations {
        // Keep most recent, compress old to inducing points
        gp.compressOldObservations()
    }

    gp.choleskyValid = false
}

func (gp *AgentGaussianProcess) compressOldObservations() {
    // Keep recent observations
    recentCount := gp.MaxObservations / 2
    if len(gp.Observations) <= recentCount {
        return
    }

    old := gp.Observations[:len(gp.Observations)-recentCount]
    recent := gp.Observations[len(gp.Observations)-recentCount:]

    // Compress old to inducing points (simple: uniform sampling)
    targetInducing := gp.MaxInducing - len(gp.InducingPoints)
    if targetInducing > 0 && len(old) > 0 {
        step := len(old) / targetInducing
        if step < 1 {
            step = 1
        }
        for i := 0; i < len(old); i += step {
            if len(gp.InducingPoints) < gp.MaxInducing {
                gp.InducingPoints = append(gp.InducingPoints, old[i])
            }
        }
    }

    gp.Observations = recent
}
```

---

## Handoff Decision Logic

```go
// core/handoff/decision.go

// ShouldTakeAction determines if handoff or eviction should be triggered.
// ALL parameters come from hierarchical blending - no hardcoded values.
func (h *HandoffController) ShouldTakeAction(
    profile *AgentHandoffProfile,
    currentCtxSize int,
    explore bool,
) (bool, ActionType) {
    gp := profile.QualityGP

    // ALL values from hierarchical blending
    peakZoneEnd := profile.GetEffectivePeakZoneEnd(explore)
    lookahead := profile.GetEffectiveLookaheadTokens(explore)
    uncertaintyThresh := profile.GetEffectiveUncertaintyThreshold(explore)
    dropThresh := profile.GetEffectiveDegradationThreshold(explore)
    qualityRatio := profile.GetEffectivePeakQualityRatio(explore)
    burnInEnd := profile.GetEffectiveBurnInEnd(false)

    // GP predictions with hierarchically-blended hyperparams
    currentQuality, currentUncertainty := gp.PredictAtContextSize(currentCtxSize, explore)
    futureQuality, _ := gp.PredictAtContextSize(currentCtxSize+lookahead, explore)

    // Peak quality reference
    peakCtxSize := profile.GetEffectivePeakZoneStart(false)
    peakQuality, _ := gp.PredictAtContextSize(peakCtxSize, false)

    // Decision using blended thresholds
    qualityThreshold := peakQuality * qualityRatio

    pastPeakZone := currentCtxSize > peakZoneEnd
    qualityDropping := (currentQuality - futureQuality) > dropThresh
    qualityBelowThreshold := currentQuality < qualityThreshold
    highUncertainty := currentUncertainty > uncertaintyThresh

    shouldAct := pastPeakZone ||
        (qualityDropping && futureQuality < qualityThreshold) ||
        qualityBelowThreshold ||
        (highUncertainty && currentCtxSize > burnInEnd)

    if !shouldAct {
        return false, ActionNone
    }

    // Action type based on agent category
    if profile.IsKnowledgeAgent() {
        return true, ActionEviction
    }
    return true, ActionHandoff
}

type ActionType int

const (
    ActionNone ActionType = iota
    ActionHandoff
    ActionEviction
)
```

---

## Prepared Context (Maintained Continuously)

```go
// core/handoff/prepared_context.go

// PreparedContext is maintained CONTINUOUSLY during normal operation.
// At handoff, it's trimmed to OptimalPreparedSize - no expensive rebuild.
// ALL budget allocations are learned distributions.
type PreparedContext struct {
    mu sync.RWMutex

    // Target configuration (from profile)
    TargetModel      ModelID
    TargetTokens     int
    RecentTurnsCount int

    // Continuously updated content
    Summary      *RollingSummary
    RecentTurns  *CircularBuffer[ConversationTurn]
    ActiveFiles  map[string]*FileState
    TaskState    *TaskState
    KeyDecisions []Decision

    // For GP/weight continuity
    QualityObservations []GPObservation
    WeightOverlay       *SessionOverlayState

    // Learned budget allocations (ALL distributions)
    SummaryBudgetWeight  *LearnedWeight
    FileBudgetWeight     *LearnedWeight
    DecisionBudgetWeight *LearnedWeight
    MaxDecisionsToKeep   *LearnedCount

    // Hierarchical priors
    AgentModelPrior *PreparedContextParams
    ModelPrior      *PreparedContextParams
    GlobalPrior     *PreparedContextParams
}

type PreparedContextParams struct {
    SummaryBudgetWeight  *LearnedWeight
    FileBudgetWeight     *LearnedWeight
    DecisionBudgetWeight *LearnedWeight
    MaxDecisionsToKeep   *LearnedCount
}

// Update is called after EVERY agent turn
func (p *PreparedContext) Update(turn *ConversationTurn, files []FileChange) {
    p.mu.Lock()
    defer p.mu.Unlock()

    p.RecentTurns.Push(turn)
    p.Summary.Incorporate(turn)

    for _, f := range files {
        p.ActiveFiles[f.Path] = f.State
    }
}

// Clone for handoff (copy-on-write, doesn't block updates)
func (p *PreparedContext) Clone() *PreparedContext {
    p.mu.RLock()
    defer p.mu.RUnlock()

    return &PreparedContext{
        TargetModel:          p.TargetModel,
        TargetTokens:         p.TargetTokens,
        RecentTurnsCount:     p.RecentTurnsCount,
        Summary:              p.Summary.Clone(),
        RecentTurns:          p.RecentTurns.Clone(),
        ActiveFiles:          cloneFileMap(p.ActiveFiles),
        TaskState:            p.TaskState.Clone(),
        KeyDecisions:         cloneDecisions(p.KeyDecisions),
        QualityObservations:  cloneObservations(p.QualityObservations),
        WeightOverlay:        p.WeightOverlay.Clone(),
        SummaryBudgetWeight:  p.SummaryBudgetWeight,
        FileBudgetWeight:     p.FileBudgetWeight,
        DecisionBudgetWeight: p.DecisionBudgetWeight,
        MaxDecisionsToKeep:   p.MaxDecisionsToKeep,
        AgentModelPrior:      p.AgentModelPrior,
        ModelPrior:           p.ModelPrior,
        GlobalPrior:          p.GlobalPrior,
    }
}

// TrimToSize trims to target - ALL allocations from hierarchical blending
func (p *PreparedContext) TrimToSize(targetTokens int, explore bool) {
    current := p.EstimateTokens()
    if current <= targetTokens {
        return
    }

    // Trim recent turns to learned count
    p.RecentTurns.TruncateTo(p.RecentTurnsCount)

    remainingBudget := targetTokens - p.RecentTurns.EstimateTokens() - p.TaskState.EstimateTokens()

    // Get effective budget weights via hierarchical blending
    summaryWeight := p.getEffectiveSummaryBudgetWeight(explore)
    fileWeight := p.getEffectiveFileBudgetWeight(explore)
    maxDecisions := p.getEffectiveMaxDecisionsToKeep(explore)

    // Normalize weights to ensure they sum properly
    totalWeight := summaryWeight + fileWeight
    if totalWeight > 0 {
        summaryWeight /= totalWeight
        fileWeight /= totalWeight
    } else {
        summaryWeight, fileWeight = 0.5, 0.5
    }

    // Apply learned allocations
    summaryBudget := int(float64(remainingBudget) * summaryWeight)
    p.Summary.CompressTo(summaryBudget)

    fileBudget := int(float64(remainingBudget) * fileWeight)
    p.pruneFilesToBudget(fileBudget)

    // Learned decision retention
    if len(p.KeyDecisions) > maxDecisions {
        p.KeyDecisions = p.KeyDecisions[len(p.KeyDecisions)-maxDecisions:]
    }
}

func (p *PreparedContext) getEffectiveSummaryBudgetWeight(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {p.SummaryBudgetWeight, p.SummaryBudgetWeight.Confidence()},
            {p.AgentModelPrior.SummaryBudgetWeight, p.AgentModelPrior.SummaryBudgetWeight.Confidence()},
            {p.ModelPrior.SummaryBudgetWeight, p.ModelPrior.SummaryBudgetWeight.Confidence()},
            {p.GlobalPrior.SummaryBudgetWeight, p.GlobalPrior.SummaryBudgetWeight.Confidence()},
        },
        explore,
    )
}

func (p *PreparedContext) getEffectiveFileBudgetWeight(explore bool) float64 {
    return blendWeight(
        []leveledParam{
            {p.FileBudgetWeight, p.FileBudgetWeight.Confidence()},
            {p.AgentModelPrior.FileBudgetWeight, p.AgentModelPrior.FileBudgetWeight.Confidence()},
            {p.ModelPrior.FileBudgetWeight, p.ModelPrior.FileBudgetWeight.Confidence()},
            {p.GlobalPrior.FileBudgetWeight, p.GlobalPrior.FileBudgetWeight.Confidence()},
        },
        explore,
    )
}

func (p *PreparedContext) getEffectiveMaxDecisionsToKeep(explore bool) int {
    return blendCount(
        []leveledParam{
            {p.MaxDecisionsToKeep, p.MaxDecisionsToKeep.Confidence()},
            {p.AgentModelPrior.MaxDecisionsToKeep, p.AgentModelPrior.MaxDecisionsToKeep.Confidence()},
            {p.ModelPrior.MaxDecisionsToKeep, p.ModelPrior.MaxDecisionsToKeep.Confidence()},
            {p.GlobalPrior.MaxDecisionsToKeep, p.GlobalPrior.MaxDecisionsToKeep.Confidence()},
        },
        explore,
    )
}

func (p *PreparedContext) EstimateTokens() int {
    return p.Summary.EstimateTokens() +
        p.RecentTurns.EstimateTokens() +
        p.estimateFileTokens() +
        p.TaskState.EstimateTokens() +
        p.estimateDecisionTokens()
}

func (p *PreparedContext) estimateFileTokens() int {
    total := 0
    for _, fs := range p.ActiveFiles {
        total += fs.EstimateTokens()
    }
    return total
}

func (p *PreparedContext) estimateDecisionTokens() int {
    total := 0
    for _, d := range p.KeyDecisions {
        total += d.EstimateTokens()
    }
    return total
}

func (p *PreparedContext) pruneFilesToBudget(budget int) {
    if p.estimateFileTokens() <= budget {
        return
    }

    // Sort files by recency (most recent first)
    type fileEntry struct {
        path  string
        state *FileState
    }
    files := make([]fileEntry, 0, len(p.ActiveFiles))
    for path, state := range p.ActiveFiles {
        files = append(files, fileEntry{path, state})
    }
    sort.Slice(files, func(i, j int) bool {
        return files[i].state.LastAccess.After(files[j].state.LastAccess)
    })

    // Keep files until budget exhausted
    newFiles := make(map[string]*FileState)
    usedBudget := 0
    for _, f := range files {
        tokens := f.state.EstimateTokens()
        if usedBudget+tokens <= budget {
            newFiles[f.path] = f.state
            usedBudget += tokens
        }
    }
    p.ActiveFiles = newFiles
}
```

---

## Weakly Informative Global Priors

```go
// core/handoff/priors.go

// InitGlobalPriors creates weakly informative priors for cold start.
// These are starting points that converge to learned values.
func InitGlobalPriors() *AgentHandoffProfile {
    return &AgentHandoffProfile{
        // GP Hyperparameters - wide uncertainty
        QualityGP: &AgentGaussianProcess{
            LengthScale:    &LearnedWeight{Alpha: 2, Beta: 2, PriorAlpha: 2, PriorBeta: 2},       // Mean 0.5, high var
            OutputVariance: &LearnedWeight{Alpha: 2, Beta: 3, PriorAlpha: 2, PriorBeta: 3},       // Mean ~0.4
            NoiseVariance:  &LearnedWeight{Alpha: 1, Beta: 9, PriorAlpha: 1, PriorBeta: 9},       // Mean ~0.1
            PriorBaseline:  &LearnedWeight{Alpha: 8, Beta: 2, PriorAlpha: 8, PriorBeta: 2},       // Mean ~0.8
            PriorDecay:     &LearnedWeight{Alpha: 1, Beta: 99, PriorAlpha: 1, PriorBeta: 99},     // Mean ~0.01
            MaxObservations: 200,
            MaxInducing:     100,
        },

        // Profile points - wide Gamma priors (will be learned per context window)
        BurnInEnd:           &LearnedContextSize{Alpha: 2, Beta: 0.0001, PriorAlpha: 2, PriorBeta: 0.0001},    // Mean ~20K
        PeakZoneStart:       &LearnedContextSize{Alpha: 2.5, Beta: 0.0001, PriorAlpha: 2.5, PriorBeta: 0.0001}, // Mean ~25K
        PeakZoneEnd:         &LearnedContextSize{Alpha: 8, Beta: 0.0001, PriorAlpha: 8, PriorBeta: 0.0001},     // Mean ~80K
        OptimalPreparedSize: &LearnedContextSize{Alpha: 2.5, Beta: 0.0001, PriorAlpha: 2.5, PriorBeta: 0.0001}, // Mean ~25K

        // Decision thresholds - centered priors
        PeakQualityRatio:     &LearnedWeight{Alpha: 8.5, Beta: 1.5, PriorAlpha: 8.5, PriorBeta: 1.5},  // Mean ~0.85
        DegradationThreshold: &LearnedWeight{Alpha: 1, Beta: 9, PriorAlpha: 1, PriorBeta: 9},          // Mean ~0.1
        LookaheadTokens:      &LearnedContextSize{Alpha: 2, Beta: 0.0004, PriorAlpha: 2, PriorBeta: 0.0004}, // Mean ~5K
        UncertaintyThreshold: &LearnedWeight{Alpha: 2, Beta: 8, PriorAlpha: 2, PriorBeta: 8},          // Mean ~0.2

        // Continuity - moderate priors
        RecentTurnsNeeded:    &LearnedCount{Alpha: 4, Beta: 1, PriorAlpha: 4, PriorBeta: 1},  // Mean ~4
        CompressionTolerance: &LearnedWeight{Alpha: 9, Beta: 1, PriorAlpha: 9, PriorBeta: 1}, // Mean ~0.9

        // Budget allocation - balanced priors
        SummaryBudgetWeight:  &LearnedWeight{Alpha: 4, Beta: 6, PriorAlpha: 4, PriorBeta: 6},  // Mean ~0.4
        FileBudgetWeight:     &LearnedWeight{Alpha: 4, Beta: 6, PriorAlpha: 4, PriorBeta: 6},  // Mean ~0.4
        DecisionBudgetWeight: &LearnedWeight{Alpha: 2, Beta: 8, PriorAlpha: 2, PriorBeta: 8},  // Mean ~0.2
        MaxDecisionsToKeep:   &LearnedCount{Alpha: 5, Beta: 1, PriorAlpha: 5, PriorBeta: 1},   // Mean ~5
    }
}

// InitModelPriors creates model-specific priors based on context window.
func InitModelPriors(modelID ModelID, contextWindow int) *AgentHandoffProfile {
    global := InitGlobalPriors()

    // Scale priors based on context window
    scaleFactor := float64(contextWindow) / 200000.0  // Relative to 200K baseline

    scaled := global.Clone()

    // Scale context-size distributions
    scaled.BurnInEnd.Alpha *= scaleFactor
    scaled.BurnInEnd.PriorAlpha *= scaleFactor
    scaled.PeakZoneStart.Alpha *= scaleFactor
    scaled.PeakZoneStart.PriorAlpha *= scaleFactor
    scaled.PeakZoneEnd.Alpha *= scaleFactor
    scaled.PeakZoneEnd.PriorAlpha *= scaleFactor
    scaled.OptimalPreparedSize.Alpha *= scaleFactor
    scaled.OptimalPreparedSize.PriorAlpha *= scaleFactor
    scaled.LookaheadTokens.Alpha *= scaleFactor
    scaled.LookaheadTokens.PriorAlpha *= scaleFactor

    return scaled
}

// InitAgentModelPriors creates (AgentType, Model) specific priors.
func InitAgentModelPriors(agentType AgentType, modelID ModelID, contextWindow int) *AgentHandoffProfile {
    modelPrior := InitModelPriors(modelID, contextWindow)

    // Agent-type specific adjustments (starting points, will be learned)
    switch agentType {
    case AgentTypeLibrarian, AgentTypeArchivalist:
        // Knowledge agents with 1M context - longer peak zones
        // No changes needed - model prior already scaled for context window

    case AgentTypeEngineer:
        // Complex tasks - may need more recent turns for continuity
        modelPrior.RecentTurnsNeeded.Alpha = 5
        modelPrior.RecentTurnsNeeded.PriorAlpha = 5

    case AgentTypeGuide, AgentTypeOrchestrator:
        // Lightweight routing - faster degradation typical
        modelPrior.PeakZoneEnd.Alpha *= 0.6
        modelPrior.PeakZoneEnd.PriorAlpha *= 0.6

    case AgentTypeInspector, AgentTypeTester:
        // Validation agents - moderate settings
        // Use model defaults
    }

    return modelPrior
}
```

---

## Handoff Execution

```go
// core/handoff/executor.go

type HandoffExecutor struct {
    profileLearner *ProfileLearner
    archivalist    ArchivalistClient
    logger         *slog.Logger
}

// ExecuteHandoff performs same-type handoff for non-knowledge agents.
// PreparedContext is already built - this is just swap + trim.
func (e *HandoffExecutor) ExecuteHandoff(
    ctx context.Context,
    oldAgent Agent,
    profile *AgentHandoffProfile,
    preparedContext *PreparedContext,
) (Agent, error) {
    // 1. Clone PreparedContext (copy-on-write, non-blocking)
    cloned := preparedContext.Clone()

    // 2. Trim to learned OptimalPreparedSize
    optimalSize := profile.GetEffectiveOptimalPreparedSize(false)
    cloned.TrimToSize(optimalSize, false)

    // 3. Create new agent instance (same type, same model)
    newAgent, err := e.createAgent(profile.AgentType, profile.ModelID, profile.ContextWindow)
    if err != nil {
        return nil, fmt.Errorf("failed to create new agent: %w", err)
    }

    // 4. Inject PreparedContext
    if err := newAgent.InjectHandoffState(cloned); err != nil {
        newAgent.Terminate()
        return nil, fmt.Errorf("failed to inject handoff state: %w", err)
    }

    // 5. Archive old agent state (async, non-blocking)
    go e.archiveAsync(ctx, oldAgent, profile)

    // 6. Terminate old agent
    if err := oldAgent.Terminate(); err != nil {
        e.logger.Warn("old agent termination failed", "error", err)
        // Non-fatal - new agent is already active
    }

    return newAgent, nil
}

func (e *HandoffExecutor) archiveAsync(ctx context.Context, agent Agent, profile *AgentHandoffProfile) {
    archiveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    state := agent.GetArchivableState()
    if err := e.archivalist.Archive(archiveCtx, state); err != nil {
        e.logger.Warn("failed to archive agent state", "error", err)
    }
}

func (e *HandoffExecutor) createAgent(agentType AgentType, modelID ModelID, contextWindow int) (Agent, error) {
    // Implementation depends on agent factory
    // Returns new instance of same (AgentType, ModelID) combination
    return nil, nil // Placeholder
}

func (p *AgentHandoffProfile) GetEffectiveOptimalPreparedSize(explore bool) int {
    return blendContextSize(
        []leveledParam{
            {p.OptimalPreparedSize, p.OptimalPreparedSize.Confidence()},
            {p.AgentModelPrior.OptimalPreparedSize, p.AgentModelPrior.OptimalPreparedSize.Confidence()},
            {p.ModelPrior.OptimalPreparedSize, p.ModelPrior.OptimalPreparedSize.Confidence()},
            {p.GlobalPrior.OptimalPreparedSize, p.GlobalPrior.OptimalPreparedSize.Confidence()},
        },
        explore,
    )
}
```

---

## Integration with Existing Infrastructure

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    INTEGRATION WITH EXISTING SYSTEMS                                 │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  MEMORY.md / SCORING.md                    HANDOFF.md                               │
│  ═══════════════════════                   ═══════════════                          │
│  HierarchicalWeightSystem ◄───────────────► Hierarchical parameter pooling          │
│  SessionWeightManager ◄───────────────────► WAL persistence, session isolation      │
│  RobustWeightDistribution ◄───────────────► LearnedWeight, LearnedContextSize       │
│  RecoveryManager ◄────────────────────────► Crash recovery for GP/profile state     │
│                                                                                     │
│  CONTEXT.md                                HANDOFF.md                               │
│  ═══════════════                           ═══════════════                          │
│  Tiered Storage (HOT/WARM/COLD) ◄─────────► Eviction targets for knowledge agents   │
│  ContextReference [CTX-REF-xxx] ◄─────────► Evicted content references              │
│  AccessTracker ◄──────────────────────────► HOT tier promotion for PreparedContext  │
│  UniversalContentStore ◄──────────────────► Archived handoff state                  │
│                                                                                     │
│  ARCHITECTURE.md                           HANDOFF.md                               │
│  ════════════════                          ═══════════════                          │
│  Agent goroutine model ◄──────────────────► Standalone vs Pipeline categorization   │
│  Pipeline execution ◄─────────────────────► Pipeline agent handoff within goroutine │
│  Archivalist ◄────────────────────────────► Async state archival                    │
│  SignalDispatcher ◄───────────────────────► Handoff/eviction event signals          │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## WAL Persistence

```go
// core/handoff/persistence.go

// WAL entry types for handoff system
const (
    EntryGPObservation     WALEntryType = "gp_observation"
    EntryProfileUpdate     WALEntryType = "profile_update"
    EntryHandoffEvent      WALEntryType = "handoff_event"
    EntryEvictionEvent     WALEntryType = "eviction_event"
)

type GPObservationEntry struct {
    AgentID     string    `json:"agent_id"`
    AgentType   string    `json:"agent_type"`
    ModelID     string    `json:"model_id"`
    ContextSize int       `json:"context_size"`
    Quality     float64   `json:"quality"`
    TurnNumber  int       `json:"turn_number"`
    Timestamp   time.Time `json:"timestamp"`
}

type ProfileUpdateEntry struct {
    AgentType   string                 `json:"agent_type"`
    ModelID     string                 `json:"model_id"`
    Level       string                 `json:"level"`  // "instance", "agent_model", "model", "global"
    ParamName   string                 `json:"param_name"`
    Alpha       float64                `json:"alpha"`
    Beta        float64                `json:"beta"`
    Timestamp   time.Time              `json:"timestamp"`
}

type HandoffEventEntry struct {
    OldAgentID  string    `json:"old_agent_id"`
    NewAgentID  string    `json:"new_agent_id"`
    AgentType   string    `json:"agent_type"`
    ModelID     string    `json:"model_id"`
    Reason      string    `json:"reason"`
    ContextSize int       `json:"context_size"`
    TargetSize  int       `json:"target_size"`
    Timestamp   time.Time `json:"timestamp"`
}

// Recovery replays WAL to restore GP and profile state
func (p *ProfileLearner) RecoverFromWAL(wal *WriteAheadLog) error {
    return wal.Replay(func(entryType WALEntryType, data []byte) error {
        switch entryType {
        case EntryGPObservation:
            var entry GPObservationEntry
            if err := json.Unmarshal(data, &entry); err != nil {
                return err
            }
            return p.replayGPObservation(entry)

        case EntryProfileUpdate:
            var entry ProfileUpdateEntry
            if err := json.Unmarshal(data, &entry); err != nil {
                return err
            }
            return p.replayProfileUpdate(entry)

        default:
            // Unknown entry type - skip
            return nil
        }
    })
}

func (p *ProfileLearner) replayGPObservation(entry GPObservationEntry) error {
    profile := p.getOrCreateProfile(AgentType(entry.AgentType), ModelID(entry.ModelID))
    profile.QualityGP.AddObservation(GPObservation{
        ContextSize: entry.ContextSize,
        Quality:     entry.Quality,
        TurnNumber:  entry.TurnNumber,
        Timestamp:   entry.Timestamp,
        Weight:      1.0,
    })
    return nil
}

func (p *ProfileLearner) replayProfileUpdate(entry ProfileUpdateEntry) error {
    // Update the appropriate level's parameter
    // Implementation depends on which param is being updated
    return nil
}
```

---

## Memory Cost

| Component | Size | Notes |
|-----------|------|-------|
| GP per agent | ~45 KB | Sparse GP, bounded inducing points |
| Profile per agent | ~2 KB | All learned distributions |
| PreparedContext per agent | 10-90 KB | Maintained continuously |
| Hierarchical priors (shared) | ~10 KB | Global + per-model + per-(agent,model) |
| **Per active agent** | ~60-140 KB | Negligible |

---

## CPU Cost

| Operation | Cost | Frequency |
|-----------|------|-----------|
| PreparedContext.Update() | ~0.2 ms | Every turn |
| GP prediction | ~1-2 ms | Every turn |
| Hierarchical blending | ~0.1 ms | Every decision |
| Cholesky recomputation | ~5-10 ms | Every ~10 observations |
| **Handoff execution** | **~1-2 ms** | On trigger |

---

## Summary

- **ALL parameters are learned distributions** - no hardcoded values
- **Hierarchical Bayesian pooling** handles cold start automatically
- **Per-agent-instance learning** - each (AgentType, Model) combination learns its own curves
- **PreparedContext maintained continuously** - handoff is pointer swap + trim
- **GP detects degradation** - replaces fixed % thresholds
- **Knowledge agents evict, others handoff** - different strategies for different agent categories
- **1M context window for Librarian/Archivalist** - fundamentally different curves than 200K agents
- **Integrates with existing infrastructure** - MEMORY.md, SCORING.md, CONTEXT.md, ARCHITECTURE.md
