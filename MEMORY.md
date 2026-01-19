# Semantic Memory Decay & Learned Parameters

## Overview

This document defines the **Fully Bayesian Hierarchical Learning System** for decay rates, thresholds, trust multipliers, and degradation curves. **Nothing is hardcoded. Everything is a posterior.**

**Implementation**: See [SCORING.md](./SCORING.md) for the full implementation architecture, including:
- Section 2: Agent-Specific Quality Models with learned weights
- Section 3: Five-level hierarchical weight learning with GP storage strategy
- Section 11: SessionWeightManager with GP, threshold, and decay observations
- Section 12: WAL entry types and RecoveryManager integration

**Integration Requirements**: This system respects and integrates with:
- [ARCHITECTURE.md](./ARCHITECTURE.md) Domain Expertise System (agents search only their home domain)
- [SCORING.md](./SCORING.md) Session Isolation (copy-on-write, three-way merge)
- [SCORING.md](./SCORING.md) Session Recovery (WAL-based durability, checkpoint integration)

This system applies to:
- **Agent Quality Observation Decay** - Learning model/agent degradation curves (GP per model)
- **Document Freshness Decay** - Learning domain-specific decay rates (Gamma posteriors)
- **Vector Retrieval Scoring** - Learning trust and relevance weights (Beta posteriors)
- **Threshold Calibration** - Learning optimal confidence cutoffs (Bayesian decision theory)

---

## Core Principle

Every parameter that affects decay, thresholds, trust, and degradation is a **distribution**, not a point estimate. All distributions are **learned from observations**. Uncertainty is **propagated** through decisions.

---

## Hierarchical Architecture

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    FULLY LEARNED DECAY & DEGRADATION SYSTEM                          │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  NOTHING IS HARDCODED. EVERYTHING IS A POSTERIOR.                                   │
│                                                                                     │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │ LAYER 1: GLOBAL PRIORS (weakly informative, shared across system)             │  │
│  │                                                                               │  │
│  │   λ_decay_global    ~ Gamma(2, 100)      # Decay rate prior                   │  │
│  │   τ_threshold_global ~ Beta(3, 3)        # Threshold prior (centered at 0.5)  │  │
│  │   trust_global      ~ Beta(2, 2)         # Trust prior (uncertain)            │  │
│  │   σ_quality_global  ~ HalfNormal(0.2)    # Quality noise prior                │  │
│  │                                                                               │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│                                          ▼                                          │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │ LAYER 2: DOMAIN PRIORS (Code, History, Academic)                              │  │
│  │                                                                               │  │
│  │   λ_decay[domain]    | λ_global ~ Gamma(α_d, β_d)                            │  │
│  │   τ_threshold[domain] | τ_global ~ Beta(α_d, β_d)                            │  │
│  │   staleness[domain]  | λ_decay[domain] ~ derived                             │  │
│  │                                                                               │  │
│  │   Learned from: "Document in domain D at age A was accurate/inaccurate"      │  │
│  │                                                                               │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│                                          ▼                                          │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │ LAYER 3: MODEL PRIORS (Sonnet 4.5, Opus 4.5, Haiku 4.5, Codex 5.2)           │  │
│  │                                                                               │  │
│  │   degradation_curve[model] ~ GaussianProcess(                                │  │
│  │       mean = μ_model(t),           # Prior mean function                     │  │
│  │       kernel = Matern(ν=2.5, ℓ)    # Smoothness prior                        │  │
│  │   )                                                                          │  │
│  │                                                                               │  │
│  │   Learned from: "Model M at turn T produced quality Q"                       │  │
│  │                                                                               │  │
│  │   NO ASSUMPTION about exponential/hyperbolic/linear - GP learns shape        │  │
│  │                                                                               │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│                                          ▼                                          │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │ LAYER 4: AGENT PRIORS (Librarian, Engineer, Inspector, etc.)                 │  │
│  │                                                                               │  │
│  │   quality_baseline[agent][model] ~ Normal(μ_am, σ_am)                        │  │
│  │   degradation_rate[agent][model] ~ inherited from model GP + agent offset    │  │
│  │   recovery_rate[agent][model]    ~ Gamma(α, β)  # How fast after handoff     │  │
│  │                                                                               │  │
│  │   Learned from: Agent-specific observations                                  │  │
│  │                                                                               │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                          │                                          │
│                                          ▼                                          │
│  ┌───────────────────────────────────────────────────────────────────────────────┐  │
│  │ LAYER 5: SESSION POSTERIORS (live, per-session)                              │  │
│  │                                                                               │  │
│  │   Current session observations update all layers                             │  │
│  │   Posteriors used for real-time decisions                                    │  │
│  │   Thompson Sampling for exploration-exploitation                             │  │
│  │                                                                               │  │
│  └───────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Model Degradation: Gaussian Process (Non-Parametric)

