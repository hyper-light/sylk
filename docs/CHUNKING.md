# Chunking Architecture

## Overview

Sylk implements a **Semantic-Aware Hierarchical Chunking** system that breaks large documents into semantically coherent units for vector embedding and retrieval. This system respects natural content boundaries (functions, sections, paragraphs) rather than using fixed-size windows.

**Core Principle**: Fixed-size chunking destroys semantic coherence. A function split at line 50 is useless. Instead, we parse content structure and chunk at meaningful boundaries.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    CHUNKING ARCHITECTURE OVERVIEW                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Traditional:  chunk_i = content[i*512 : (i+1)*512]                         │
│                (destroys semantic units, splits mid-sentence)               │
│                                                                             │
│  Sylk:         chunk_i = semantic_unit(content, level=L, index=i)           │
│                (respects functions, sections, paragraphs)                   │
│                                                                             │
│  Benefits:                                                                  │
│    - Complete semantic units → better embeddings                            │
│    - Hierarchical structure → multi-level retrieval                         │
│    - Natural boundaries → precise retrieval (get function, not file)        │
│    - Cross-references → understand dependencies                             │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Table of Contents

1. [Semantic-Aware Chunking by Content Type](#1-semantic-aware-chunking-by-content-type)
2. [Adaptive Chunk Sizing](#2-adaptive-chunk-sizing)
3. [Hierarchical Chunk Structure](#3-hierarchical-chunk-structure)
4. [Storage Model](#4-storage-model)
5. [Retrieval Strategy](#5-retrieval-strategy)
6. [Multi-Level Embedding](#6-multi-level-embedding)
7. [Quality Integration](#7-quality-integration)
8. [Chunk Parameter Learning](#8-chunk-parameter-learning)
9. [Implementation Files](#9-implementation-files)

---

## 1. Semantic-Aware Chunking by Content Type

Different content types have natural semantic boundaries. We use specialized parsers for each.

### Source Code (AST-Based)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ SOURCE CODE CHUNKING                                                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│ Parser: tree-sitter (universal, incremental, error-tolerant)                │
│                                                                             │
│ Hierarchy:                                                                  │
│   Level 0: File (package declaration + imports as preamble)                 │
│   Level 1: Top-level declarations (type, const block, var block)            │
│   Level 2: Functions, Methods, Interface definitions                        │
│   Level 3: Significant blocks (large switch cases, nested funcs)            │
│                                                                             │
│ Chunk includes:                                                             │
│   • Leading comments/docstrings (semantic context)                          │
│   • Full signature (for functions)                                          │
│   • Complete body (never split mid-statement)                               │
│                                                                             │
│ Special handling:                                                           │
│   • Struct fields + methods grouped as "type bundle"                        │
│   • Interface + all implementors tracked (cross-reference)                  │
│   • Test functions linked to tested functions                               │
│                                                                             │
│ Example:                                                                    │
│   ┌──────────────────────────────────────────────────────────────────┐      │
│   │ // auth.go                                                       │      │
│   │ package auth           ─┐                                        │      │
│   │                         │ Level 0: File preamble                 │      │
│   │ import (                │                                        │      │
│   │     "context"           │                                        │      │
│   │     "errors"           ─┘                                        │      │
│   │ )                                                                │      │
│   │                                                                  │      │
│   │ // User represents...  ─┐                                        │      │
│   │ type User struct {      │ Level 1: Type declaration              │      │
│   │     ID   string         │                                        │      │
│   │     Name string        ─┘                                        │      │
│   │ }                                                                │      │
│   │                                                                  │      │
│   │ // Validate checks...  ─┐                                        │      │
│   │ func (u *User) Validate │ Level 2: Method                        │      │
│   │     if u.ID == "" {     │                                        │      │
│   │         return err      │                                        │      │
│   │     }                   │                                        │      │
│   │     return nil         ─┘                                        │      │
│   │ }                                                                │      │
│   └──────────────────────────────────────────────────────────────────┘      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Markdown (Header-Based Hierarchical)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ MARKDOWN CHUNKING                                                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│ Hierarchy:                                                                  │
│   Level 0: Document (title + frontmatter)                                   │
│   Level 1: H1 sections                                                      │
│   Level 2: H2 sections                                                      │
│   Level 3: H3+ sections OR large paragraphs (>500 tokens)                   │
│                                                                             │
│ Chunk includes:                                                             │
│   • Header text (critical for semantic meaning)                             │
│   • All content until next same-or-higher-level header                      │
│   • Code blocks kept atomic (never split)                                   │
│   • Lists kept atomic unless >200 tokens                                    │
│                                                                             │
│ Example:                                                                    │
│   ┌──────────────────────────────────────────────────────────────────┐      │
│   │ # Authentication Guide    ─── Level 0: Document                  │      │
│   │                                                                  │      │
│   │ ## Overview              ─┐                                      │      │
│   │                           │                                      │      │
│   │ This guide covers...      │ Level 1: H2 Section                  │      │
│   │                          ─┘                                      │      │
│   │                                                                  │      │
│   │ ## Setup                 ─┐                                      │      │
│   │                           │ Level 1: H2 Section                  │      │
│   │ ### Prerequisites        ─┼─┐                                    │      │
│   │                           │ │ Level 2: H3 Section                │      │
│   │ You will need...         ─┘─┘                                    │      │
│   │                                                                  │      │
│   │ ### Installation         ─┐                                      │      │
│   │                           │ Level 2: H3 Section                  │      │
│   │ ```bash                   │ (code block kept atomic)             │      │
│   │ npm install auth         ─┘                                      │      │
│   │ ```                                                              │      │
│   └──────────────────────────────────────────────────────────────────┘      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Academic Papers (Section + Semantic Unit)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ ACADEMIC PAPER CHUNKING                                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│ Hierarchy:                                                                  │
│   Level 0: Paper (title + abstract + authors)                               │
│   Level 1: Major sections (Introduction, Methods, Results, etc.)            │
│   Level 2: Subsections                                                      │
│   Level 3: Paragraphs + special units                                       │
│                                                                             │
│ Special atomic units (NEVER split):                                         │
│   • Equations + surrounding explanation                                     │
│   • Figures + captions                                                      │
│   • Tables + captions                                                       │
│   • Algorithm blocks                                                        │
│   • Theorem/Proof/Lemma blocks                                              │
│                                                                             │
│ Citation handling:                                                          │
│   • Paragraphs containing citations include citation marker                 │
│   • Reference metadata stored in chunk metadata                             │
│   • Cross-reference to cited papers if in index                             │
│                                                                             │
│ Example:                                                                    │
│   ┌──────────────────────────────────────────────────────────────────┐      │
│   │ Title: Attention Is All You Need                                 │      │
│   │ Abstract: The dominant...    ─── Level 0: Paper header           │      │
│   │                                                                  │      │
│   │ 1. Introduction             ─┐                                   │      │
│   │                              │ Level 1: Major section            │      │
│   │ Recurrent neural networks... │                                   │      │
│   │                             ─┘                                   │      │
│   │                                                                  │      │
│   │ 3.2 Attention               ─┐                                   │      │
│   │                              │ Level 2: Subsection               │      │
│   │ An attention function...     │                                   │      │
│   │                              │                                   │      │
│   │ Equation 1:                 ─┼─┐                                 │      │
│   │ Attention(Q,K,V) = ...       │ │ Level 3: Equation (atomic)      │      │
│   │                             ─┘─┘                                 │      │
│   └──────────────────────────────────────────────────────────────────┘      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### LLM Conversations (Turn + Topic-Based)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ LLM CONVERSATION CHUNKING                                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│ Hierarchy:                                                                  │
│   Level 0: Session                                                          │
│   Level 1: Topic segments (detected via embedding discontinuity)            │
│   Level 2: Turn pairs (user message + assistant response)                   │
│   Level 3: Large code blocks within responses                               │
│                                                                             │
│ Topic detection:                                                            │
│   • Compute rolling embedding similarity between turns                      │
│   • Significant drop (>0.3 cosine distance) = topic boundary                │
│   • Explicit markers ("Now let's talk about...", "Moving on...")            │
│                                                                             │
│ Turn pairs kept together:                                                   │
│   • Question without answer is useless                                      │
│   • Answer without question lacks context                                   │
│   • Always chunk as (user_msg, assistant_response) pair                     │
│                                                                             │
│ Example:                                                                    │
│   ┌──────────────────────────────────────────────────────────────────┐      │
│   │ Session: 2024-01-15-auth-refactor                                │      │
│   │                                  ─── Level 0: Session            │      │
│   │                                                                  │      │
│   │ [Topic: Authentication Setup]   ─┐                               │      │
│   │                                  │ Level 1: Topic segment        │      │
│   │ User: How do I set up auth?     ─┼─┐                             │      │
│   │ Assistant: To set up auth...     │ │ Level 2: Turn pair          │      │
│   │                                 ─┘─┘                             │      │
│   │                                                                  │      │
│   │ [Topic: Database Migration]     ─┐ (topic shift detected)        │      │
│   │                                  │                               │      │
│   │ User: Now about the database... ─┼─┐                             │      │
│   │ Assistant: For migrations...     │ │                             │      │
│   │                                 ─┘─┘                             │      │
│   └──────────────────────────────────────────────────────────────────┘      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Config Files (Schema-Aware)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ CONFIG FILE CHUNKING                                                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│ Hierarchy:                                                                  │
│   Level 0: File                                                             │
│   Level 1: Top-level keys                                                   │
│   Level 2: Nested objects/arrays (if >100 tokens)                           │
│                                                                             │
│ Schema detection:                                                           │
│   • Known schemas get custom parsers:                                       │
│     - package.json: scripts, dependencies, devDependencies as L1            │
│     - go.mod: module, require blocks as L1                                  │
│     - Dockerfile: stage blocks as L1                                        │
│     - docker-compose.yml: services as L1, each service as L2                │
│   • Unknown schemas: chunk at top-level keys                                │
│   • Small files (<200 tokens): keep atomic                                  │
│                                                                             │
│ Example (docker-compose.yml):                                               │
│   ┌──────────────────────────────────────────────────────────────────┐      │
│   │ version: "3.8"                  ─── Level 0: File                │      │
│   │                                                                  │      │
│   │ services:                       ─┐                               │      │
│   │   api:                          ─┼─┐ Level 1: services block     │      │
│   │     image: myapp:latest          │ │                             │      │
│   │     ports:                       │ │ Level 2: api service        │      │
│   │       - "8080:8080"              │ │                             │      │
│   │     environment:                ─┘─┘                             │      │
│   │       - DB_HOST=db                                               │      │
│   │                                                                  │      │
│   │   db:                           ─┐                               │      │
│   │     image: postgres:15           │ Level 2: db service           │      │
│   │     volumes:                     │                               │      │
│   │       - pgdata:/var/lib/...    ─┘                                │      │
│   └──────────────────────────────────────────────────────────────────┘      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Adaptive Chunk Sizing

Even with semantic boundaries, we need size constraints for embedding models.

**CORE PRINCIPLE: Soft targets are learned, not hardcoded.** While MaxTokens is a true embedding model constraint, the soft parameters (TargetTokens, MinTokens, ContextTokens) directly affect retrieval quality and should be learned from usage feedback.

### Configuration

```go
// core/chunking/config.go

// ChunkConfig controls chunk sizing behavior.
// Hard limits are fixed (embedding model constraints).
// Soft targets are LEARNED from retrieval feedback.
type ChunkConfig struct {
    // Hard limits (embedding model constraints - FIXED, not learned)
    MaxTokens       int  // e.g., 8192 for modern models (physical constraint)

    // Soft targets (LEARNED from retrieval quality feedback)
    TargetTokens        *LearnedContextSize  // Optimal chunk size for retrieval
    MinTokens           *LearnedContextSize  // Below this, chunks become noise
    ContextTokensBefore *LearnedContextSize  // Leading context improves retrieval
    ContextTokensAfter  *LearnedContextSize  // Trailing context improves retrieval

    // Overflow handling (can be learned per-domain based on retrieval success)
    OverflowStrategy        OverflowStrategy
    OverflowStrategyWeights *LearnedOverflowWeights  // Learned preference per domain
}

// LearnedContextSize uses Gamma distribution for positive integer parameters.
// Gamma(α, β) where E[X] = α/β, Var[X] = α/β²
// See SCORING.md for full LearnedContextSize specification.
type LearnedContextSize struct {
    Alpha            float64 `json:"alpha"`              // Shape parameter
    Beta             float64 `json:"beta"`               // Rate parameter
    EffectiveSamples float64 `json:"effective_samples"`  // For recency weighting
    PriorAlpha       float64 `json:"prior_alpha"`        // For drift protection
    PriorBeta        float64 `json:"prior_beta"`         // For drift protection
}

func (c *LearnedContextSize) Mean() int {
    return int(math.Round(c.Alpha / c.Beta))
}

func (c *LearnedContextSize) Sample() int {
    return int(math.Round(gammaSample(c.Alpha, c.Beta)))
}

func (c *LearnedContextSize) Confidence() float64 {
    return 1.0 - 1.0/(1.0 + c.EffectiveSamples/10.0)
}

// LearnedOverflowWeights tracks which overflow strategy works best per domain.
// Uses Dirichlet-Multinomial conjugate for categorical learning.
type LearnedOverflowWeights struct {
    RecursiveCount  float64 `json:"recursive_count"`   // Pseudo-counts for recursive split
    SentenceCount   float64 `json:"sentence_count"`    // Pseudo-counts for sentence split
    TruncateCount   float64 `json:"truncate_count"`    // Pseudo-counts for truncate+summary
}

func (w *LearnedOverflowWeights) BestStrategy() OverflowStrategy {
    total := w.RecursiveCount + w.SentenceCount + w.TruncateCount
    if total == 0 {
        return OverflowSplitRecursive  // Default
    }
    if w.RecursiveCount >= w.SentenceCount && w.RecursiveCount >= w.TruncateCount {
        return OverflowSplitRecursive
    }
    if w.SentenceCount >= w.TruncateCount {
        return OverflowSplitSentence
    }
    return OverflowTruncateWithSummary
}

func (w *LearnedOverflowWeights) Sample() OverflowStrategy {
    // Thompson Sampling: sample from Dirichlet, pick highest
    samples := dirichletSample([]float64{w.RecursiveCount + 1, w.SentenceCount + 1, w.TruncateCount + 1})
    maxIdx := 0
    for i := 1; i < len(samples); i++ {
        if samples[i] > samples[maxIdx] {
            maxIdx = i
        }
    }
    return OverflowStrategy(maxIdx)
}

type OverflowStrategy int

const (
    // If semantic unit exceeds MaxTokens:
    OverflowSplitRecursive  OverflowStrategy = iota  // Split at next hierarchy level
    OverflowSplitSentence                            // Split at sentence boundaries
    OverflowTruncateWithSummary                      // Truncate + add summary chunk
)

// PriorChunkConfig returns weakly informative priors for a domain.
// These are starting points that get updated by retrieval feedback.
func PriorChunkConfig(domain Domain) *ChunkConfig {
    switch domain {
    case DomainCode:
        return &ChunkConfig{
            MaxTokens: 4096,  // Hard limit (functions rarely exceed this)
            // Priors centered on reasonable values with wide uncertainty
            TargetTokens:        &LearnedContextSize{Alpha: 30, Beta: 0.1, PriorAlpha: 30, PriorBeta: 0.1},   // Prior mean 300
            MinTokens:           &LearnedContextSize{Alpha: 3, Beta: 0.1, PriorAlpha: 3, PriorBeta: 0.1},     // Prior mean 30
            ContextTokensBefore: &LearnedContextSize{Alpha: 5, Beta: 0.1, PriorAlpha: 5, PriorBeta: 0.1},     // Prior mean 50
            ContextTokensAfter:  &LearnedContextSize{Alpha: 0.1, Beta: 0.1, PriorAlpha: 0.1, PriorBeta: 0.1}, // Prior mean ~1 (near zero)
            OverflowStrategy:    OverflowSplitRecursive,
            OverflowStrategyWeights: &LearnedOverflowWeights{RecursiveCount: 3, SentenceCount: 1, TruncateCount: 1},
        }
    case DomainAcademic:
        return &ChunkConfig{
            MaxTokens: 2048,  // Hard limit (paragraphs + equations)
            TargetTokens:        &LearnedContextSize{Alpha: 50, Beta: 0.1, PriorAlpha: 50, PriorBeta: 0.1},   // Prior mean 500
            MinTokens:           &LearnedContextSize{Alpha: 10, Beta: 0.1, PriorAlpha: 10, PriorBeta: 0.1},   // Prior mean 100
            ContextTokensBefore: &LearnedContextSize{Alpha: 10, Beta: 0.1, PriorAlpha: 10, PriorBeta: 0.1},   // Prior mean 100
            ContextTokensAfter:  &LearnedContextSize{Alpha: 5, Beta: 0.1, PriorAlpha: 5, PriorBeta: 0.1},     // Prior mean 50
            OverflowStrategy:    OverflowSplitSentence,
            OverflowStrategyWeights: &LearnedOverflowWeights{RecursiveCount: 1, SentenceCount: 3, TruncateCount: 1},
        }
    case DomainHistory:
        return &ChunkConfig{
            MaxTokens: 1024,  // Hard limit (conversation turns)
            TargetTokens:        &LearnedContextSize{Alpha: 40, Beta: 0.1, PriorAlpha: 40, PriorBeta: 0.1},   // Prior mean 400
            MinTokens:           &LearnedContextSize{Alpha: 5, Beta: 0.1, PriorAlpha: 5, PriorBeta: 0.1},     // Prior mean 50
            ContextTokensBefore: &LearnedContextSize{Alpha: 10, Beta: 0.1, PriorAlpha: 10, PriorBeta: 0.1},   // Prior mean 100
            ContextTokensAfter:  &LearnedContextSize{Alpha: 0.1, Beta: 0.1, PriorAlpha: 0.1, PriorBeta: 0.1}, // Prior mean ~1 (near zero)
            OverflowStrategy:    OverflowSplitRecursive,
            OverflowStrategyWeights: &LearnedOverflowWeights{RecursiveCount: 3, SentenceCount: 1, TruncateCount: 1},
        }
    default:
        return &ChunkConfig{
            MaxTokens: 8192,
            TargetTokens:        &LearnedContextSize{Alpha: 51.2, Beta: 0.1, PriorAlpha: 51.2, PriorBeta: 0.1}, // Prior mean 512
            MinTokens:           &LearnedContextSize{Alpha: 5, Beta: 0.1, PriorAlpha: 5, PriorBeta: 0.1},       // Prior mean 50
            ContextTokensBefore: &LearnedContextSize{Alpha: 10, Beta: 0.1, PriorAlpha: 10, PriorBeta: 0.1},     // Prior mean 100
            ContextTokensAfter:  &LearnedContextSize{Alpha: 5, Beta: 0.1, PriorAlpha: 5, PriorBeta: 0.1},       // Prior mean 50
            OverflowStrategy:    OverflowSplitRecursive,
            OverflowStrategyWeights: &LearnedOverflowWeights{RecursiveCount: 2, SentenceCount: 2, TruncateCount: 1},
        }
    }
}

// GetEffectiveTargetTokens returns the effective target using hierarchical blending.
func (c *ChunkConfig) GetEffectiveTargetTokens(explore bool) int {
    if explore {
        return c.TargetTokens.Sample()
    }
    return c.TargetTokens.Mean()
}

// GetEffectiveMinTokens returns the effective minimum using hierarchical blending.
func (c *ChunkConfig) GetEffectiveMinTokens(explore bool) int {
    if explore {
        return c.MinTokens.Sample()
    }
    return c.MinTokens.Mean()
}

// GetEffectiveContextBefore returns the effective context before using hierarchical blending.
func (c *ChunkConfig) GetEffectiveContextBefore(explore bool) int {
    if explore {
        return c.ContextTokensBefore.Sample()
    }
    return c.ContextTokensBefore.Mean()
}

// GetEffectiveContextAfter returns the effective context after using hierarchical blending.
func (c *ChunkConfig) GetEffectiveContextAfter(explore bool) int {
    if explore {
        return c.ContextTokensAfter.Sample()
    }
    return c.ContextTokensAfter.Mean()
}

// GetEffectiveOverflowStrategy returns the strategy using Thompson Sampling or best estimate.
func (c *ChunkConfig) GetEffectiveOverflowStrategy(explore bool) OverflowStrategy {
    if c.OverflowStrategyWeights == nil {
        return c.OverflowStrategy
    }
    if explore {
        return c.OverflowStrategyWeights.Sample()
    }
    return c.OverflowStrategyWeights.BestStrategy()
}
```

### Overflow Handling: Recursive Semantic Split

When a semantic unit exceeds MaxTokens, we split at the next semantic level:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ OVERFLOW HANDLING: RECURSIVE SEMANTIC SPLIT                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Example: Function with 1500 tokens (max=1024)                              │
│                                                                             │
│  Step 1: Try to split at next semantic level (blocks within function)       │
│    ┌────────────────────────────────────────────┐                           │
│    │ func ProcessData(data []byte) error {     │                           │
│    │     // validation block (400 tokens)      │ → Chunk A                 │
│    │     if err := validate(data); err != nil {│                           │
│    │         ...                               │                           │
│    │     }                                     │                           │
│    │                                           │                           │
│    │     // transformation block (600 tokens)  │ → Chunk B                 │
│    │     result := transform(data)             │                           │
│    │     ...                                   │                           │
│    │                                           │                           │
│    │     // persistence block (500 tokens)     │ → Chunk C                 │
│    │     return persist(result)                │                           │
│    │ }                                         │                           │
│    └────────────────────────────────────────────┘                           │
│                                                                             │
│  Step 2: Each sub-chunk includes function signature as context_before       │
│    Chunk A context_before: "func ProcessData(data []byte) error {"         │
│    Chunk B context_before: "func ProcessData(data []byte) error {"         │
│    Chunk C context_before: "func ProcessData(data []byte) error {"         │
│                                                                             │
│  Step 3: If sub-chunk still too large, continue recursively                 │
│    Split at statement boundaries, then expression boundaries                │
│                                                                             │
│  Step 4: Last resort - sentence/line-level split with token overlap         │
│    Include 50-100 tokens of overlap at boundaries                           │
│                                                                             │
│  Relationship preservation:                                                 │
│    All sub-chunks maintain parent_chunk_id pointing to the function chunk   │
│    Original function chunk exists at Level 2, sub-chunks at Level 3         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Implementation

```go
// core/chunking/splitter.go

type SemanticSplitter struct {
    parsers    map[string]Parser  // Language-specific parsers
    tokenizer  Tokenizer
    config     *ChunkConfig
}

func (s *SemanticSplitter) Split(content string, contentType ContentType, language string) ([]*Chunk, error) {
    parser := s.parsers[language]
    if parser == nil {
        parser = s.parsers["generic"]
    }

    // Parse into AST/structure
    tree, err := parser.Parse(content)
    if err != nil {
        return nil, fmt.Errorf("parse error: %w", err)
    }

    // Build initial chunks from semantic units
    chunks := s.buildChunksFromTree(tree, 0, nil)

    // Handle overflow
    chunks = s.handleOverflow(chunks)

    // Link siblings
    s.linkSiblings(chunks)

    return chunks, nil
}

func (s *SemanticSplitter) handleOverflow(chunks []*Chunk) []*Chunk {
    result := make([]*Chunk, 0, len(chunks))

    for _, chunk := range chunks {
        tokens := s.tokenizer.Count(chunk.Content)

        if tokens <= s.config.MaxTokens {
            chunk.TokenCount = tokens
            result = append(result, chunk)
            continue
        }

        // Overflow: split recursively
        switch s.config.OverflowStrategy {
        case OverflowSplitRecursive:
            subChunks := s.splitRecursive(chunk)
            result = append(result, subChunks...)

        case OverflowSplitSentence:
            subChunks := s.splitBySentence(chunk)
            result = append(result, subChunks...)

        case OverflowTruncateWithSummary:
            truncated, summary := s.truncateWithSummary(chunk)
            result = append(result, truncated, summary)
        }
    }

    return result
}

func (s *SemanticSplitter) splitRecursive(chunk *Chunk) []*Chunk {
    // Try to find natural split points within the chunk
    splitPoints := s.findSemanticSplitPoints(chunk)

    if len(splitPoints) == 0 {
        // No semantic boundaries, fall back to sentence split
        return s.splitBySentence(chunk)
    }

    subChunks := make([]*Chunk, 0, len(splitPoints)+1)

    for i, point := range splitPoints {
        var start, end int
        if i == 0 {
            start = 0
        } else {
            start = splitPoints[i-1]
        }
        end = point

        subChunk := &Chunk{
            ID:            generateChunkID(),
            ParentChunkID: &chunk.ID,
            Level:         chunk.Level + 1,
            Sequence:      i,
            StartOffset:   chunk.StartOffset + start,
            EndOffset:     chunk.StartOffset + end,
            Content:       chunk.Content[start:end],
            ContextBefore: chunk.ContextBefore,  // Inherit parent's context
            ChunkType:     ChunkTypeBlock,
        }

        // Recursive check
        if s.tokenizer.Count(subChunk.Content) > s.config.MaxTokens {
            subChunks = append(subChunks, s.splitRecursive(subChunk)...)
        } else {
            subChunks = append(subChunks, subChunk)
        }
    }

    return subChunks
}
```

---

## 3. Hierarchical Chunk Structure

Chunks form a bidirectional hierarchical graph with cross-references.

### Structure

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ HIERARCHICAL CHUNK GRAPH                                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│                         ┌─────────────┐                                     │
│                         │  File Node  │ (GraphNode)                         │
│                         │ auth.go     │                                     │
│                         └──────┬──────┘                                     │
│                                │ parent_of                                  │
│              ┌─────────────────┼─────────────────┐                          │
│              ▼                 ▼                 ▼                          │
│       ┌────────────┐    ┌────────────┐    ┌────────────┐                    │
│       │ Chunk L1   │    │ Chunk L1   │    │ Chunk L1   │                    │
│       │ imports    │◄──►│ type User  │◄──►│ type Auth  │                    │
│       │ seq=0      │    │ seq=1      │    │ seq=2      │                    │
│       └────────────┘    └─────┬──────┘    └─────┬──────┘                    │
│                               │                 │                           │
│                    ┌──────────┴──────┐    ┌─────┴─────────┐                 │
│                    ▼                 ▼    ▼               ▼                 │
│              ┌──────────┐     ┌──────────┐ ┌──────────┐ ┌──────────┐        │
│              │ Chunk L2 │◄───►│ Chunk L2 │ │ Chunk L2 │◄──────────►│ Chunk L2│
│              │ NewUser()│     │ Validate │ │ Login()  │ │ Logout() │        │
│              │ seq=0    │     │ seq=1    │ │ seq=0    │ │ seq=1    │        │
│              └──────────┘     └──────────┘ └────┬─────┘ └──────────┘        │
│                    │                            │                           │
│                    │         cross_references   │                           │
│                    │         ┌──────────────────┘                           │
│                    ▼         ▼                                              │
│              ┌─────────────────────┐                                        │
│              │    Chunk L2         │ (in session_manager.go)                │
│              │ SessionManager.End()│                                        │
│              └─────────────────────┘                                        │
│                                                                             │
│  Relationships:                                                             │
│    ───► parent_chunk_id (hierarchical parent)                               │
│    ◄──► prev/next_sibling_id (sequential siblings)                          │
│    - - - cross_refs (semantic: calls, implements, references)               │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Chunk Type Definition

```go
// core/chunking/types.go

// Chunk represents a semantic unit within a larger document.
type Chunk struct {
    // Identity
    ID          string `json:"id"`           // Unique chunk ID (UUID)
    ContentHash string `json:"content_hash"` // SHA-256 for dedup + quality linking

    // Hierarchy
    RootNodeID     string  `json:"root_node_id"`      // GraphNode this belongs to
    ParentChunkID  *string `json:"parent_chunk_id"`   // nil if top-level chunk
    Level          int     `json:"level"`             // 0=file, 1=section, 2=function, 3=block
    Sequence       int     `json:"sequence"`          // Order among siblings

    // Sibling navigation (for context expansion)
    PrevSiblingID  *string `json:"prev_sibling_id"`
    NextSiblingID  *string `json:"next_sibling_id"`

    // Content boundaries (in source document)
    StartOffset    int     `json:"start_offset"`
    EndOffset      int     `json:"end_offset"`
    StartLine      int     `json:"start_line"`
    EndLine        int     `json:"end_line"`

    // Chunk classification
    ChunkType      ChunkType `json:"chunk_type"`
    Language       string    `json:"language,omitempty"`

    // Size metrics
    TokenCount     int     `json:"token_count"`
    CharCount      int     `json:"char_count"`

    // Content
    Content        string  `json:"content,omitempty"`

    // Context for retrieval (always included with chunk)
    ContextBefore  string  `json:"context_before"`   // e.g., function signature
    ContextAfter   string  `json:"context_after"`    // e.g., closing context

    // Semantic anchors (for code)
    SymbolName     string   `json:"symbol_name,omitempty"`
    Signature      string   `json:"signature,omitempty"`
    Symbols        []string `json:"symbols,omitempty"`      // Symbols defined
    References     []string `json:"references,omitempty"`   // Symbols referenced

    // Cross-references (to other chunks)
    CrossRefs      []CrossReference `json:"cross_refs,omitempty"`

    // Quality signals (chunk-specific)
    ParseQuality   float64 `json:"parse_quality"`    // Parse confidence (0-1)
    Completeness   float64 `json:"completeness"`     // Is this complete? (0-1)

    // Timestamps
    CreatedAt      time.Time `json:"created_at"`
    UpdatedAt      time.Time `json:"updated_at"`
}

// ChunkType categorizes the semantic unit.
type ChunkType string

const (
    // Code chunks
    ChunkTypeFile       ChunkType = "file"
    ChunkTypePackage    ChunkType = "package"
    ChunkTypeImports    ChunkType = "imports"
    ChunkTypeType       ChunkType = "type"
    ChunkTypeFunction   ChunkType = "function"
    ChunkTypeMethod     ChunkType = "method"
    ChunkTypeInterface  ChunkType = "interface"
    ChunkTypeBlock      ChunkType = "block"
    ChunkTypeComment    ChunkType = "comment"

    // Markdown chunks
    ChunkTypeDocument   ChunkType = "document"
    ChunkTypeSection    ChunkType = "section"
    ChunkTypeParagraph  ChunkType = "paragraph"
    ChunkTypeCodeBlock  ChunkType = "code_block"
    ChunkTypeList       ChunkType = "list"

    // Academic chunks
    ChunkTypeAbstract   ChunkType = "abstract"
    ChunkTypeEquation   ChunkType = "equation"
    ChunkTypeFigure     ChunkType = "figure"
    ChunkTypeTable      ChunkType = "table"
    ChunkTypeTheorem    ChunkType = "theorem"
    ChunkTypeCitation   ChunkType = "citation"

    // Conversation chunks
    ChunkTypeSession    ChunkType = "session"
    ChunkTypeTopic      ChunkType = "topic"
    ChunkTypeTurn       ChunkType = "turn"

    // Config chunks
    ChunkTypeConfigRoot ChunkType = "config_root"
    ChunkTypeConfigKey  ChunkType = "config_key"
)

// CrossReference links chunks semantically.
type CrossReference struct {
    TargetChunkID string        `json:"target_chunk_id"`
    RefType       ReferenceType `json:"ref_type"`
    Confidence    float64       `json:"confidence"`
}

// ReferenceType categorizes cross-references.
type ReferenceType string

const (
    RefTypeCalls       ReferenceType = "calls"
    RefTypeCalledBy    ReferenceType = "called_by"
    RefTypeImplements  ReferenceType = "implements"
    RefTypeExtends     ReferenceType = "extends"
    RefTypeImports     ReferenceType = "imports"
    RefTypeReferences  ReferenceType = "references"
    RefTypeTests       ReferenceType = "tests"
    RefTypeSimilar     ReferenceType = "similar"
)
```

---

## 4. Storage Model

Dedicated ChunkStore with separate tables for metadata, content, vectors, and cross-references.

### Schema

```sql
-- core/chunking/schema.sql

-- ============================================================================
-- CHUNK METADATA
-- ============================================================================

CREATE TABLE chunks (
    id              TEXT PRIMARY KEY,
    content_hash    TEXT NOT NULL,        -- SHA-256 for dedup + quality linking

    -- Hierarchy links
    root_node_id    TEXT NOT NULL,        -- GraphNode this belongs to
    parent_chunk_id TEXT,                 -- NULL if top-level
    level           INTEGER NOT NULL,     -- 0=root, 1=section, 2=function, etc.
    sequence        INTEGER NOT NULL,     -- Order among siblings

    -- Sibling navigation (denormalized for fast traversal)
    prev_sibling_id TEXT,
    next_sibling_id TEXT,

    -- Content boundaries
    start_offset    INTEGER NOT NULL,
    end_offset      INTEGER NOT NULL,
    start_line      INTEGER,
    end_line        INTEGER,

    -- Classification
    chunk_type      TEXT NOT NULL,
    language        TEXT,

    -- Size
    token_count     INTEGER NOT NULL,
    char_count      INTEGER NOT NULL,

    -- Semantic anchors
    symbol_name     TEXT,
    signature       TEXT,

    -- Context (for retrieval, stored inline for small values)
    context_before  TEXT,
    context_after   TEXT,

    -- Quality
    parse_quality   REAL DEFAULT 1.0,
    completeness    REAL DEFAULT 1.0,

    -- Timestamps
    created_at      INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at      INTEGER NOT NULL DEFAULT (unixepoch()),

    FOREIGN KEY (root_node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (parent_chunk_id) REFERENCES chunks(id) ON DELETE SET NULL
);

-- ============================================================================
-- CHUNK CONTENT (separate table for large content)
-- ============================================================================

CREATE TABLE chunk_content (
    chunk_id        TEXT PRIMARY KEY,
    content         TEXT NOT NULL,

    FOREIGN KEY (chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- ============================================================================
-- CHUNK SYMBOLS (many-to-many: chunk defines/references symbols)
-- ============================================================================

CREATE TABLE chunk_symbols (
    chunk_id        TEXT NOT NULL,
    symbol          TEXT NOT NULL,
    relation        TEXT NOT NULL,        -- 'defines' or 'references'
    line_number     INTEGER,              -- Where in chunk

    PRIMARY KEY (chunk_id, symbol, relation),
    FOREIGN KEY (chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- ============================================================================
-- CHUNK CROSS-REFERENCES
-- ============================================================================

CREATE TABLE chunk_cross_refs (
    source_chunk_id TEXT NOT NULL,
    target_chunk_id TEXT NOT NULL,
    ref_type        TEXT NOT NULL,        -- calls, implements, tests, etc.
    confidence      REAL DEFAULT 1.0,
    metadata        TEXT,                 -- JSON for additional context

    PRIMARY KEY (source_chunk_id, target_chunk_id, ref_type),
    FOREIGN KEY (source_chunk_id) REFERENCES chunks(id) ON DELETE CASCADE,
    FOREIGN KEY (target_chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- ============================================================================
-- CHUNK VECTORS (one embedding per chunk)
-- ============================================================================

CREATE TABLE chunk_vectors (
    chunk_id        TEXT PRIMARY KEY,
    root_node_id    TEXT NOT NULL,        -- Denormalized for filtering
    level           INTEGER NOT NULL,     -- Denormalized for level queries
    chunk_type      TEXT NOT NULL,        -- Denormalized for type filtering

    embedding       BLOB NOT NULL,        -- float32 array
    magnitude       REAL NOT NULL,
    dimensions      INTEGER NOT NULL,

    FOREIGN KEY (chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- ============================================================================
-- INDICES
-- ============================================================================

-- Hierarchy traversal
CREATE INDEX idx_chunks_root ON chunks(root_node_id);
CREATE INDEX idx_chunks_parent ON chunks(parent_chunk_id);
CREATE INDEX idx_chunks_siblings ON chunks(parent_chunk_id, sequence);

-- Filtering
CREATE INDEX idx_chunks_type ON chunks(chunk_type);
CREATE INDEX idx_chunks_level ON chunks(level);
CREATE INDEX idx_chunks_hash ON chunks(content_hash);
CREATE INDEX idx_chunks_symbol ON chunks(symbol_name) WHERE symbol_name IS NOT NULL;

-- Symbol lookup
CREATE INDEX idx_chunk_symbols_symbol ON chunk_symbols(symbol);
CREATE INDEX idx_chunk_symbols_chunk ON chunk_symbols(chunk_id);

-- Cross-reference traversal
CREATE INDEX idx_chunk_refs_target ON chunk_cross_refs(target_chunk_id);

-- Vector filtering
CREATE INDEX idx_chunk_vectors_root ON chunk_vectors(root_node_id);
CREATE INDEX idx_chunk_vectors_level ON chunk_vectors(level);
CREATE INDEX idx_chunk_vectors_type ON chunk_vectors(chunk_type);
```

### ChunkStore Implementation

```go
// core/chunking/store.go

// ChunkStore manages chunk persistence and retrieval.
type ChunkStore struct {
    db    *sql.DB
    cache *ristretto.Cache
}

// NewChunkStore creates a new chunk store.
func NewChunkStore(db *sql.DB) (*ChunkStore, error) {
    cache, err := ristretto.NewCache(&ristretto.Config{
        NumCounters: 1e6,
        MaxCost:     100 << 20,  // 100MB
        BufferItems: 64,
    })
    if err != nil {
        return nil, err
    }

    return &ChunkStore{db: db, cache: cache}, nil
}

// SaveChunks persists a batch of chunks (typically all chunks from one file).
func (s *ChunkStore) SaveChunks(ctx context.Context, chunks []*Chunk) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // Prepared statements for batch insert
    chunkStmt, _ := tx.PrepareContext(ctx, `
        INSERT INTO chunks (
            id, content_hash, root_node_id, parent_chunk_id, level, sequence,
            prev_sibling_id, next_sibling_id, start_offset, end_offset,
            start_line, end_line, chunk_type, language, token_count, char_count,
            symbol_name, signature, context_before, context_after,
            parse_quality, completeness
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            content_hash = excluded.content_hash,
            updated_at = unixepoch()
    `)
    defer chunkStmt.Close()

    contentStmt, _ := tx.PrepareContext(ctx, `
        INSERT INTO chunk_content (chunk_id, content)
        VALUES (?, ?)
        ON CONFLICT(chunk_id) DO UPDATE SET content = excluded.content
    `)
    defer contentStmt.Close()

    symbolStmt, _ := tx.PrepareContext(ctx, `
        INSERT INTO chunk_symbols (chunk_id, symbol, relation)
        VALUES (?, ?, ?)
        ON CONFLICT DO NOTHING
    `)
    defer symbolStmt.Close()

    for _, chunk := range chunks {
        // Insert chunk metadata
        _, err := chunkStmt.ExecContext(ctx,
            chunk.ID, chunk.ContentHash, chunk.RootNodeID, chunk.ParentChunkID,
            chunk.Level, chunk.Sequence, chunk.PrevSiblingID, chunk.NextSiblingID,
            chunk.StartOffset, chunk.EndOffset, chunk.StartLine, chunk.EndLine,
            string(chunk.ChunkType), chunk.Language, chunk.TokenCount, chunk.CharCount,
            chunk.SymbolName, chunk.Signature, chunk.ContextBefore, chunk.ContextAfter,
            chunk.ParseQuality, chunk.Completeness,
        )
        if err != nil {
            return fmt.Errorf("insert chunk %s: %w", chunk.ID, err)
        }

        // Insert content
        _, err = contentStmt.ExecContext(ctx, chunk.ID, chunk.Content)
        if err != nil {
            return fmt.Errorf("insert content for %s: %w", chunk.ID, err)
        }

        // Insert symbols
        for _, sym := range chunk.Symbols {
            symbolStmt.ExecContext(ctx, chunk.ID, sym, "defines")
        }
        for _, ref := range chunk.References {
            symbolStmt.ExecContext(ctx, chunk.ID, ref, "references")
        }
    }

    return tx.Commit()
}

// GetChunk retrieves a single chunk by ID.
func (s *ChunkStore) GetChunk(ctx context.Context, id string) (*Chunk, error) {
    // Check cache
    if cached, ok := s.cache.Get(id); ok {
        return cached.(*Chunk), nil
    }

    chunk := &Chunk{}
    err := s.db.QueryRowContext(ctx, `
        SELECT
            c.id, c.content_hash, c.root_node_id, c.parent_chunk_id,
            c.level, c.sequence, c.prev_sibling_id, c.next_sibling_id,
            c.start_offset, c.end_offset, c.start_line, c.end_line,
            c.chunk_type, c.language, c.token_count, c.char_count,
            c.symbol_name, c.signature, c.context_before, c.context_after,
            c.parse_quality, c.completeness, c.created_at, c.updated_at,
            cc.content
        FROM chunks c
        LEFT JOIN chunk_content cc ON c.id = cc.chunk_id
        WHERE c.id = ?
    `, id).Scan(/* fields */)

    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    // Cache with TTL
    s.cache.SetWithTTL(id, chunk, 1, 5*time.Minute)

    return chunk, nil
}

// GetChunksForNode retrieves all chunks belonging to a GraphNode.
func (s *ChunkStore) GetChunksForNode(ctx context.Context, nodeID string) ([]*Chunk, error) {
    rows, err := s.db.QueryContext(ctx, `
        SELECT
            c.id, c.content_hash, c.root_node_id, c.parent_chunk_id,
            c.level, c.sequence, c.chunk_type, c.language,
            c.token_count, c.symbol_name, c.signature
        FROM chunks c
        WHERE c.root_node_id = ?
        ORDER BY c.level, c.sequence
    `, nodeID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var chunks []*Chunk
    for rows.Next() {
        chunk := &Chunk{}
        rows.Scan(/* fields */)
        chunks = append(chunks, chunk)
    }

    return chunks, rows.Err()
}

// GetSiblings retrieves sibling chunks (same parent, ordered by sequence).
func (s *ChunkStore) GetSiblings(ctx context.Context, chunkID string) ([]*Chunk, error) {
    // First get the chunk to find its parent
    chunk, err := s.GetChunk(ctx, chunkID)
    if err != nil || chunk == nil {
        return nil, err
    }

    rows, err := s.db.QueryContext(ctx, `
        SELECT id, sequence, chunk_type, token_count, symbol_name
        FROM chunks
        WHERE parent_chunk_id = ? OR (parent_chunk_id IS NULL AND root_node_id = ? AND level = ?)
        ORDER BY sequence
    `, chunk.ParentChunkID, chunk.RootNodeID, chunk.Level)
    // ... process rows
}

// GetChildren retrieves child chunks of a given chunk.
func (s *ChunkStore) GetChildren(ctx context.Context, parentID string) ([]*Chunk, error) {
    rows, err := s.db.QueryContext(ctx, `
        SELECT id, sequence, chunk_type, token_count, symbol_name
        FROM chunks
        WHERE parent_chunk_id = ?
        ORDER BY sequence
    `, parentID)
    // ... process rows
}

// FindBySymbol finds chunks that define or reference a symbol.
func (s *ChunkStore) FindBySymbol(ctx context.Context, symbol string, relation string) ([]*Chunk, error) {
    rows, err := s.db.QueryContext(ctx, `
        SELECT c.id, c.root_node_id, c.chunk_type, c.symbol_name, c.signature
        FROM chunks c
        JOIN chunk_symbols cs ON c.id = cs.chunk_id
        WHERE cs.symbol = ? AND cs.relation = ?
    `, symbol, relation)
    // ... process rows
}
```

---

## 5. Retrieval Strategy

Hierarchical Matryoshka Retrieval with adaptive expansion based on query type and match confidence.

### Retrieval Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ HIERARCHICAL MATRYOSHKA RETRIEVAL                                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Query: "how does the auth validation work?"                                │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ STAGE 1: FINE-GRAINED SEARCH (Level 2-3 chunks)                     │    │
│  │                                                                     │    │
│  │ Search HNSW index at finest granularity                             │    │
│  │ Returns: Top-K chunks with similarity scores                        │    │
│  │                                                                     │    │
│  │ Results:                                                            │    │
│  │   1. auth.go:Validate() (L2)     sim=0.92                           │    │
│  │   2. auth.go:checkToken() (L2)   sim=0.85                           │    │
│  │   3. user.go:ValidateUser() (L2) sim=0.78                           │    │
│  │   4. middleware.go:authBlock (L3) sim=0.71                          │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                            │                                                │
│                            ▼                                                │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ STAGE 2: SCORE PROPAGATION (bottom-up)                              │    │
│  │                                                                     │    │
│  │ Propagate chunk scores to ancestors with decay (0.3 per level)      │    │
│  │                                                                     │    │
│  │ auth.go (L0):                                                       │    │
│  │   propagated = 0.92 * 0.3 + 0.85 * 0.3 = 0.53                       │    │
│  │   (two highly-matching children)                                    │    │
│  │                                                                     │    │
│  │ user.go (L0):                                                       │    │
│  │   propagated = 0.78 * 0.3 = 0.23                                    │    │
│  │   (one matching child)                                              │    │
│  │                                                                     │    │
│  │ This identifies which FILES are most relevant overall               │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                            │                                                │
│                            ▼                                                │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ STAGE 3: ADAPTIVE EXPANSION                                         │    │
│  │                                                                     │    │
│  │ Based on: query specificity, match confidence, context needs        │    │
│  │                                                                     │    │
│  │ Decision matrix:                                                    │    │
│  │ ┌──────────────────┬──────────────────┬─────────────────────────┐   │    │
│  │ │ Confidence       │ Query Type       │ Expansion Strategy      │   │    │
│  │ ├──────────────────┼──────────────────┼─────────────────────────┤   │    │
│  │ │ High (>0.85)     │ Specific         │ Chunk + context only    │   │    │
│  │ │ High (>0.85)     │ Exploratory      │ Chunk + siblings        │   │    │
│  │ │ Medium (0.6-0.85)│ Any              │ Chunk + parent context  │   │    │
│  │ │ Low (<0.6)       │ Any              │ Parent chunk level      │   │    │
│  │ │ Any              │ "How does X work"│ Chunk + cross-refs      │   │    │
│  │ └──────────────────┴──────────────────┴─────────────────────────┘   │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                            │                                                │
│                            ▼                                                │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ STAGE 4: CONTEXT ASSEMBLY                                           │    │
│  │                                                                     │    │
│  │ Assemble final context with proper ordering:                        │    │
│  │                                                                     │    │
│  │ For auth.go:Validate() with "how does X work" query:                │    │
│  │                                                                     │    │
│  │   1. File header (package, imports)       ← orientation             │    │
│  │   2. Type definition (if method)          ← understand receiver     │    │
│  │   3. The Validate() chunk                 ← primary content         │    │
│  │   4. checkToken() (called by Validate)    ← dependency              │    │
│  │   5. error types (referenced)             ← context                 │    │
│  │                                                                     │    │
│  │ Result: Coherent, minimal, complete context                         │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Implementation

```go
// core/chunking/retrieval.go

// ChunkRetriever handles chunk-based search with hierarchical expansion.
type ChunkRetriever struct {
    chunkStore      *ChunkStore
    vectorIndex     *hnsw.Index
    signalStore     *quality.UnifiedSignalStore
    crossRefIndex   *CrossRefIndex

    // Configuration
    PropagationDecay   float64  // Score decay per level (default: 0.3)
    ExpansionThreshold float64  // Confidence below which to expand (default: 0.6)
    MaxContextTokens   int      // Max tokens to return per result (default: 4096)
}

// RetrievalRequest specifies search parameters.
type RetrievalRequest struct {
    QueryEmbedding  []float32
    QueryText       string
    QueryType       QueryType      // specific, exploratory, how_does_x_work

    Domain          Domain
    Limit           int
    MinConfidence   float64

    // Level preferences
    PreferredLevels []int          // e.g., [2, 3] for function/block level
    AllowExpansion  bool
    MaxExpansionTokens int
}

// QueryType affects expansion strategy.
type QueryType int

const (
    QueryTypeSpecific    QueryType = iota  // "what is the return type of X"
    QueryTypeExploratory                    // "show me the auth code"
    QueryTypeHowDoesXWork                   // "how does X work"
    QueryTypeDebug                          // "why is X failing"
)

// RetrievalResult contains a matched chunk with context.
type RetrievalResult struct {
    // Primary match
    Chunk           *Chunk
    Similarity      float64
    QualityScore    float64
    FinalScore      float64

    // Expansion context
    ContextChunks   []*Chunk       // Additional chunks for context

    // Provenance
    RootNode        *GraphNode
    HierarchyPath   []string       // [file_id, section_id, function_id, chunk_id]

    // Assembly
    AssembledContent string        // Ready-to-use context
    TotalTokens      int
}

// Retrieve performs hierarchical chunk retrieval.
func (r *ChunkRetriever) Retrieve(ctx context.Context, req *RetrievalRequest) ([]RetrievalResult, error) {
    // STAGE 1: Fine-grained vector search
    searchOpts := &hnsw.SearchOptions{
        LevelFilter:  req.PreferredLevels,
        DomainFilter: &req.Domain,
    }

    candidates, err := r.vectorIndex.Search(req.QueryEmbedding, req.Limit*3, searchOpts)
    if err != nil {
        return nil, fmt.Errorf("vector search: %w", err)
    }

    // Load chunk metadata for candidates
    scoredChunks := make([]ScoredChunk, len(candidates))
    for i, c := range candidates {
        chunk, _ := r.chunkStore.GetChunk(ctx, c.ID)
        scoredChunks[i] = ScoredChunk{
            Chunk:      chunk,
            Similarity: c.Score,
        }
    }

    // STAGE 2: Score propagation
    propagatedScores := r.propagateScores(ctx, scoredChunks)

    // STAGE 3: Apply quality scoring (from unified signal store)
    r.applyQualityScoring(ctx, scoredChunks, propagatedScores)

    // Sort by final score
    sort.Slice(scoredChunks, func(i, j int) bool {
        return scoredChunks[i].FinalScore > scoredChunks[j].FinalScore
    })

    // STAGE 4: Adaptive expansion and assembly
    results := make([]RetrievalResult, 0, req.Limit)
    for _, scored := range scoredChunks[:min(len(scoredChunks), req.Limit)] {
        result := r.expandAndAssemble(ctx, scored, req)
        results = append(results, result)
    }

    return results, nil
}

// propagateScores propagates chunk scores up the hierarchy.
func (r *ChunkRetriever) propagateScores(ctx context.Context, chunks []ScoredChunk) map[string]float64 {
    propagated := make(map[string]float64)

    for _, c := range chunks {
        currentID := c.Chunk.ID
        currentScore := c.Similarity

        for currentID != "" {
            // Max aggregation: keep highest score for each ancestor
            if currentScore > propagated[currentID] {
                propagated[currentID] = currentScore
            }

            // Move to parent with decay
            chunk, _ := r.chunkStore.GetChunk(ctx, currentID)
            if chunk == nil || chunk.ParentChunkID == nil {
                break
            }
            currentID = *chunk.ParentChunkID
            currentScore *= r.PropagationDecay
        }
    }

    return propagated
}

// expandAndAssemble determines expansion strategy and assembles context.
func (r *ChunkRetriever) expandAndAssemble(
    ctx context.Context,
    scored ScoredChunk,
    req *RetrievalRequest,
) RetrievalResult {

    result := RetrievalResult{
        Chunk:      scored.Chunk,
        Similarity: scored.Similarity,
        FinalScore: scored.FinalScore,
    }

    if !req.AllowExpansion {
        result.AssembledContent = r.assembleMinimal(scored.Chunk)
        result.TotalTokens = scored.Chunk.TokenCount
        return result
    }

    // Determine expansion strategy
    strategy := r.selectExpansionStrategy(scored, req.QueryType)

    // Execute expansion
    switch strategy {
    case ExpansionMinimal:
        // Just the chunk with its built-in context
        result.ContextChunks = nil

    case ExpansionSiblings:
        // Include prev/next siblings at same level
        result.ContextChunks = r.getSiblingContext(ctx, scored.Chunk, req.MaxExpansionTokens)

    case ExpansionParent:
        // Include parent chunk (one level up)
        if scored.Chunk.ParentChunkID != nil {
            parent, _ := r.chunkStore.GetChunk(ctx, *scored.Chunk.ParentChunkID)
            if parent != nil {
                result.ContextChunks = []*Chunk{parent}
            }
        }

    case ExpansionCrossRefs:
        // Include chunks that this chunk calls/references
        refs := r.getCrossRefContext(ctx, scored.Chunk, req.MaxExpansionTokens)
        result.ContextChunks = refs

    case ExpansionHierarchy:
        // Full hierarchy: file header + type definition + chunk + deps
        result.ContextChunks = r.getHierarchyContext(ctx, scored.Chunk, req.MaxExpansionTokens)
    }

    // Assemble final content
    result.AssembledContent = r.assembleContent(scored.Chunk, result.ContextChunks)
    result.TotalTokens = countTokens(result.AssembledContent)

    // Build hierarchy path
    result.HierarchyPath = r.buildHierarchyPath(ctx, scored.Chunk)

    return result
}

// selectExpansionStrategy chooses how to expand based on confidence and query type.
func (r *ChunkRetriever) selectExpansionStrategy(scored ScoredChunk, queryType QueryType) ExpansionStrategy {
    switch {
    case queryType == QueryTypeHowDoesXWork:
        // Always include dependencies for "how does X work"
        return ExpansionCrossRefs

    case queryType == QueryTypeDebug:
        // Include full hierarchy for debugging
        return ExpansionHierarchy

    case scored.Similarity > 0.85 && queryType == QueryTypeSpecific:
        // High confidence + specific query = minimal expansion
        return ExpansionMinimal

    case scored.Similarity > 0.85:
        // High confidence + exploratory = show siblings
        return ExpansionSiblings

    case scored.Similarity > r.ExpansionThreshold:
        // Medium confidence = parent context
        return ExpansionParent

    default:
        // Low confidence = go up a level
        return ExpansionParent
    }
}

// assembleContent combines chunk and context into coherent text.
func (r *ChunkRetriever) assembleContent(primary *Chunk, context []*Chunk) string {
    var sb strings.Builder

    // Sort context by hierarchy: file header → type → function → block
    sortedContext := r.sortByHierarchy(context)

    // Add context before primary chunk
    for _, c := range sortedContext {
        if c.Level < primary.Level || c.Sequence < primary.Sequence {
            if c.ContextBefore != "" {
                sb.WriteString(c.ContextBefore)
                sb.WriteString("\n")
            }
            sb.WriteString(c.Content)
            sb.WriteString("\n\n")
        }
    }

    // Add primary chunk with its context
    if primary.ContextBefore != "" {
        sb.WriteString(primary.ContextBefore)
        sb.WriteString("\n")
    }
    sb.WriteString(primary.Content)
    if primary.ContextAfter != "" {
        sb.WriteString("\n")
        sb.WriteString(primary.ContextAfter)
    }

    // Add context after primary chunk
    for _, c := range sortedContext {
        if c.Level >= primary.Level && c.Sequence > primary.Sequence {
            sb.WriteString("\n\n")
            sb.WriteString(c.Content)
        }
    }

    return sb.String()
}
```

---

## 6. Multi-Level Embedding

Chunks at different levels may need different embedding strategies. We support Matryoshka-style embeddings for level-aware search.

### Configuration

```go
// core/chunking/embedding.go

// MultiLevelEmbedder produces embeddings with level-aware strategies.
type MultiLevelEmbedder struct {
    baseEmbedder    Embedder
    tokenizer       Tokenizer

    // Matryoshka support (truncated embeddings for different levels)
    UseMatryoshka   bool

    // Level-specific strategies
    LevelStrategies map[int]*EmbeddingStrategy
}

// EmbeddingStrategy configures how to embed a chunk at a given level.
type EmbeddingStrategy struct {
    IncludeContext   bool    // Include context_before/after in text
    IncludeSignature bool    // Include function signature for inner blocks
    IncludeParentContext bool // Prepend parent's context
    MaxTokens        int     // Truncate input before embedding
    DimensionCutoff  int     // For Matryoshka: use first N dimensions (0 = all)
}

// DefaultLevelStrategies returns sensible defaults.
func DefaultLevelStrategies() map[int]*EmbeddingStrategy {
    return map[int]*EmbeddingStrategy{
        0: {  // File level
            IncludeContext:   false,
            IncludeSignature: false,
            MaxTokens:        2048,
            DimensionCutoff:  0,  // Full dimensions for coarse search
        },
        1: {  // Section/Type level
            IncludeContext:   true,
            IncludeSignature: false,
            MaxTokens:        1024,
            DimensionCutoff:  0,
        },
        2: {  // Function/Method level
            IncludeContext:   true,
            IncludeSignature: true,
            MaxTokens:        512,
            DimensionCutoff:  0,
        },
        3: {  // Block level
            IncludeContext:       true,
            IncludeSignature:     true,
            IncludeParentContext: true,  // Include parent function signature
            MaxTokens:            512,
            DimensionCutoff:      0,
        },
    }
}

// EmbedChunk produces an embedding for a chunk using level-appropriate strategy.
func (e *MultiLevelEmbedder) EmbedChunk(chunk *Chunk) ([]float32, error) {
    strategy := e.LevelStrategies[chunk.Level]
    if strategy == nil {
        strategy = e.LevelStrategies[2]  // Default to function-level strategy
    }

    // Build text to embed
    var text strings.Builder

    // Include parent context if configured
    if strategy.IncludeParentContext && chunk.ContextBefore != "" {
        text.WriteString(chunk.ContextBefore)
        text.WriteString("\n")
    }

    // Include signature if available and configured
    if strategy.IncludeSignature && chunk.Signature != "" {
        text.WriteString(chunk.Signature)
        text.WriteString("\n")
    }

    // Main content
    text.WriteString(chunk.Content)

    // Include trailing context
    if strategy.IncludeContext && chunk.ContextAfter != "" {
        text.WriteString("\n")
        text.WriteString(chunk.ContextAfter)
    }

    // Truncate if needed
    textStr := text.String()
    if strategy.MaxTokens > 0 {
        tokens := e.tokenizer.Tokenize(textStr)
        if len(tokens) > strategy.MaxTokens {
            textStr = e.tokenizer.Detokenize(tokens[:strategy.MaxTokens])
        }
    }

    // Embed
    embedding, err := e.baseEmbedder.Embed(textStr)
    if err != nil {
        return nil, err
    }

    // Matryoshka truncation if configured
    if e.UseMatryoshka && strategy.DimensionCutoff > 0 && strategy.DimensionCutoff < len(embedding) {
        embedding = embedding[:strategy.DimensionCutoff]
        embedding = normalize(embedding)
    }

    return embedding, nil
}

// EmbedChunks embeds multiple chunks in batch.
func (e *MultiLevelEmbedder) EmbedChunks(chunks []*Chunk) (map[string][]float32, error) {
    results := make(map[string][]float32, len(chunks))

    // Group by level for batch efficiency (same strategy)
    byLevel := make(map[int][]*Chunk)
    for _, c := range chunks {
        byLevel[c.Level] = append(byLevel[c.Level], c)
    }

    for level, levelChunks := range byLevel {
        strategy := e.LevelStrategies[level]

        // Build texts
        texts := make([]string, len(levelChunks))
        for i, c := range levelChunks {
            texts[i] = e.buildEmbeddingText(c, strategy)
        }

        // Batch embed
        embeddings, err := e.baseEmbedder.EmbedBatch(texts)
        if err != nil {
            return nil, err
        }

        // Store results
        for i, c := range levelChunks {
            emb := embeddings[i]
            if e.UseMatryoshka && strategy.DimensionCutoff > 0 {
                emb = normalize(emb[:strategy.DimensionCutoff])
            }
            results[c.ID] = emb
        }
    }

    return results, nil
}
```

---

## 7. Quality Integration

Chunks integrate with the quality scoring system (SCORING.md) via content_hash.

### Quality Signal Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ CHUNK QUALITY INTEGRATION                                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Chunk has content_hash ─────────────────────────────────────────┐          │
│                                                                  │          │
│                                                                  ▼          │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │ unified_quality_signals (from SCORING.md)                           │    │
│  │                                                                     │    │
│  │ • usage_feedback_score  ← how often was this chunk useful?          │    │
│  │ • staleness_score       ← when was this last validated?             │    │
│  │ • authority_score       ← internal + external citations             │    │
│  │ • trust_level           ← inherited from parent GraphNode           │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  Chunk-specific quality signals:                                            │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                                                                     │    │
│  │ • parse_quality    ← how cleanly did this parse? (AST errors?)      │    │
│  │ • completeness     ← is this a complete semantic unit?              │    │
│  │ • level_weight     ← preference for this hierarchy level            │    │
│  │                                                                     │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                             │
│  Combined scoring:                                                          │
│                                                                             │
│    final_score = similarity * 0.4                                           │
│                + quality_score * 0.3     (from unified_quality_signals)     │
│                + parse_quality * 0.1                                        │
│                + completeness * 0.1                                         │
│                + propagated_score * 0.1  (from ancestor matches)            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Integration Code

```go
// core/chunking/quality.go

// applyQualityScoring adds quality signals to chunk scores.
func (r *ChunkRetriever) applyQualityScoring(
    ctx context.Context,
    chunks []ScoredChunk,
    propagated map[string]float64,
) {
    // Batch-fetch quality signals by content_hash
    hashes := make([]string, len(chunks))
    for i, c := range chunks {
        hashes[i] = c.Chunk.ContentHash
    }

    signals, _ := r.signalStore.GetMany(ctx, hashes)

    for i := range chunks {
        chunk := chunks[i].Chunk
        sig := signals[chunk.ContentHash]

        // Quality score from unified signal store
        var qualityScore float64
        if sig != nil {
            qualityScore = sig.ComputeQualityScore()
        } else {
            qualityScore = 0.5  // Default neutral
        }

        // Chunk-specific quality
        parseQuality := chunk.ParseQuality
        completeness := chunk.Completeness

        // Propagated score from ancestors
        propScore := propagated[chunk.ID]

        // Combine
        chunks[i].QualityScore = qualityScore
        chunks[i].FinalScore =
            chunks[i].Similarity * 0.4 +
            qualityScore * 0.3 +
            parseQuality * 0.1 +
            completeness * 0.1 +
            propScore * 0.1
    }
}

// RecordChunkUsage records feedback when a chunk is used.
func (r *ChunkRetriever) RecordChunkUsage(ctx context.Context, chunkID string, signal FeedbackSignal) error {
    chunk, err := r.chunkStore.GetChunk(ctx, chunkID)
    if err != nil {
        return err
    }

    // Update unified signal store by content_hash
    return r.signalStore.UpdateSignal(chunk.ContentHash, SignalUpdate{
        Type:  signal.Type,
        Delta: signal.Value,
    })
}
```

---

## 8. Chunk Parameter Learning

**CORE PRINCIPLE: Chunking parameters are learned from retrieval feedback.** When a chunk is retrieved and used (or not used), that observation updates the domain's chunk configuration posteriors.

### Learning Signal Flow

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                    CHUNK PARAMETER LEARNING FLOW                                     │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │ INDEXING TIME: Use current learned parameters                               │    │
│  │                                                                             │    │
│  │   config := GetChunkConfig(domain, explore=true)  // Thompson Sampling      │    │
│  │   targetTokens := config.GetEffectiveTargetTokens(explore)                  │    │
│  │   contextBefore := config.GetEffectiveContextBefore(explore)                │    │
│  │   // ... chunk content using these parameters                               │    │
│  │   chunk.ConfigSnapshot = {targetTokens, contextBefore, ...}  // Record      │    │
│  └─────────────────────────────────────────────────────────────────────────────┘    │
│                                          │                                          │
│                                          ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │ RETRIEVAL TIME: Chunk is retrieved for a query                              │    │
│  │                                                                             │    │
│  │   results := retriever.Retrieve(query)                                      │    │
│  │   for _, result := range results {                                          │    │
│  │       // Chunk was retrieved with similarity score                          │    │
│  │       // Track: (chunk_id, token_count, context_before, context_after)      │    │
│  │   }                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────────┘    │
│                                          │                                          │
│                                          ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │ USAGE TIME: Agent uses (or ignores) retrieved chunk                         │    │
│  │                                                                             │    │
│  │   POSITIVE SIGNAL (chunk was useful):                                       │    │
│  │     • Agent cited the chunk in response                                     │    │
│  │     • Agent used code from the chunk                                        │    │
│  │     • User approved response containing chunk                               │    │
│  │                                                                             │    │
│  │   NEGATIVE SIGNAL (chunk was not useful):                                   │    │
│  │     • Chunk retrieved but not cited/used                                    │    │
│  │     • User rejected response containing chunk                               │    │
│  │     • Agent explicitly noted chunk was irrelevant                           │    │
│  └─────────────────────────────────────────────────────────────────────────────┘    │
│                                          │                                          │
│                                          ▼                                          │
│  ┌─────────────────────────────────────────────────────────────────────────────┐    │
│  │ UPDATE TIME: Update chunk config posteriors                                 │    │
│  │                                                                             │    │
│  │   observation := ChunkUsageObservation{                                     │    │
│  │       Domain:       chunk.Domain,                                           │    │
│  │       TokenCount:   chunk.TokenCount,                                       │    │
│  │       ContextBefore: len(chunk.ContextBefore),                              │    │
│  │       ContextAfter:  len(chunk.ContextAfter),                               │    │
│  │       WasUseful:     true/false,                                            │    │
│  │       OverflowStrategy: chunk.OverflowStrategy,  // If this was overflow    │    │
│  │   }                                                                         │    │
│  │                                                                             │    │
│  │   chunkConfigLearner.RecordObservation(observation)                         │    │
│  │   // Updates: TargetTokens, MinTokens, ContextBefore/After posteriors       │    │
│  └─────────────────────────────────────────────────────────────────────────────┘    │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### What Gets Learned

| Parameter | Observable Signal | Learning Mechanism |
|-----------|-------------------|-------------------|
| `TargetTokens` | Chunks at size X used Y% of the time | Gamma posterior update based on (size, wasUseful) |
| `MinTokens` | Tiny chunks (<N tokens) almost never used | Learns lower bound below which chunks are noise |
| `ContextTokensBefore` | Chunks with more context used more | Gamma posterior on optimal context size |
| `ContextTokensAfter` | Trailing context helps for certain domains | Gamma posterior per domain |
| `OverflowStrategy` | Which strategy's chunks get used more | Dirichlet-Multinomial on strategy success rates |

### ChunkConfigLearner Implementation

```go
// core/chunking/config_learner.go

// ChunkConfigLearner maintains learned chunk configuration posteriors per domain.
type ChunkConfigLearner struct {
    // Per-domain learned configs
    DomainConfigs map[Domain]*ChunkConfig

    // Global priors (shared across domains)
    GlobalPriors *ChunkConfig

    // Hierarchical blending weights
    DomainConfidence map[Domain]float64  // More observations = higher confidence

    // WAL for crash recovery
    wal *WriteAheadLog
}

// ChunkUsageObservation records a single chunk usage event.
type ChunkUsageObservation struct {
    Domain          Domain
    TokenCount      int
    ContextBefore   int
    ContextAfter    int
    WasUseful       bool
    OverflowStrategy *OverflowStrategy  // Non-nil if chunk was created via overflow
    Timestamp       time.Time
}

// RecordObservation updates posteriors based on chunk usage.
func (l *ChunkConfigLearner) RecordObservation(obs ChunkUsageObservation) {
    config := l.DomainConfigs[obs.Domain]
    if config == nil {
        config = PriorChunkConfig(obs.Domain)
        l.DomainConfigs[obs.Domain] = config
    }

    // Apply exponential decay to existing evidence (recency weighting)
    decayFactor := 0.999
    l.applyDecay(config, decayFactor)

    // Update TargetTokens posterior
    // If chunk was useful, its size is evidence for good TargetTokens
    if obs.WasUseful {
        // Bayesian update: observed useful chunk at size X
        // Increases belief that TargetTokens should be near X
        l.updateGammaPosterior(config.TargetTokens, obs.TokenCount, 1.0)
    } else {
        // Chunk was not useful - weaker evidence against this size
        // (Could be many reasons why not useful, so smaller update)
        l.updateGammaPosterior(config.TargetTokens, obs.TokenCount, 0.2)
    }

    // Update ContextTokensBefore posterior
    if obs.WasUseful && obs.ContextBefore > 0 {
        l.updateGammaPosterior(config.ContextTokensBefore, obs.ContextBefore, 1.0)
    }

    // Update ContextTokensAfter posterior
    if obs.WasUseful && obs.ContextAfter > 0 {
        l.updateGammaPosterior(config.ContextTokensAfter, obs.ContextAfter, 1.0)
    }

    // Update MinTokens (learn lower bound)
    if !obs.WasUseful && obs.TokenCount < config.MinTokens.Mean() {
        // Tiny chunk was not useful - evidence that MinTokens should be higher
        config.MinTokens.Alpha += 0.5
    }

    // Update overflow strategy weights
    if obs.OverflowStrategy != nil && config.OverflowStrategyWeights != nil {
        if obs.WasUseful {
            switch *obs.OverflowStrategy {
            case OverflowSplitRecursive:
                config.OverflowStrategyWeights.RecursiveCount += 1.0
            case OverflowSplitSentence:
                config.OverflowStrategyWeights.SentenceCount += 1.0
            case OverflowTruncateWithSummary:
                config.OverflowStrategyWeights.TruncateCount += 1.0
            }
        }
    }

    // Update domain confidence
    l.DomainConfidence[obs.Domain] = config.TargetTokens.Confidence()

    // Log to WAL for crash recovery
    l.wal.LogChunkConfigObservation(obs)
}

// updateGammaPosterior updates a Gamma distribution based on an observation.
func (l *ChunkConfigLearner) updateGammaPosterior(param *LearnedContextSize, observed int, weight float64) {
    // For Gamma(α, β), conjugate update with observation x:
    // α' = α + weight, β' = β + weight/x (approximately, for rate parameterization)
    // This shifts the mean toward the observed value
    observedFloat := float64(observed)
    if observedFloat > 0 {
        param.Alpha += weight
        param.Beta += weight / observedFloat
        param.EffectiveSamples += weight
    }
}

// GetConfig returns the effective chunk config for a domain with hierarchical blending.
func (l *ChunkConfigLearner) GetConfig(domain Domain, explore bool) *ChunkConfig {
    domainConfig := l.DomainConfigs[domain]
    if domainConfig == nil {
        domainConfig = PriorChunkConfig(domain)
    }

    // Blend domain config with global priors based on confidence
    confidence := l.DomainConfidence[domain]
    if confidence < 0.5 {
        // Low confidence: blend more with global priors
        return l.blendConfigs(l.GlobalPriors, domainConfig, confidence)
    }

    return domainConfig
}

// blendConfigs blends two configs based on weight (0 = all prior, 1 = all domain).
func (l *ChunkConfigLearner) blendConfigs(prior, domain *ChunkConfig, weight float64) *ChunkConfig {
    blend := func(p, d *LearnedContextSize) *LearnedContextSize {
        return &LearnedContextSize{
            Alpha:            p.Alpha*(1-weight) + d.Alpha*weight,
            Beta:             p.Beta*(1-weight) + d.Beta*weight,
            EffectiveSamples: p.EffectiveSamples*(1-weight) + d.EffectiveSamples*weight,
            PriorAlpha:       p.PriorAlpha,
            PriorBeta:        p.PriorBeta,
        }
    }

    return &ChunkConfig{
        MaxTokens:           domain.MaxTokens,  // Hard limit from domain
        TargetTokens:        blend(prior.TargetTokens, domain.TargetTokens),
        MinTokens:           blend(prior.MinTokens, domain.MinTokens),
        ContextTokensBefore: blend(prior.ContextTokensBefore, domain.ContextTokensBefore),
        ContextTokensAfter:  blend(prior.ContextTokensAfter, domain.ContextTokensAfter),
        OverflowStrategy:    domain.OverflowStrategy,
        OverflowStrategyWeights: domain.OverflowStrategyWeights,
    }
}
```

### WAL Integration

```go
// core/chunking/persistence.go

// WAL entry type for chunk config observations
const EntryChunkConfigObservation = 0x30

// ChunkConfigObservationEntry is the WAL entry for chunk learning.
type ChunkConfigObservationEntry struct {
    Domain          Domain    `json:"domain"`
    TokenCount      int       `json:"token_count"`
    ContextBefore   int       `json:"context_before"`
    ContextAfter    int       `json:"context_after"`
    WasUseful       bool      `json:"was_useful"`
    OverflowStrategy *int     `json:"overflow_strategy,omitempty"`
    Timestamp       int64     `json:"timestamp"`
}

// RecoverFromWAL replays chunk config observations to restore learned state.
func (l *ChunkConfigLearner) RecoverFromWAL(wal *WriteAheadLog) error {
    return wal.Replay(func(entry Entry) error {
        if entry.Type != EntryChunkConfigObservation {
            return nil  // Skip non-chunk entries
        }

        var obs ChunkConfigObservationEntry
        if err := json.Unmarshal(entry.Data, &obs); err != nil {
            return err
        }

        // Replay observation
        usageObs := ChunkUsageObservation{
            Domain:        obs.Domain,
            TokenCount:    obs.TokenCount,
            ContextBefore: obs.ContextBefore,
            ContextAfter:  obs.ContextAfter,
            WasUseful:     obs.WasUseful,
            Timestamp:     time.Unix(obs.Timestamp, 0),
        }
        if obs.OverflowStrategy != nil {
            strategy := OverflowStrategy(*obs.OverflowStrategy)
            usageObs.OverflowStrategy = &strategy
        }

        l.RecordObservation(usageObs)
        return nil
    })
}
```

### Example: Learning in Action

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ EXAMPLE: Learning Optimal Code Chunk Size                                            │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│ Initial prior for DomainCode:                                                       │
│   TargetTokens ~ Gamma(30, 0.1)  →  E[X] = 300 tokens                              │
│                                                                                     │
│ Observations over time:                                                             │
│   Chunk at 250 tokens: USED ✓                                                       │
│   Chunk at 450 tokens: USED ✓                                                       │
│   Chunk at 180 tokens: NOT USED ✗                                                   │
│   Chunk at 520 tokens: USED ✓                                                       │
│   Chunk at 400 tokens: USED ✓                                                       │
│   Chunk at 150 tokens: NOT USED ✗                                                   │
│   Chunk at 480 tokens: USED ✓                                                       │
│                                                                                     │
│ Updated posterior:                                                                  │
│   TargetTokens ~ Gamma(35.2, 0.082)  →  E[X] = 429 tokens                          │
│                                                                                     │
│ Learning result:                                                                    │
│   System learned that code chunks around 400-500 tokens are more useful             │
│   than the initial prior of 300 tokens. Future chunking will target ~430 tokens.   │
│                                                                                     │
│ Why this matters:                                                                   │
│   • 300-token chunks were often too small (incomplete functions)                    │
│   • 400-500 token chunks capture complete functions with docstrings                 │
│   • System AUTOMATICALLY adapts without manual tuning                               │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 9. Implementation Files

### New Files to Create

```
core/chunking/
├── types.go                 # Chunk, ChunkType, CrossReference types
├── config.go                # ChunkConfig with LearnedContextSize, LearnedOverflowWeights
├── config_learner.go        # ChunkConfigLearner for Bayesian parameter learning
├── config_persistence.go    # WAL integration for chunk config observations
├── store.go                 # ChunkStore (SQLite persistence)
├── schema.sql               # Database schema
├── splitter.go              # SemanticSplitter (main chunking logic)
├── retrieval.go             # ChunkRetriever (hierarchical search)
├── embedding.go             # MultiLevelEmbedder
├── quality.go               # Quality integration + usage feedback recording
├── parsers/
│   ├── interface.go         # Parser interface
│   ├── treesitter.go        # tree-sitter based code parser
│   ├── markdown.go          # Markdown parser
│   ├── academic.go          # Academic paper parser
│   ├── conversation.go      # LLM conversation parser
│   └── config.go            # Config file parser
└── *_test.go                # Tests for each
```

### Integration Points

| Existing System | Integration Point |
|-----------------|-------------------|
| `GraphNode` | Chunks link via `root_node_id` |
| `VectorData` | Replaced by `chunk_vectors` table |
| `unified_quality_signals` | Links via `content_hash` |
| `SearchCoordinator` (DS.9.x) | Uses `ChunkRetriever` for search |
| `HybridCoordinator` | Returns chunks instead of full documents |
| `FeedbackPipeline` | Records chunk-level feedback → updates ChunkConfigLearner |
| `WriteAheadLog` | Persists chunk config observations for crash recovery |
| `HierarchicalWeightSystem` | Shares LearnedContextSize type definition |
| `SessionWeightManager` | ChunkConfigLearner follows same copy-on-write pattern |

### Migration from Node-Level to Chunk-Level Vectors

```go
// core/chunking/migration.go

// MigrateToChunks migrates existing node-level vectors to chunk-level.
func MigrateToChunks(ctx context.Context, db *sql.DB, splitter *SemanticSplitter, embedder *MultiLevelEmbedder) error {
    // 1. Get all nodes with content
    nodes, _ := getAllNodesWithContent(ctx, db)

    for _, node := range nodes {
        // 2. Split node content into chunks
        chunks, err := splitter.Split(node.Content, node.ContentType, node.Language)
        if err != nil {
            log.Printf("warn: failed to chunk node %s: %v", node.ID, err)
            continue
        }

        // 3. Set root_node_id for all chunks
        for _, chunk := range chunks {
            chunk.RootNodeID = node.ID
        }

        // 4. Generate embeddings
        embeddings, err := embedder.EmbedChunks(chunks)
        if err != nil {
            log.Printf("warn: failed to embed chunks for node %s: %v", node.ID, err)
            continue
        }

        // 5. Save chunks and vectors
        if err := chunkStore.SaveChunks(ctx, chunks); err != nil {
            return err
        }

        for chunkID, embedding := range embeddings {
            if err := saveChunkVector(ctx, db, chunkID, embedding); err != nil {
                return err
            }
        }

        // 6. Optionally: remove old node-level vector
        // (or keep for coarse-grained search)
    }

    return nil
}
```

---

## Summary

The Chunking Architecture provides:

1. **Semantic-Aware Chunking** - AST-based for code, header-based for markdown, section-based for academic, turn-based for conversations

2. **Adaptive Sizing with Learned Parameters** - Respects semantic boundaries while enforcing token limits; soft targets (TargetTokens, MinTokens, ContextTokens) are **learned from retrieval feedback**, not hardcoded

3. **Hierarchical Structure** - Multi-level chunks with parent/sibling/cross-reference relationships

4. **Dedicated Storage** - Separate tables for chunks, content, symbols, cross-refs, and vectors

5. **Matryoshka Retrieval** - Fine-grained search with score propagation and adaptive expansion

6. **Level-Aware Embedding** - Different embedding strategies per hierarchy level

7. **Quality Integration** - Links to unified signal store via content_hash for quality scoring

8. **Chunk Parameter Learning** - Bayesian learning of optimal chunk sizes per domain based on usage feedback; follows same patterns as SCORING.md (Gamma posteriors, Thompson Sampling, WAL persistence)

9. **Cross-Reference Tracking** - Understands calls, implements, tests relationships between chunks

---

*Last updated: 2025-01-18*
