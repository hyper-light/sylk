package mitigations

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/google/uuid"
)

// HallucinationFirewall prevents storage of unverified LLM outputs.
type HallucinationFirewall struct {
	db            *vectorgraphdb.VectorGraphDB
	verifier      CodeVerifier
	minConfidence float64
	queueMu       sync.RWMutex
	pendingItems  map[string]*ReviewItem
	reviewTTL     time.Duration
	maxPending    int
}

// FirewallConfig configures the hallucination firewall.
type FirewallConfig struct {
	MinConfidence   float64
	ReviewQueueSize int
	ReviewTTL       time.Duration
}

// DefaultFirewallConfig returns the default firewall configuration.
func DefaultFirewallConfig() FirewallConfig {
	return FirewallConfig{
		MinConfidence:   0.6,
		ReviewQueueSize: 1000,
		ReviewTTL:       24 * time.Hour,
	}
}

// NewHallucinationFirewall creates a new HallucinationFirewall.
func NewHallucinationFirewall(
	db *vectorgraphdb.VectorGraphDB,
	verifier CodeVerifier,
	config FirewallConfig,
) *HallucinationFirewall {
	return &HallucinationFirewall{
		db:            db,
		verifier:      verifier,
		minConfidence: config.MinConfidence,
		pendingItems:  make(map[string]*ReviewItem),
		reviewTTL:     config.ReviewTTL,
		maxPending:    config.ReviewQueueSize,
	}
}

// Verify performs verification on a node before storage.
func (f *HallucinationFirewall) Verify(
	ctx context.Context,
	node *vectorgraphdb.GraphNode,
	source SourceType,
) (*VerificationResult, error) {
	if source == SourceTypeCode {
		return f.verifyCodeSource(ctx, node)
	}

	if source == SourceTypeLLM {
		return f.verifyLLMSource(ctx, node)
	}

	return f.verifyOtherSource(ctx, node, source)
}

func (f *HallucinationFirewall) verifyCodeSource(
	ctx context.Context,
	node *vectorgraphdb.GraphNode,
) (*VerificationResult, error) {
	result, err := VerifyNode(ctx, node, f.verifier, f.db)
	if err != nil {
		return nil, err
	}
	result.Confidence = adjustConfidenceForCode(result.Confidence)
	result.Verified = result.Confidence >= f.minConfidence
	return result, nil
}

func adjustConfidenceForCode(base float64) float64 {
	boosted := base * 1.2
	if boosted > 1.0 {
		return 1.0
	}
	return boosted
}

func (f *HallucinationFirewall) verifyLLMSource(
	ctx context.Context,
	node *vectorgraphdb.GraphNode,
) (*VerificationResult, error) {
	result, err := VerifyNode(ctx, node, f.verifier, f.db)
	if err != nil {
		return nil, err
	}
	result.Confidence = adjustConfidenceForLLM(result.Confidence)
	result.Verified = result.Confidence >= f.minConfidence
	if !result.Verified && result.Confidence >= 0.4 {
		result.ShouldQueue = true
		result.QueueReason = "LLM content requires human review"
	}
	return result, nil
}

func adjustConfidenceForLLM(base float64) float64 {
	return base * 0.7
}

func (f *HallucinationFirewall) verifyOtherSource(
	ctx context.Context,
	node *vectorgraphdb.GraphNode,
	source SourceType,
) (*VerificationResult, error) {
	result, err := VerifyNode(ctx, node, f.verifier, f.db)
	if err != nil {
		return nil, err
	}
	result.Confidence = result.Confidence * source.BaseTrust()
	result.Verified = result.Confidence >= f.minConfidence
	return result, nil
}

// Store attempts to store a node after verification.
func (f *HallucinationFirewall) Store(
	ctx context.Context,
	node *vectorgraphdb.GraphNode,
	verification *VerificationResult,
) error {
	if !verification.Verified {
		if verification.ShouldQueue {
			return f.queueForReview(node, verification.QueueReason, verification.Confidence)
		}
		return fmt.Errorf("node rejected: confidence %.2f below threshold %.2f",
			verification.Confidence, f.minConfidence)
	}

	return f.storeVerified(ctx, node, verification)
}

