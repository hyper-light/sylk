# Quality-Based Scoring Architecture

## Overview

Sylk implements a **Bayesian Quality-Adjusted Ranking (BQAR)** system that dynamically weights search results based on quality signals. This system applies to both VectorDB (semantic search) and Document DB (Bleve full-text search), with unified scoring when querying both.

**Core Principle**: Similarity scores are necessary but not sufficient. Quality signals (usage feedback, staleness, authority, trust) must adjust ranking to surface the most valuable content.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    QUALITY-ADJUSTED RANKING OVERVIEW                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Traditional:  final_score = similarity                                     │
│                                                                             │
│  BQAR:         final_score = f(similarity, quality_signals, learned_weights)│
│                                                                             │
│  Where quality_signals include:                                             │
│    - Usage feedback (was this helpful before?)                              │
│    - Staleness (is this current?)                                           │
│    - Authority (is this from a trusted source?)                             │
│    - Cross-validation (do both DBs agree?)                                  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Table of Contents

1. [Quality Signal Taxonomy](#1-quality-signal-taxonomy)
2. [Agent-Specific Quality Models](#2-agent-specific-quality-models)
3. [Hierarchical Weight Learning](#3-hierarchical-weight-learning)
4. [Quality Signal Storage](#4-quality-signal-storage)
5. [Feedback Attribution System](#5-feedback-attribution-system)
6. [Citation Tracking](#6-citation-tracking)
7. [VectorDB Integration](#7-vectordb-integration)
8. [Document DB Integration](#8-document-db-integration)
9. [Unified Hybrid Search](#9-unified-hybrid-search)
10. [Sudden Shift Detection](#10-sudden-shift-detection)
11. [Weight Isolation & Persistence](#11-weight-isolation--persistence)
12. [Integration with Existing Infrastructure](#12-integration-with-existing-infrastructure)
13. [Implementation Files](#13-implementation-files)

---

## 1. Quality Signal Taxonomy

### Signal Categories (Priority Order)

| Priority | Category | Description |
|----------|----------|-------------|
| **Highest** | Usage Feedback | Was this result helpful in past queries? |
| **Highest** | Query-Specific Quality | Domain match, historical relevance for similar queries |
| **High** | Temporal Quality | Content age, last validation, staleness decay |
| **High** | Structural Quality | Parsing confidence, completeness, content density |
| **High** | Relational Quality | Citation count, graph centrality, cross-validation |
| **Medium** | Source Authority | Official docs vs blog, peer-reviewed vs informal |

### Agent-Specific Signal Priorities

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ LIBRARIAN (DomainCode)                                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│ Primary: STALENESS - local code is truth, but MUST be current               │
│                                                                             │
│ • Local code = absolute truth (TrustLevel: Ground = 100)                    │
│ • Staleness is CRITICAL - code older than last git commit penalized         │
│ • Authority signal N/A (all local code equally authoritative)               │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│ ACADEMIC (DomainAcademic)                                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│ Primary: AUTHORITY + CITATIONS - old but authoritative is valuable          │
│                                                                             │
│ • Source authority matters (peer-reviewed > blog > StackOverflow)           │
│ • Citation count is strong signal (heavily cited = valuable)                │
│ • Age penalty is GENTLE - classic algorithms/papers retain value            │
│ • Old + heavily cited can OVERRIDE recency                                  │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│ ARCHIVALIST (DomainHistory)                                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│ Primary: HISTORICAL RELEVANCE - what was useful for similar contexts?       │
│                                                                             │
│ • Usage feedback is critical (what helped before?)                          │
│ • Session proximity matters (related to current conversation?)              │
│ • Staleness: NONE for decisions/failures (history is history)               │
│ • Staleness: MILD for session-specific content                              │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│ CROSS-DOMAIN (Architect coordination)                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│ Primary: CROSS-VALIDATION - both DBs agree on relevance                     │
│                                                                             │
│ • Bleve + VectorDB agreement is strong signal                               │
│ • Source diversity matters (results from multiple domains)                  │
│ • Query coverage (does result set cover query intent?)                      │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Agent-Specific Quality Models

**CORE PRINCIPLE: Nothing is hardcoded. Everything is a posterior.** All weights, decay rates, and thresholds are learned from observations. See [MEMORY.md](./MEMORY.md) for the full Bayesian learning architecture.

### Quality Weight Configuration

```go
// core/quality/weights.go

// QualityWeights configures how quality signals affect final score.
// Note: These are NOT point estimates - they are distributions.
type QualityWeights struct {
    // Source weights (similarity-based) - learned posteriors
    VectorWeight *LearnedWeight `json:"vector_weight"`
    GraphWeight  *LearnedWeight `json:"graph_weight"`
    BleveWeight  *LearnedWeight `json:"bleve_weight"`

    // Quality signal weights - learned posteriors
    TrustLevelWeight    *LearnedWeight `json:"trust_level_weight"`
    StalenessWeight     *LearnedWeight `json:"staleness_weight"`
    UsageFeedbackWeight *LearnedWeight `json:"usage_feedback_weight"`
    AuthorityWeight     *LearnedWeight `json:"authority_weight"`

    // Cross-validation bonus - learned posterior
    CrossValidationBonus *LearnedWeight `json:"cross_validation_bonus"`
}

// LearnedWeight is a Beta distribution posterior, not a point estimate.
type LearnedWeight struct {
    Alpha            float64 `json:"alpha"`              // Posterior α
    Beta             float64 `json:"beta"`               // Posterior β
    EffectiveSamples float64 `json:"effective_samples"`  // For decay tracking
    PriorAlpha       float64 `json:"prior_alpha"`        // For drift protection
    PriorBeta        float64 `json:"prior_beta"`         // For drift protection
}

func (w *LearnedWeight) Mean() float64 {
    return w.Alpha / (w.Alpha + w.Beta)
}

func (w *LearnedWeight) Variance() float64 {
    sum := w.Alpha + w.Beta
    return (w.Alpha * w.Beta) / (sum * sum * (sum + 1))
}

func (w *LearnedWeight) Sample() float64 {
    return betaSample(w.Alpha, w.Beta)  // Thompson Sampling
}

func (w *LearnedWeight) Confidence() float64 {
    // Higher effective samples = more confident
    return 1.0 - 1.0/(1.0 + w.EffectiveSamples/10.0)
}
```

### Prior Weights by Agent Domain (Weakly Informative)

```go
// PriorQualityWeights returns weakly informative priors for an agent domain.
// These are NOT used directly - they are priors that get updated by observations.
// The actual weights used at query time are SAMPLED from POSTERIORS.
func PriorQualityWeights(agentDomain Domain) *QualityWeights {
    switch agentDomain {
    case DomainCode:
        // Librarian: prior belief that staleness matters more
        return &QualityWeights{
            VectorWeight:         &LearnedWeight{Alpha: 7, Beta: 13, PriorAlpha: 7, PriorBeta: 13},   // Prior mean 0.35
            GraphWeight:          &LearnedWeight{Alpha: 3, Beta: 17, PriorAlpha: 3, PriorBeta: 17},   // Prior mean 0.15
            TrustLevelWeight:     &LearnedWeight{Alpha: 1, Beta: 19, PriorAlpha: 1, PriorBeta: 19},   // Prior mean 0.05
            StalenessWeight:      &LearnedWeight{Alpha: 5, Beta: 15, PriorAlpha: 5, PriorBeta: 15},   // Prior mean 0.25 (high for code)
            UsageFeedbackWeight:  &LearnedWeight{Alpha: 3, Beta: 17, PriorAlpha: 3, PriorBeta: 17},   // Prior mean 0.15
            AuthorityWeight:      &LearnedWeight{Alpha: 1, Beta: 99, PriorAlpha: 1, PriorBeta: 99},   // Prior mean ~0 (N/A for code)
            CrossValidationBonus: &LearnedWeight{Alpha: 1, Beta: 19, PriorAlpha: 1, PriorBeta: 19},   // Prior mean 0.05
        }
    case DomainAcademic:
        // Academic: prior belief that authority and citations matter
        return &QualityWeights{
            VectorWeight:         &LearnedWeight{Alpha: 6, Beta: 14, PriorAlpha: 6, PriorBeta: 14},   // Prior mean 0.30
            GraphWeight:          &LearnedWeight{Alpha: 2, Beta: 18, PriorAlpha: 2, PriorBeta: 18},   // Prior mean 0.10
            TrustLevelWeight:     &LearnedWeight{Alpha: 3, Beta: 17, PriorAlpha: 3, PriorBeta: 17},   // Prior mean 0.15
            StalenessWeight:      &LearnedWeight{Alpha: 1, Beta: 19, PriorAlpha: 1, PriorBeta: 19},   // Prior mean 0.05 (low - old papers valuable)
            UsageFeedbackWeight:  &LearnedWeight{Alpha: 3, Beta: 17, PriorAlpha: 3, PriorBeta: 17},   // Prior mean 0.15
            AuthorityWeight:      &LearnedWeight{Alpha: 4, Beta: 16, PriorAlpha: 4, PriorBeta: 16},   // Prior mean 0.20 (high for academic)
            CrossValidationBonus: &LearnedWeight{Alpha: 1, Beta: 19, PriorAlpha: 1, PriorBeta: 19},   // Prior mean 0.05
        }
    case DomainHistory:
        // Archivalist: prior belief that usage feedback matters most
        return &QualityWeights{
            VectorWeight:         &LearnedWeight{Alpha: 6, Beta: 14, PriorAlpha: 6, PriorBeta: 14},   // Prior mean 0.30
            GraphWeight:          &LearnedWeight{Alpha: 3, Beta: 17, PriorAlpha: 3, PriorBeta: 17},   // Prior mean 0.15
            TrustLevelWeight:     &LearnedWeight{Alpha: 2, Beta: 18, PriorAlpha: 2, PriorBeta: 18},   // Prior mean 0.10
            StalenessWeight:      &LearnedWeight{Alpha: 2, Beta: 18, PriorAlpha: 2, PriorBeta: 18},   // Prior mean 0.10
            UsageFeedbackWeight:  &LearnedWeight{Alpha: 5, Beta: 15, PriorAlpha: 5, PriorBeta: 15},   // Prior mean 0.25 (high - what helped before)
            AuthorityWeight:      &LearnedWeight{Alpha: 1, Beta: 19, PriorAlpha: 1, PriorBeta: 19},   // Prior mean 0.05
            CrossValidationBonus: &LearnedWeight{Alpha: 1, Beta: 19, PriorAlpha: 1, PriorBeta: 19},   // Prior mean 0.05
        }
    default:
        // Balanced, high-uncertainty priors
        return &QualityWeights{
            VectorWeight:         &LearnedWeight{Alpha: 3, Beta: 7, PriorAlpha: 3, PriorBeta: 7},     // Prior mean 0.30, high variance
            GraphWeight:          &LearnedWeight{Alpha: 1.5, Beta: 8.5, PriorAlpha: 1.5, PriorBeta: 8.5},
            TrustLevelWeight:     &LearnedWeight{Alpha: 1, Beta: 9, PriorAlpha: 1, PriorBeta: 9},
            StalenessWeight:      &LearnedWeight{Alpha: 1.5, Beta: 8.5, PriorAlpha: 1.5, PriorBeta: 8.5},
            UsageFeedbackWeight:  &LearnedWeight{Alpha: 1.5, Beta: 8.5, PriorAlpha: 1.5, PriorBeta: 8.5},
            AuthorityWeight:      &LearnedWeight{Alpha: 1, Beta: 9, PriorAlpha: 1, PriorBeta: 9},
            CrossValidationBonus: &LearnedWeight{Alpha: 0.5, Beta: 9.5, PriorAlpha: 0.5, PriorBeta: 9.5},
        }
    }
}
```

### Staleness Decay (Learned, Not Hardcoded)

```go
// LearnedDecayRate represents a Gamma-distributed decay rate posterior.
// λ ~ Gamma(α, β) where E[λ] = α/β, Var[λ] = α/β²
type LearnedDecayRate struct {
    Alpha            float64 `json:"alpha"`              // Shape parameter
    Beta             float64 `json:"beta"`               // Rate parameter
    EffectiveSamples float64 `json:"effective_samples"`  // For recency weighting
}

func (d *LearnedDecayRate) Mean() float64 {
    return d.Alpha / d.Beta
}

func (d *LearnedDecayRate) Sample() float64 {
    return gammaSample(d.Alpha, d.Beta)  // Thompson Sampling
}

func (d *LearnedDecayRate) HalfLife() time.Duration {
    // Half-life = ln(2) / λ
    lambda := d.Mean()
    halfLifeHours := math.Ln2 / lambda
    return time.Duration(halfLifeHours) * time.Hour
}

// DomainDecayRates holds learned decay rates per domain.
// These are POSTERIORS updated from observations of document accuracy vs age.
type DomainDecayRates struct {
    Code     *LearnedDecayRate `json:"code"`
    Academic *LearnedDecayRate `json:"academic"`
    History  *LearnedDecayRate `json:"history"`
}

// PriorDecayRates returns weakly informative Gamma priors for decay rates.
// These encode our PRIOR BELIEF but are UPDATED by observations.
func PriorDecayRates() *DomainDecayRates {
    return &DomainDecayRates{
        // Code: Prior belief that decay is fast
        // E[λ] = 2/200 = 0.01 (half-life ~69 hours ≈ 3 days)
        // But wide variance allows learning actual rate
        Code: &LearnedDecayRate{Alpha: 2, Beta: 200},

        // Academic: Prior belief that decay is very slow
        // E[λ] = 2/2000 = 0.001 (half-life ~693 hours ≈ 29 days)
        Academic: &LearnedDecayRate{Alpha: 2, Beta: 2000},

        // History: Prior belief that decay is medium
        // E[λ] = 2/500 = 0.004 (half-life ~173 hours ≈ 7 days)
        History: &LearnedDecayRate{Alpha: 2, Beta: 500},
    }
}

// ComputeStaleness returns a 0-1 score based on content age using LEARNED decay rate.
// NOT hardcoded - uses posterior sample or mean depending on explore/exploit mode.
func (qs *QualityScorer) ComputeStaleness(modTime time.Time, domain Domain, explore bool) float64 {
    age := time.Since(modTime).Hours()

    // Get learned decay rate for this domain
    var decayRate *LearnedDecayRate
    switch domain {
    case DomainCode:
        decayRate = qs.decayRates.Code
    case DomainAcademic:
        decayRate = qs.decayRates.Academic
    case DomainHistory:
        decayRate = qs.decayRates.History
    default:
        // Fallback: geometric mean of domain rates
        decayRate = &LearnedDecayRate{
            Alpha: (qs.decayRates.Code.Alpha + qs.decayRates.History.Alpha) / 2,
            Beta:  (qs.decayRates.Code.Beta + qs.decayRates.History.Beta) / 2,
        }
    }

    // Thompson Sampling: sample from posterior for exploration
    var lambda float64
    if explore {
        lambda = decayRate.Sample()
    } else {
        lambda = decayRate.Mean()
    }

    // Exponential decay: score = e^(-λt)
    return math.Exp(-lambda * age)
}

// UpdateDecayRate updates the posterior when we observe document accuracy.
// Observation: document at age A was accurate (1.0) or inaccurate (0.0).
func (qs *QualityScorer) UpdateDecayRate(domain Domain, ageHours float64, wasAccurate bool) {
    var decayRate *LearnedDecayRate
    switch domain {
    case DomainCode:
        decayRate = qs.decayRates.Code
    case DomainAcademic:
        decayRate = qs.decayRates.Academic
    case DomainHistory:
        decayRate = qs.decayRates.History
    default:
        return
    }

    // Apply temporal decay to existing evidence (recency weighting)
    decayFactor := 0.999
    decayRate.Alpha *= decayFactor
    decayRate.Beta *= decayFactor
    decayRate.EffectiveSamples *= decayFactor

    // Update posterior based on observation
    // If document was accurate at this age, evidence for SLOWER decay (smaller λ)
    // If document was inaccurate at this age, evidence for FASTER decay (larger λ)
    if wasAccurate {
        // Accurate at age A → decrease λ estimate
        // Bayesian update: more consistent with smaller λ
        decayRate.Beta += ageHours  // Increases E[λ]'s denominator
    } else {
        // Inaccurate at age A → increase λ estimate
        decayRate.Alpha += 1  // Increases E[λ]'s numerator
    }

    decayRate.EffectiveSamples++
}
```

---

## 3. Hierarchical Weight Learning

The system uses a **five-level hierarchical Bayesian model** with **Thompson Sampling** for exploration-exploitation balance. This is a full cross-product hierarchy with **bidirectional information flow**.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    FIVE-LEVEL HIERARCHICAL WEIGHT LEARNING                           │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  LAYER 5: GLOBAL PRIORS (weakly informative, shared across system)                  │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  GlobalSignalPrior[signal] ~ Beta(α_global, β_global)                         │  │
│  │  GlobalDecayPrior          ~ Gamma(2, 200)                                    │  │
│  │  GlobalThresholdPrior      ~ Beta(3, 3)  # Centered at 0.5                    │  │
│  │                                                                               │  │
│  │  - Learned from ALL usage across ALL domains/models/agents                    │  │
│  │  - Provides baseline: "usage_feedback is generally important"                 │  │
│  │  - Slow update rate (batch updates, high evidence threshold)                  │  │
│  │  - Information flows UP from lower layers (empirical Bayes)                   │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│                      ┌───────────────────┼───────────────────┐                      │
│                      ▼                   ▼                   ▼                      │
│  LAYER 4: DOMAIN PRIORS (Code, Academic, History)                                   │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  DomainSignalPrior[domain][signal] ~ Beta(α_d, β_d)                           │  │
│  │  DomainDecayRate[domain]           ~ Gamma(α_d, β_d) | GlobalDecay            │  │
│  │  DomainThreshold[domain]           ~ Beta(α_d, β_d) | GlobalThreshold         │  │
│  │                                                                               │  │
│  │  - Inherits from global, adapts to domain characteristics                     │  │
│  │  - "For Code domain, staleness matters MORE than global average"              │  │
│  │  - Medium-slow update rate                                                    │  │
│  │  - Information flows UP (posteriors inform global) and DOWN (global informs   │  │
│  │    domains with few observations)                                             │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│            ┌───────────────┬─────────────┼─────────────┬───────────────┐            │
│            ▼               ▼             ▼             ▼               ▼            │
│  LAYER 3: MODEL PRIORS (Sonnet 4.5, Opus 4.5, Haiku 4.5, Codex 5.2, etc.)          │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  ModelDegradation[model] ~ GaussianProcess(                                   │  │
│  │      mean = μ_model(t),           # Prior mean function                       │  │
│  │      kernel = Matern(ν=2.5, ℓ)    # Smoothness prior                          │  │
│  │  )                                                                            │  │
│  │  ModelQualityBaseline[model] ~ Normal(μ, σ) | Domain                          │  │
│  │                                                                               │  │
│  │  - NO ASSUMPTION about exponential/hyperbolic/linear - GP learns shape        │  │
│  │  - Learned from: "Model M at turn T produced quality Q"                       │  │
│  │  - Can discover warm-up periods, plateaus, sudden drops                       │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│            ┌───────────────┬─────────────┼─────────────┬───────────────┐            │
│            ▼               ▼             ▼             ▼               ▼            │
│  LAYER 2: AGENT PRIORS (Librarian, Engineer, Inspector, Academic, Archivalist)     │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  AgentWeights[agent][signal] ~ Beta(α_a, β_a) | Domain × Model                │  │
│  │  AgentDegradationOffset[agent][model] ~ Normal(0, σ_agent)                    │  │
│  │  AgentThresholdAdjustment[agent] ~ Normal(0, σ_threshold)                     │  │
│  │                                                                               │  │
│  │  - Inherits from domain AND model (cross-product)                             │  │
│  │  - "For Librarian using Opus 4.5, degradation is SLOWER than model average"   │  │
│  │  - Medium update rate (per-session batch updates)                             │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│                                          ▼                                          │
│  LAYER 1: SESSION POSTERIORS (live, per-session, complete isolation)               │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  SessionWeights[signal] = sample from Agent × Model × Domain prior            │  │
│  │  SessionGPObservations = [(turn, quality), ...]  # Full history in session    │  │
│  │  SessionThresholdFeedback = [(decision, outcome), ...]                        │  │
│  │                                                                               │  │
│  │  - Thompson Sampling: sample weights, observe outcome, update                 │  │
│  │  - Handles exploration: occasionally tries non-obvious weightings             │  │
│  │  - Fast adaptation within conversation                                        │  │
│  │  - COMPLETE isolation from other sessions (copy-on-write)                     │  │
│  │  - Merges to project base on session complete (three-way merge)               │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Hierarchy Structure: Full Cross-Product with Bidirectional Flow

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    CROSS-PRODUCT LEAF NODES                                          │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  Each leaf node is a SPECIFIC combination:                                          │
│                                                                                     │
│      (Domain, Model, Agent, Signal) → LearnedWeight                                 │
│                                                                                     │
│  Example leaf nodes:                                                                │
│                                                                                     │
│      (Code, Opus4.5, Librarian, Staleness)   → Beta(α=12, β=8)  # High staleness   │
│      (Code, Opus4.5, Librarian, Authority)   → Beta(α=1, β=99)  # Low authority    │
│      (Academic, Opus4.5, Academic, Authority) → Beta(α=15, β=5) # High authority   │
│      (History, Haiku4.5, Archivalist, Usage) → Beta(α=18, β=7)  # High usage       │
│                                                                                     │
│  Total leaf nodes = |Domains| × |Models| × |Agents| × |Signals|                    │
│                   = 3 × 4 × 5 × 7 = 420 leaf distributions                          │
│                                                                                     │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│  BIDIRECTIONAL INFORMATION FLOW                                                     │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│                                                                                     │
│  UPWARD FLOW (Empirical Bayes):                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │  Observations at leaves → aggregate → update parent priors                  │    │
│  │                                                                             │    │
│  │  Example: Many Code domain observations show staleness weight ~0.25         │    │
│  │           → Update DomainPrior[Code][Staleness] to center near 0.25         │    │
│  │           → This helps new agents in Code domain start with better prior    │    │
│  └─────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                     │
│  DOWNWARD FLOW (Hierarchical Priors):                                               │
│  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │  Parent priors → inform child priors → regularize toward parents            │    │
│  │                                                                             │    │
│  │  Example: New agent "Reviewer" has no observations                          │    │
│  │           → Uses DomainPrior[Code] as starting point                        │    │
│  │           → Weighted by confidence: α_child = α_parent * weight             │    │
│  └─────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                     │
│  CROSS-DOMAIN SHARING (Partial Pooling):                                            │
│  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │  Similar domains share information with shrinkage                           │    │
│  │                                                                             │    │
│  │  Example: Code and History both care about recency                          │    │
│  │           → Partial pooling of decay rate estimates                         │    │
│  │           → More confident domain provides info to less confident           │    │
│  └─────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Implementation

```go
// core/quality/hierarchical_weights.go

// HierarchicalWeightSystem implements 5-level Bayesian hierarchy with full cross-product.
type HierarchicalWeightSystem struct {
    // Level 5: Global priors (shared across system)
    GlobalPriors     map[QualitySignal]*LearnedWeight
    GlobalDecayPrior *LearnedDecayRate
    GlobalThreshold  *LearnedThreshold

    // Level 4: Domain-specific priors
    DomainPriors     map[Domain]map[QualitySignal]*LearnedWeight
    DomainDecayRates *DomainDecayRates
    DomainThresholds map[Domain]*LearnedThreshold

    // Level 3: Model-specific priors (includes GP for degradation)
    ModelGPs         map[ModelID]*ModelGaussianProcess  // GP per model
    ModelBaselines   map[ModelID]*LearnedWeight

    // Level 2: Agent-specific priors (cross-product with Domain × Model)
    AgentWeights     map[AgentType]map[QualitySignal]*LearnedWeight
    AgentDegOffsets  map[AgentType]map[ModelID]*LearnedWeight  // Degradation offsets
    AgentThreshAdj   map[AgentType]*LearnedWeight              // Threshold adjustments

    // Level 1: Session-scoped (ephemeral, per-session, copy-on-write)
    // Managed by SessionWeightManager (see Section 11)

    // Session-level cache
    ContextCache *ristretto.Cache  // query_hash → sampled weights

    // Update coordination
    GlobalUpdateBuffer chan Observation
    DomainUpdateBuffer map[Domain]chan Observation
    ModelUpdateBuffer  map[ModelID]chan Observation
    AgentUpdateBuffer  map[AgentType]chan Observation
    BatchSize          int
}

// GetWeights returns quality signal weights for a specific context.
// Samples from the full hierarchy: Global → Domain → Model → Agent → Session.
func (h *HierarchicalWeightSystem) GetWeights(
    ctx context.Context,
    domain Domain,
    model ModelID,
    agent AgentType,
    queryContext *QueryContext,
    explore bool,  // Thompson Sampling exploration mode
) map[QualitySignal]float64 {

    // Check context cache first
    cacheKey := h.computeCacheKey(domain, model, agent, queryContext)
    if cached, ok := h.ContextCache.Get(cacheKey); ok {
        return cached.(map[QualitySignal]float64)
    }

    weights := make(map[QualitySignal]float64)

    for signal := range h.GlobalPriors {
        // Compute effective posterior from hierarchy
        effectivePosterior := h.computeEffectivePosterior(domain, model, agent, signal)

        // Thompson Sampling: sample or use mean
        var weight float64
        if explore {
            weight = effectivePosterior.Sample()
        } else {
            weight = effectivePosterior.Mean()
        }

        // Query-context adjustment (learned modifiers)
        contextModifier := h.getContextModifier(queryContext, signal)
        weights[signal] = weight * contextModifier
    }

    // Normalize to sum to 1.0
    h.normalizeWeights(weights)

    // Cache for this query context
    h.ContextCache.SetWithTTL(cacheKey, weights, 1, 5*time.Minute)

    return weights
}

// computeEffectivePosterior combines all hierarchy levels into effective posterior.
func (h *HierarchicalWeightSystem) computeEffectivePosterior(
    domain Domain,
    model ModelID,
    agent AgentType,
    signal QualitySignal,
) *LearnedWeight {
    // Get priors at each level
    globalPrior := h.GlobalPriors[signal]
    domainPrior := h.DomainPriors[domain][signal]
    agentPrior := h.AgentWeights[agent][signal]

    // Combine using hierarchical shrinkage
    // More observations at lower level = less shrinkage toward parent
    globalConf := globalPrior.Confidence()
    domainConf := domainPrior.Confidence()
    agentConf := agentPrior.Confidence()

    // Weighted combination (more confident level gets more weight)
    totalConf := globalConf + domainConf + agentConf
    if totalConf == 0 {
        totalConf = 1  // Prevent division by zero
    }

    combinedAlpha := (globalPrior.Alpha*globalConf +
        domainPrior.Alpha*domainConf +
        agentPrior.Alpha*agentConf) / totalConf
    combinedBeta := (globalPrior.Beta*globalConf +
        domainPrior.Beta*domainConf +
        agentPrior.Beta*agentConf) / totalConf

    return &LearnedWeight{
        Alpha:            combinedAlpha,
        Beta:             combinedBeta,
        EffectiveSamples: globalPrior.EffectiveSamples + domainPrior.EffectiveSamples + agentPrior.EffectiveSamples,
    }
}

// ObserveOutcome records feedback and triggers hierarchical updates.
func (h *HierarchicalWeightSystem) ObserveOutcome(obs Observation) {
    // Propagate to all relevant levels
    h.AgentUpdateBuffer[obs.Agent] <- obs
    h.ModelUpdateBuffer[obs.Model] <- obs
    h.DomainUpdateBuffer[obs.Domain] <- obs
    h.GlobalUpdateBuffer <- obs
}
```

### Gaussian Process for Model Degradation

```go
// core/quality/model_gp.go

// ModelGaussianProcess represents a GP for learning model quality degradation curves.
// NO ASSUMPTION about exponential/hyperbolic/linear - learns actual shape from data.
type ModelGaussianProcess struct {
    ModelID    ModelID

    // Observations: (turn, quality) pairs
    Observations []GPObservation  // Full history during session

    // GP Hyperparameters (learned)
    OutputVariance *LearnedWeight   // σ² ~ InverseGamma via reparameterization
    LengthScale    *LearnedWeight   // ℓ ~ LogNormal via reparameterization
    NoiseVariance  float64          // Observation noise

    // Prior mean function: m(t) = baseline - decay * t (weakly informative)
    PriorBaseline  float64  // e.g., 0.8
    PriorDecay     float64  // e.g., 0.001 (very weak)

    // Cached Cholesky decomposition (updated incrementally)
    choleskyL      [][]float64
    choleskyValid  bool

    // Inducing points for sparse approximation (used at checkpoint)
    InducingPoints []GPObservation
    MaxInducing    int  // e.g., 50
}

type GPObservation struct {
    Turn    int     `json:"turn"`
    Quality float64 `json:"quality"`
    Time    int64   `json:"time"`  // Unix timestamp for temporal ordering
}

// Predict returns the predicted quality at turn t with uncertainty.
func (gp *ModelGaussianProcess) Predict(turn int) (mean float64, stddev float64) {
    if len(gp.Observations) == 0 {
        // No observations - return prior
        return gp.priorMean(turn), math.Sqrt(gp.OutputVariance.Mean())
    }

    // Compute posterior mean and variance using standard GP equations
    // K = kernel matrix for observations
    // k* = kernel vector between turn and observations
    // μ* = k* K⁻¹ y
    // σ²* = k** - k* K⁻¹ k*ᵀ

    k_star := gp.computeKernelVector(turn)
    k_star_star := gp.kernel(turn, turn)

    // Use cached Cholesky for efficient solve
    gp.ensureCholesky()

    alpha := gp.solveCholesky(gp.getObservationValues())
    mean = gp.priorMean(turn) + dotProduct(k_star, alpha)

    v := gp.solveCholeskyLower(k_star)
    variance := k_star_star - dotProduct(v, v)
    if variance < 0 {
        variance = 0  // Numerical stability
    }
    stddev = math.Sqrt(variance)

    return mean, stddev
}

// Sample returns a sampled quality prediction (for Thompson Sampling).
func (gp *ModelGaussianProcess) Sample(turn int) float64 {
    mean, stddev := gp.Predict(turn)
    return mean + stddev*rand.NormFloat64()
}

// AddObservation adds a new (turn, quality) observation and updates the GP.
func (gp *ModelGaussianProcess) AddObservation(turn int, quality float64) {
    gp.Observations = append(gp.Observations, GPObservation{
        Turn:    turn,
        Quality: quality,
        Time:    time.Now().Unix(),
    })

    // Incremental Cholesky update (O(n²) not O(n³))
    gp.updateCholeskyIncremental()
}

// CompressToInducingPoints creates sparse approximation for checkpoint/merge.
func (gp *ModelGaussianProcess) CompressToInducingPoints() {
    if len(gp.Observations) <= gp.MaxInducing {
        gp.InducingPoints = gp.Observations
        return
    }

    // Select inducing points using k-means in turn space
    // Or use uniform spacing + include extremes
    gp.InducingPoints = selectInducingPoints(gp.Observations, gp.MaxInducing)
}

// kernel computes the Matern 2.5 kernel between two turns.
func (gp *ModelGaussianProcess) kernel(t1, t2 int) float64 {
    sigma2 := gp.OutputVariance.Mean()
    ell := gp.LengthScale.Mean()

    r := math.Abs(float64(t1-t2)) / ell
    sqrtFive := math.Sqrt(5)

    // Matern 2.5: k(r) = σ² * (1 + √5*r + 5r²/3) * exp(-√5*r)
    return sigma2 * (1 + sqrtFive*r + 5*r*r/3) * math.Exp(-sqrtFive*r)
}

// priorMean returns the prior mean function value at turn t.
func (gp *ModelGaussianProcess) priorMean(turn int) float64 {
    return gp.PriorBaseline - gp.PriorDecay*float64(turn)
}
```

### GP Storage Strategy: Hybrid Approach

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    GP STORAGE STRATEGY (HYBRID)                                      │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  DURING SESSION: Full Observation History                                           │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  • Keep all (turn, quality) observations in memory                            │  │
│  │  • O(n²) updates acceptable for session-length n (~100 turns typical)         │  │
│  │  • Exact GP inference (no approximation)                                      │  │
│  │  • Logged to WAL for crash recovery                                           │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│                                          ▼                                          │
│  AT CHECKPOINT/MERGE: Inducing Point Compression                                    │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  • Compress to M inducing points (M=50 typical)                               │  │
│  │  • Preserves predictive distribution with O(1/M) approximation error          │  │
│  │  • Storage: ~50 × 2 × 8 bytes = 800 bytes per model                           │  │
│  │  • Sufficient for accurate handoff decisions                                  │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│                                          ▼                                          │
│  MERGING GPS FROM CONCURRENT SESSIONS:                                              │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  • Each session has inducing points for model M                               │  │
│  │  • Merge by combining inducing point sets                                     │  │
│  │  • Re-select M points from combined set (maintains sparsity)                  │  │
│  │  • Product-of-experts approximation for conflicting observations              │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                     │
│  COMPUTATIONAL COST:                                                                │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │  • Session observation: O(n²) Cholesky update, n ≤ 100 typical → ~10ms        │  │
│  │  • Prediction: O(n) with cached Cholesky → ~0.1ms                             │  │
│  │  • Compression: O(n × M) k-means → ~1ms                                       │  │
│  │  • Merge: O(M²) for M inducing points → ~0.1ms                                │  │
│  │  • Total overhead per session: negligible                                     │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Threshold Learning (Bayesian Decision Theory)

```go
// core/quality/learned_threshold.go

// LearnedThreshold represents a learned decision threshold using Bayesian decision theory.
// NOT hardcoded - learned from review outcomes and feedback.
type LearnedThreshold struct {
    // Base threshold posterior (per domain)
    Posterior *LearnedWeight  // Beta distribution over optimal threshold

    // Learned transition model: P(good | confidence)
    TransitionMu    float64  // Mean of transition
    TransitionSigma float64  // Uncertainty in transition

    // Cost ratio (can also be learned)
    FalsePositiveCost float64  // Cost of accepting bad content
    FalseNegativeCost float64  // Cost of rejecting good content

    // Observations from review queue
    Observations []ThresholdObservation
}

type ThresholdObservation struct {
    Confidence float64 `json:"confidence"`
    Outcome    bool    `json:"outcome"`  // true = good (approved), false = bad (rejected)
    Time       int64   `json:"time"`
}

// OptimalThreshold returns the learned optimal threshold.
// Can sample for exploration or use mean for exploitation.
func (t *LearnedThreshold) OptimalThreshold(explore bool) float64 {
    if explore {
        return t.Posterior.Sample()
    }
    return t.Posterior.Mean()
}

// UpdateFromReview updates the threshold posterior based on review outcome.
func (t *LearnedThreshold) UpdateFromReview(confidence float64, wasGood bool) {
    t.Observations = append(t.Observations, ThresholdObservation{
        Confidence: confidence,
        Outcome:    wasGood,
        Time:       time.Now().Unix(),
    })

    // Update transition model using logistic regression (online)
    // P(good | c) = sigmoid((c - μ) / σ)
    t.updateTransitionModel()

    // Compute optimal threshold using Bayesian decision theory
    // τ* = argmin E[L(τ)] where L(τ) = C_FP * P(FP|τ) + C_FN * P(FN|τ)
    optimalThreshold := t.computeOptimalThreshold()

    // Update posterior to center near optimal
    // Evidence strength based on number of observations
    evidenceWeight := math.Min(float64(len(t.Observations))/100.0, 1.0)
    t.Posterior.Alpha = t.Posterior.PriorAlpha * (1 - evidenceWeight) + optimalThreshold * 10 * evidenceWeight
    t.Posterior.Beta = t.Posterior.PriorBeta * (1 - evidenceWeight) + (1 - optimalThreshold) * 10 * evidenceWeight
    t.Posterior.EffectiveSamples = float64(len(t.Observations))
}

// computeOptimalThreshold uses learned P(good|c) to find loss-minimizing threshold.
func (t *LearnedThreshold) computeOptimalThreshold() float64 {
    // Grid search over threshold values
    bestThreshold := 0.5
    bestLoss := math.MaxFloat64

    for tau := 0.1; tau <= 0.9; tau += 0.01 {
        // P(FP | τ) = P(bad AND accepted) = P(accepted | bad) * P(bad)
        // P(FN | τ) = P(good AND rejected) = P(rejected | good) * P(good)

        pFP := t.estimateFalsePositiveRate(tau)
        pFN := t.estimateFalseNegativeRate(tau)

        loss := t.FalsePositiveCost*pFP + t.FalseNegativeCost*pFN

        if loss < bestLoss {
            bestLoss = loss
            bestThreshold = tau
        }
    }

    return bestThreshold
}

// AgentThresholdAdjustment allows per-agent deviation from domain threshold.
func (h *HierarchicalWeightSystem) GetEffectiveThreshold(
    domain Domain,
    agent AgentType,
    explore bool,
) float64 {
    // Base domain threshold
    baseThreshold := h.DomainThresholds[domain].OptimalThreshold(explore)

    // Agent-specific adjustment
    adjustment := h.AgentThreshAdj[agent]
    var offset float64
    if explore {
        offset = adjustment.Sample() - 0.5  // Center adjustment around 0
    } else {
        offset = adjustment.Mean() - 0.5
    }

    // Effective threshold = base + offset, clamped to [0.1, 0.9]
    effective := baseThreshold + offset*0.2  // Scale offset
    return math.Max(0.1, math.Min(0.9, effective))
}
```

### RobustWeightDistribution

Uses the same `RobustWeightDistribution` pattern from CONTEXT.md, extended for hierarchical updates:

```go
// From CONTEXT.md - core/context/adaptive_reward.go

// RobustWeightDistribution handles outliers, non-stationarity, and cold start.
// Extended for hierarchical Bayesian updates.
type RobustWeightDistribution struct {
    Alpha float64
    Beta  float64

    // Robustness tracking
    effectiveSamples float64
    priorAlpha       float64  // Original prior for drift
    priorBeta        float64

    // Hierarchical shrinkage
    parentAlpha float64  // Parent level's current α
    parentBeta  float64  // Parent level's current β
    shrinkage   float64  // How much to shrink toward parent (0-1)
}

func (w *RobustWeightDistribution) Mean() float64 {
    return w.Alpha / (w.Alpha + w.Beta)
}

func (w *RobustWeightDistribution) Variance() float64 {
    sum := w.Alpha + w.Beta
    return (w.Alpha * w.Beta) / (sum * sum * (sum + 1))
}

func (w *RobustWeightDistribution) Sample() float64 {
    return betaSample(w.Alpha, w.Beta)
}

func (w *RobustWeightDistribution) Confidence() float64 {
    return 1.0 - 1.0/(1.0 + w.effectiveSamples/10.0)
}

func (w *RobustWeightDistribution) Update(observation float64, satisfaction float64, config *UpdateConfig) {
    // 1. Outlier detection - reject observations far from current belief
    mean := w.Mean()
    stddev := math.Sqrt(w.Variance())

    if stddev > 0 {
        zScore := math.Abs(observation-mean) / stddev
        if zScore > config.OutlierThreshold {  // e.g., 3.0
            return  // Reject outlier
        }
    }

    // 2. Exponential decay on existing evidence (recency weighting)
    w.Alpha *= config.DecayFactor  // e.g., 0.999
    w.Beta *= config.DecayFactor
    w.effectiveSamples *= config.DecayFactor

    // 3. Add new observation
    weight := math.Abs(satisfaction)
    if satisfaction > 0 {
        w.Alpha += weight * observation
    } else {
        w.Beta += weight * (1 - observation)
    }
    w.effectiveSamples++

    // 4. Hierarchical shrinkage - blend toward parent when uncertain
    if w.effectiveSamples < config.MinSamplesForIndependence {
        shrinkage := w.shrinkage * (1.0 - w.Confidence())
        w.Alpha = w.Alpha*(1-shrinkage) + w.parentAlpha*shrinkage
        w.Beta = w.Beta*(1-shrinkage) + w.parentBeta*shrinkage
    }

    // 5. Drift protection - blend toward prior if too confident
    if w.effectiveSamples > config.MaxEffectiveSamples {
        w.Alpha = w.Alpha*config.DriftRate + w.priorAlpha*(1-config.DriftRate)
        w.Beta = w.Beta*config.DriftRate + w.priorBeta*(1-config.DriftRate)
    }
}

// SyncFromParent updates parent reference for hierarchical shrinkage.
func (w *RobustWeightDistribution) SyncFromParent(parentAlpha, parentBeta float64) {
    w.parentAlpha = parentAlpha
    w.parentBeta = parentBeta
}
```

---

## 4. Quality Signal Storage

### Three-Tier Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ QUALITY SIGNAL STORAGE ARCHITECTURE                                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ TIER 1: STATIC SIGNALS (stored at ingestion, rarely change)         │    │
│  │ Storage: SQLite columns on document/vector tables                   │    │
│  │ Update: On re-index or explicit refresh                             │    │
│  │                                                                     │    │
│  │ • source_authority_score   REAL    -- computed at ingestion         │    │
│  │ • structural_quality_score REAL    -- parsing confidence            │    │
│  │ • external_citation_count  INTEGER -- from external APIs            │    │
│  │ • content_hash             TEXT    -- for staleness detection       │    │
│  │ • ingestion_timestamp      INTEGER -- creation time                 │    │
│  │ • last_validation_time     INTEGER -- when we confirmed accuracy    │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                           │                                                 │
│                           ▼                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ TIER 2: AGGREGATED DYNAMIC SIGNALS (async updated, time-decayed)    │    │
│  │ Storage: Separate quality_signals table with TTL                    │    │
│  │ Update: Write-behind from feedback events, periodic decay           │    │
│  │                                                                     │    │
│  │ • retrieval_count_decayed  REAL    -- EMA of retrieval frequency    │    │
│  │ • inclusion_count_decayed  REAL    -- EMA of context inclusions     │    │
│  │ • reference_count_decayed  REAL    -- EMA of explicit references    │    │
│  │ • positive_feedback_score  REAL    -- aggregated positive signals   │    │
│  │ • negative_feedback_score  REAL    -- aggregated negative signals   │    │
│  │ • internal_citation_count  INTEGER -- how often agents cite this    │    │
│  │ • last_feedback_time       INTEGER -- for recency weighting         │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                           │                                                 │
│                           ▼                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ TIER 3: QUERY-CONTEXT SIGNALS (computed on-demand, cached briefly)  │    │
│  │ Storage: Ristretto cache with 60s TTL                               │    │
│  │ Compute: At query time from Tier 1 + Tier 2 + query context         │    │
│  │                                                                     │    │
│  │ • domain_match_score       -- from DE classification                │    │
│  │ • query_similarity_boost   -- semantic distance to query            │    │
│  │ • conversation_relevance   -- relationship to recent queries        │    │
│  │ • cross_validation_score   -- Bleve + Vector agreement              │    │
│  │ • staleness_score          -- computed from timestamps + git        │    │
│  │ • COMPOSITE_QUALITY_SCORE  -- final weighted combination            │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Unified Signal Store Schema

```sql
-- core/quality/schema.sql
-- Keyed by content_hash for unified access across VectorDB and DocumentDB

CREATE TABLE IF NOT EXISTS unified_quality_signals (
    content_hash    TEXT PRIMARY KEY,

    -- Identity links (for joining back to source DBs)
    bleve_doc_id    TEXT,
    vector_node_id  TEXT,

    -- Domain and type
    domain          INTEGER NOT NULL,
    content_type    TEXT NOT NULL,

    -- Tier 1: Static signals (from ingestion)
    trust_level     INTEGER DEFAULT 70,
    source_authority REAL DEFAULT 0.5,
    structural_quality REAL DEFAULT 1.0,
    external_citations INTEGER DEFAULT 0,

    -- Tier 2: Dynamic signals (from feedback pipeline)
    retrieval_ema   REAL DEFAULT 0.0,
    inclusion_ema   REAL DEFAULT 0.0,
    reference_ema   REAL DEFAULT 0.0,
    positive_score  REAL DEFAULT 0.0,
    negative_score  REAL DEFAULT 0.0,
    internal_citations INTEGER DEFAULT 0,

    -- Computed scores (updated async)
    usage_feedback_score REAL DEFAULT 0.5,
    authority_score      REAL DEFAULT 0.5,

    -- Staleness tracking
    content_mod_time INTEGER,
    last_validated   INTEGER,
    last_accessed    INTEGER,
    last_update      INTEGER NOT NULL DEFAULT (unixepoch()),

    -- Cross-DB presence tracking
    in_bleve        BOOLEAN DEFAULT FALSE,
    in_vectordb     BOOLEAN DEFAULT FALSE
);

CREATE INDEX idx_unified_signals_bleve ON unified_quality_signals(bleve_doc_id)
    WHERE bleve_doc_id IS NOT NULL;
CREATE INDEX idx_unified_signals_vector ON unified_quality_signals(vector_node_id)
    WHERE vector_node_id IS NOT NULL;
CREATE INDEX idx_unified_signals_domain ON unified_quality_signals(domain);
CREATE INDEX idx_unified_signals_update ON unified_quality_signals(last_update);
```

### Signal Store Implementation

```go
// core/quality/unified_signals.go

type UnifiedSignalStore struct {
    db          *sql.DB
    cache       *ristretto.Cache
    writeBuffer chan SignalUpdate
    decayTicker *time.Ticker

    DecayHalfLife time.Duration  // How fast signals decay (e.g., 7 days)
}

// GetByContentHash retrieves signals for content.
func (s *UnifiedSignalStore) GetByContentHash(ctx context.Context, hash string) (*UnifiedSignals, error) {
    // Check cache first
    cacheKey := "sig:" + hash
    if cached, ok := s.cache.Get(cacheKey); ok {
        return cached.(*UnifiedSignals), nil
    }

    signals := &UnifiedSignals{}
    err := s.db.QueryRowContext(ctx, `
        SELECT
            content_hash, bleve_doc_id, vector_node_id,
            domain, content_type, trust_level, source_authority,
            retrieval_ema, inclusion_ema, reference_ema,
            positive_score, negative_score, internal_citations, external_citations,
            usage_feedback_score, authority_score,
            content_mod_time, last_validated, last_accessed, last_update,
            in_bleve, in_vectordb
        FROM unified_quality_signals
        WHERE content_hash = ?
    `, hash).Scan(/* fields */)

    if err == sql.ErrNoRows {
        return nil, nil  // No signals yet
    }
    if err != nil {
        return nil, err
    }

    // Cache with short TTL
    s.cache.SetWithTTL(cacheKey, signals, 1, 5*time.Minute)

    return signals, nil
}

// GetMany retrieves signals for multiple content hashes efficiently.
func (s *UnifiedSignalStore) GetMany(ctx context.Context, hashes []string) (map[string]*UnifiedSignals, error) {
    // Batch query for efficiency
    // Uses IN clause with prepared statement
}

// Background decay process
func (s *UnifiedSignalStore) runDecayProcess(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case <-s.decayTicker.C:
            s.applyTimeDecay(ctx)
        }
    }
}

func (s *UnifiedSignalStore) applyTimeDecay(ctx context.Context) error {
    // Exponential decay: new_value = old_value * e^(-λt)
    decayLambda := math.Ln2 / s.DecayHalfLife.Seconds()

    _, err := s.db.ExecContext(ctx, `
        UPDATE unified_quality_signals
        SET
            retrieval_ema = retrieval_ema * exp(-? * (unixepoch() - last_update)),
            inclusion_ema = inclusion_ema * exp(-? * (unixepoch() - last_update)),
            reference_ema = reference_ema * exp(-? * (unixepoch() - last_update)),
            positive_score = positive_score * exp(-? * (unixepoch() - last_update)),
            negative_score = negative_score * exp(-? * (unixepoch() - last_update)),
            last_update = unixepoch()
        WHERE last_update < unixepoch() - 3600  -- Only decay hourly+
    `, decayLambda, decayLambda, decayLambda, decayLambda, decayLambda)

    return err
}
```

---

## 5. Feedback Attribution System

### Multi-Level Feedback Tracking

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ MULTI-LEVEL FEEDBACK ATTRIBUTION SYSTEM                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  FEEDBACK LEVELS (in order of signal strength):                             │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ LEVEL 0: RETRIEVAL (retrieved but NOT included in context)          │    │
│  │ Signal: WEAK NEGATIVE (-0.1)                                        │    │
│  │ Meaning: System thought this was relevant, but filtering rejected   │    │
│  │ Attribution: Direct (we know exactly which docs were retrieved)     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                           │                                                 │
│                           ▼                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ LEVEL 1: INCLUSION (included in agent's context window)             │    │
│  │ Signal: WEAK POSITIVE (+0.2)                                        │    │
│  │ Meaning: Passed quality threshold, presented to agent               │    │
│  │ Attribution: Direct (we track what went into context)               │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                           │                                                 │
│                           ▼                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ LEVEL 2: REFERENCE (agent explicitly used/cited content)            │    │
│  │ Signal: MEDIUM POSITIVE (+0.5)                                      │    │
│  │ Meaning: Agent found this valuable enough to reference              │    │
│  │ Attribution: Semantic matching (response ↔ document similarity)     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                           │                                                 │
│                           ▼                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ LEVEL 3: RESPONSE SUCCESS (user satisfied, no follow-up needed)     │    │
│  │ Signal: STRONG POSITIVE (+0.8)                                      │    │
│  │ Meaning: The overall response was good                              │    │
│  │ Attribution: CAUSAL (see below)                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                           │                                                 │
│                           ▼                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ LEVEL 4: EXPLICIT FEEDBACK (user thumbs up/down on response)        │    │
│  │ Signal: STRONGEST (+1.0 / -1.0)                                     │    │
│  │ Meaning: User explicitly rated the interaction                      │    │
│  │ Attribution: CAUSAL (see below)                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Causal Attribution

For Level 3-4 feedback, we need to determine which documents **caused** the good/bad outcome:

```go
// core/quality/causal_attribution.go

type CausalAttributor struct {
    embedder         Embedder
    attentionModel   *AttentionAttributionModel  // Learned attention weights

    CounterfactualSamples int  // How many counterfactuals to estimate
}

type AttributionResult struct {
    DocumentID         string
    CausalContribution float64  // [-1, 1] how much this doc contributed
    Confidence         float64  // How confident we are in this attribution
    Method             string   // "attention", "semantic", "counterfactual"
}

func (a *CausalAttributor) Attribute(
    ctx context.Context,
    response string,
    includedDocs []Document,
    feedbackSignal float64,
) ([]AttributionResult, error) {

    results := make([]AttributionResult, len(includedDocs))

    // Method 1: Semantic Similarity Attribution
    // How much does each document's content appear in the response?
    responseEmbed := a.embedder.Embed(response)
    for i, doc := range includedDocs {
        docEmbed := a.embedder.Embed(doc.Content)
        similarity := cosineSimilarity(responseEmbed, docEmbed)
        results[i].SemanticContribution = similarity
    }

    // Method 2: Attention-based Attribution (if available)
    if a.attentionModel != nil {
        attentionWeights := a.attentionModel.GetAttention(response, includedDocs)
        for i, weight := range attentionWeights {
            results[i].AttentionWeight = weight
        }
    }

    // Method 3: Counterfactual Attribution (most robust)
    // "Would the response have been as good WITHOUT this document?"
    for i, doc := range includedDocs {
        counterfactualDocs := removeDoc(includedDocs, i)
        counterfactualQuality := a.estimateCounterfactualQuality(ctx, response, counterfactualDocs)
        results[i].CounterfactualContribution = feedbackSignal - counterfactualQuality
    }

    // Combine methods with confidence weighting
    for i := range results {
        r := &results[i]

        if r.CounterfactualContribution != 0 {
            r.CausalContribution = 0.5*r.CounterfactualContribution +
                                   0.3*r.AttentionWeight*feedbackSignal +
                                   0.2*r.SemanticContribution*feedbackSignal
            r.Confidence = 0.9
            r.Method = "counterfactual+ensemble"
        } else if r.AttentionWeight != 0 {
            r.CausalContribution = 0.6*r.AttentionWeight*feedbackSignal +
                                   0.4*r.SemanticContribution*feedbackSignal
            r.Confidence = 0.7
            r.Method = "attention+semantic"
        } else {
            r.CausalContribution = r.SemanticContribution * feedbackSignal
            r.Confidence = 0.5
            r.Method = "semantic"
        }
    }

    return results, nil
}
```

### Feedback Pipeline

```go
// core/quality/feedback_pipeline.go

type FeedbackPipeline struct {
    signalStore     *UnifiedSignalStore
    attributor      *CausalAttributor
    weightSystem    *HierarchicalWeightSystem

    eventBuffer     chan FeedbackEvent
    batchProcessor  *BatchProcessor
}

type FeedbackEvent struct {
    SessionID      string
    QueryID        string
    Level          FeedbackLevel
    Signal         float64
    IncludedDocs   []string  // Content hashes
    Response       string
    Timestamp      time.Time
    Agent          AgentType
}

func (p *FeedbackPipeline) RecordFeedback(event FeedbackEvent) {
    p.eventBuffer <- event
}

func (p *FeedbackPipeline) processFeedbackBatch(events []FeedbackEvent) {
    for _, event := range events {
        switch event.Level {
        case FeedbackLevelRetrieval:
            // Weak negative for retrieved-but-not-included
            for _, hash := range event.RetrievedNotIncluded {
                p.signalStore.UpdateSignal(hash, SignalUpdate{
                    Type:   SignalRetrieval,
                    Delta:  -0.1,
                })
            }

        case FeedbackLevelInclusion:
            // Weak positive for included
            for _, hash := range event.IncludedDocs {
                p.signalStore.UpdateSignal(hash, SignalUpdate{
                    Type:   SignalInclusion,
                    Delta:  +0.2,
                })
            }

        case FeedbackLevelReference:
            // Medium positive with semantic attribution
            attributions := p.attributor.AttributeByReference(event.Response, event.IncludedDocs)
            for _, attr := range attributions {
                p.signalStore.UpdateSignal(attr.ContentHash, SignalUpdate{
                    Type:   SignalReference,
                    Delta:  +0.5 * attr.Confidence,
                })
            }

        case FeedbackLevelSuccess, FeedbackLevelExplicit:
            // Strong signal with full causal attribution
            attributions, _ := p.attributor.Attribute(
                context.Background(),
                event.Response,
                p.loadDocs(event.IncludedDocs),
                event.Signal,
            )
            for _, attr := range attributions {
                p.signalStore.UpdateSignal(attr.ContentHash, SignalUpdate{
                    Type:       SignalCausal,
                    Delta:      attr.CausalContribution * event.Signal,
                    Confidence: attr.Confidence,
                })

                // Also update hierarchical weight system
                p.weightSystem.ObserveOutcome(Observation{
                    ContentHash:   attr.ContentHash,
                    Agent:         event.Agent,
                    QueryContext:  event.QueryContext,
                    Outcome:       attr.CausalContribution * event.Signal,
                    Confidence:    attr.Confidence,
                })
            }
        }
    }
}
```

---

## 6. Citation Tracking

### Dual-Source Citation System

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ DUAL-SOURCE CITATION TRACKING                                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ EXTERNAL CITATIONS (Global Authority)                               │    │
│  │                                                                     │    │
│  │ Sources:                                                            │    │
│  │   • Semantic Scholar API (papers, citation counts)                  │    │
│  │   • Google Scholar (if available)                                   │    │
│  │   • npm/PyPI/crates.io download counts (for libraries)              │    │
│  │   • GitHub stars/forks (for referenced repos)                       │    │
│  │   • Official documentation badges                                   │    │
│  │                                                                     │    │
│  │ Signals:                                                            │    │
│  │   • citation_count      -- raw citation number                      │    │
│  │   • citation_velocity   -- citations per year (recent impact)       │    │
│  │   • h_index_of_authors  -- author credibility                       │    │
│  │   • venue_prestige      -- where published (ICML vs arxiv-only)     │    │
│  │   • is_peer_reviewed    -- boolean                                  │    │
│  │                                                                     │    │
│  │ Update: Batch refresh every 24-72 hours (external API rate limits)  │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                           │                                                 │
│                           ▼                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ INTERNAL CITATIONS (Local Relevance)                                │    │
│  │                                                                     │    │
│  │ Signals:                                                            │    │
│  │   • agent_reference_count    -- how often agents cite this          │    │
│  │   • cross_agent_references   -- cited by MULTIPLE agents            │    │
│  │   • user_explicit_requests   -- user asked for this specifically    │    │
│  │   • successful_usage_count   -- times used in successful responses  │    │
│  │   • co_citation_patterns     -- what else is cited alongside        │    │
│  │                                                                     │    │
│  │ Update: Real-time from feedback pipeline                            │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                           │                                                 │
│                           ▼                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ AUTHORITY FUSION (Combined Score)                                   │    │
│  │                                                                     │    │
│  │ Matrix of document value:                                           │    │
│  │                                                                     │    │
│  │              │ High External │ Low External  │                      │    │
│  │ ─────────────┼───────────────┼───────────────┤                      │    │
│  │ High Internal│ GOLD (1.0)    │ NICHE (0.7)   │  ← Very relevant     │    │
│  │ Low Internal │ CANONICAL(0.6)│ UNTESTED(0.3) │  ← Maybe relevant    │    │
│  │                                                                     │    │
│  │ GOLD: Authoritative AND we use it → highest weight                  │    │
│  │ NICHE: We use it heavily but world doesn't know → still valuable    │    │
│  │ CANONICAL: World loves it but we don't use → potential value        │    │
│  │ UNTESTED: Neither signal → needs exploration                        │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Authority Score Computation

```go
// core/quality/citation_tracker.go

type CitationTracker struct {
    db              *sql.DB
    externalAPIs    []ExternalCitationAPI
    signalStore     *UnifiedSignalStore

    ExternalWeight  float64  // Base weight for external (e.g., 0.4)
    InternalWeight  float64  // Base weight for internal (e.g., 0.6)
}

type AuthorityQuadrant string

const (
    QuadrantGold      AuthorityQuadrant = "gold"       // High external + high internal
    QuadrantNiche     AuthorityQuadrant = "niche"      // Low external + high internal
    QuadrantCanonical AuthorityQuadrant = "canonical"  // High external + low internal
    QuadrantUntested  AuthorityQuadrant = "untested"   // Low external + low internal
)

func (t *CitationTracker) ComputeAuthorityScore(
    ctx context.Context,
    contentHash string,
    domain Domain,
) (*AuthorityScore, error) {

    external, _ := t.getExternalCitations(ctx, contentHash)
    internal, _ := t.getInternalCitations(ctx, contentHash)

    // Normalize to 0-1 scale
    externalNorm := t.normalizeExternal(external)
    internalNorm := t.normalizeInternal(internal)

    // Quadrant-based fusion
    return t.fuseAuthority(externalNorm, internalNorm), nil
}

func (t *CitationTracker) fuseAuthority(external, internal float64) *AuthorityScore {
    const highThreshold = 0.5

    var quadrant AuthorityQuadrant
    var score, confidence float64

    highExternal := external >= highThreshold
    highInternal := internal >= highThreshold

    switch {
    case highExternal && highInternal:
        quadrant = QuadrantGold
        score = 0.7 + 0.3*(external*internal)
        confidence = 0.95

    case !highExternal && highInternal:
        quadrant = QuadrantNiche
        score = 0.5 + 0.3*internal + 0.1*external
        confidence = 0.8

    case highExternal && !highInternal:
        quadrant = QuadrantCanonical
        score = 0.3 + 0.25*external + 0.1*internal
        confidence = 0.6

    default:
        quadrant = QuadrantUntested
        score = 0.2 + 0.1*external + 0.1*internal
        confidence = 0.4
    }

    return &AuthorityScore{
        Quadrant:   quadrant,
        Score:      score,
        Confidence: confidence,
    }
}
```

---

## 7. VectorDB Integration

### Existing Types Used

The VectorDB already has quality-relevant fields:

```go
// core/vectorgraphdb/types.go (existing)

type GraphNode struct {
    // ... existing fields ...

    Confidence       float64          `json:"confidence,omitempty"`
    TrustLevel       TrustLevel       `json:"trust_level,omitempty"`
    // TrustLevel values: Ground=100, Recent=80, Standard=70, Academic=60,
    //                    OldHistory=40, Blog=30, LLM=20
}

type GraphEdge struct {
    Weight    float64        `json:"weight"`  // Edge weight for scoring
}
```

### Quality-Aware Query Engine

```go
// core/vectorgraphdb/quality_query.go

// QualityQueryEngine wraps QueryEngine with quality-aware scoring.
type QualityQueryEngine struct {
    *QueryEngine                          // Embed existing engine
    signalStore  *quality.UnifiedSignalStore
    weights      *quality.HierarchicalWeightSystem
    scorer       *quality.QualityScorer
}

// QualityAdjustedResult extends HybridResult with quality scoring.
type QualityAdjustedResult struct {
    HybridResult                    // Embed existing type

    QualityScore     float64
    StalenessScore   float64
    UsageFeedback    float64
    AuthorityScore   float64
    AdjustedScore    float64

    Signals          *quality.UnifiedSignals `json:"signals,omitempty"`
}

// QualityHybridQuery performs hybrid query with quality adjustment.
func (qqe *QualityQueryEngine) QualityHybridQuery(
    ctx context.Context,
    query []float32,
    seedNodes []string,
    opts *HybridQueryOptions,
    agentDomain Domain,
) ([]QualityAdjustedResult, error) {

    // 1. Get base hybrid results using existing QueryEngine
    baseResults, err := qqe.HybridQuery(query, seedNodes, opts)
    if err != nil {
        return nil, err
    }

    // 2. Get quality weights for this agent/query context
    qualityWeights := qqe.weights.GetWeights(ctx, agentDomain, nil)

    // 3. Apply quality adjustment to each result
    adjustedResults := make([]QualityAdjustedResult, 0, len(baseResults))
    for _, base := range baseResults {
        adjusted := qqe.applyQualityAdjustment(ctx, base, qualityWeights, agentDomain)
        adjustedResults = append(adjustedResults, adjusted)
    }

    // 4. Re-sort by adjusted score
    sort.Slice(adjustedResults, func(i, j int) bool {
        return adjustedResults[i].AdjustedScore > adjustedResults[j].AdjustedScore
    })

    return adjustedResults, nil
}

func (qqe *QualityQueryEngine) applyQualityAdjustment(
    ctx context.Context,
    base HybridResult,
    weights map[string]float64,
    domain Domain,
) QualityAdjustedResult {

    // Fetch quality signals by content hash
    signals, _ := qqe.signalStore.GetByVectorNodeID(ctx, base.Node.ID)

    result := QualityAdjustedResult{HybridResult: base}

    // Trust score from node's existing TrustLevel
    result.TrustScore = float64(base.Node.TrustLevel) / 100.0

    // Staleness score with domain-specific decay
    result.StalenessScore = qqe.scorer.ComputeStaleness(base.Node.UpdatedAt, domain)

    // Usage and authority from signals
    if signals != nil {
        result.UsageFeedback = signals.UsageFeedbackScore
        result.AuthorityScore = signals.AuthorityScore
        result.Signals = signals
    } else {
        result.UsageFeedback = 0.5
        result.AuthorityScore = base.Node.Confidence
    }

    // Combine quality components
    result.QualityScore =
        result.TrustScore * weights["trust_level"] +
        result.StalenessScore * weights["staleness"] +
        result.UsageFeedback * weights["usage_feedback"] +
        result.AuthorityScore * weights["authority"]

    // Final adjusted score
    result.AdjustedScore =
        base.VectorScore * weights["vector"] +
        base.GraphScore * weights["graph"] +
        result.QualityScore

    return result
}
```

---

## 8. Document DB Integration

### Extended Document Type

```go
// core/search/document.go - additions to existing Document struct

type Document struct {
    // ... existing fields (ID, Path, Type, Content, Checksum, etc.) ...

    // Quality Fields
    Domain          Domain  `json:"domain"`
    SourceAgent     string  `json:"source_agent,omitempty"`
    TrustLevel      int     `json:"trust_level"`
    Confidence      float64 `json:"confidence"`
    SourceAuthority float64 `json:"source_authority,omitempty"`
    ExternalCitations int   `json:"external_citations,omitempty"`
}
```

### Quality-Aware Search Coordinator (DS.9.2)

```go
// core/search/coordinator/coordinator.go

type SearchCoordinator struct {
    bleveSearcher   *BleveSearcher
    vectorSearcher  *vectorgraphdb.VectorSearcher
    rrfMerger       *RRFMerger
    signalStore     *quality.UnifiedSignalStore
    scorer          *quality.QualityScorer
    weights         *quality.HierarchicalWeightSystem
}

// QualityScoredDocument is a document with full quality breakdown.
type QualityScoredDocument struct {
    Document      *search.Document

    BleveScore    float64
    VectorScore   float64
    RRFScore      float64

    TrustScore       float64
    StalenessScore   float64
    UsageScore       float64
    AuthorityScore   float64
    QualityScore     float64

    InBothSources bool
    CrossValBonus float64
    FinalScore    float64
}

func (sc *SearchCoordinator) Search(
    ctx context.Context,
    req *HybridSearchRequest,
) (*HybridSearchResult, error) {

    // 1. Parallel Bleve + Vector search
    // 2. RRF fusion
    // 3. Apply quality scoring (see Unified Hybrid Search below)

    // ...
}
```

---

## 9. Unified Hybrid Search

When querying BOTH VectorDB and Document DB, quality scoring happens AFTER fusion using unified identity.

### Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     UNIFIED HYBRID SEARCH FLOW                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  User Query                                                                 │
│      │                                                                      │
│      ▼                                                                      │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ PHASE 1: PARALLEL RAW SEARCH (no quality scoring yet)               │    │
│  │                                                                     │    │
│  │   ┌─────────────┐              ┌─────────────┐                      │    │
│  │   │   Bleve     │              │  VectorDB   │                      │    │
│  │   │  (text)     │              │ (semantic)  │                      │    │
│  │   └──────┬──────┘              └──────┬──────┘                      │    │
│  │          │                            │                             │    │
│  │          ▼                            ▼                             │    │
│  │   BleveResult[]               SearchResult[]                        │    │
│  │   - doc_id                    - node_id                             │    │
│  │   - text_score                - similarity                          │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                        │                  │                                 │
│                        └────────┬─────────┘                                 │
│                                 ▼                                           │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ PHASE 2: IDENTITY RESOLUTION                                        │    │
│  │                                                                     │    │
│  │   Map Bleve doc_id ←──► VectorDB node_id via content_hash           │    │
│  │   Identify: same content in both? different content?                │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                 │                                           │
│                                 ▼                                           │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ PHASE 3: RRF FUSION                                                 │    │
│  │                                                                     │    │
│  │   Merge by identity, compute RRF score                              │    │
│  │   Track: in_bleve, in_vector, in_both                               │    │
│  │   Formula: score = Σ 1/(k + rank_i), k=60                           │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                 │                                           │
│                                 ▼                                           │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ PHASE 4: UNIFIED QUALITY SCORING                                    │    │
│  │                                                                     │    │
│  │   For each fused result:                                            │    │
│  │     1. Get quality signals by content_hash (unified store)          │    │
│  │     2. Compute quality components                                   │    │
│  │     3. Apply cross-validation bonus if in_both                      │    │
│  │     4. Combine: final = RRF + quality + cross_val                   │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                 │                                           │
│                                 ▼                                           │
│                        UnifiedResult[]                                      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Content Identity Resolution

```go
// core/quality/identity.go

type ContentIdentity struct {
    ContentHash  string  `json:"content_hash"`
    BleveDocID   *string `json:"bleve_doc_id,omitempty"`
    VectorNodeID *string `json:"vector_node_id,omitempty"`
    Domain       Domain  `json:"domain"`
    Path         string  `json:"path,omitempty"`
}

type IdentityResolver struct {
    db *sql.DB
}

func (ir *IdentityResolver) ResolveMany(hashes []string) (map[string]*ContentIdentity, error) {
    // Batch query to resolve content hashes to identities
}
```

### Hybrid Coordinator

```go
// core/search/hybrid/coordinator.go

type HybridCoordinator struct {
    bleveSearcher    *search.BleveSearcher
    vectorSearcher   *vectorgraphdb.VectorSearcher
    rrfMerger        *RRFMerger
    identityResolver *quality.IdentityResolver
    signalStore      *quality.UnifiedSignalStore
    scorer           *quality.QualityScorer
    weights          *quality.HierarchicalWeightSystem
}

type UnifiedResult struct {
    ContentHash  string
    Identity     *quality.ContentIdentity
    Content      *UnifiedContent

    // Source scores
    BleveScore   float64
    VectorScore  float64
    InBleve      bool
    InVectorDB   bool
    InBoth       bool

    // Fusion
    RRFScore     float64

    // Quality
    TrustScore       float64
    StalenessScore   float64
    UsageFeedback    float64
    AuthorityScore   float64
    QualityScore     float64

    // Cross-validation
    CrossValBonus    float64

    // Final
    FinalScore       float64
}

func (hc *HybridCoordinator) Search(
    ctx context.Context,
    req *HybridSearchRequest,
) (*HybridSearchResult, error) {

    // PHASE 1: Parallel raw search
    var wg sync.WaitGroup
    var bleveResults, vectorResults []Result

    wg.Add(2)
    go func() {
        defer wg.Done()
        bleveResults, _ = hc.bleveSearcher.Search(ctx, req.TextQuery, req.Limit*2)
    }()
    go func() {
        defer wg.Done()
        vectorResults, _ = hc.vectorSearcher.Search(req.Embedding, req.Limit*2)
    }()
    wg.Wait()

    // PHASE 2: Identity resolution
    identities, _ := hc.resolveIdentities(ctx, bleveResults, vectorResults)

    // PHASE 3: RRF fusion
    fusedResults := hc.rrfMerger.MergeWithIdentity(bleveResults, vectorResults, identities, 60)

    // PHASE 4: Unified quality scoring
    qualityWeights := hc.weights.GetWeights(ctx, req.AgentDomain, nil)

    // Batch-fetch signals
    hashes := extractHashes(fusedResults)
    signals, _ := hc.signalStore.GetMany(ctx, hashes)

    // Apply quality scoring
    results := make([]UnifiedResult, len(fusedResults))
    for i, fr := range fusedResults {
        results[i] = hc.scoreResult(fr, signals[fr.ContentHash], qualityWeights, req.AgentDomain)
    }

    // Sort by final score
    sort.Slice(results, func(i, j int) bool {
        return results[i].FinalScore > results[j].FinalScore
    })

    return &HybridSearchResult{Results: results[:min(len(results), req.Limit)]}, nil
}

func (hc *HybridCoordinator) scoreResult(
    fr FusedResult,
    signals *quality.UnifiedSignals,
    weights map[string]float64,
    domain Domain,
) UnifiedResult {

    result := UnifiedResult{
        ContentHash: fr.ContentHash,
        Identity:    fr.Identity,
        BleveScore:  fr.BleveScore,
        VectorScore: fr.VectorScore,
        InBleve:     fr.InBleve,
        InVectorDB:  fr.InVectorDB,
        InBoth:      fr.InBleve && fr.InVectorDB,
        RRFScore:    fr.RRFScore,
    }

    // Quality components
    if signals != nil {
        result.TrustScore = float64(signals.TrustLevel) / 100.0
        result.StalenessScore = hc.scorer.ComputeStaleness(
            time.Unix(signals.ContentModTime, 0), domain,
        )
        result.UsageFeedback = signals.UsageFeedbackScore
        result.AuthorityScore = signals.AuthorityScore
    } else {
        result.TrustScore = 0.7
        result.StalenessScore = 1.0
        result.UsageFeedback = 0.5
        result.AuthorityScore = 0.5
    }

    // Combine quality
    result.QualityScore =
        result.TrustScore * weights["trust_level"] +
        result.StalenessScore * weights["staleness"] +
        result.UsageFeedback * weights["usage_feedback"] +
        result.AuthorityScore * weights["authority"]

    // Cross-validation bonus
    if result.InBoth {
        result.CrossValBonus = weights["cross_validation"]
    }

    // Final score
    result.FinalScore =
        result.RRFScore * (weights["vector"] + weights["bleve"]) +
        result.QualityScore +
        result.CrossValBonus

    return result
}
```

### Cross-Validation Signal

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ CROSS-VALIDATION SIGNAL STRENGTH                                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Query: "how do we handle authentication errors"                            │
│                                                                             │
│  ┌─────────────────┐     ┌─────────────────┐                                │
│  │     Bleve       │     │    VectorDB     │                                │
│  │  (text match)   │     │ (semantic match)│                                │
│  └────────┬────────┘     └────────┬────────┘                                │
│           │                       │                                         │
│           ▼                       ▼                                         │
│  auth_handler.go ──────────── auth_handler.go  ← IN BOTH = strong signal    │
│  error_types.go                                                             │
│                     ──────── login_flow.go     ← semantic only              │
│  config.yaml                                   ← text only (has "auth")     │
│                                                                             │
│  Cross-validation: "auth_handler.go" matches BOTH lexically AND             │
│  semantically → high confidence this is relevant                            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 10. Sudden Shift Detection

To handle sudden conversation shifts, we use change-point detection to avoid over-fitting recent patterns.

```go
// core/quality/shift_detector.go

type ConversationShiftDetector struct {
    RecentQueries     [][]float64  // Rolling window of query embeddings
    WindowSize        int          // e.g., 5 queries
    ShiftThreshold    float64      // Cosine distance threshold
    DampeningFactor   float64      // How much to reduce recent weight influence
}

type ShiftResult struct {
    IsShift     bool
    Magnitude   float64
    Dampening   float64
}

func (d *ConversationShiftDetector) DetectShift(newQuery []float64) ShiftResult {
    if len(d.RecentQueries) < 2 {
        return ShiftResult{IsShift: false}
    }

    // Compare new query to centroid of recent queries
    centroid := d.computeCentroid(d.RecentQueries)
    distance := cosineDistance(newQuery, centroid)

    if distance > d.ShiftThreshold {
        return ShiftResult{
            IsShift:   true,
            Magnitude: distance,
            Dampening: d.DampeningFactor,
        }
    }
    return ShiftResult{IsShift: false}
}
```

**When shift detected:**
- Dampen recent usage feedback weights (don't over-index on what was relevant 2 queries ago)
- Increase prior weight (fall back to base quality signals)
- Expand search scope (shift suggests we may need different content)

---

## 11. Weight Isolation & Persistence

Sylk is a **multi-session terminal application** where multiple sessions can execute simultaneously on the same project, each doing entirely different work. This creates two critical requirements:

1. **Session Isolation**: Weights learned in Session A must NOT interfere with Session B during concurrent execution
2. **Session Persistence**: Weights must survive suspend/resume and crash/recovery scenarios

### Multi-Session Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ MULTI-SESSION WEIGHT ISOLATION                                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Same Project: /home/user/my-app                                            │
│  Same Time: Tuesday 2pm                                                     │
│                                                                             │
│  Terminal 1 (Session A)         Terminal 2 (Session B)                      │
│  ┌─────────────────────┐        ┌─────────────────────┐                     │
│  │ "Help me refactor   │        │ "Debug why the      │                     │
│  │  the auth system"   │        │  database migrations│                     │
│  │                     │        │  are failing"       │                     │
│  │ Learning:           │        │                     │                     │
│  │ • auth/* is relevant│        │ Learning:           │                     │
│  │ • staleness critical│        │ • migrations/* key  │                     │
│  │ • tests less useful │        │ • logs very useful  │                     │
│  └─────────────────────┘        │ • old history helps │                     │
│           │                     └─────────────────────┘                     │
│           │                              │                                  │
│           ▼                              ▼                                  │
│  ┌─────────────────────┐        ┌─────────────────────┐                     │
│  │ Session A Weights   │        │ Session B Weights   │                     │
│  │ (ISOLATED)          │        │ (ISOLATED)          │                     │
│  │                     │        │                     │
│  │ ~/.sylk/sessions/   │        │ ~/.sylk/sessions/   │                     │
│  │   {session_A}/      │        │   {session_B}/      │                     │
│  │   weights/          │        │   weights/          │                     │
│  └─────────────────────┘        └─────────────────────┘                     │
│                                                                             │
│  GUARANTEE: Session A's learning NEVER affects Session B's retrieval        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Weight Scope by Hierarchy Level

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ WEIGHT SCOPE BY HIERARCHY LEVEL                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Level 3: GLOBAL PRIORS                                                     │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ Scope: Shared across ALL sessions (with decay)                      │    │
│  │ Storage: ~/.sylk/global/weights.db                                  │    │
│  │ Purpose: "Usage feedback is generally useful"                       │    │
│  │ Isolation: NONE (intentionally shared)                              │    │
│  │ Persistence: Yes, with 90-day half-life decay                       │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  Level 2: PROJECT-SCOPED PRIORS                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ Scope: Per-project, shared across sessions on same project          │    │
│  │ Storage: ~/.sylk/projects/{project_hash}/weights/base.db            │    │
│  │ Purpose: "For this codebase, Librarian cares about staleness"       │    │
│  │ Isolation: READ-ONLY during session execution                       │    │
│  │ Persistence: Yes, with 30-day half-life decay                       │    │
│  │ Update: Merge from session weights on session complete              │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  Level 1: SESSION-SCOPED WEIGHTS (Query-Context)                            │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ Scope: Current session ONLY                                         │    │
│  │ Storage: ~/.sylk/sessions/{session_id}/weights/                     │    │
│  │ Purpose: "In THIS conversation, recency matters more"               │    │
│  │ Isolation: COMPLETE (copy-on-write from project base)               │    │
│  │ Persistence: Yes (survives suspend/resume and crash)                │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  Auxiliary State (Session-Only, In-Memory):                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ • Thompson sampling exploration state                               │    │
│  │ • Shift detection rolling window                                    │    │
│  │ • Context cache (query → weights mapping)                           │    │
│  │ • Recent query embedding history                                    │    │
│  │                                                                     │    │
│  │ Storage: In-memory, logged to WAL for crash recovery                │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Copy-on-Write Weight Isolation

Sessions use **copy-on-write** semantics for weight isolation. The project base is READ-ONLY during execution; all writes go to a session-local overlay.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ COPY-ON-WRITE LAYERED WEIGHT STORAGE                                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  READ PATH                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  Request: Get weight for (Agent=Librarian, Signal=Staleness)        │    │
│  │                                                                     │    │
│  │  1. Check session overlay (in-memory)                               │    │
│  │     └─► Found? Return it. DONE.                                     │    │
│  │                                                                     │    │
│  │  2. Check session overlay (on-disk, if evicted from memory)         │    │
│  │     └─► Found? Load to memory, return it. DONE.                     │    │
│  │                                                                     │    │
│  │  3. Read from project base (immutable during session)               │    │
│  │     └─► Return base value.                                          │    │
│  │                                                                     │    │
│  │  Session NEVER modifies project base. Complete isolation.           │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  WRITE PATH                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  1. Log observation to WAL (durability guarantee)                   │    │
│  │  2. If first write to this key: copy from base to overlay           │    │
│  │  3. Apply observation to overlay (in-memory)                        │    │
│  │  4. Mark key as "dirty" in overlay                                  │    │
│  │                                                                     │    │
│  │  Project base is READ-ONLY. No locks needed between sessions.       │    │
│  │  Multiple sessions can share same base safely.                      │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  STORAGE LAYOUT                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  ~/.sylk/projects/{hash}/weights/                                   │    │
│  │  ├── base.db              ← Immutable during sessions               │    │
│  │  ├── base.version         ← Incremented only on merge               │    │
│  │  └── snapshots/           ← Historical for three-way merge          │    │
│  │      ├── v005.snapshot                                              │    │
│  │      └── v006.snapshot                                              │    │
│  │                                                                     │    │
│  │  ~/.sylk/sessions/{id}/weights/                                     │    │
│  │  ├── overlay.db           ← Session-local changes only              │    │
│  │  ├── meta.json            ← Fork version, checkpoint info           │    │
│  │  └── (WAL is in session's wal/ directory)                           │    │
│  │                                                                     │    │
│  │  Memory:                                                            │    │
│  │  ┌─────────────────────────────────────────────────────┐            │    │
│  │  │ Session A                                           │            │    │
│  │  │ ┌─────────────────┐   ┌──────────────────────────┐  │            │    │
│  │  │ │ Overlay (dirty) │ + │ Base (read-only, shared) │  │            │    │
│  │  │ └─────────────────┘   └──────────────────────────┘  │            │    │
│  │  └─────────────────────────────────────────────────────┘            │    │
│  │                                                                     │    │
│  │  ┌─────────────────────────────────────────────────────┐            │    │
│  │  │ Session B                                           │            │    │
│  │  │ ┌─────────────────┐   ┌──────────────────────────┐  │            │    │
│  │  │ │ Overlay (dirty) │ + │ Base (read-only, shared) │  │            │    │
│  │  │ └─────────────────┘   └──────────────────────────┘  │            │    │
│  │  └─────────────────────────────────────────────────────┘            │    │
│  │                                                                     │    │
│  │  Base is memory-mapped, shared across all sessions (read-only).     │    │
│  │  Each session has tiny overlay for its changes only.                │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Weight State Lifecycle

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ WEIGHT LIFECYCLE ACROSS SESSION STATES                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  SESSION START (Create/Activate)                                            │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  1. Create session directory: ~/.sylk/sessions/{session_id}/        │    │
│  │                                                                     │    │
│  │  2. Record fork point:                                              │    │
│  │     • Read current project base version                             │    │
│  │     • Store in session metadata: forked_from_version                │    │
│  │                                                                     │    │
│  │  3. Initialize empty overlay:                                       │    │
│  │     • No file copy needed (copy-on-write)                           │    │
│  │     • In-memory overlay starts empty                                │    │
│  │     • Reads fall through to project base                            │    │
│  │                                                                     │    │
│  │  Result: Instant "fork" with zero disk I/O                          │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  DURING EXECUTION (Active)                                                  │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  On weight observation:                                             │    │
│  │    1. Log to WAL BEFORE applying (durability)                       │    │
│  │    2. Copy-on-write if first modification to key                    │    │
│  │    3. Apply to in-memory overlay                                    │    │
│  │                                                                     │    │
│  │  Periodic checkpoint (via existing Checkpointer):                   │    │
│  │    • Overlay state included in SessionSnapshot                      │    │
│  │    • WAL sequence recorded                                          │    │
│  │                                                                     │    │
│  │  Concurrent sessions:                                               │    │
│  │    • Complete isolation - no shared mutable state                   │    │
│  │    • No locks needed between sessions                               │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  ON SUSPEND                                                                 │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  1. Trigger WAL sync (fsync any buffered entries)                   │    │
│  │                                                                     │    │
│  │  2. Write overlay to disk: sessions/{id}/weights/overlay.db         │    │
│  │                                                                     │    │
│  │  3. Add WeightState to SessionSnapshot:                             │    │
│  │     {                                                               │    │
│  │       "forked_from_version": 7,                                     │    │
│  │       "wal_sequence": 1234,                                         │    │
│  │       "overlay_checksum": "abc123...",                              │    │
│  │       "dirty_keys_count": 42                                        │    │
│  │     }                                                               │    │
│  │                                                                     │    │
│  │  4. Release memory (overlay can be reloaded on resume)              │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  ON RESUME                                                                  │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  1. Read WeightState from SessionSnapshot                           │    │
│  │                                                                     │    │
│  │  2. Load overlay from sessions/{id}/weights/overlay.db              │    │
│  │                                                                     │    │
│  │  3. Replay WAL entries from snapshot's wal_sequence                 │    │
│  │     (in case entries were written after snapshot but before suspend)│    │
│  │                                                                     │    │
│  │  4. Continue execution with restored state                          │    │
│  │                                                                     │    │
│  │  Result: Exactly where we left off                                  │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  ON CRASH (Unexpected Termination)                                          │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  Recovery on next startup (via existing RecoveryManager):           │    │
│  │                                                                     │    │
│  │  1. Detect incomplete session (lock file exists, no clean shutdown) │    │
│  │                                                                     │    │
│  │  2. Load most recent checkpoint                                     │    │
│  │     • Includes WeightState with wal_sequence                        │    │
│  │                                                                     │    │
│  │  3. Load overlay from checkpoint or overlay.db                      │    │
│  │                                                                     │    │
│  │  4. Replay WAL entries from checkpoint.wal_sequence + 1             │    │
│  │     • Weight observations re-applied to overlay                     │    │
│  │     • ZERO data loss (all observations were in WAL)                 │    │
│  │                                                                     │    │
│  │  5. Resume session with recovered state                             │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  ON SESSION COMPLETE                                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │  Three-way merge: (base-at-fork, current-base, session-overlay)     │    │
│  │                                                                     │    │
│  │  1. Load base snapshot at forked_from_version                       │    │
│  │  2. Load current base (may have changed if other sessions merged)   │    │
│  │  3. Load session overlay                                            │    │
│  │                                                                     │    │
│  │  4. For each weight key:                                            │    │
│  │     • Compute our delta: session - base_at_fork                     │    │
│  │     • Compute others' delta: current_base - base_at_fork            │    │
│  │     • Merge: new = base + our_delta + others_delta                  │    │
│  │     • Detect conflicts (opposite directions)                        │    │
│  │                                                                     │    │
│  │  5. Atomic write with version check:                                │    │
│  │     • Acquire project lock                                          │    │
│  │     • Verify base.version hasn't changed                            │    │
│  │     • Write merged weights                                          │    │
│  │     • Increment base.version                                        │    │
│  │     • Release lock                                                  │    │
│  │     • If version changed, retry merge with new base                 │    │
│  │                                                                     │    │
│  │  6. Cleanup session weight files                                    │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Three-Way Merge for Weight Distributions

When merging session learnings back to the project base, we use three-way merge semantics similar to git:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ THREE-WAY WEIGHT MERGE                                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Session forked at base version 5                                           │
│  Session completing, current base is version 7                              │
│  (Other sessions merged while we were working)                              │
│                                                                             │
│  ┌─────────────────┐                                                        │
│  │ BASE @ FORK (v5)│ ─── What we started with                               │
│  │ α=10, β=5       │                                                        │
│  └────────┬────────┘                                                        │
│           │                                                                 │
│           ├────────────────────────────┐                                    │
│           │                            │                                    │
│           ▼                            ▼                                    │
│  ┌─────────────────┐          ┌─────────────────┐                           │
│  │ SESSION (ours)  │          │ CURRENT BASE(v7)│                           │
│  │ α=15, β=7       │          │ α=12, β=8       │                           │
│  │                 │          │                 │                           │
│  │ Our changes:    │          │ Others' changes:│                           │
│  │ Δα=+5, Δβ=+2    │          │ Δα=+2, Δβ=+3    │                           │
│  └────────┬────────┘          └────────┬────────┘                           │
│           │                            │                                    │
│           └────────────┬───────────────┘                                    │
│                        │                                                    │
│                        ▼                                                    │
│           ┌─────────────────────────┐                                       │
│           │ THREE-WAY MERGE         │                                       │
│           │                         │                                       │
│           │ New α = base_α          │                                       │
│           │       + our_Δα          │                                       │
│           │       + others_Δα       │                                       │
│           │     = 10 + 5 + 2 = 17   │                                       │
│           │                         │                                       │
│           │ New β = base_β          │                                       │
│           │       + our_Δβ          │                                       │
│           │       + others_Δβ       │                                       │
│           │     = 5 + 2 + 3 = 10    │                                       │
│           │                         │                                       │
│           │ Result: α=17, β=10      │                                       │
│           │ (mean = 0.63)           │                                       │
│           └─────────────────────────┘                                       │
│                                                                             │
│  This correctly combines ALL evidence from ALL sessions.                    │
│  No information lost. Mathematically sound for Beta distributions.          │
│                                                                             │
│  CONFLICT DETECTION:                                                        │
│  If our_direction != others_direction (we learned "good", they learned      │
│  "bad"), resolve by weighting by effective_samples (more evidence wins)     │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### SessionSnapshot Integration

The existing `SessionSnapshot` (from ARCHITECTURE.md) is extended to include weight state:

```go
// Updated SessionSnapshot from ARCHITECTURE.md

type SessionSnapshot struct {
    Session           *Session              `json:"session"`
    ArchivalistState  *ArchivalistSnapshot  `json:"archivalist_state"`
    GuideState        *GuideSnapshot        `json:"guide_state"`
    DAGState          *DAGSnapshot          `json:"dag_state,omitempty"`
    PendingMessages   []*PendingMessage     `json:"pending_messages,omitempty"`
    AgentStates       map[string]*AgentSnapshot `json:"agent_states,omitempty"`

    // Weight state for suspend/resume (NEW)
    WeightState       *WeightState          `json:"weight_state,omitempty"`

    CreatedAt         time.Time             `json:"created_at"`
}

// WeightState captures weight system state for session persistence.
type WeightState struct {
    // Fork tracking
    ForkedFromVersion int64  `json:"forked_from_version"`
    ProjectHash       string `json:"project_hash"`

    // WAL coordination
    WALSequence       uint64 `json:"wal_sequence"`

    // Overlay state
    OverlayChecksum   string `json:"overlay_checksum"`
    DirtyKeysCount    int    `json:"dirty_keys_count"`

    // Timestamps
    LastCheckpoint    int64  `json:"last_checkpoint"`
}
```

---

## 12. Integration with Existing Infrastructure

The weight isolation and persistence system integrates with Sylk's existing concurrency infrastructure rather than building new components.

### Existing Infrastructure (Already Implemented)

Sylk already has battle-tested implementations for:

| Component | Location | Purpose |
|-----------|----------|---------|
| **WriteAheadLog** | `core/concurrency/wal.go` | Durable logging with fsync, segmented files, replay |
| **Checkpointer** | `core/concurrency/checkpointer.go` | Periodic snapshots, atomic writes, cleanup |
| **RecoveryManager** | `core/concurrency/recovery.go` | Crash detection, checkpoint loading, WAL replay |
| **StagingManager** | `core/concurrency/staging.go` | Copy-on-read isolation, hash-based conflict detection, merge |

### WriteAheadLog Integration

The existing `WriteAheadLog` supports custom entry types. We add new entry types for the Bayesian learning system:

```go
// core/concurrency/wal_entry.go (existing file, add new types)

type WALEntryType int

const (
    EntryStateChange       WALEntryType = iota
    EntryLLMRequest
    EntryLLMResponse
    EntryFileChange
    EntryWeightObservation   // Weight learning observation (5-level hierarchy)
    EntryGPObservation       // GP model degradation observation
    EntryThresholdFeedback   // Threshold decision feedback (Bayesian decision theory)
    EntryDecayFeedback       // Decay rate learning feedback
)

// WeightObservationPayload is the payload for EntryWeightObservation entries.
// Extended to include domain and model for full 5-level hierarchy.
type WeightObservationPayload struct {
    Domain        int     `json:"domain"`        // Domain (Code, Academic, History)
    Model         string  `json:"model"`         // Model ID
    Agent         string  `json:"agent"`         // Agent type
    Signal        string  `json:"signal"`        // Quality signal
    Outcome       float64 `json:"outcome"`       // Observed outcome [0, 1]
    Satisfaction  float64 `json:"satisfaction"`  // Satisfaction signal [-1, 1]
    QueryContext  uint64  `json:"query_ctx"`     // Query context hash
    ContentHash   string  `json:"content_hash"`  // Content hash for attribution
}

// GPObservationPayload is the payload for EntryGPObservation entries.
// Records (turn, quality) observations for model degradation GP.
type GPObservationPayload struct {
    Model   string  `json:"model"`    // Model ID
    Turn    int     `json:"turn"`     // Turn number in session
    Quality float64 `json:"quality"`  // Observed quality [0, 1]
}

// ThresholdFeedbackPayload is the payload for EntryThresholdFeedback entries.
// Records review queue outcomes for Bayesian threshold learning.
type ThresholdFeedbackPayload struct {
    Domain     int     `json:"domain"`      // Domain for threshold
    Confidence float64 `json:"confidence"`  // Confidence score that triggered review
    WasGood    bool    `json:"was_good"`    // Whether content was approved (good) or rejected (bad)
}

// DecayFeedbackPayload is the payload for EntryDecayFeedback entries.
// Records document accuracy observations for decay rate learning.
type DecayFeedbackPayload struct {
    Domain      int     `json:"domain"`       // Domain for decay rate
    AgeHours    float64 `json:"age_hours"`    // Age of document in hours
    WasAccurate bool    `json:"was_accurate"` // Whether document was still accurate at this age
}
```

The existing WAL provides:

```go
// core/concurrency/wal.go (existing)

type WriteAheadLog struct {
    config       WALConfig
    currentFile  *os.File
    writer       *bufio.Writer
    sequence     uint64
    // ...
}

// Append logs an entry with durability guarantee
func (w *WriteAheadLog) Append(entry *WALEntry) (uint64, error)

// ReadFrom replays entries from a sequence number
func (w *WriteAheadLog) ReadFrom(sequence uint64) ([]*WALEntry, error)

// Sync forces fsync (called automatically based on SyncMode)
func (w *WriteAheadLog) Sync() error

// Truncate removes entries before a sequence (after checkpoint)
func (w *WriteAheadLog) Truncate(beforeSequence uint64) error
```

### Checkpointer Integration

The existing `Checkpointer` uses a `CheckpointStateProvider` interface for custom state collection:

```go
// core/concurrency/checkpointer.go (existing)

type CheckpointStateProvider interface {
    CollectState(cp *Checkpoint) error
}
```

We implement this interface for weights:

```go
// core/quality/weight_checkpoint.go (new)

// WeightStateProvider implements CheckpointStateProvider for weight state.
type WeightStateProvider struct {
    sessionWeights *SessionWeightManager
}

func (p *WeightStateProvider) CollectState(cp *Checkpoint) error {
    state := p.sessionWeights.GetWeightState()

    // Serialize overlay to checkpoint
    overlayData, err := p.sessionWeights.SerializeOverlay()
    if err != nil {
        return err
    }

    cp.SetData("weight_state", state)
    cp.SetData("weight_overlay", overlayData)

    return nil
}
```

### RecoveryManager Integration

The existing `RecoveryManager` replays WAL entries after loading a checkpoint. Extended for all Bayesian learning entry types:

```go
// core/concurrency/recovery.go (existing, with additions)

func (r *RecoveryManager) applyWALEntry(entry *WALEntry) error {
    switch entry.Type {
    case EntryStateChange:
        return nil
    case EntryLLMRequest, EntryLLMResponse:
        return nil
    case EntryFileChange:
        return nil
    // Bayesian learning system entries
    case EntryWeightObservation:
        return r.replayWeightObservation(entry)
    case EntryGPObservation:
        return r.replayGPObservation(entry)
    case EntryThresholdFeedback:
        return r.replayThresholdFeedback(entry)
    case EntryDecayFeedback:
        return r.replayDecayFeedback(entry)
    }
    return nil
}

func (r *RecoveryManager) replayWeightObservation(entry *WALEntry) error {
    payload := entry.Payload.(*WeightObservationPayload)

    // Re-apply observation to weight system (with full hierarchy)
    return r.weightManager.ApplyWeightObservation(&WeightObservation{
        Domain:       Domain(payload.Domain),
        Model:        ModelID(payload.Model),
        Agent:        payload.Agent,
        Signal:       payload.Signal,
        Outcome:      payload.Outcome,
        Satisfaction: payload.Satisfaction,
        QueryContext: payload.QueryContext,
        ContentHash:  payload.ContentHash,
    })
}

func (r *RecoveryManager) replayGPObservation(entry *WALEntry) error {
    payload := entry.Payload.(*GPObservationPayload)

    // Re-apply GP observation to model degradation tracking
    return r.weightManager.ApplyGPObservation(
        ModelID(payload.Model),
        payload.Turn,
        payload.Quality,
    )
}

func (r *RecoveryManager) replayThresholdFeedback(entry *WALEntry) error {
    payload := entry.Payload.(*ThresholdFeedbackPayload)

    // Re-apply threshold feedback for Bayesian threshold learning
    return r.weightManager.ApplyThresholdFeedback(
        Domain(payload.Domain),
        payload.Confidence,
        payload.WasGood,
    )
}

func (r *RecoveryManager) replayDecayFeedback(entry *WALEntry) error {
    payload := entry.Payload.(*DecayFeedbackPayload)

    // Re-apply decay feedback for decay rate learning
    return r.weightManager.ApplyDecayFeedback(
        Domain(payload.Domain),
        payload.AgeHours,
        payload.WasAccurate,
    )
}
```

### StagingManager Pattern for Weights

The weight system follows the same pattern as `StagingManager` for copy-on-read isolation:

```go
// Comparison: StagingManager vs SessionWeightManager

// StagingManager (existing) - for file isolation
type PipelineStaging struct {
    pipelineID string
    stagingDir string
    fileHashes map[string]string    // path → hash at read time
    deleted    map[string]bool
    manager    *StagingManager
}

func (ps *PipelineStaging) ReadFile(relativePath string) ([]byte, error) {
    // 1. Check staging (overlay)
    // 2. Copy from work dir on first read (copy-on-read)
    // 3. Record hash for conflict detection
}

func (ps *PipelineStaging) Merge() (*MergeResult, error) {
    // Three-way merge with conflict detection
}

// SessionWeightManager (new) - same pattern for weights
type SessionWeightManager struct {
    sessionID     string
    overlayDir    string
    overlay       map[WeightKey]*RobustWeightDistribution  // key → weight
    dirtyKeys     map[WeightKey]struct{}
    forkedVersion int64
    baseWeights   *ProjectWeights  // read-only
}

func (m *SessionWeightManager) GetWeight(key WeightKey) *RobustWeightDistribution {
    // 1. Check overlay (in-memory)
    // 2. Read from base (copy-on-write on first modification)
}

func (m *SessionWeightManager) MergeToProject() error {
    // Three-way merge with conflict detection
}
```

### Complete Integration Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ WEIGHT SYSTEM INTEGRATION WITH EXISTING INFRASTRUCTURE                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│                           ┌─────────────────┐                               │
│                           │ SessionManager  │                               │
│                           │   (existing)    │                               │
│                           └────────┬────────┘                               │
│                                    │                                        │
│                    Creates/Manages │                                        │
│                                    ▼                                        │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         Session                                      │   │
│  │  ┌─────────────────────────────────────────────────────────────────┐ │   │
│  │  │ SessionWeightManager (new)                                      │ │   │
│  │  │                                                                 │ │   │
│  │  │  ┌─────────────────┐   ┌─────────────────┐   ┌──────────────┐  │ │   │
│  │  │  │ In-Memory       │   │ Overlay DB      │   │ Project Base │  │ │   │
│  │  │  │ Overlay         │ ← │ (session-local) │ ← │ (read-only)  │  │ │   │
│  │  │  │ (fast access)   │   │ (persistence)   │   │ (shared)     │  │ │   │
│  │  │  └────────┬────────┘   └─────────────────┘   └──────────────┘  │ │   │
│  │  │           │                                                     │ │   │
│  │  │           │ Writes logged to                                    │ │   │
│  │  │           ▼                                                     │ │   │
│  │  │  ┌─────────────────────────────────────────────────────────┐   │ │   │
│  │  │  │ WriteAheadLog (existing)                                │   │ │   │
│  │  │  │                                                         │   │ │   │
│  │  │  │ • EntryWeightObservation added to entry types           │   │ │   │
│  │  │  │ • fsync on write (configurable: every/batched/periodic) │   │ │   │
│  │  │  │ • Segmented files with rotation                         │   │ │   │
│  │  │  └─────────────────────────────────────────────────────────┘   │ │   │
│  │  └─────────────────────────────────────────────────────────────────┘ │   │
│  │                                                                      │   │
│  │  ┌─────────────────────────────────────────────────────────────────┐ │   │
│  │  │ Checkpointer (existing)                                         │ │   │
│  │  │                                                                 │ │   │
│  │  │ • WeightStateProvider implements CheckpointStateProvider        │ │   │
│  │  │ • Weight state included in periodic checkpoints                 │ │   │
│  │  │ • Atomic write via temp file + rename                           │ │   │
│  │  └─────────────────────────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │ RecoveryManager (existing)                                           │   │
│  │                                                                      │   │
│  │ On crash recovery:                                                   │   │
│  │   1. Load checkpoint (includes WeightState)                          │   │
│  │   2. Replay WAL from checkpoint.wal_sequence + 1                     │   │
│  │   3. EntryWeightObservation entries re-applied to weights            │   │
│  │   4. Session resumes with recovered weight state                     │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │ On Session Complete                                                  │   │
│  │                                                                      │   │
│  │ ThreeWayMerger (new, follows StagingManager pattern):                │   │
│  │   1. Load base at forked_from_version                                │   │
│  │   2. Load current base                                               │   │
│  │   3. Compute deltas, merge with conflict resolution                  │   │
│  │   4. Atomic write with optimistic locking                            │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Implementation Types

```go
// core/quality/session_weights.go

// SessionWeightManager handles weight isolation and persistence per-session.
// Extended to support GP observations, threshold feedback, and decay rate learning.
// Follows the same patterns as StagingManager for copy-on-write isolation.
type SessionWeightManager struct {
    sessionID       string
    sessionDir      string                    // ~/.sylk/sessions/{id}/
    projectDir      string                    // ~/.sylk/projects/{hash}/

    // In-memory state (isolated to this session)
    overlay         map[WeightKey]*RobustWeightDistribution
    dirtyKeys       map[WeightKey]struct{}
    mu              sync.RWMutex

    // GP observations per model (full history during session)
    gpObservations  map[ModelID][]GPObservation
    gpDirty         map[ModelID]struct{}

    // Threshold feedback observations per domain
    thresholdObs    map[Domain][]ThresholdObservation
    thresholdDirty  map[Domain]struct{}

    // Decay rate observations per domain
    decayObs        map[Domain][]DecayObservation
    decayDirty      map[Domain]struct{}

    // Base layer (read-only, shared)
    baseWeights     *ProjectWeights
    baseGPs         map[ModelID]*ModelGaussianProcess
    baseThresholds  map[Domain]*LearnedThreshold
    baseDecayRates  *DomainDecayRates
    forkedVersion   int64

    // Integration with existing infrastructure
    wal             *concurrency.WriteAheadLog  // Existing WAL
    checkpointer    *concurrency.Checkpointer   // Existing checkpointer
}

type WeightKey struct {
    Domain Domain `json:"domain"`
    Model  ModelID `json:"model"`
    Agent  string `json:"agent"`
    Signal string `json:"signal"`
}

// DecayObservation records document accuracy at a given age.
type DecayObservation struct {
    Domain     Domain  `json:"domain"`
    AgeHours   float64 `json:"age_hours"`
    WasAccurate bool   `json:"was_accurate"`
    Time       int64   `json:"time"`
}

// NewSessionWeightManager creates isolated weights for a session.
func NewSessionWeightManager(
    sessionID string,
    sessionDir string,
    projectDir string,
    wal *concurrency.WriteAheadLog,
    checkpointer *concurrency.Checkpointer,
) (*SessionWeightManager, error) {

    mgr := &SessionWeightManager{
        sessionID:       sessionID,
        sessionDir:      sessionDir,
        projectDir:      projectDir,
        overlay:         make(map[WeightKey]*RobustWeightDistribution),
        dirtyKeys:       make(map[WeightKey]struct{}),
        gpObservations:  make(map[ModelID][]GPObservation),
        gpDirty:         make(map[ModelID]struct{}),
        thresholdObs:    make(map[Domain][]ThresholdObservation),
        thresholdDirty:  make(map[Domain]struct{}),
        decayObs:        make(map[Domain][]DecayObservation),
        decayDirty:      make(map[Domain]struct{}),
        wal:             wal,
    }

    // Load project base (read-only, potentially memory-mapped)
    base, version, err := LoadProjectWeights(projectDir)
    if err != nil {
        return nil, err
    }
    mgr.baseWeights = base
    mgr.forkedVersion = version

    // Load base GPs, thresholds, and decay rates
    mgr.baseGPs, _ = LoadProjectGPs(projectDir)
    mgr.baseThresholds, _ = LoadProjectThresholds(projectDir)
    mgr.baseDecayRates, _ = LoadProjectDecayRates(projectDir)

    // Register with checkpointer
    if checkpointer != nil {
        checkpointer.RegisterStateProvider(&WeightStateProvider{mgr})
    }

    return mgr, nil
}

// GetWeight retrieves a weight, checking overlay first, then base.
func (m *SessionWeightManager) GetWeight(key WeightKey) *RobustWeightDistribution {
    m.mu.RLock()
    defer m.mu.RUnlock()

    // Check overlay first (modified weights)
    if dist, ok := m.overlay[key]; ok {
        return dist
    }

    // Fall through to base (read-only)
    return m.baseWeights.Get(key)
}

// ObserveWeightOutcome records a weight observation with WAL durability.
func (m *SessionWeightManager) ObserveWeightOutcome(obs *WeightObservation) error {
    key := WeightKey{Domain: obs.Domain, Model: obs.Model, Agent: obs.Agent, Signal: obs.Signal}

    // 1. Log to WAL FIRST (durability guarantee)
    entry := &concurrency.WALEntry{
        Type:    concurrency.EntryWeightObservation,
        Payload: obs.ToPayload(),
    }
    if _, err := m.wal.Append(entry); err != nil {
        return fmt.Errorf("WAL append failed: %w", err)
    }

    // 2. Apply to overlay (after WAL guarantees durability)
    m.mu.Lock()
    defer m.mu.Unlock()

    dist, ok := m.overlay[key]
    if !ok {
        // Copy-on-write: copy from base on first modification
        dist = m.baseWeights.Get(key).Clone()
        m.overlay[key] = dist
    }

    dist.Update(obs.Outcome, obs.Satisfaction, DefaultUpdateConfig())
    m.dirtyKeys[key] = struct{}{}

    return nil
}

// ObserveGPQuality records a (turn, quality) observation for model degradation GP.
func (m *SessionWeightManager) ObserveGPQuality(model ModelID, turn int, quality float64) error {
    obs := GPObservation{
        Turn:    turn,
        Quality: quality,
        Time:    time.Now().Unix(),
    }

    // 1. Log to WAL FIRST
    entry := &concurrency.WALEntry{
        Type: concurrency.EntryGPObservation,
        Payload: &GPObservationPayload{
            Model:   string(model),
            Turn:    turn,
            Quality: quality,
        },
    }
    if _, err := m.wal.Append(entry); err != nil {
        return fmt.Errorf("WAL append failed: %w", err)
    }

    // 2. Add to session's GP observations
    m.mu.Lock()
    defer m.mu.Unlock()

    m.gpObservations[model] = append(m.gpObservations[model], obs)
    m.gpDirty[model] = struct{}{}

    return nil
}

// GetGP returns the GP for a model with session observations applied.
func (m *SessionWeightManager) GetGP(model ModelID) *ModelGaussianProcess {
    m.mu.RLock()
    defer m.mu.RUnlock()

    // Get base GP (or create new one)
    gp := m.baseGPs[model]
    if gp == nil {
        gp = NewModelGaussianProcess(model)
    } else {
        gp = gp.Clone()
    }

    // Apply session observations
    for _, obs := range m.gpObservations[model] {
        gp.AddObservation(obs.Turn, obs.Quality)
    }

    return gp
}

// ObserveThresholdFeedback records threshold decision outcome.
func (m *SessionWeightManager) ObserveThresholdFeedback(domain Domain, confidence float64, wasGood bool) error {
    obs := ThresholdObservation{
        Confidence: confidence,
        Outcome:    wasGood,
        Time:       time.Now().Unix(),
    }

    // 1. Log to WAL FIRST
    entry := &concurrency.WALEntry{
        Type: concurrency.EntryThresholdFeedback,
        Payload: &ThresholdFeedbackPayload{
            Domain:     int(domain),
            Confidence: confidence,
            WasGood:    wasGood,
        },
    }
    if _, err := m.wal.Append(entry); err != nil {
        return fmt.Errorf("WAL append failed: %w", err)
    }

    // 2. Add to session's threshold observations
    m.mu.Lock()
    defer m.mu.Unlock()

    m.thresholdObs[domain] = append(m.thresholdObs[domain], obs)
    m.thresholdDirty[domain] = struct{}{}

    return nil
}

// GetThreshold returns the learned threshold for a domain with session feedback applied.
func (m *SessionWeightManager) GetThreshold(domain Domain) *LearnedThreshold {
    m.mu.RLock()
    defer m.mu.RUnlock()

    // Get base threshold (or create new one)
    threshold := m.baseThresholds[domain]
    if threshold == nil {
        threshold = NewLearnedThreshold(domain)
    } else {
        threshold = threshold.Clone()
    }

    // Apply session observations
    for _, obs := range m.thresholdObs[domain] {
        threshold.UpdateFromReview(obs.Confidence, obs.Outcome)
    }

    return threshold
}

// ObserveDecayFeedback records document accuracy for decay rate learning.
func (m *SessionWeightManager) ObserveDecayFeedback(domain Domain, ageHours float64, wasAccurate bool) error {
    obs := DecayObservation{
        Domain:      domain,
        AgeHours:    ageHours,
        WasAccurate: wasAccurate,
        Time:        time.Now().Unix(),
    }

    // 1. Log to WAL FIRST
    entry := &concurrency.WALEntry{
        Type: concurrency.EntryDecayFeedback,
        Payload: &DecayFeedbackPayload{
            Domain:      int(domain),
            AgeHours:    ageHours,
            WasAccurate: wasAccurate,
        },
    }
    if _, err := m.wal.Append(entry); err != nil {
        return fmt.Errorf("WAL append failed: %w", err)
    }

    // 2. Add to session's decay observations
    m.mu.Lock()
    defer m.mu.Unlock()

    m.decayObs[domain] = append(m.decayObs[domain], obs)
    m.decayDirty[domain] = struct{}{}

    return nil
}

// GetWeightState returns state for SessionSnapshot (extended for new observation types).
func (m *SessionWeightManager) GetWeightState() *WeightState {
    m.mu.RLock()
    defer m.mu.RUnlock()

    return &WeightState{
        ForkedFromVersion:   m.forkedVersion,
        WALSequence:         m.wal.CurrentSequence(),
        DirtyWeightKeys:     len(m.dirtyKeys),
        DirtyGPModels:       len(m.gpDirty),
        DirtyThresholds:     len(m.thresholdDirty),
        DirtyDecayDomains:   len(m.decayDirty),
        GPObservationCount:  m.countGPObservations(),
        ThresholdObsCount:   m.countThresholdObservations(),
        DecayObsCount:       m.countDecayObservations(),
        LastCheckpoint:      time.Now().Unix(),
    }
}

// SerializeOverlay serializes all overlay state for checkpointing.
func (m *SessionWeightManager) SerializeOverlay() ([]byte, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    state := &SessionOverlayState{
        Weights:      m.overlay,
        GPObs:        m.gpObservations,
        ThresholdObs: m.thresholdObs,
        DecayObs:     m.decayObs,
    }

    return json.Marshal(state)
}

// RestoreOverlay restores all overlay state from checkpoint data.
func (m *SessionWeightManager) RestoreOverlay(data []byte) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    var state SessionOverlayState
    if err := json.Unmarshal(data, &state); err != nil {
        return err
    }

    m.overlay = state.Weights
    m.gpObservations = state.GPObs
    m.thresholdObs = state.ThresholdObs
    m.decayObs = state.DecayObs

    // Rebuild dirty tracking
    for key := range m.overlay {
        m.dirtyKeys[key] = struct{}{}
    }
    for model := range m.gpObservations {
        m.gpDirty[model] = struct{}{}
    }
    for domain := range m.thresholdObs {
        m.thresholdDirty[domain] = struct{}{}
    }
    for domain := range m.decayObs {
        m.decayDirty[domain] = struct{}{}
    }

    return nil
}

// SessionOverlayState holds all overlay state for serialization.
type SessionOverlayState struct {
    Weights      map[WeightKey]*RobustWeightDistribution `json:"weights"`
    GPObs        map[ModelID][]GPObservation             `json:"gp_observations"`
    ThresholdObs map[Domain][]ThresholdObservation       `json:"threshold_observations"`
    DecayObs     map[Domain][]DecayObservation           `json:"decay_observations"`
}
```

### Three-Way Merge Implementation

```go
// core/quality/weight_merger.go

// ThreeWayMerger performs git-like three-way merge for weight distributions.
type ThreeWayMerger struct {
    projectDir string
}

// MergeSessionToProject merges session learnings back to project base.
func (m *ThreeWayMerger) MergeSessionToProject(
    sessionWeights *SessionWeightManager,
) (*MergeResult, error) {

    maxRetries := 3

    for attempt := 0; attempt < maxRetries; attempt++ {
        // 1. Load the three states
        baseAtFork, _ := m.loadSnapshot(sessionWeights.forkedVersion)
        currentBase, currentVersion := m.loadCurrentBase()
        sessionOverlay := sessionWeights.overlay

        // 2. Perform three-way merge
        merged, conflicts := m.merge(baseAtFork, currentBase, sessionOverlay)

        // 3. Attempt atomic write with version check
        success, err := m.atomicWrite(merged, currentVersion)
        if err != nil {
            return nil, err
        }

        if success {
            return &MergeResult{
                MergedKeys:     len(merged),
                Conflicts:      conflicts,
                NewVersion:     currentVersion + 1,
            }, nil
        }

        // Version changed, retry with new base
        log.Printf("Weight merge conflict, retrying (attempt %d/%d)", attempt+1, maxRetries)
    }

    return nil, fmt.Errorf("merge failed after %d retries", maxRetries)
}

func (m *ThreeWayMerger) merge(
    baseAtFork, currentBase *ProjectWeights,
    sessionOverlay map[WeightKey]*RobustWeightDistribution,
) (map[WeightKey]*RobustWeightDistribution, []MergeConflict) {

    merged := make(map[WeightKey]*RobustWeightDistribution)
    var conflicts []MergeConflict

    // Start with current base
    for key, dist := range currentBase.weights {
        merged[key] = dist.Clone()
    }

    // Apply session changes
    for key, sessionDist := range sessionOverlay {
        baseDist := baseAtFork.Get(key)
        currentDist := currentBase.Get(key)

        // Calculate deltas
        ourDeltaAlpha := sessionDist.Alpha - baseDist.Alpha
        ourDeltaBeta := sessionDist.Beta - baseDist.Beta

        theirDeltaAlpha := currentDist.Alpha - baseDist.Alpha
        theirDeltaBeta := currentDist.Beta - baseDist.Beta

        // Check for conflict (opposite directions)
        ourDirection := sign(ourDeltaAlpha - ourDeltaBeta)
        theirDirection := sign(theirDeltaAlpha - theirDeltaBeta)

        if ourDirection != 0 && theirDirection != 0 && ourDirection != theirDirection {
            // Conflict: resolve by weighting by evidence
            conflict := m.resolveConflict(key, baseDist, sessionDist, currentDist)
            conflicts = append(conflicts, conflict)
            merged[key] = conflict.Resolution
        } else {
            // No conflict: additive merge
            merged[key] = &RobustWeightDistribution{
                Alpha:            baseDist.Alpha + ourDeltaAlpha + theirDeltaAlpha,
                Beta:             baseDist.Beta + ourDeltaBeta + theirDeltaBeta,
                EffectiveSamples: baseDist.EffectiveSamples +
                    (sessionDist.EffectiveSamples - baseDist.EffectiveSamples) +
                    (currentDist.EffectiveSamples - baseDist.EffectiveSamples),
            }
        }
    }

    return merged, conflicts
}

func (m *ThreeWayMerger) atomicWrite(
    weights map[WeightKey]*RobustWeightDistribution,
    expectedVersion int64,
) (bool, error) {

    lockPath := filepath.Join(m.projectDir, "weights", "base.lock")
    lock, err := acquireFileLock(lockPath)
    if err != nil {
        return false, err
    }
    defer lock.Release()

    // Double-check version under lock
    actualVersion := m.readVersion()
    if actualVersion != expectedVersion {
        return false, nil  // Retry with new base
    }

    // Write new base
    basePath := filepath.Join(m.projectDir, "weights", "base.db")
    if err := writeWeights(basePath, weights); err != nil {
        return false, err
    }

    // Save snapshot for future three-way merges
    snapshotPath := filepath.Join(m.projectDir, "weights", "snapshots",
        fmt.Sprintf("v%03d.snapshot", actualVersion+1))
    writeWeights(snapshotPath, weights)

    // Increment version
    m.writeVersion(actualVersion + 1)

    return true, nil
}
```

### Summary of Integration Points

| Existing Component | Integration Method | Purpose |
|--------------------|-------------------|---------|
| `WriteAheadLog` | Add `EntryWeightObservation` type | Durable logging of weight observations |
| `Checkpointer` | Implement `CheckpointStateProvider` | Include weight state in periodic snapshots |
| `RecoveryManager` | Add case for `EntryWeightObservation` | Replay weight observations after crash |
| `StagingManager` | Follow same pattern | Copy-on-write isolation for weights |
| `SessionSnapshot` | Add `WeightState` field | Persist weight state across suspend/resume |

---

## 13. Implementation Files

### New Files to Create

```
core/quality/
├── weights.go                 # QualityWeights, DefaultQualityWeights
├── hierarchical_weights.go    # HierarchicalWeightSystem, Thompson Sampling
├── quality_scorer.go          # QualityScorer (staleness, usage, authority)
├── unified_signals.go         # UnifiedSignalStore
├── identity.go                # ContentIdentity, IdentityResolver
├── feedback_pipeline.go       # FeedbackPipeline, FeedbackEvent
├── causal_attribution.go      # CausalAttributor
├── citation_tracker.go        # CitationTracker, dual-source
├── shift_detector.go          # ConversationShiftDetector
├── session_weights.go         # SessionWeightManager (copy-on-write isolation)
├── weight_merger.go           # ThreeWayMerger (git-like weight merging)
├── weight_checkpoint.go       # WeightStateProvider (checkpoint integration)
├── project_weights.go         # ProjectWeights (base weights with versioning)
├── schema.sql                 # unified_quality_signals table
└── *_test.go                  # Tests for each

core/vectorgraphdb/
├── quality_query.go           # QualityQueryEngine, QualityAdjustedResult
└── quality_query_test.go

core/search/
├── coordinator/
│   ├── coordinator.go         # SearchCoordinator with quality (DS.9.2)
│   ├── rrf.go                 # RRFMerger with identity (DS.9.3)
│   └── signal_store.go        # DocumentSignalStore
└── hybrid/
    ├── coordinator.go         # HybridCoordinator (unified search)
    └── coordinator_test.go
```

### Modified Files

```
core/vectorgraphdb/types.go    # Verify TrustLevel, Confidence used
core/search/document.go        # Add Domain, TrustLevel, Confidence fields

# Session Weight Infrastructure (integrate with existing concurrency package)
core/concurrency/wal_entry.go  # Add EntryWeightObservation type
core/concurrency/recovery.go   # Add replayWeightObservation() case
core/session/snapshot.go       # Add WeightState field to SessionSnapshot
```

### Integration with Existing Systems

| Existing System | Integration Point |
|-----------------|-------------------|
| `RobustWeightDistribution` (CONTEXT.md) | Powers hierarchical weight learning |
| `GraphNode.TrustLevel` | Used directly in `computeTrustScore()` |
| `GraphNode.Confidence` | Fallback for authority when no signals |
| `QueryEngine.HybridQuery()` | Wrapped by `QualityQueryEngine` |
| `UniversalContentStore` | Uses `HybridCoordinator` for search |
| `SearchCoordinator` (DS.9.2) | Extended with quality scoring |
| `RRFMerger` (DS.9.3) | Extended with identity tracking |
| `WriteAheadLog` (concurrency) | Add `EntryWeightObservation` for durable logging |
| `Checkpointer` (concurrency) | `WeightStateProvider` implements `CheckpointStateProvider` |
| `RecoveryManager` (concurrency) | Replay weight observations on crash recovery |
| `StagingManager` (concurrency) | Pattern for copy-on-write weight isolation |
| `SessionSnapshot` (session) | Add `WeightState` for suspend/resume persistence |

---

## Summary

**CORE PRINCIPLE: Nothing is hardcoded. Everything is a posterior.** See [MEMORY.md](./MEMORY.md) for the full Bayesian architecture.

The Quality-Based Scoring system provides:

1. **Fully Bayesian Learned Weights** - All quality signal weights are Beta posteriors, not point estimates. Updated from observations with Thompson Sampling for exploration-exploitation.

2. **Five-Level Hierarchical Learning** - Global → Domain → Model → Agent → Session hierarchy with full cross-product (420 leaf nodes) and bidirectional information flow (empirical Bayes upward, hierarchical priors downward).

3. **Gaussian Process Model Degradation** - Non-parametric GP learns actual degradation curves per model. NO assumption about exponential/hyperbolic/linear - discovers warm-up periods, plateaus, and sudden drops from data.

4. **Learned Decay Rates** - Document freshness decay rates are Gamma posteriors per domain, updated from "document at age A was accurate/inaccurate" observations.

5. **Bayesian Decision Theory Thresholds** - Confidence thresholds learned from review queue outcomes using expected loss minimization. Per-domain base + per-agent adjustments.

6. **Learned Trust Multipliers** - Source trust (LLM, Code, User, etc.) as Beta posteriors per source type and model, updated from verification outcomes.

7. **Domain Expertise Compliance** - Respects ARCHITECTURE.md Domain Expertise System: agents search only their home domain (Librarian→Code, Academic→Academic, Archivalist→History).

8. **Session Weight Isolation** - Copy-on-write semantics ensure concurrent sessions never interfere. Full overlay state includes weights, GP observations, threshold feedback, and decay feedback.

9. **GP Storage Strategy** - Hybrid approach: full observation history during session (exact GP inference), inducing point compression at checkpoint (800 bytes per model). Merges via product-of-experts.

10. **Session Weight Persistence** - WAL-based durability with four new entry types: `EntryWeightObservation`, `EntryGPObservation`, `EntryThresholdFeedback`, `EntryDecayFeedback`. Zero data loss across suspend/resume and crash/recovery.

11. **Three-Way Weight Merging** - Git-like merge semantics for all learned components: weights (Δα, Δβ), GP inducing points, threshold posteriors, decay rate posteriors.

12. **Integration with Existing Infrastructure** - Leverages existing `WriteAheadLog`, `Checkpointer`, `RecoveryManager`, and `StagingManager` patterns rather than building new components
