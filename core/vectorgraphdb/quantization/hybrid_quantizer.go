package quantization

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// =============================================================================
// Hybrid Quantizer Interface
// =============================================================================
//
// HybridQuantizer provides a unified interface for the 4-layer hybrid
// quantization architecture. It combines:
//
//   Layer 1: RaBitQ  - Instant baseline (~88% recall, zero training)
//   Layer 2: LOPQ    - Local codebooks (+2-3% recall, background training)
//   Layer 3: Remove-Birth - Streaming centroid adaptation
//   Layer 4: Query-Adaptive - Per-query subspace weighting (+1% recall)

// =============================================================================
// HybridQuantizationLayer
// =============================================================================

// HybridQuantizationLayer identifies which quantization layer is active.
type HybridQuantizationLayer int

const (
	// LayerRaBitQ is the instant baseline layer (Layer 1).
	LayerRaBitQ HybridQuantizationLayer = iota

	// LayerLOPQ is the locally-optimized layer (Layer 2).
	LayerLOPQ

	// LayerQueryAdaptive is query-time weighting (Layer 4).
	// Not a separate encoding layer - enhances distance computation.
	LayerQueryAdaptive
)

// String returns a string representation.
func (l HybridQuantizationLayer) String() string {
	switch l {
	case LayerRaBitQ:
		return "RaBitQ"
	case LayerLOPQ:
		return "LOPQ"
	case LayerQueryAdaptive:
		return "QueryAdaptive"
	default:
		return fmt.Sprintf("Unknown(%d)", l)
	}
}

// =============================================================================
// HybridCode
// =============================================================================

// HybridCode represents a vector encoded by the hybrid quantizer.
// Contains codes from active layers.
type HybridCode struct {
	// ActiveLayer indicates which encoding layer was used.
	ActiveLayer HybridQuantizationLayer

	// RaBitQCode is the Layer 1 code (always present as baseline).
	RaBitQCode RaBitQCode

	// LOPQCode is the Layer 2 code (present when LOPQ is ready).
	LOPQCode *LOPQCode

	// VectorID is the original vector's identifier (for re-encoding).
	VectorID string

	// EncodedAt is when the vector was encoded.
	EncodedAt time.Time
}

// HasLOPQ returns true if LOPQ encoding is available.
func (c *HybridCode) HasLOPQ() bool {
	return c.LOPQCode != nil
}

// Clone returns a deep copy of the code.
func (c *HybridCode) Clone() *HybridCode {
	if c == nil {
		return nil
	}
	clone := &HybridCode{
		ActiveLayer: c.ActiveLayer,
		RaBitQCode:  c.RaBitQCode.Clone(),
		VectorID:    c.VectorID,
		EncodedAt:   c.EncodedAt,
	}
	if c.LOPQCode != nil {
		lopqClone := c.LOPQCode.Clone()
		clone.LOPQCode = &lopqClone
	}
	return clone
}

// String returns a string representation for debugging.
func (c *HybridCode) String() string {
	if c == nil {
		return "HybridCode(nil)"
	}
	if c.HasLOPQ() {
		return fmt.Sprintf("HybridCode{Layer:%s, LOPQ:%s}", c.ActiveLayer, c.LOPQCode)
	}
	return fmt.Sprintf("HybridCode{Layer:%s, RaBitQ:%s}", c.ActiveLayer, c.RaBitQCode)
}

// =============================================================================
// HybridQuantizerStatus
// =============================================================================

// HybridQuantizerStatus provides visibility into the quantizer's state.
type HybridQuantizerStatus struct {
	// Layer1Ready indicates RaBitQ encoder is initialized.
	Layer1Ready bool

	// Layer2Stats contains LOPQ statistics.
	Layer2Stats LOPQStats

	// Layer3Stats contains adaptation statistics.
	Layer3Stats AdaptationStats

	// Layer4Enabled indicates query-adaptive weighting is active.
	Layer4Enabled bool

	// OverallRecallEstimate is the estimated recall based on active layers.
	// ~88% for RaBitQ only, ~91% with partial LOPQ, ~93% with full LOPQ.
	OverallRecallEstimate float64

	// TrainingQueueStats contains background training status.
	TrainingQueueStats TrainingQueueStats

	// LastUpdated is when this status was computed.
	LastUpdated time.Time
}

// ActiveLayerDescription returns a human-readable description of active layers.
func (s HybridQuantizerStatus) ActiveLayerDescription() string {
	layers := []string{}
	if s.Layer1Ready {
		layers = append(layers, "RaBitQ")
	}
	if s.Layer2Stats.ReadyPartitions > 0 {
		layers = append(layers, fmt.Sprintf("LOPQ(%.0f%%)", s.Layer2Stats.ReadyRatio()*100))
	}
	if s.Layer4Enabled {
		layers = append(layers, "QueryAdaptive")
	}
	if len(layers) == 0 {
		return "None"
	}
	return fmt.Sprintf("%v", layers)
}

// String returns a string representation.
func (s HybridQuantizerStatus) String() string {
	return fmt.Sprintf("HybridStatus{Layers:%s, EstRecall:%.1f%%, Training:%d pending}",
		s.ActiveLayerDescription(), s.OverallRecallEstimate*100, s.TrainingQueueStats.PendingJobs)
}

// =============================================================================
// HybridQuantizerConfig
// =============================================================================

