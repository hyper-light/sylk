package mitigations

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockCodeVerifier struct {
	fileExists    bool
	funcExists    bool
	patternExists bool
	patternConf   float64
}

func (m *mockCodeVerifier) VerifyFileExists(_ string) (bool, error) {
	return m.fileExists, nil
}

func (m *mockCodeVerifier) VerifyFunctionExists(_, _ string) (bool, error) {
	return m.funcExists, nil
}

func (m *mockCodeVerifier) VerifyPatternExists(_ string) (bool, float64, error) {
	return m.patternExists, m.patternConf, nil
}

func TestHallucinationFirewall_Verify_CodeSource(t *testing.T) {
	verifier := &mockCodeVerifier{fileExists: true}
	firewall := NewHallucinationFirewall(nil, verifier, DefaultFirewallConfig())

	node := &vectorgraphdb.GraphNode{
		ID:       "test-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{"path": "/test/file.go"},
	}

	result, err := firewall.Verify(context.Background(), node, SourceTypeCode)

	require.NoError(t, err)
	assert.True(t, result.Verified)
	assert.GreaterOrEqual(t, result.Confidence, 0.6)
}

func TestHallucinationFirewall_Verify_LLMSource_LowConfidence(t *testing.T) {
	verifier := &mockCodeVerifier{fileExists: false}
	firewall := NewHallucinationFirewall(nil, verifier, DefaultFirewallConfig())

	node := &vectorgraphdb.GraphNode{
		ID:       "test-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}

	result, err := firewall.Verify(context.Background(), node, SourceTypeLLM)

	require.NoError(t, err)
	assert.False(t, result.Verified)
	assert.True(t, result.ShouldQueue || result.Confidence < 0.6)
}

func TestHallucinationFirewall_Store_Verified(t *testing.T) {
	verifier := &mockCodeVerifier{fileExists: true}
	firewall := NewHallucinationFirewall(nil, verifier, DefaultFirewallConfig())

	node := &vectorgraphdb.GraphNode{
		ID:       "test-node",
		Metadata: map[string]any{},
	}

	verification := &VerificationResult{
		Verified:   true,
		Confidence: 0.9,
	}

	err := firewall.Store(context.Background(), node, verification)

	require.NoError(t, err)
	assert.True(t, node.Metadata["verified"].(bool))
	assert.Equal(t, 0.9, node.Metadata["confidence"].(float64))
}

func TestHallucinationFirewall_Store_Rejected(t *testing.T) {
	firewall := NewHallucinationFirewall(nil, nil, DefaultFirewallConfig())

	node := &vectorgraphdb.GraphNode{ID: "test-node"}
	verification := &VerificationResult{
		Verified:    false,
		Confidence:  0.3,
		ShouldQueue: false,
	}

	err := firewall.Store(context.Background(), node, verification)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rejected")
}

func TestHallucinationFirewall_ReviewQueue(t *testing.T) {
	config := FirewallConfig{
		MinConfidence:   0.6,
		ReviewQueueSize: 10,
		ReviewTTL:       1 * time.Hour,
	}
	firewall := NewHallucinationFirewall(nil, nil, config)

	node := &vectorgraphdb.GraphNode{ID: "test-node", Metadata: map[string]any{}}
	verification := &VerificationResult{
		Verified:    false,
		Confidence:  0.5,
		ShouldQueue: true,
		QueueReason: "needs review",
	}

	err := firewall.Store(context.Background(), node, verification)
	assert.NoError(t, err)

	queue := firewall.GetReviewQueue(10)
	assert.Len(t, queue, 1)
	assert.Equal(t, "test-node", queue[0].Node.ID)
}

func TestHallucinationFirewall_ApproveReview(t *testing.T) {
	firewall := NewHallucinationFirewall(nil, nil, DefaultFirewallConfig())

	node := &vectorgraphdb.GraphNode{ID: "test-node", Metadata: map[string]any{}}
	verification := &VerificationResult{
		Verified:    false,
		Confidence:  0.5,
		ShouldQueue: true,
	}

	_ = firewall.Store(context.Background(), node, verification)

	queue := firewall.GetReviewQueue(10)
	require.Len(t, queue, 1)

	err := firewall.ApproveReview(queue[0].ID, "test-reviewer")
	assert.NoError(t, err)

	assert.NotNil(t, queue[0].ReviewedAt)
	assert.True(t, *queue[0].Approved)
}

func TestHallucinationFirewall_RejectReview(t *testing.T) {
	firewall := NewHallucinationFirewall(nil, nil, DefaultFirewallConfig())

	node := &vectorgraphdb.GraphNode{ID: "test-node", Metadata: map[string]any{}}
	verification := &VerificationResult{
		Verified:    false,
		Confidence:  0.5,
		ShouldQueue: true,
	}

	_ = firewall.Store(context.Background(), node, verification)

	queue := firewall.GetReviewQueue(10)
	require.Len(t, queue, 1)

	err := firewall.RejectReview(queue[0].ID, "invalid content")
	assert.NoError(t, err)

	assert.NotNil(t, queue[0].ReviewedAt)
	assert.False(t, *queue[0].Approved)
}

func TestHallucinationFirewall_Metrics(t *testing.T) {
	firewall := NewHallucinationFirewall(nil, nil, DefaultFirewallConfig())

	metrics := firewall.GetMetrics()

	assert.Equal(t, 0, metrics.QueueDepth)
	assert.Equal(t, 0, metrics.PendingReviews)
}

func TestHallucinationFirewall_BackgroundCleanup(t *testing.T) {
	config := FirewallConfig{
		MinConfidence:   0.6,
		ReviewQueueSize: 100,
		ReviewTTL:       50 * time.Millisecond, // Short TTL for testing
	}
	firewall := NewHallucinationFirewall(nil, nil, config)
	defer firewall.Close()

	// Add an item that will expire quickly
	node := &vectorgraphdb.GraphNode{ID: "cleanup-test", Metadata: map[string]any{}}
	verification := &VerificationResult{
		Verified:    false,
		Confidence:  0.5,
		ShouldQueue: true,
	}
	err := firewall.Store(context.Background(), node, verification)
	require.NoError(t, err)

	// Verify item is in queue
	queue := firewall.GetReviewQueue(10)
	assert.Len(t, queue, 1)

	// Start background cleanup with short interval
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = firewall.StartBackgroundCleanup(ctx, 25*time.Millisecond)
	require.NoError(t, err)

	// Wait for item to expire and be cleaned
	time.Sleep(150 * time.Millisecond)

	// Verify item was cleaned up
	metrics := firewall.GetMetrics()
	assert.Equal(t, 0, metrics.PendingReviews)
}

func TestHallucinationFirewall_StopBackgroundCleanup_Safe(t *testing.T) {
	firewall := NewHallucinationFirewall(nil, nil, DefaultFirewallConfig())

	// Multiple calls to StopBackgroundCleanup should be safe
	firewall.StopBackgroundCleanup()
	firewall.StopBackgroundCleanup()
	firewall.StopBackgroundCleanup()

	// Close should also be safe
	firewall.Close()
}

func TestHallucinationFirewall_Close_StopsCleanup(t *testing.T) {
	firewall := NewHallucinationFirewall(nil, nil, DefaultFirewallConfig())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start cleanup
	err := firewall.StartBackgroundCleanup(ctx, 100*time.Millisecond)
	require.NoError(t, err)

	// Close should stop cleanup without panic
	firewall.Close()

	// Additional close should be safe
	firewall.Close()
}

func TestHallucinationFirewall_WithScope(t *testing.T) {
	config := FirewallConfig{
		MinConfidence:   0.6,
		ReviewQueueSize: 100,
		ReviewTTL:       50 * time.Millisecond,
	}

	// Create a GoroutineScope for tracking
	pressureLevel := &atomic.Int32{}
	budget := concurrency.NewGoroutineBudget(pressureLevel)
	budget.RegisterAgent("test-firewall", "tester")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scope := concurrency.NewGoroutineScope(ctx, "test-firewall", budget)

	// Create firewall with scope
	firewall := NewHallucinationFirewallWithScope(nil, nil, config, scope)
	defer firewall.Close()

	// Start tracked cleanup
	err := firewall.StartBackgroundCleanup(ctx, 25*time.Millisecond)
	require.NoError(t, err)

	// Worker should be tracked
	assert.Equal(t, 1, scope.WorkerCount())

	// Cleanup properly
	err = scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)
	assert.NoError(t, err)
}
