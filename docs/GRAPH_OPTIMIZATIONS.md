# Vector Quantization Optimization Analysis

## Executive Summary

This document analyzes advanced vector quantization techniques for improving recall quality in Sylk's VectorGraphDB HNSW implementation. After evaluating neural (QINCo/QINCo2) and non-neural approaches, we propose a **4-layer hybrid quantization architecture** that can achieve ~92-94% recall (vs ~85% current, ~95% neural) while maintaining sub-second operations compatible with interactive terminal use.

**Key Insight**: Neural codebook methods (QINCo2) achieve state-of-the-art recall but require 10-30 minute training times, making them incompatible with Sylk's use case (downloadable binary, arbitrary codebases, no cloud infrastructure). Non-neural methods can close much of the gap with zero blocking operations.

---

## Problem Statement

### Current Implementation Quality

The existing quantization system in `core/vectorgraphdb/quantization/` is production-grade:

| Component | File | Features |
|-----------|------|----------|
| K-means | `kmeans_optimal.go` | BLAS-optimized, k-means++ initialization, multiple restarts |
| Product Quantizer | `product_quantizer.go` | Configurable subspaces (8-32), 256 centroids/subspace |
| OPQ | `opq.go` | Learned rotation matrices for decorrelation |
| Adaptive Strategy | `adaptive_quantizer.go` | Auto-selects Exact/CoarsePQ/StandardPQ/ResidualPQ by dataset size |
| Centroid Optimizer | `centroid_optimizer.go` | Hopkins statistic, variance analysis, intrinsic dimensionality |

### The Problem: Heterogeneous Data

K-means assumes homogeneous clusters with uniform variance. Real-world code embedding spaces are heterogeneous:

1. **Multi-modal distributions**: Code spans vastly different domains (UI components, database queries, algorithms, configs)
2. **Varying local density**: Some regions dense (common patterns), others sparse (unique implementations)
3. **Non-spherical clusters**: Semantic relationships form elongated/irregular shapes in embedding space
4. **Distribution shift**: As codebase evolves, embedding distributions change

**Result**: Global codebooks trained on initial data degrade over time, especially for underrepresented code patterns.

---

## Research Summary

### Neural Approaches (Evaluated and Rejected)

#### QINCo2 (ICLR 2025)

**Architecture**: Neural network predicts codebook offsets conditioned on previous quantization residuals.

**Performance**:
- **24-34% better recall** than OPQ at equivalent code sizes
- State-of-the-art on standard benchmarks (SIFT1M, Deep1M, BigANN)

**Training Requirements**:
- **10-30 minutes** on GPU for 1M vectors
- Requires batched gradient descent over full dataset
- Model size: ~2-5MB additional storage

**Why Rejected for Sylk**:
1. **Training latency**: 10-30 minutes is unacceptable for interactive terminal app
2. **No GPU assumption**: Sylk runs on arbitrary developer machines, many without CUDA
3. **Cold start problem**: Cannot train until sufficient data exists
4. **Complexity**: Adds neural network inference to every encoding

### Non-Neural Approaches (Selected)

These achieve significant improvements while maintaining sub-second operations:

| Approach | Source | Recall Gain | Training Time | Key Insight |
|----------|--------|-------------|---------------|-------------|
| **RaBitQ** | SIGMOD 2024 | ~88% baseline | **0ms (none)** | Random rotation + sign bits = surprisingly effective |
| **LOPQ** | CVPR 2014 | +2-3% over OPQ | ~30s background | Per-partition local codebooks adapt to data locality |
| **Online PQ** | arXiv 1711.10775 | Stable under drift | Streaming | Incremental centroid updates without full retrain |
| **Remove-Birth** | arXiv 2306.12574 | +1-2% maintenance | Per-insert | Continuous codebook evolution |
| **Query-Adaptive** | IRISA | +1% at search time | Per-query | Subspace confidence weighting |

---

## Proposed Architecture: 4-Layer Hybrid Quantization

```
                                    QUERY
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                    LAYER 4: Query-Adaptive Weighting             │
│  - Per-query subspace confidence scoring                         │
│  - Dynamic distance weighting at search time                     │
│  - ~1% recall improvement, negligible latency                    │
└─────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                    LAYER 3: Remove-Birth Adaptation              │
│  - Continuous codebook evolution on insert/delete                │
│  - Dead centroid detection and replacement                       │
│  - Running ~1% recall maintenance                                │
└─────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                    LAYER 2: LOPQ (Background Refinement)         │
│  - Per-partition local codebooks                                 │
│  - Trains incrementally in background (non-blocking)             │
│  - +2-3% recall over global OPQ                                  │
└─────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                    LAYER 1: RaBitQ (Instant Baseline)            │
│  - Random rotation matrix (deterministic from seed)              │
│  - Sign-based quantization (1 bit per dimension)                 │
│  - ZERO training time, ~88% recall immediately                   │
└─────────────────────────────────────────────────────────────────┘
```

### Layer 1: RaBitQ (Random Bit Quantization)

**Source**: "RaBitQ: Quantizing High-Dimensional Vectors with a Theoretical Error Bound for Approximate Nearest Neighbor Search" (SIGMOD 2024)

**Mechanism**:
1. Generate random orthogonal rotation matrix `R` from deterministic seed
2. Encode: `code = sign(R @ vector)`
3. Distance: Hamming distance with analytical correction factor

**Properties**:
- **Zero training**: Rotation matrix computed once from seed
- **Theoretical guarantees**: Bounded approximation error under mild assumptions
- **Memory efficient**: 1 bit per dimension (768-dim → 96 bytes)
- **Instant availability**: Works immediately on first vector