We do **not** assume exponential decay. We learn the actual shape.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    GAUSSIAN PROCESS DEGRADATION MODEL                                │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  For model M, quality at turn t:                                                    │
│                                                                                     │
│      Q_M(t) ~ GP(m(t), k(t, t'))                                                   │
│                                                                                     │
│  Prior mean function m(t):                                                          │
│      m(t) = 0.8 - 0.001*t    # Weak prior: slight decline, but learnable           │
│                                                                                     │
│  Covariance kernel k(t, t'):                                                        │
│      k(t, t') = σ² * Matern(|t-t'|; ν=2.5, ℓ)                                      │
│                                                                                     │
│      Where:                                                                         │
│        σ² ~ InverseGamma(α, β)    # Output variance (learned)                      │
│        ℓ  ~ LogNormal(μ, σ)       # Length scale (learned)                         │
│        ν = 2.5                    # Smoothness (twice differentiable)              │
│                                                                                     │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│  EXAMPLE: Learning Sonnet 4.5 Degradation                                           │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│                                                                                     │
│  Observations:                                                                      │
│    Turn 1:  Q = 0.72                                                               │
│    Turn 5:  Q = 0.85  (warm-up!)                                                   │
│    Turn 10: Q = 0.88                                                               │
│    Turn 20: Q = 0.82                                                               │
│    Turn 35: Q = 0.71                                                               │
│    Turn 50: Q = 0.58                                                               │
│                                                                                     │
│  GP Posterior learns NON-MONOTONIC curve:                                           │
│                                                                                     │
│  Q(t)                                                                              │
│   │                                                                                │
│  1.0┤                                                                              │
│     │         ┌──────╮                                                             │
│  0.8┤    ╭───╯       ╰────╮                                                        │
│     │   ╱                  ╰───╮                                                   │
│  0.6┤──╯                        ╰───╮                                              │
│     │                                ╰────                                         │
│  0.4┤                                     ╰───                                     │
│     │                                                                              │
│  0.2┤                                                                              │
│     │                                                                              │
│   0 ┼────┬────┬────┬────┬────┬────┬────┬────► t                                   │
│     0   10   20   30   40   50   60   70                                           │
│                                                                                     │
│  Shaded region = 95% credible interval (uncertainty)                               │
│                                                                                     │
│  Key insight: Learned that Sonnet has WARM-UP period (turns 1-10)                  │
│               then plateau (turns 10-25) then decline (turns 25+)                  │
│               Exponential assumption would MISS the warm-up                         │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Decay Rate Learning (Conjugate Bayesian)

For document freshness decay:

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    DECAY RATE LEARNING                                               │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  Model:                                                                             │
│      Document freshness: f(age) = e^(-λ * age)                                     │
│      Decay rate: λ ~ Gamma(α, β)                                                   │
│                                                                                     │
│  Observation types:                                                                 │
│      POSITIVE: Document at age A was still accurate                                │
│      NEGATIVE: Document at age A was outdated/wrong                                │
│                                                                                     │
│  Likelihood:                                                                        │
│      P(accurate | age, λ) = sigmoid(f(age) - τ)                                    │
│                           = sigmoid(e^(-λ*age) - τ)                                │
│                                                                                     │
│  Update (variational or MCMC):                                                      │
│      P(λ | observations) ∝ P(observations | λ) * P(λ)                              │
│                                                                                     │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│  HIERARCHICAL STRUCTURE                                                             │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│                                                                                     │
│                     λ_global ~ Gamma(2, 200)                                       │
│                            │                                                        │
│            ┌───────────────┼───────────────┐                                        │
│            ▼               ▼               ▼                                        │
│       λ_code          λ_history       λ_academic                                   │
│     ~ Gamma(α_c, β_c) ~ Gamma(α_h, β_h) ~ Gamma(α_a, β_a)                         │
│            │               │               │                                        │
│     Posterior:       Posterior:       Posterior:                                   │
│     E[λ] = 0.015     E[λ] = 0.008     E[λ] = 0.001                                │
│     Var[λ] = small   Var[λ] = medium  Var[λ] = large                              │
│     (many obs)       (some obs)       (few obs)                                    │
│                                                                                     │
│  Information flows UP (posteriors inform global) and DOWN (global informs          │
│  domains with few observations)                                                     │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Threshold Learning (Bayesian Decision Theory)

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    THRESHOLD LEARNING                                                │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  Problem: What confidence threshold τ minimizes expected loss?                      │
│                                                                                     │
│  Loss function:                                                                     │
│      L(τ) = C_FP * P(false positive | τ) + C_FN * P(false negative | τ)           │
│                                                                                     │
│  Where:                                                                             │
│      C_FP = cost of accepting bad content (hallucination propagates)               │
│      C_FN = cost of rejecting good content (useful info lost)                      │
│                                                                                     │
│  Learning P(FP | τ) and P(FN | τ):                                                 │
│                                                                                     │
│      From review queue:                                                            │
│        Items with confidence c that were APPROVED → P(good | c)                    │
│        Items with confidence c that were REJECTED → P(bad | c)                     │
│                                                                                     │
│      Model: P(good | c) = Φ((c - μ) / σ)  where μ, σ learned                       │
│                                                                                     │
│  Optimal threshold:                                                                 │
│      τ* = argmin E[L(τ) | data]                                                    │
│                                                                                     │
│      With uncertainty: P(τ* | data) is a distribution                              │
│                                                                                     │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│  EXAMPLE: Threshold learning from review queue                                      │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│                                                                                     │
│  Observations:                                                                      │
│    c=0.42: Rejected (bad content)                                                  │
│    c=0.48: Rejected (bad content)                                                  │
│    c=0.53: Approved (was actually good)                                            │
│    c=0.55: Approved (was actually good)                                            │
│    c=0.58: Rejected (bad content)                                                  │
│    c=0.61: Approved (was actually good)                                            │
│    c=0.67: Approved (was actually good)                                            │
│                                                                                     │
│  Learned: P(good | c) transition happens around c ≈ 0.55                           │
│                                                                                     │
│  If C_FP >> C_FN (hallucinations very costly):                                     │
│      τ* ≈ 0.62 (conservative, accept fewer)                                        │
│                                                                                     │
│  If C_FP ≈ C_FN:                                                                   │
│      τ* ≈ 0.55 (balanced)                                                          │
│                                                                                     │
│  Posterior: τ* ~ Normal(0.57, 0.04)  # Mean 0.57, uncertainty ±0.04                │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. Trust Multiplier Learning

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    TRUST MULTIPLIER LEARNING                                         │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  For each source type s:                                                            │
│                                                                                     │
│      trust[s] ~ Beta(α_s, β_s)                                                     │
│                                                                                     │
│  Observations:                                                                      │
│      Content from source s verified CORRECT → α_s += 1                             │
│      Content from source s verified WRONG   → β_s += 1                             │
│                                                                                     │
│  With temporal decay (recent matters more):                                         │
│      α_s *= decay_factor  before adding new observation                            │
│      β_s *= decay_factor                                                           │
│                                                                                     │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│  HIERARCHICAL TRUST                                                                 │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│                                                                                     │
│                    trust_llm_global                                                 │
│                          │                                                          │
│           ┌──────────────┼──────────────┐                                           │
│           ▼              ▼              ▼                                           │
│    trust_llm_sonnet  trust_llm_opus  trust_llm_haiku                               │
│           │              │              │                                           │
│           ▼              ▼              ▼                                           │
│    trust_llm_sonnet  trust_llm_opus  trust_llm_haiku                               │
│    _for_code         _for_code       _for_code                                     │
│                                                                                     │
│  Each level learned from observations at that level                                │
│  Information shared across hierarchy                                                │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 5. Decision Making Under Uncertainty

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    DECISION MAKING WITH LEARNED POSTERIORS                           │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  For handoff decision at turn t for agent A using model M:                          │
│                                                                                     │
│  1. SAMPLE from posteriors (Thompson Sampling):                                     │
│                                                                                     │
│      λ̃ ~ P(λ | data)                    # Decay rate sample                        │
│      Q̃(t) ~ GP_M posterior               # Quality prediction sample               │
│      τ̃ ~ P(τ | data)                    # Threshold sample                         │
│                                                                                     │
│  2. COMPUTE decision:                                                               │
│                                                                                     │
│      If Q̃(t) < τ̃:                                                                 │
│          decision = HANDOFF                                                         │
│      Else:                                                                          │
│          decision = CONTINUE                                                        │
│                                                                                     │
│  3. OBSERVE outcome and UPDATE posteriors:                                          │
│                                                                                     │
│      After turn t, observe actual quality q_t                                      │
│      Update GP: add observation (t, q_t)                                           │
│      Update threshold posterior if explicit feedback                                │
│                                                                                     │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│  WHY THOMPSON SAMPLING?                                                             │
│  ═══════════════════════════════════════════════════════════════════════════════    │
│                                                                                     │
│  • Explores when uncertain (wide posteriors → variable samples)                    │
│  • Exploits when confident (narrow posteriors → consistent samples)                │
│  • Automatically balances exploration-exploitation                                  │
│  • No tuning of exploration parameters needed                                       │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 6. Observation Pipeline

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    OBSERVATION PIPELINE                                              │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  DOCUMENT OBSERVATIONS                     AGENT OBSERVATIONS                       │
│  ─────────────────────                     ──────────────────                       │
│                                                                                     │
│  ┌─────────────────────┐                  ┌─────────────────────┐                   │
│  │ Document retrieved  │                  │ Agent produces      │                   │
│  │ at age A            │                  │ output at turn T    │                   │
│  └──────────┬──────────┘                  └──────────┬──────────┘                   │
│             │                                        │                              │
│             ▼                                        ▼                              │
│  ┌─────────────────────┐                  ┌─────────────────────┐                   │
│  │ Was it used/cited?  │                  │ Quality assessment  │                   │
│  │ Was it accurate?    │                  │ (per-agent criteria)│                   │
│  │ User feedback?      │                  │ Q ∈ [0, 1]         │                   │
│  └──────────┬──────────┘                  └──────────┬──────────┘                   │
│             │                                        │                              │
│             ▼                                        ▼                              │
│  ┌─────────────────────┐                  ┌─────────────────────┐                   │
│  │ Update:             │                  │ Update:             │                   │
│  │ • λ_decay[domain]   │                  │ • GP_M(t) posterior │                   │
│  │ • trust[source]     │                  │ • Agent baseline    │                   │
│  │ • threshold[domain] │                  │ • Handoff threshold │                   │
│  └─────────────────────┘                  └─────────────────────┘                   │
│                                                                                     │
│  REVIEW QUEUE OBSERVATIONS                HANDOFF OBSERVATIONS                      │
│  ─────────────────────────                ────────────────────                      │
│                                                                                     │
│  ┌─────────────────────┐                  ┌─────────────────────┐                   │
│  │ Item queued at      │                  │ Handoff triggered   │                   │
│  │ confidence C        │                  │ at turn T           │                   │
│  └──────────┬──────────┘                  └──────────┬──────────┘                   │
│             │                                        │                              │
│             ▼                                        ▼                              │
│  ┌─────────────────────┐                  ┌─────────────────────┐                   │
│  │ Reviewer decision:  │                  │ New agent quality   │                   │
│  │ APPROVE / REJECT    │                  │ at turn T+1, T+2... │                   │
│  └──────────┬──────────┘                  └──────────┬──────────┘                   │
│             │                                        │                              │
│             ▼                                        ▼                              │
│  ┌─────────────────────┐                  ┌─────────────────────┐                   │
│  │ Update:             │                  │ Evaluate:           │                   │
│  │ • P(good | c) model │                  │ Was handoff good?   │                   │
│  │ • Optimal threshold │                  │ If not, adjust      │                   │
│  │   τ* posterior      │                  │ threshold posterior │                   │
│  └─────────────────────┘                  └─────────────────────┘                   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 7. What This Replaces

| Hardcoded | Fully Learned |
|-----------|---------------|
| `CodeDecayRate: 0.01` | `λ_code ~ Gamma(α, β)` posterior from observations |
| `MinConfidence: 0.6` | `τ* ~ P(τ | review_outcomes)` Bayesian decision theory |
| `SourceTypeLLM: 0.5` | `trust_llm[model] ~ Beta(α, β)` per-model learned |
| `7 * 24 * time.Hour` staleness | Derived from `P(stale | age, λ) > threshold` |
| Assumed exponential decay | GP learns actual curve shape (allows warm-up, plateau, etc.) |
| Assumed model degradation rate | GP per model, learned from quality observations |
| Fixed burn-in period | Learned from "when does GP posterior become confident?" |
| Fixed hysteresis parameters | Learned from "what smoothing minimizes flapping?" |

---

## 8. Computational Approach

Despite complexity, this is performant:

| Component | Method | Cost |
|-----------|--------|------|
| GP updates | Incremental Cholesky | O(n²) per observation, but n is bounded per session |
| Hierarchical updates | Conjugate priors where possible | O(1) for Beta/Gamma updates |
| Posterior sampling | Pre-computed sufficient statistics | O(1) per sample |
| Cross-level sharing | Empirical Bayes | Batch updates, async |
| Caching | Posterior parameters, not samples | Minimal memory |

---

## 9. Application Domains

### 9.1 Agent Quality (Handoff Decisions)

- **GP per model** learns degradation curve shape
- **Hierarchical agent priors** share information across similar agents
- **Thompson Sampling** for handoff threshold decisions
- **Uncertainty-aware**: Wide posteriors → more exploration

### 9.2 Document Freshness (Retrieval Decay)

- **λ per domain** learned from "was document at age A accurate?"
- **Hierarchical sharing** across domains
- **Staleness threshold** derived from learned λ and quality threshold

### 9.3 Vector Similarity (Trust Adjustment)

- **Trust per source type** learned from verification outcomes
- **Confidence threshold** learned from review queue decisions
- **Final score** = similarity × trust × freshness (all learned)

### 9.4 Cross-System Integration

- Same Bayesian framework for all components
- Shared observation pipeline
- Unified posterior storage
- Consistent uncertainty propagation

---

## 10. Key Integration Decisions (Finalized)

The following architectural decisions were made to maximize robustness, correctness, and performance with no regard to complexity:

### 10.1 GP Storage Strategy: Hybrid Approach

**Decision**: Full observation history during session + inducing point compression at checkpoint.

| Phase | Strategy | Rationale |
|-------|----------|-----------|
| During Session | Full history (all observations) | Exact GP inference, O(n²) acceptable for n~100 |
| At Checkpoint | Compress to M=50 inducing points | 800 bytes per model, O(1/M) approximation error |
| On Merge | Combine inducing sets, re-select M | Product-of-experts for conflicting observations |

**Implementation**: See `SCORING.md` Section 3 (GP Storage Strategy) and `ModelGaussianProcess.CompressToInducingPoints()`.

### 10.2 Hierarchy Structure: Full Cross-Product with Bidirectional Flow

**Decision**: Full cross-product leaves (Domain × Model × Agent × Signal = 420 nodes) with hierarchical priors and bidirectional information flow.

| Direction | Mechanism | Purpose |
|-----------|-----------|---------|
| Upward | Empirical Bayes | Leaf posteriors inform parent priors |
| Downward | Hierarchical Shrinkage | Parent priors regularize uncertain children |
| Cross-Domain | Partial Pooling | Similar domains share information with shrinkage |

**Implementation**: See `SCORING.md` Section 3 (`computeEffectivePosterior()` and hierarchy diagram).

### 10.3 Threshold Learning: Per-Domain Base + Per-Agent Adjustments

**Decision**: Domain-specific base thresholds (learned from review queue) with agent-specific adjustment offsets.

| Component | Distribution | Learning Source |
|-----------|--------------|-----------------|
| Domain Base | `τ_domain ~ Beta(α, β)` | Review queue: P(good \| confidence) |
| Agent Adjustment | `Δτ_agent ~ Normal(0, σ)` | Agent-specific outcomes vs domain baseline |
| Effective | `τ = τ_domain + Δτ_agent * 0.2` | Clamped to [0.1, 0.9] |

**Implementation**: See `SCORING.md` Section 3 (`LearnedThreshold` and `GetEffectiveThreshold()`).

### 10.4 Session Isolation Compliance

All learned state (weights, GP observations, threshold feedback, decay feedback) follows copy-on-write isolation:

```
SessionWeightManager
├── overlay (weights)           → Three-way merge (Δα, Δβ)
├── gpObservations (per model)  → Combine inducing points
├── thresholdObs (per domain)   → Combine observations, recompute optimal
└── decayObs (per domain)       → Combine observations, update posterior
```

**Implementation**: See `SCORING.md` Section 11 (`SessionWeightManager` and `SessionOverlayState`).

### 10.5 WAL Entry Types

Four new WAL entry types for Bayesian learning durability:

| Entry Type | Purpose | Replay Method |
|------------|---------|---------------|
| `EntryWeightObservation` | Weight learning (5-level hierarchy) | `replayWeightObservation()` |
| `EntryGPObservation` | GP model degradation | `replayGPObservation()` |
| `EntryThresholdFeedback` | Threshold learning | `replayThresholdFeedback()` |
| `EntryDecayFeedback` | Decay rate learning | `replayDecayFeedback()` |

**Implementation**: See `SCORING.md` Section 12 (WriteAheadLog Integration and RecoveryManager Integration).

---

*Last updated: 2025-01-18*