func (f *HallucinationFirewall) storeVerified(
	_ context.Context,
	node *vectorgraphdb.GraphNode,
	verification *VerificationResult,
) error {
	node.Metadata["verified"] = true
	node.Metadata["confidence"] = verification.Confidence
	node.Metadata["verified_at"] = time.Now().Format(time.RFC3339)
	return nil
}

// queueForReview adds a node to the review queue.
func (f *HallucinationFirewall) queueForReview(
	node *vectorgraphdb.GraphNode,
	reason string,
	confidence float64,
) error {
	item := &ReviewItem{
		ID:         uuid.New().String(),
		Node:       node,
		Reason:     reason,
		Confidence: confidence,
		QueuedAt:   time.Now(),
		ExpiresAt:  time.Now().Add(f.reviewTTL),
	}

	f.queueMu.Lock()
	defer f.queueMu.Unlock()

	if len(f.pendingItems) >= f.maxPending {
		return fmt.Errorf("review queue full")
	}

	f.pendingItems[item.ID] = item
	return nil
}

// GetReviewQueue returns pending review items.
func (f *HallucinationFirewall) GetReviewQueue(limit int) []*ReviewItem {
	f.queueMu.RLock()
	defer f.queueMu.RUnlock()

	items := make([]*ReviewItem, 0, limit)
	now := time.Now()

	for _, item := range f.pendingItems {
		if f.shouldIncludeInQueue(item, now) {
			items = append(items, item)
			if len(items) >= limit {
				break
			}
		}
	}
	return items
}

func (f *HallucinationFirewall) shouldIncludeInQueue(item *ReviewItem, now time.Time) bool {
	return !item.ExpiresAt.Before(now) && item.ReviewedAt == nil
}

// ApproveReview approves a queued review item.
func (f *HallucinationFirewall) ApproveReview(id, reviewer string) error {
	f.queueMu.Lock()
	defer f.queueMu.Unlock()

	item, ok := f.pendingItems[id]
	if !ok {
		return fmt.Errorf("review item not found: %s", id)
	}

	now := time.Now()
	item.ReviewedAt = &now
	item.ReviewedBy = reviewer
	approved := true
	item.Approved = &approved

	return nil
}

// RejectReview rejects a queued review item.
func (f *HallucinationFirewall) RejectReview(id, reason string) error {
	f.queueMu.Lock()
	defer f.queueMu.Unlock()

	item, ok := f.pendingItems[id]
	if !ok {
		return fmt.Errorf("review item not found: %s", id)
	}

	now := time.Now()
	item.ReviewedAt = &now
	item.Reason = reason
	approved := false
	item.Approved = &approved

	return nil
}

// CleanExpired removes expired review items.
func (f *HallucinationFirewall) CleanExpired() int {
	f.queueMu.Lock()
	defer f.queueMu.Unlock()

	now := time.Now()
	removed := 0

	for id, item := range f.pendingItems {
		if item.ExpiresAt.Before(now) {
			delete(f.pendingItems, id)
			removed++
		}
	}
	return removed
}

// GetMetrics returns firewall metrics.
func (f *HallucinationFirewall) GetMetrics() FirewallMetrics {
	f.queueMu.RLock()
	defer f.queueMu.RUnlock()

	metrics := FirewallMetrics{
		QueueDepth: len(f.pendingItems),
	}

	for _, item := range f.pendingItems {
		categorizeItem(&metrics, item)
	}

	return metrics
}

func categorizeItem(metrics *FirewallMetrics, item *ReviewItem) {
	switch {
	case item.ReviewedAt == nil:
		metrics.PendingReviews++
	case item.Approved != nil && *item.Approved:
		metrics.ApprovedCount++
	default:
		metrics.RejectedCount++
	}
}

// FirewallMetrics contains operational metrics for the firewall.
type FirewallMetrics struct {
	QueueDepth     int `json:"queue_depth"`
	PendingReviews int `json:"pending_reviews"`
	ApprovedCount  int `json:"approved_count"`
	RejectedCount  int `json:"rejected_count"`
}

func (f *HallucinationFirewall) Close() {
	f.queueMu.Lock()
	f.pendingItems = make(map[string]*ReviewItem)
	f.queueMu.Unlock()
}