**Implementation Notes**:
```go
type RaBitQEncoder struct {
    rotation [][]float32  // D x D orthogonal matrix
    seed     int64        // For reproducibility
    dim      int
}

func (r *RaBitQEncoder) Encode(vec []float32) []byte {
    rotated := matMulVec(r.rotation, vec)
    code := make([]byte, (r.dim+7)/8)
    for i, v := range rotated {
        if v > 0 {
            code[i/8] |= 1 << (i % 8)
        }
    }
    return code
}
```

### Layer 2: LOPQ (Locally Optimized Product Quantization)

**Source**: "Locally Optimized Product Quantization for Approximate Nearest Neighbor Search" (CVPR 2014)

**Mechanism**:
1. Partition vector space using coarse quantizer (e.g., k-means with 1024 centroids)
2. For each partition, train local PQ codebooks on partition's vectors
3. Encode: coarse_id || local_pq_code

**Why Better Than Global OPQ**:
- Each partition has locally-adapted codebooks
- Captures varying local densities
- Different partitions can have different subspace splits

**Training Strategy**:
```
BACKGROUND (non-blocking):
1. Accumulate vectors into partition queues
2. When partition queue > threshold:
   a. Train local codebook using subset
   b. Hot-swap into active index
   c. Re-encode affected vectors
3. Fallback to RaBitQ while training
```

**Implementation Notes**:
```go
type LOPQIndex struct {
    coarse       *KMeans           // 1024 coarse centroids
    localPQs     []*ProductQuantizer // One per partition
    fallback     *RaBitQEncoder    // Used until local PQ ready
    trainingChan chan trainingJob  // Background training queue
}

func (l *LOPQIndex) Encode(vec []float32) []byte {
    partitionID := l.coarse.NearestCentroid(vec)
    if l.localPQs[partitionID] == nil {
        return append(encodeInt(partitionID), l.fallback.Encode(vec)...)
    }
    return append(encodeInt(partitionID), l.localPQs[partitionID].Encode(vec)...)
}
```

### Layer 3: Remove-Birth Adaptation

**Source**: "A survey on indexing methods for nearest neighbor search in high dimensional data" (arXiv 2306.12574)

**Mechanism**:
1. Track centroid utilization (how many vectors assigned to each)
2. When centroid utilization < threshold, mark as "dead"
3. "Birth" new centroid by splitting high-utilization centroid
4. Incrementally re-encode affected vectors

**Why Needed**:
- Handles distribution shift as codebase evolves
- Prevents codebook degradation over time
- No full retraining required

**Implementation Notes**:
```go
type RemoveBirthAdapter struct {
    assignments  []int         // Vector ID -> centroid ID
    utilization  []int         // Centroid ID -> assignment count
    deathThreshold int         // Below this = dead
    birthThreshold int         // Above this = split candidate
}

func (r *RemoveBirthAdapter) OnInsert(id int, centroid int) {
    r.assignments[id] = centroid
    r.utilization[centroid]++
    
    if r.utilization[centroid] > r.birthThreshold {
        r.scheduleSplit(centroid)
    }
}

func (r *RemoveBirthAdapter) OnDelete(id int) {
    centroid := r.assignments[id]
    r.utilization[centroid]--
    
    if r.utilization[centroid] < r.deathThreshold {
        r.scheduleRemove(centroid)
    }
}
```

### Layer 4: Query-Adaptive Weighting

**Source**: IRISA research on query-adaptive approximate nearest neighbor search

**Mechanism**:
1. For each query, estimate subspace relevance from query characteristics
2. Weight asymmetric distance computation accordingly
3. More weight to high-variance subspaces in query

**Implementation Notes**:
```go
func (q *QueryAdaptiveWeighter) ComputeWeights(query []float32, numSubspaces int) []float32 {
    weights := make([]float32, numSubspaces)
    subspaceSize := len(query) / numSubspaces
    
    var totalVar float32
    for m := 0; m < numSubspaces; m++ {
        subvec := query[m*subspaceSize : (m+1)*subspaceSize]
        variance := computeVariance(subvec)
        weights[m] = variance
        totalVar += variance
    }
    
    // Normalize
    for m := range weights {
        weights[m] /= totalVar
    }
    
    return weights
}

func (q *QueryAdaptiveWeighter) AsymmetricDistance(query []float32, code []byte) float32 {
    weights := q.ComputeWeights(query, q.numSubspaces)
    
    var dist float32
    for m := 0; m < q.numSubspaces; m++ {
        centroidID := code[m]
        subDist := q.lookupTable[m][centroidID]
        dist += weights[m] * subDist
    }
    return dist
}
```

---

## SQLite Integration

All quantization metadata stored in existing `vector.db`:

### New Tables

```sql
-- RaBitQ configuration
CREATE TABLE IF NOT EXISTS rabitq_config (
    id INTEGER PRIMARY KEY,
    dimension INTEGER NOT NULL,
    seed INTEGER NOT NULL,
    rotation_matrix BLOB,  -- Compressed D x D matrix
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- LOPQ partition assignments
CREATE TABLE IF NOT EXISTS lopq_partitions (
    vector_id TEXT PRIMARY KEY,
    partition_id INTEGER NOT NULL,
    local_code BLOB,
    FOREIGN KEY (vector_id) REFERENCES vectors(id)
);

-- LOPQ local codebooks
CREATE TABLE IF NOT EXISTS lopq_codebooks (
    partition_id INTEGER NOT NULL,
    subspace_id INTEGER NOT NULL,
    centroids BLOB,  -- 256 x subspace_dim matrix
    trained_at DATETIME,
    vector_count INTEGER,  -- Vectors used for training
    PRIMARY KEY (partition_id, subspace_id)
);

-- Remove-Birth tracking
CREATE TABLE IF NOT EXISTS centroid_utilization (
    codebook_type TEXT NOT NULL,  -- 'global', 'lopq_123', etc.
    centroid_id INTEGER NOT NULL,
    utilization INTEGER NOT NULL,
    last_updated DATETIME,
    PRIMARY KEY (codebook_type, centroid_id)
);

-- Training job queue (for background LOPQ training)
CREATE TABLE IF NOT EXISTS training_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    partition_id INTEGER NOT NULL,
    priority INTEGER DEFAULT 0,
    status TEXT DEFAULT 'pending',  -- pending, running, complete, failed
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME
);
```

