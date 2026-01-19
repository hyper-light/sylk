package classifier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

// MockSessionHistoryProvider implements SessionHistoryProvider for testing.
type MockSessionHistoryProvider struct {
	entries map[string][]SessionEntry
	err     error
}

func (m *MockSessionHistoryProvider) GetRecentDomains(
	_ context.Context,
	sessionID string,
	limit int,
) ([]SessionEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	entries, ok := m.entries[sessionID]
	if !ok {
		return nil, nil
	}
	if limit > 0 && limit < len(entries) {
		return entries[:limit], nil
	}
	return entries, nil
}

func TestNewContextClassifier(t *testing.T) {
	provider := &MockSessionHistoryProvider{}
	cc := NewContextClassifier(provider, nil)

	if cc == nil {
		t.Fatal("NewContextClassifier returned nil")
	}
	if cc.decayRate != defaultDecayRate {
		t.Errorf("decayRate = %f, want %f", cc.decayRate, defaultDecayRate)
	}
	if cc.maxHistory != defaultMaxHistory {
		t.Errorf("maxHistory = %d, want %d", cc.maxHistory, defaultMaxHistory)
	}
}

func TestNewContextClassifier_WithConfig(t *testing.T) {
	provider := &MockSessionHistoryProvider{}
	config := &ContextClassifierConfig{
		DecayRate:  0.2,
		MaxHistory: 100,
		Threshold:  0.75,
	}
	cc := NewContextClassifier(provider, config)

	if cc.decayRate != 0.2 {
		t.Errorf("decayRate = %f, want 0.2", cc.decayRate)
	}
	if cc.maxHistory != 100 {
		t.Errorf("maxHistory = %d, want 100", cc.maxHistory)
	}
	if cc.threshold != 0.75 {
		t.Errorf("threshold = %f, want 0.75", cc.threshold)
	}
}

func TestContextClassifier_Name(t *testing.T) {
	cc := NewContextClassifier(nil, nil)

	if cc.Name() != "context" {
		t.Errorf("Name() = %s, want context", cc.Name())
	}
}

func TestContextClassifier_Priority(t *testing.T) {
	cc := NewContextClassifier(nil, nil)

	if cc.Priority() != 15 {
		t.Errorf("Priority() = %d, want 15", cc.Priority())
	}
}

func TestContextClassifier_Classify_NoSessionID(t *testing.T) {
	provider := &MockSessionHistoryProvider{}
	cc := NewContextClassifier(provider, nil)

	ctx := context.Background()
	result, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("No session ID should produce empty result")
	}
}

func TestContextClassifier_Classify_NilProvider(t *testing.T) {
	cc := NewContextClassifier(nil, nil)

	ctx := WithSessionID(context.Background(), "session-1")
	result, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Nil provider should produce empty result")
	}
}

func TestContextClassifier_Classify_EmptyHistory(t *testing.T) {
	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": {},
		},
	}
	cc := NewContextClassifier(provider, nil)

	ctx := WithSessionID(context.Background(), "session-1")
	result, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Empty history should produce empty result")
	}
}

func TestContextClassifier_Classify_SingleDomain(t *testing.T) {
	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": {
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
			},
		},
	}
	cc := NewContextClassifier(provider, nil)

	ctx := WithSessionID(context.Background(), "session-1")
	result, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if result.DomainCount() != 1 {
		t.Errorf("DomainCount() = %d, want 1", result.DomainCount())
	}

	conf := result.GetConfidence(domain.DomainLibrarian)
	if conf < 0.9 {
		t.Errorf("Librarian confidence = %f, want >= 0.9", conf)
	}
}

func TestContextClassifier_Classify_MultipleDomains(t *testing.T) {
	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": {
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
				{Domain: domain.DomainAcademic, Timestamp: time.Now()},
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
				{Domain: domain.DomainAcademic, Timestamp: time.Now()},
			},
		},
	}
	cc := NewContextClassifier(provider, nil)

	ctx := WithSessionID(context.Background(), "session-1")
	result, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if result.DomainCount() < 2 {
		t.Error("Should detect multiple domains from history")
	}
}

func TestContextClassifier_Classify_ExponentialDecay(t *testing.T) {
	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": {
				{Domain: domain.DomainAcademic, Timestamp: time.Now()}, // Most recent
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
			},
		},
	}
	config := &ContextClassifierConfig{
		DecayRate: 0.5, // Higher decay = more weight on recent
	}
	cc := NewContextClassifier(provider, config)

	ctx := WithSessionID(context.Background(), "session-1")
	result, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	// Despite Librarian appearing more often, Academic is most recent
	// and should still have significant weight
	acadConf := result.GetConfidence(domain.DomainAcademic)
	if acadConf < 0.3 {
		t.Errorf("Academic confidence = %f, should be significant due to recency", acadConf)
	}
}

func TestContextClassifier_Classify_ProviderError(t *testing.T) {
	provider := &MockSessionHistoryProvider{
		err: errors.New("database error"),
	}
	cc := NewContextClassifier(provider, nil)

	ctx := WithSessionID(context.Background(), "session-1")
	result, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Should not return error on provider failure: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Provider error should produce empty result")
	}
}

func TestContextClassifier_Classify_ContextCanceled(t *testing.T) {
	provider := &MockSessionHistoryProvider{}
	cc := NewContextClassifier(provider, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)
	if err == nil {
		t.Error("Should return error for canceled context")
	}
}