// HybridQuantizerConfig configures the hybrid quantizer.
type HybridQuantizerConfig struct {
	// VectorDimension is the dimension of input vectors (required).
	VectorDimension int

	// RaBitQConfig configures Layer 1 (RaBitQ).
	RaBitQConfig RaBitQConfig

	// LOPQConfig configures Layer 2 (LOPQ).
	LOPQConfig LOPQConfig

	// AdaptationConfig configures Layer 3 (Remove-Birth).
	AdaptationConfig AdaptationConfig

	// QueryAdaptiveConfig configures Layer 4 (Query-Adaptive).
	QueryAdaptiveConfig QueryAdaptiveConfig

	// TrainingQueueConfig configures background training.
	TrainingQueueConfig TrainingQueueConfig

	// PreferLOPQ uses LOPQ when available, falling back to RaBitQ.
	// Default: true.
	PreferLOPQ bool
}

// DefaultHybridQuantizerConfig returns a HybridQuantizerConfig with defaults.
// VectorDimension must still be set before use.
func DefaultHybridQuantizerConfig() HybridQuantizerConfig {
	return HybridQuantizerConfig{
		VectorDimension:     0,
		RaBitQConfig:        DefaultRaBitQConfig(),
		LOPQConfig:          DefaultLOPQConfig(),
		AdaptationConfig:    DefaultAdaptationConfig(),
		QueryAdaptiveConfig: DefaultQueryAdaptiveConfig(),
		TrainingQueueConfig: DefaultTrainingQueueConfig(),
		PreferLOPQ:          true,
	}
}

// Validate checks that the configuration is valid.
func (c HybridQuantizerConfig) Validate() error {
	if c.VectorDimension <= 0 {
		return ErrHybridInvalidDimension
	}

	c.RaBitQConfig.Dimension = c.VectorDimension
	if err := c.RaBitQConfig.Validate(); err != nil {
		return fmt.Errorf("rabitq: %w", err)
	}

	c.LOPQConfig.VectorDimension = c.VectorDimension
	if err := c.LOPQConfig.Validate(); err != nil {
		return fmt.Errorf("lopq: %w", err)
	}

	if err := c.AdaptationConfig.Validate(); err != nil {
		return fmt.Errorf("adaptation: %w", err)
	}

	if err := c.QueryAdaptiveConfig.Validate(); err != nil {
		return fmt.Errorf("query_adaptive: %w", err)
	}

	if err := c.TrainingQueueConfig.Validate(); err != nil {
		return fmt.Errorf("training_queue: %w", err)
	}

	return nil
}

// String returns a string representation.
func (c HybridQuantizerConfig) String() string {
	return fmt.Sprintf("HybridConfig{Dim:%d, PreferLOPQ:%t}", c.VectorDimension, c.PreferLOPQ)
}

// =============================================================================
// HybridQuantizer Interface
// =============================================================================

// HybridQuantizer provides unified access to the 4-layer quantization system.
// Implementations are safe for concurrent use.
type HybridQuantizer interface {
	// Encode encodes a vector using the best available layer.
	// Layer selection: LOPQ if ready for partition, else RaBitQ.
	Encode(ctx context.Context, vector []float32) (*HybridCode, error)

	// EncodeBatch encodes multiple vectors efficiently.
	// Uses parallel encoding where possible.
	EncodeBatch(ctx context.Context, vectors [][]float32) ([]*HybridCode, error)

	// Decode reconstructs an approximate vector from a HybridCode.
	// Uses the code's active layer for reconstruction.
	Decode(code *HybridCode) ([]float32, error)

	// Distance computes approximate squared L2 distance.
	// Uses query-adaptive weighting when enabled.
	Distance(query []float32, code *HybridCode) (float32, error)

	// DistanceBatch computes distances to multiple codes efficiently.
	// Pre-computes query profile once for all codes.
	DistanceBatch(query []float32, codes []*HybridCode) ([]float32, error)

	// GetStatus returns current quantizer status across all layers.
	GetStatus() HybridQuantizerStatus

	// GetRaBitQEncoder returns the Layer 1 encoder.
	GetRaBitQEncoder() RaBitQEncoder

	// GetLOPQIndex returns the Layer 2 index.
	GetLOPQIndex() LOPQIndex

	// GetAdaptationTracker returns the Layer 3 tracker.
	GetAdaptationTracker() AdaptationTracker

	// ScheduleTraining triggers background LOPQ training for a partition.
	ScheduleTraining(ctx context.Context, partition PartitionID) error

	// Start begins background training workers.
	Start(ctx context.Context) error

	// Stop gracefully stops background workers.
	Stop() error
}

// =============================================================================
// HybridQuantizer Errors
// =============================================================================

var (
	// ErrHybridInvalidDimension indicates dimension must be positive.
	ErrHybridInvalidDimension = errors.New("hybrid: dimension must be positive")

	// ErrHybridDimensionMismatch indicates vector dimension doesn't match config.
	ErrHybridDimensionMismatch = errors.New("hybrid: vector dimension mismatch")

	// ErrHybridNilCode indicates a nil code was provided.
	ErrHybridNilCode = errors.New("hybrid: nil code provided")

	// ErrHybridNotInitialized indicates quantizer hasn't been initialized.
	ErrHybridNotInitialized = errors.New("hybrid: not initialized")

	// ErrHybridAlreadyStarted indicates quantizer is already running.
	ErrHybridAlreadyStarted = errors.New("hybrid: already started")

	// ErrHybridNotRunning indicates quantizer is not running.
	ErrHybridNotRunning = errors.New("hybrid: not running")
)