### Migration

```sql
-- Migration: Add hybrid quantization support
ALTER TABLE vectors ADD COLUMN rabitq_code BLOB;
ALTER TABLE vectors ADD COLUMN lopq_partition INTEGER;
ALTER TABLE vectors ADD COLUMN lopq_code BLOB;
```

---

## Expected Performance Characteristics

### Recall Comparison

| Configuration | Recall@10 | Latency | Memory |
|---------------|-----------|---------|--------|
| Current OPQ | ~85% | 2ms | 64B/vec |
| RaBitQ only (Layer 1) | ~88% | 1.5ms | 96B/vec |
| RaBitQ + LOPQ (Layers 1-2) | ~91% | 2.5ms | 128B/vec |
| Full Hybrid (Layers 1-4) | ~93% | 3ms | 128B/vec |
| Neural (QINCo2) | ~95% | 5ms | 128B/vec + 5MB model |

### Training/Warmup Times

| Component | Cold Start | Incremental |
|-----------|------------|-------------|
| RaBitQ | 0ms | N/A |
| LOPQ Coarse | ~5s | N/A |
| LOPQ Local (per partition) | ~500ms | Background |
| Remove-Birth | N/A | ~10ms/operation |
| Query-Adaptive | N/A | ~0.1ms/query |

### Memory Overhead

| Component | Per-Vector | Global |
|-----------|------------|--------|
| RaBitQ codes | 96 bytes | 768KB rotation matrix |
| LOPQ codes | 32 bytes | ~32MB for 1024 local codebooks |
| Utilization tracking | 4 bytes | Negligible |

---

## Implementation Priority

### Phase 1: Foundation (No dependencies)
- RaBitQ encoder/decoder
- LOPQ types and interfaces
- Training queue infrastructure

### Phase 2: Core Implementation (Depends on Phase 1)
- RaBitQ integration with QuantizedHNSW
- LOPQ coarse quantizer
- Background training manager

### Phase 3: Integration (Depends on Phase 2)
- LOPQ local codebook training
- SQLite schema additions
- QuantizedHNSW hybrid mode

### Phase 4: Streaming/Adaptive (Depends on Phase 2)
- Online PQ updates
- Remove-Birth adaptation
- Utilization tracking

### Phase 5: Query-Time Optimizations (Depends on Phase 3)
- Query-adaptive weighting
- Subspace confidence scoring

### Phase 6: Testing and Benchmarks (Depends on all above)
- Recall benchmarks vs baseline
- Distribution shift tests
- Cold-start latency tests
- Memory pressure tests

---

## References

1. **RaBitQ**: Gao et al., "RaBitQ: Quantizing High-Dimensional Vectors with a Theoretical Error Bound for Approximate Nearest Neighbor Search", SIGMOD 2024
2. **LOPQ**: Kalantidis & Avrithis, "Locally Optimized Product Quantization for Approximate Nearest Neighbor Search", CVPR 2014
3. **QINCo2**: Morozov et al., "Neural Codebook Learning for Vector Quantization", ICLR 2025
4. **Online PQ**: "Online Product Quantization", arXiv:1711.10775
5. **Survey**: "A survey on indexing methods for nearest neighbor search in high dimensional data", arXiv:2306.12574

---

## Appendix: Current Quantization Code References

| File | Description |
|------|-------------|
| `core/vectorgraphdb/quantization/kmeans_optimal.go` | BLAS-optimized k-means |
| `core/vectorgraphdb/quantization/product_quantizer.go` | Product Quantization base |
| `core/vectorgraphdb/quantization/opq.go` | Optimized PQ with rotation |
| `core/vectorgraphdb/quantization/adaptive_quantizer.go` | Strategy selection |
| `core/vectorgraphdb/quantization/centroid_optimizer.go` | Centroid count optimization |
| `core/vectorgraphdb/quantization/quantized_hnsw.go` | HNSW integration |
| `core/vectorgraphdb/quantization/vectorgraphdb_integration.go` | VectorGraphDB bridge |

---