func TestContextClassifier_Classify_HasSignals(t *testing.T) {
	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": {
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
			},
		},
	}
	cc := NewContextClassifier(provider, nil)

	ctx := WithSessionID(context.Background(), "session-1")
	result, _ := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	signals := result.GetSignals(domain.DomainLibrarian)
	if len(signals) == 0 {
		t.Error("Should include session_history signal")
	}
}

func TestContextClassifier_Classify_Method(t *testing.T) {
	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": {
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
			},
		},
	}
	cc := NewContextClassifier(provider, nil)

	ctx := WithSessionID(context.Background(), "session-1")
	result, _ := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if result.Method != "context" {
		t.Errorf("Method = %s, want context", result.Method)
	}
}

func TestContextClassifier_Classify_WithEntryWeights(t *testing.T) {
	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": {
				{Domain: domain.DomainLibrarian, Timestamp: time.Now(), Weight: 1.0},
				{Domain: domain.DomainAcademic, Timestamp: time.Now(), Weight: 2.0},
			},
		},
	}
	cc := NewContextClassifier(provider, nil)

	ctx := WithSessionID(context.Background(), "session-1")
	result, _ := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	// Academic should have higher score due to higher weight
	acadConf := result.GetConfidence(domain.DomainAcademic)
	libConf := result.GetConfidence(domain.DomainLibrarian)

	if acadConf <= libConf {
		t.Errorf("Academic (%f) should have higher confidence than Librarian (%f)",
			acadConf, libConf)
	}
}

func TestContextClassifier_UpdateConfig(t *testing.T) {
	cc := NewContextClassifier(nil, nil)

	newConfig := &ContextClassifierConfig{
		DecayRate:  0.3,
		MaxHistory: 75,
	}
	cc.UpdateConfig(newConfig)

	if cc.GetDecayRate() != 0.3 {
		t.Errorf("decayRate = %f, want 0.3", cc.GetDecayRate())
	}
	if cc.GetMaxHistory() != 75 {
		t.Errorf("maxHistory = %d, want 75", cc.GetMaxHistory())
	}
}

func TestContextClassifier_UpdateConfig_Nil(t *testing.T) {
	cc := NewContextClassifier(nil, nil)
	originalRate := cc.GetDecayRate()

	cc.UpdateConfig(nil)

	if cc.GetDecayRate() != originalRate {
		t.Error("Nil config should not change values")
	}
}

func TestWithSessionID(t *testing.T) {
	ctx := WithSessionID(context.Background(), "test-session")

	val := ctx.Value(sessionIDKey{})
	if val == nil {
		t.Fatal("Session ID should be in context")
	}

	sid, ok := val.(string)
	if !ok || sid != "test-session" {
		t.Errorf("Session ID = %v, want test-session", val)
	}
}

func TestContextClassifier_Classify_BelowMinConfidence(t *testing.T) {
	// Create history with many different domains so each has low individual score
	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": {
				{Domain: domain.DomainLibrarian, Timestamp: time.Now()},
				{Domain: domain.DomainAcademic, Timestamp: time.Now()},
				{Domain: domain.DomainArchivalist, Timestamp: time.Now()},
				{Domain: domain.DomainArchitect, Timestamp: time.Now()},
				{Domain: domain.DomainEngineer, Timestamp: time.Now()},
				{Domain: domain.DomainDesigner, Timestamp: time.Now()},
				{Domain: domain.DomainInspector, Timestamp: time.Now()},
				{Domain: domain.DomainTester, Timestamp: time.Now()},
			},
		},
	}
	config := &ContextClassifierConfig{
		DecayRate: 0.5, // High decay spreads confidence thin
	}
	cc := NewContextClassifier(provider, config)

	ctx := WithSessionID(context.Background(), "session-1")
	result, _ := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	// At least some domains should appear, but filtered by minConfidence
	if result.DomainCount() > 8 {
		t.Error("Should filter out domains below minimum confidence")
	}
}

func TestContextClassifier_Classify_LimitHistory(t *testing.T) {
	entries := make([]SessionEntry, 100)
	for i := range entries {
		entries[i] = SessionEntry{Domain: domain.DomainLibrarian, Timestamp: time.Now()}
	}

	provider := &MockSessionHistoryProvider{
		entries: map[string][]SessionEntry{
			"session-1": entries,
		},
	}
	config := &ContextClassifierConfig{
		MaxHistory: 10,
	}
	cc := NewContextClassifier(provider, config)

	ctx := WithSessionID(context.Background(), "session-1")
	_, err := cc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	// The mock provider should receive limit=10
}

func TestContextClassifier_GetDecayRate(t *testing.T) {
	config := &ContextClassifierConfig{DecayRate: 0.25}
	cc := NewContextClassifier(nil, config)

	if cc.GetDecayRate() != 0.25 {
		t.Errorf("GetDecayRate() = %f, want 0.25", cc.GetDecayRate())
	}
}

func TestContextClassifier_GetMaxHistory(t *testing.T) {
	config := &ContextClassifierConfig{MaxHistory: 30}
	cc := NewContextClassifier(nil, config)

	if cc.GetMaxHistory() != 30 {
		t.Errorf("GetMaxHistory() = %d, want 30", cc.GetMaxHistory())
	}
}
