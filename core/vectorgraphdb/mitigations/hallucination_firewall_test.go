package mitigations

import (
	"context"
	"testing"
	"time"

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