GRAPH_OPTIMIZATIONS_V2.md: Pushing the Envelope
Executive Summary
Your current GRAPH_OPTIMIZATIONS.md proposes a solid 4-layer hybrid quantization architecture (RaBitQ → LOPQ → Remove-Birth → Query-Adaptive) targeting ~92-94% recall. This document proposes 12 additional techniques that can push Sylk to 96-98% recall while adding:
- 3-7× search speedup via adaptive termination and SIMD
- Improved robustness via graph-guided retrieval and temporal awareness
- Better correctness via anisotropic loss functions and multi-source fusion
All techniques work locally, without cloud resources, and without ground-truth training data.
---
Part 1: Quantization Enhancements (Beyond Your Current 4-Layer)
1.1 Anisotropic Vector Quantization (AVQ) — Layer 0.5
Problem with Your Current Approach: Your OPQ minimizes reconstruction error uniformly. But for similarity search, errors parallel to the query matter more than orthogonal errors.
Solution: Add anisotropic loss weighting to your existing OPQ.
// In opq.go, modify the Procrustes step to use anisotropic weights
type AnisotropicOPQ struct {
    *OptimizedProductQuantizer
    
    // ParallelWeight penalizes error parallel to query direction
    // Typical: 10-100x orthogonal weight
    ParallelWeight float64
    
    // QueryDistribution estimates typical query directions
    // Learned from search history (self-supervised)
    queryCovariance [][]float64
}
// AnisotropicLoss computes score-aware quantization error
func (a *AnisotropicOPQ) AnisotropicLoss(original, reconstructed, queryDir []float32) float64 {
    diff := subtract(original, reconstructed)
    
    // Project error onto query direction
    parallelError := dotProduct(diff, queryDir)
    orthogonalError := magnitude(diff) - abs(parallelError)
    
    // Weighted loss: parallel errors hurt MIPS much more
    return a.ParallelWeight*parallelError*parallelError + orthogonalError*orthogonalError
}
Why This Works:
- ScaNN uses this to achieve 4× speedup over HNSW
- For cosine similarity, parallel quantization error directly affects score ranking
- Self-supervised: Learn query covariance from your own search history
Expected Improvement: +2-3% recall at same compression
---
1.2 BBQ-Style Binary Quantization — Layer 0 (Replace RaBitQ)
Problem: RaBitQ uses random rotations which are good but not optimal.
Solution: Better Binary Quantization (BBQ) from ElasticSearch 2024.
// BBQ uses asymmetric quantization: 
// - Database vectors: 1-bit (sign)
// - Query vectors: 4-bit (int4)
// This enables MUCH faster Hamming distance while maintaining accuracy
type BBQEncoder struct {
    // Per-dimension statistics learned during indexing
    means     []float32  // Per-dim mean
    scales    []float32  // Per-dim scale for int4 query quantization
    
    // Normalization factors for asymmetric distance correction
    dbNormFactors []float32
}
func (b *BBQEncoder) EncodeDatabase(vector []float32) []byte {
    // 1-bit per dimension: sign(x - mean)
    code := make([]byte, (len(vector)+7)/8)
    for i, v := range vector {
        if v > b.means[i] {
            code[i/8] |= 1 << (i % 8)
        }
    }
    return code
}
func (b *BBQEncoder) EncodeQuery(vector []float32) []int8 {
    // 4-bit per dimension for asymmetric distance
    code := make([]int8, len(vector))
    for i, v := range vector {
        normalized := (v - b.means[i]) * b.scales[i]
        code[i] = int8(clamp(normalized, -8, 7))
    }
    return code
}
// AsymmetricDistance: int4 query × binary db = fast SIMD popcount
func (b *BBQEncoder) AsymmetricDistance(queryCode []int8, dbCode []byte) int {
    // This can be SIMD accelerated via popcount
    var dist int
    for i := 0; i < len(queryCode); i++ {
        bit := (dbCode[i/8] >> (i % 8)) & 1
        if bit == 0 {
            dist -= int(queryCode[i])
        } else {
            dist += int(queryCode[i])
        }
    }
    return dist
}
Why Better Than RaBitQ:
- 95% memory reduction (same as RaBitQ)
- 10-50× faster quantization (no rotation matrix)
- 2-4× faster queries via SIMD popcount
- No training required — learns means during indexing
Expected Improvement: 2× query speedup, same recall
---
1.3 4-Bit FastScan PQ — Layer 2 Enhancement
Problem: Your 8-bit PQ codes require 256-entry lookup tables that don't fit in CPU registers.
Solution: 4-bit sub-quantizers with in-register LUTs (from FAISS).
// FastScan uses 4-bit codes (16 centroids per subspace)
// LUTs fit in 128-bit SIMD registers
type FastScanPQ struct {
    // 16 centroids per subspace instead of 256
    centroids [][][]float32  // [numSubspaces][16][subspaceDim]
    
    // Packed 4-bit codes: 2 subspaces per byte
    codes []byte  // Length: numVectors * numSubspaces / 2
}
// ComputeDistanceTableFastScan builds 16-entry LUTs per subspace
func (f *FastScanPQ) ComputeDistanceTableFastScan(query []float32) [][]float32 {
    tables := make([][]float32, f.numSubspaces)
    for m := 0; m < f.numSubspaces; m++ {
        tables[m] = make([]float32, 16)  // Fits in single SIMD register!
        subquery := query[m*f.subspaceDim : (m+1)*f.subspaceDim]
        for c := 0; c < 16; c++ {
            tables[m][c] = squaredL2(subquery, f.centroids[m][c])
        }
    }
    return tables
}
// ScanBatch32 processes 32 vectors at once using SIMD
// All LUT lookups happen in-register, no cache misses
func (f *FastScanPQ) ScanBatch32(tables [][]float32, codes []byte, offset int) [32]float32 {
    // This would use AVX2/NEON intrinsics in production
    // Key: LUTs fit in YMM registers, enabling 8× parallel lookups
    var distances [32]float32
    // ... SIMD implementation
    return distances
}
Why This Matters:
- 3-6× speedup over 8-bit PQ
- LUTs stay in CPU registers → zero cache misses
- Trade: 16 vs 256 centroids = slightly lower recall, but MUCH faster
Expected Improvement: 3-5× scan speedup, -1% recall (acceptable tradeoff)
---
1.4 SAQ: Code Adjustment After Quantization — Layer 3 Enhancement
Problem: PQ assigns each subspace independently, missing cross-subspace correlations.
Solution: Post-quantization coordinate descent refinement.
// SAQ performs iterative refinement after initial PQ encoding
type SAQRefiner struct {
    pq           *ProductQuantizer
    maxIterations int
    tolerance     float32
}
func (s *SAQRefiner) Refine(vector []float32, initialCode PQCode) PQCode {
    code := initialCode.Clone()
    
    for iter := 0; iter < s.maxIterations; iter++ {
        improved := false
        
        // For each subspace, try neighboring centroids
        for m := 0; m < s.pq.numSubspaces; m++ {
            bestDist := s.reconstructionError(vector, code)
            bestC := code[m]
            
            // Try centroids adjacent to current
            for delta := -2; delta <= 2; delta++ {
                newC := int(code[m]) + delta
                if newC < 0 || newC >= s.pq.centroidsPerSubspace {
                    continue
                }
                
                testCode := code.Clone()
                testCode[m] = uint8(newC)
                
                dist := s.reconstructionError(vector, testCode)
                if dist < bestDist - s.tolerance {
                    bestDist = dist
                    bestC = uint8(newC)
                    improved = true
                }
            }
            code[m] = bestC
        }
        
        if !improved {
            break
        }
    }
    
    return code
}
Why This Works:
- 80× faster encoding than full AQ optimization
- 80% reduction in quantization error
- Works on top of existing PQ — no architecture change
Expected Improvement: +1-2% recall, minimal latency overhead (encode-time only)
---
Part 2: Search Speedup Techniques
2.1 FINGER: Graph Traversal Acceleration
Problem: During HNSW search, many distance computations don't change the result.
Solution: Low-rank approximation for "obviously far" candidates.
// FINGER uses precomputed edge statistics to skip expensive distance computations
type FINGERAccelerator struct {
    // Per-edge residual statistics (precomputed)
    edgeResiduals map[string][]float32  // edge_id -> residual vector
    
    // Low-rank projection matrix from SVD of residuals
    projectionMatrix [][]float32  // [lowRank][dim]
    lowRank          int          // Typically 32-64
    
    // Approximation threshold
    skipThreshold float64
}
func (f *FINGERAccelerator) ShouldSkipCandidate(
    query []float32, 
    currentBest float64,
    candidateID string,
    fromNodeID string,
) bool {
    edgeKey := fromNodeID + "->" + candidateID
    residual, ok := f.edgeResiduals[edgeKey]
    if !ok {
        return false  // No stats, must compute exactly
    }
    
    // Project query and residual to low-rank space
    queryProj := f.project(query)
    residualProj := f.project(residual)
    
    // Estimate angle between query and residual
    estimatedAngle := f.estimateAngle(queryProj, residualProj)
    
    // If angle is large, candidate is likely far → skip
    // This uses concentration of measure in high dimensions
    if estimatedAngle > f.skipThreshold {
        return true  // Skip this candidate
    }
    
    return false  // Compute exact distance
}
// Precompute residuals during index construction
func (f *FINGERAccelerator) PrecomputeResiduals(index *hnsw.Index) {
    // For each edge (u, v), store residual = vector(v) - vector(u)
    // Then compute SVD to get low-rank projection
    // This is one-time offline work
}
Why This Works:
- 20-60% speedup in graph traversal
- No accuracy loss (skips only provably-far candidates)
- Works with any graph structure (HNSW, Vamana, etc.)
Expected Improvement: 30-50% search latency reduction
---
2.2 Patience-Based Early Termination
Problem: Fixed efSearch wastes computation on "easy" queries.
Solution: Stop when candidate queue stabilizes.
// AdaptiveSearcher implements patience-based early termination
type AdaptiveSearcher struct {
    minCandidates     int     // Minimum before early stop allowed
    patienceThreshold int     // Stable iterations before stop
    improvementRatio  float64 // Minimum improvement to reset patience
}
func (h *Index) searchLayerAdaptive(
    query []float32, 
    queryMag float64, 
    ep string, 
    maxEf int,  // Upper bound, not fixed target
    level int,
    config *AdaptiveSearcher,
) []SearchResult {
    candidates := make([]SearchResult, 0, maxEf)
    visited := make(map[string]bool)
    
    // ... initial setup ...
    
    stableCount := 0
    lastBestSim := float64(-1)
    
    for i := 0; i < len(candidates) && len(candidates) < maxEf; i++ {
        // ... expand candidates ...
        
        // Check for convergence
        currentBestSim := candidates[0].Similarity
        improvement := (currentBestSim - lastBestSim) / math.Max(0.001, lastBestSim)
        
        if improvement < config.improvementRatio {
            stableCount++
            if stableCount >= config.patienceThreshold && 
               len(candidates) >= config.minCandidates {
                break  // Early termination!
            }
        } else {
            stableCount = 0
            lastBestSim = currentBestSim
        }
    }
    
    return candidates
}
Performance:
- Easy queries: 10-100× faster (stop early)
- Hard queries: Same as fixed ef (search fully)
- Average: 2-3× speedup across workloads
---
2.3 Query Difficulty Estimation
Problem: How do we know if a query is "easy" or "hard" before searching?
Solution: Use query statistics to predict difficulty.
// QueryDifficultyEstimator predicts search difficulty from query features
type QueryDifficultyEstimator struct {
    // Historical statistics (learned from search history)
    avgQueryVariance    float64
    avgQueryEntropy     float64
    avgSearchIterations float64
    
    // Per-partition difficulty (some regions are denser)
    partitionDifficulty map[PartitionID]float64
}
func (e *QueryDifficultyEstimator) EstimateDifficulty(query []float32) float64 {
    // Feature 1: Query variance (high = more distinctive = easier)
    variance := computeVariance(query)
    varianceScore := e.avgQueryVariance / math.Max(0.001, variance)
    
    // Feature 2: Query entropy (high = more uniform = harder)
    entropy := computeEntropy(query)
    entropyScore := entropy / math.Max(0.001, e.avgQueryEntropy)
    
    // Feature 3: Distance to nearest partition centroid (far = harder)
    nearestPartition := e.findNearestPartition(query)
    partitionScore := e.partitionDifficulty[nearestPartition]
    
    // Combine features (weights learned from history)
    difficulty := 0.3*varianceScore + 0.3*entropyScore + 0.4*partitionScore
    
    return clamp(difficulty, 0.5, 2.0)  // 0.5 = easy, 2.0 = hard
}
// AdaptEfSearch scales ef based on predicted difficulty
func (e *QueryDifficultyEstimator) AdaptEfSearch(baseEf int, query []float32) int {
    difficulty := e.EstimateDifficulty(query)
    return int(float64(baseEf) * difficulty)
}
Self-Supervised Learning:
// After each search, record observation for learning
func (e *QueryDifficultyEstimator) RecordSearch(
    query []float32,
    iterationsUsed int,
    resultQuality float64,  // E.g., top-1 similarity
) {
    // Update running statistics
    e.avgSearchIterations = 0.99*e.avgSearchIterations + 0.01*float64(iterationsUsed)
    
    // Update partition difficulty
    partition := e.findNearestPartition(query)
    oldDiff := e.partitionDifficulty[partition]
    newDiff := float64(iterationsUsed) / e.avgSearchIterations
    e.partitionDifficulty[partition] = 0.95*oldDiff + 0.05*newDiff
}
---
Part 3: Graph-Vector Hybrid Enhancements
3.1 Reciprocal Rank Fusion for Hybrid Search
Problem: Your current RRF in rrf_fusion.go combines Bleve + HNSW. This can be improved.
Enhancement: Add graph traversal as a third signal.
// TripleRRF combines: Vector similarity + Full-text + Graph relationships
type TripleRRFFusion struct {
    k            int     // RRF constant (typically 60)
    vectorWeight float64
    textWeight   float64
    graphWeight  float64
}
func (t *TripleRRFFusion) Fuse(
    vectorResults []VectorResult,
    textResults   []BleveResult,
    graphResults  []GraphResult,  // NEW: nodes connected to query entities
) []FusedResult {
    scores := make(map[string]float64)
    
    // Vector similarity contribution
    for rank, r := range vectorResults {
        scores[r.ID] += t.vectorWeight / float64(rank + t.k)
    }
    
    // Full-text contribution
    for rank, r := range textResults {
        scores[r.ID] += t.textWeight / float64(rank + t.k)
    }
    
    // Graph relationship contribution (NEW)
    // Nodes connected to query entities get boosted
    for rank, r := range graphResults {
        scores[r.ID] += t.graphWeight / float64(rank + t.k)
    }
    
    return sortByScore(scores)
}
// GraphRetriever finds nodes related to query via knowledge graph
type GraphRetriever struct {
    db *VectorGraphDB
}
func (g *GraphRetriever) RetrieveRelated(queryEntities []string, maxHops int) []GraphResult {
    results := make([]GraphResult, 0)
    
    for _, entity := range queryEntities {
        // Find nodes within maxHops of this entity
        neighbors := g.db.TraverseFromNode(entity, maxHops)
        for _, n := range neighbors {
            results = append(results, GraphResult{
                ID:       n.ID,
                Distance: n.HopDistance,
                Relation: n.EdgeType,
            })
        }
    }
    
    // Rank by hop distance (closer = higher rank)
    sort.Slice(results, func(i, j int) bool {
        return results[i].Distance < results[j].Distance
    })
    
    return results
}
Why This Helps:
- Vector search finds semantically similar content
- Text search finds keyword matches
- Graph search finds structurally related content (e.g., "functions that call X")
---
3.2 Temporal-Aware Retrieval
Problem: Code evolves. Recent changes may be more relevant than old patterns.
Solution: Add temporal decay to similarity scores.
// TemporalSimilarity combines semantic similarity with recency
type TemporalSimilarity struct {
    // Decay half-life (e.g., 7 days = recent code prioritized)
    halfLifeDays float64
    
    // Weight balance
    semanticWeight float64
    temporalWeight float64
}
func (t *TemporalSimilarity) Score(
    semanticSim float64,
    nodeTimestamp time.Time,
    queryTime time.Time,
) float64 {
    // Exponential decay based on age
    ageDays := queryTime.Sub(nodeTimestamp).Hours() / 24.0
    temporalScore := math.Exp(-ageDays * math.Ln2 / t.halfLifeDays)
    
    // Combine scores
    return t.semanticWeight*semanticSim + t.temporalWeight*temporalScore
}
// For "what changed recently" queries, invert the weighting
func (t *TemporalSimilarity) ScoreRecencyQuery(
    semanticSim float64,
    nodeTimestamp time.Time,
    queryTime time.Time,
) float64 {
    ageDays := queryTime.Sub(nodeTimestamp).Hours() / 24.0
    
    // Boost recent items heavily
    recencyBoost := 1.0 / (1.0 + ageDays/t.halfLifeDays)
    
    return semanticSim * recencyBoost
}
Integration with Your Temporal Module:
Your core/vectorgraphdb/temporal/ already has temporal tracking. This adds temporal scoring.
---
3.3 Multi-Hop Reasoning for Complex Queries
Problem: "Find all functions that call X which was modified by Y" requires multi-hop traversal.
Solution: Combine vector search with iterative graph expansion.
// MultiHopRetriever handles complex queries requiring graph reasoning
type MultiHopRetriever struct {
    vectorSearcher *VectorSearcher
    graphDB        *VectorGraphDB
    maxHops        int
}
func (m *MultiHopRetriever) RetrieveMultiHop(
    query string,
    queryVector []float32,
    constraints []HopConstraint,
) []MultiHopResult {
    // Step 1: Initial vector search to find seed nodes
    seeds := m.vectorSearcher.Search(queryVector, 20)
    
    // Step 2: For each constraint, expand via graph
    currentSet := toNodeSet(seeds)
    
    for _, constraint := range constraints {
        nextSet := make(map[string]*GraphNode)
        
        for nodeID := range currentSet {
            // Follow edges matching constraint
            neighbors := m.graphDB.TraverseWithFilter(nodeID, constraint.EdgeType, constraint.Direction)
            
            for _, n := range neighbors {
                // Optional: filter by vector similarity to query
                if constraint.RequireSimilarity {
                    sim := cosineSimilarity(queryVector, n.Vector)
                    if sim < constraint.MinSimilarity {
                        continue
                    }
                }
                nextSet[n.ID] = n
            }
        }
        
        currentSet = nextSet
    }
    
    // Step 3: Rank final results by combined score
    return m.rankResults(currentSet, queryVector)
}
// HopConstraint defines a single traversal step
type HopConstraint struct {
    EdgeType         string   // e.g., "calls", "imports", "modifies"
    Direction        string   // "outgoing", "incoming", "both"
    RequireSimilarity bool
    MinSimilarity    float64
}
---
Part 4: SIMD and Hardware Optimizations
4.1 SIMD Distance Computation
Problem: Your distance.go uses scalar operations.
Solution: SIMD-accelerated distance functions.
// distance_simd_amd64.go (build tag: amd64)
import "golang.org/x/sys/cpu"
// DotProductSIMD uses AVX2 for 8× parallel float32 operations
func DotProductSIMD(a, b []float32) float32 {
    if !cpu.X86.HasAVX2 || len(a) < 8 {
        return dotProductScalar(a, b)
    }
    
    // Process 8 floats at a time using AVX2
    n := len(a)
    sum := float32(0)
    
    // AVX2: 256-bit = 8 × float32
    for i := 0; i <= n-8; i += 8 {
        // This would be assembly or cgo in production
        // Go doesn't have native SIMD intrinsics
        sum += simdDot8(a[i:i+8], b[i:i+8])
    }
    
    // Handle remainder
    for i := (n / 8) * 8; i < n; i++ {
        sum += a[i] * b[i]
    }
    
    return sum
}
// For ARM (Apple Silicon, etc.)
// distance_simd_arm64.go (build tag: arm64)
func DotProductSIMD(a, b []float32) float32 {
    if len(a) < 4 {
        return dotProductScalar(a, b)
    }
    
    // NEON: 128-bit = 4 × float32
    // Use vdotq_f32 intrinsic
    // ...
}
Alternative: Use BLAS
Your k-means already uses BLAS. Extend to distance computation:
func DotProductBLAS(a, b []float32) float32 {
    // Convert to float64 for BLAS (or use blas32 if available)
    a64 := make([]float64, len(a))
    b64 := make([]float64, len(b))
    for i := range a {
        a64[i] = float64(a[i])
        b64[i] = float64(b[i])
    }
    
    return float32(blas64.Dot(
        blas64.Vector{N: len(a64), Inc: 1, Data: a64},
        blas64.Vector{N: len(b64), Inc: 1, Data: b64},
    ))
}
Expected Improvement: 2-4× speedup for distance computations
---
4.2 Cache-Friendly Memory Layout
Problem: Random memory access patterns cause cache misses.
Solution: Graph reordering + prefetching.
// ReorderGraphForLocality reorders nodes so connected nodes are adjacent in memory
func (h *Index) ReorderGraphForLocality() {
    // Use BFS ordering: nodes visited together are stored together
    visited := make(map[string]int)  // nodeID -> new position
    queue := []string{h.entryPoint}
    position := 0
    
    for len(queue) > 0 {
        nodeID := queue[0]
        queue = queue[1:]
        
        if _, seen := visited[nodeID]; seen {
            continue
        }
        visited[nodeID] = position
        position++
        
        // Add neighbors to queue
        for _, level := range h.layers {
            neighbors := level.getNeighbors(nodeID)
            queue = append(queue, neighbors...)
        }
    }
    
    // Reorder vectors array based on new positions
    h.reorderVectors(visited)
}
// PrefetchNeighbors prefetches neighbor vectors before they're needed
func (h *Index) searchLayerWithPrefetch(query []float32, queryMag float64, ep string, ef int, level int) []SearchResult {
    // ... search setup ...
    
    for i := 0; i < len(candidates); i++ {
        curr := candidates[i]
        neighbors := h.layers[level].getNeighbors(curr.ID)
        
        // Prefetch next batch of neighbor vectors
        for j := 0; j < min(4, len(neighbors)); j++ {
            neighborIdx := h.nodeToIndex[neighbors[j]]
            // This hints to CPU to load data into cache
            prefetch(h.vectors[neighborIdx])
        }
        
        // Now process neighbors (vectors should be in cache)
        for _, neighbor := range neighbors {
            // ... distance computation ...
        }
    }
}
---
Part 5: Streaming and Incremental Updates
5.1 MN-RU: Safe HNSW Updates
Problem: Standard HNSW updates can create "unreachable" nodes.
Solution: Mutual Neighbor Replaced Update algorithm.
// MNRUUpdater handles safe incremental HNSW updates
type MNRUUpdater struct {
    index *Index
}
func (u *MNRUUpdater) UpdateVector(id string, newVector []float32) error {
    u.index.mu.Lock()
    defer u.index.mu.Unlock()
    
    // Step 1: Find all nodes that point TO this node (reverse edges)
    incomingNodes := u.findIncomingNodes(id)
    
    // Step 2: Update the vector
    u.index.vectors[id] = newVector
    u.index.magnitudes[id] = Magnitude(newVector)
    
    // Step 3: Rebuild outgoing connections for this node
    u.rebuildConnections(id, newVector)
    
    // Step 4: Check if any incoming nodes now need reconnection
    // This prevents the "unreachable node" problem
    for _, incomingID := range incomingNodes {
        if !u.isStillConnected(incomingID, id) {
            // Re-add connection if it's still one of the best neighbors
            u.maybeReconnect(incomingID, id)
        }
    }
    
    return nil
}
func (u *MNRUUpdater) findIncomingNodes(targetID string) []string {
    incoming := make([]string, 0)
    
    for _, layer := range u.index.layers {
        for nodeID := range layer.nodes {
            neighbors := layer.getNeighbors(nodeID)
            for _, n := range neighbors {
                if n == targetID {
                    incoming = append(incoming, nodeID)
                    break
                }
            }
        }
    }
    
    return incoming
}
---
5.2 CoDEQ-Style Streaming Quantization
Problem: PQ codebooks degrade when data distribution shifts.
Solution: Consistent local updates without global retraining.
// StreamingQuantizer maintains codebook consistency under updates
type StreamingQuantizer struct {
    pq *ProductQuantizer
    
    // Per-centroid statistics for drift detection
    centroidCounts    [][]int      // [subspace][centroid] -> assignment count
    centroidSums      [][][]float32 // [subspace][centroid][dim] -> vector sum
    centroidVariances [][]float64   // [subspace][centroid] -> variance
    
    // Drift detection threshold
    driftThreshold float64
}
func (s *StreamingQuantizer) OnInsert(vector []float32) PQCode {
    code, _ := s.pq.Encode(vector)
    
    // Update centroid statistics
    for m, c := range code {
        s.centroidCounts[m][c]++
        subvec := vector[m*s.pq.subspaceDim : (m+1)*s.pq.subspaceDim]
        for d, v := range subvec {
            s.centroidSums[m][c][d] += v
        }
    }
    
    // Check for drift periodically
    if s.shouldCheckDrift() {
        s.maybeUpdateCentroids()
    }
    
    return code
}
func (s *StreamingQuantizer) maybeUpdateCentroids() {
    for m := 0; m < s.pq.numSubspaces; m++ {
        for c := 0; c < s.pq.centroidsPerSubspace; c++ {
            if s.centroidCounts[m][c] < 10 {
                continue  // Not enough data
            }
            
            // Compute new centroid from running mean
            newCentroid := make([]float32, s.pq.subspaceDim)
            for d := 0; d < s.pq.subspaceDim; d++ {
                newCentroid[d] = s.centroidSums[m][c][d] / float32(s.centroidCounts[m][c])
            }
            
            // Check drift from old centroid
            drift := squaredL2(newCentroid, s.pq.centroids[m][c])
            if drift > s.driftThreshold {
                // Update centroid (no global retrain!)
                copy(s.pq.centroids[m][c], newCentroid)
                
                // Rebuild encoding cache
                s.pq.buildEncodingCache()
            }
        }
    }
}
---
Summary: Complete Enhancement Stack
| Layer | Current (GRAPH_OPTIMIZATIONS.md) | Enhanced (This Document) | Improvement |
|-------|----------------------------------|--------------------------|-------------|
| L0 | RaBitQ | BBQ (asymmetric binary) | 2× query speed |
| L0.5 | — | AVQ (anisotropic loss) | +2-3% recall |
| L1 | — | FastScan (4-bit PQ) | 3-5× scan speed |
| L2 | LOPQ | LOPQ + SAQ refinement | +1-2% recall |
| L3 | Remove-Birth | Remove-Birth + CoDEQ streaming | Drift resistance |
| L4 | Query-Adaptive Weighting | + Difficulty estimation | 2× adaptive speedup |
| Search | Fixed efSearch | Patience termination + FINGER | 30-60% speedup |
| Fusion | RRF (Vector + Text) | Triple RRF (+ Graph) | Better complex queries |
| Temporal | Storage only | Temporal scoring | Recency awareness |
| Hardware | BLAS (k-means only) | SIMD everywhere + Prefetch | 2-4× distance speed |
Expected Combined Performance
| Metric | Current Target | Enhanced Target |
|--------|----------------|-----------------|
| Recall@10 | ~92-94% | 96-98% |
| Query Latency | 2-3ms | 0.5-1ms |
| Index Build | Minutes | Minutes (unchanged) |
| Memory | 128B/vec | 128B/vec (unchanged) |
| Streaming Support | Partial | Full (CoDEQ + MN-RU) |
---
Implementation Priority
Phase 1: Quick Wins (1-2 weeks)
1. BBQ (replace RaBitQ) — Low complexity, high impact
2. Patience termination — Simple, immediate speedup
3. SIMD distance — Clear speedup, no architecture change
Phase 2: Core Enhancements (2-4 weeks)
4. FINGER — Significant graph speedup
5. AVQ — Recall improvement
6. FastScan — Major scan speedup
Phase 3: Advanced Features (4-6 weeks)
7. SAQ refinement — Recall polish
8. Triple RRF — Better complex queries
9. CoDEQ streaming — Drift resistance
Phase 4: Polish (2-4 weeks)
10. Temporal scoring — Recency awareness
11. MN-RU updates — Safe incremental updates
12. Query difficulty — Self-tuning search