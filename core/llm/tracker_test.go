package llm

import (
	"sync"
	"testing"
	"time"
)

func TestNewUsageTracker(t *testing.T) {
	tracker := NewUsageTracker()

	if tracker == nil {
		t.Fatal("NewUsageTracker returned nil")
	}
	if tracker.totalTokens != 0 {
		t.Errorf("expected totalTokens = 0, got %d", tracker.totalTokens)
	}
	if len(tracker.records) != 0 {
		t.Errorf("expected empty records, got %d", len(tracker.records))
	}
}

func TestTrackerRecord(t *testing.T) {
	tracker := NewUsageTracker()

	record := UsageRecord{
		SessionID:    "session1",
		TaskID:       "task1",
		AgentType:    "engineer",
		Provider:     "openai",
		InputTokens:  100,
		OutputTokens: 200,
		TotalTokens:  300,
		Cost:         0.01,
	}

	tracker.Record(record)

	if tracker.TotalTokens() != 300 {
		t.Errorf("expected totalTokens = 300, got %d", tracker.TotalTokens())
	}
	if len(tracker.Records()) != 1 {
		t.Errorf("expected 1 record, got %d", len(tracker.Records()))
	}
}

func TestTrackerTotalTokens(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{TotalTokens: 100})
	tracker.Record(UsageRecord{TotalTokens: 200})
	tracker.Record(UsageRecord{TotalTokens: 300})

	if tracker.TotalTokens() != 600 {
		t.Errorf("expected totalTokens = 600, got %d", tracker.TotalTokens())
	}
}

func TestTrackerTokensBySession(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{SessionID: "session1", TotalTokens: 100})
	tracker.Record(UsageRecord{SessionID: "session1", TotalTokens: 150})
	tracker.Record(UsageRecord{SessionID: "session2", TotalTokens: 200})

	if tokens := tracker.TokensBySession("session1"); tokens != 250 {
		t.Errorf("expected session1 tokens = 250, got %d", tokens)
	}
	if tokens := tracker.TokensBySession("session2"); tokens != 200 {
		t.Errorf("expected session2 tokens = 200, got %d", tokens)
	}
	if tokens := tracker.TokensBySession("nonexistent"); tokens != 0 {
		t.Errorf("expected nonexistent session tokens = 0, got %d", tokens)
	}
}

func TestTrackerTokensByTask(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{TaskID: "task1", TotalTokens: 500})
	tracker.Record(UsageRecord{TaskID: "task1", TotalTokens: 300})
	tracker.Record(UsageRecord{TaskID: "task2", TotalTokens: 100})

	if tokens := tracker.TokensByTask("task1"); tokens != 800 {
		t.Errorf("expected task1 tokens = 800, got %d", tokens)
	}
	if tokens := tracker.TokensByTask("task2"); tokens != 100 {
		t.Errorf("expected task2 tokens = 100, got %d", tokens)
	}
	if tokens := tracker.TokensByTask("nonexistent"); tokens != 0 {
		t.Errorf("expected nonexistent task tokens = 0, got %d", tokens)
	}
}

func TestTrackerTokensByProvider(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{Provider: "openai", TotalTokens: 1000})
	tracker.Record(UsageRecord{Provider: "openai", TotalTokens: 500})
	tracker.Record(UsageRecord{Provider: "anthropic", TotalTokens: 750})

	if tokens := tracker.TokensByProvider("openai"); tokens != 1500 {
		t.Errorf("expected openai tokens = 1500, got %d", tokens)
	}
	if tokens := tracker.TokensByProvider("anthropic"); tokens != 750 {
		t.Errorf("expected anthropic tokens = 750, got %d", tokens)
	}
	if tokens := tracker.TokensByProvider("nonexistent"); tokens != 0 {
		t.Errorf("expected nonexistent provider tokens = 0, got %d", tokens)
	}
}

func TestTrackerTokensByAgent(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{AgentType: "engineer", TotalTokens: 400})
	tracker.Record(UsageRecord{AgentType: "engineer", TotalTokens: 200})
	tracker.Record(UsageRecord{AgentType: "architect", TotalTokens: 600})

	if tokens := tracker.TokensByAgent("engineer"); tokens != 600 {
		t.Errorf("expected engineer tokens = 600, got %d", tokens)
	}
	if tokens := tracker.TokensByAgent("architect"); tokens != 600 {
		t.Errorf("expected architect tokens = 600, got %d", tokens)
	}
	if tokens := tracker.TokensByAgent("nonexistent"); tokens != 0 {
		t.Errorf("expected nonexistent agent tokens = 0, got %d", tokens)
	}
}

func TestTrackerRecordsCopy(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{TotalTokens: 100})
	tracker.Record(UsageRecord{TotalTokens: 200})

	records := tracker.Records()

	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}

	records[0].TotalTokens = 9999
	internalRecords := tracker.Records()
	if internalRecords[0].TotalTokens == 9999 {
		t.Error("Records() should return a copy, not internal slice")
	}
}

func TestCalculateCost(t *testing.T) {
	tests := []struct {
		name         string
		inputTokens  int64
		outputTokens int64
		inputPrice   float64
		outputPrice  float64
		expectedCost float64
	}{
		{
			name:         "basic calculation",
			inputTokens:  1000,
			outputTokens: 500,
			inputPrice:   3.0,
			outputPrice:  15.0,
			expectedCost: 0.0105,
		},
		{
			name:         "zero tokens",
			inputTokens:  0,
			outputTokens: 0,
			inputPrice:   3.0,
			outputPrice:  15.0,
			expectedCost: 0,
		},
		{
			name:         "million tokens",
			inputTokens:  1_000_000,
			outputTokens: 1_000_000,
			inputPrice:   3.0,
			outputPrice:  15.0,
			expectedCost: 18.0,
		},
		{
			name:         "zero price",
			inputTokens:  1000,
			outputTokens: 1000,
			inputPrice:   0,
			outputPrice:  0,
			expectedCost: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cost := CalculateCost(tc.inputTokens, tc.outputTokens, tc.inputPrice, tc.outputPrice)
			tolerance := 0.0001
			if diff := cost - tc.expectedCost; diff > tolerance || diff < -tolerance {
				t.Errorf("expected cost = %f, got %f", tc.expectedCost, cost)
			}
		})
	}
}

func TestTrackerConcurrency(t *testing.T) {
	tracker := NewUsageTracker()

	var wg sync.WaitGroup
	numGoroutines := 100
	recordsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				tracker.Record(UsageRecord{
					SessionID:   "session",
					TaskID:      "task",
					AgentType:   "engineer",
					Provider:    "openai",
					TotalTokens: 1,
				})
			}
		}(i)
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tracker.TotalTokens()
				tracker.TokensBySession("session")
				tracker.TokensByTask("task")
				tracker.TokensByProvider("openai")
				tracker.TokensByAgent("engineer")
				tracker.Records()
			}
		}()
	}

	wg.Wait()

	expectedTokens := int64(numGoroutines * recordsPerGoroutine)
	if tracker.TotalTokens() != expectedTokens {
		t.Errorf("expected totalTokens = %d, got %d", expectedTokens, tracker.TotalTokens())
	}
}

func TestTrackerEmptyData(t *testing.T) {
	tracker := NewUsageTracker()

	if tracker.TotalTokens() != 0 {
		t.Errorf("expected 0 total tokens, got %d", tracker.TotalTokens())
	}
	if tracker.TokensBySession("any") != 0 {
		t.Errorf("expected 0 session tokens, got %d", tracker.TokensBySession("any"))
	}
	if tracker.TokensByTask("any") != 0 {
		t.Errorf("expected 0 task tokens, got %d", tracker.TokensByTask("any"))
	}
	if tracker.TokensByProvider("any") != 0 {
		t.Errorf("expected 0 provider tokens, got %d", tracker.TokensByProvider("any"))
	}
	if tracker.TokensByAgent("any") != 0 {
		t.Errorf("expected 0 agent tokens, got %d", tracker.TokensByAgent("any"))
	}
	if len(tracker.Records()) != 0 {
		t.Errorf("expected empty records, got %d", len(tracker.Records()))
	}
}

func TestUsageRecordFields(t *testing.T) {
	now := time.Now()
	latency := 100 * time.Millisecond

	record := UsageRecord{
		Timestamp:    now,
		SessionID:    "session1",
		PipelineID:   "pipeline1",
		TaskID:       "task1",
		AgentID:      "agent1",
		AgentType:    "engineer",
		Provider:     "openai",
		Model:        "gpt-4",
		InputTokens:  100,
		OutputTokens: 200,
		TotalTokens:  300,
		Cost:         0.015,
		Currency:     "USD",
		Latency:      latency,
	}

	if record.Timestamp != now {
		t.Error("timestamp mismatch")
	}
	if record.SessionID != "session1" {
		t.Error("session ID mismatch")
	}
	if record.PipelineID != "pipeline1" {
		t.Error("pipeline ID mismatch")
	}
	if record.TaskID != "task1" {
		t.Error("task ID mismatch")
	}
	if record.AgentID != "agent1" {
		t.Error("agent ID mismatch")
	}
	if record.AgentType != "engineer" {
		t.Error("agent type mismatch")
	}
	if record.Provider != "openai" {
		t.Error("provider mismatch")
	}
	if record.Model != "gpt-4" {
		t.Error("model mismatch")
	}
	if record.InputTokens != 100 {
		t.Error("input tokens mismatch")
	}
	if record.OutputTokens != 200 {
		t.Error("output tokens mismatch")
	}
	if record.TotalTokens != 300 {
		t.Error("total tokens mismatch")
	}
	if record.Cost != 0.015 {
		t.Error("cost mismatch")
	}
	if record.Currency != "USD" {
		t.Error("currency mismatch")
	}
	if record.Latency != latency {
		t.Error("latency mismatch")
	}
}

func TestTrackerAggregationAccuracy(t *testing.T) {
	tracker := NewUsageTracker()

	records := []UsageRecord{
		{SessionID: "s1", TaskID: "t1", AgentType: "a1", Provider: "p1", TotalTokens: 100},
		{SessionID: "s1", TaskID: "t1", AgentType: "a1", Provider: "p2", TotalTokens: 200},
		{SessionID: "s1", TaskID: "t2", AgentType: "a2", Provider: "p1", TotalTokens: 300},
		{SessionID: "s2", TaskID: "t3", AgentType: "a1", Provider: "p1", TotalTokens: 400},
		{SessionID: "s2", TaskID: "t3", AgentType: "a2", Provider: "p2", TotalTokens: 500},
	}

	for _, r := range records {
		tracker.Record(r)
	}

	if tracker.TotalTokens() != 1500 {
		t.Errorf("expected total = 1500, got %d", tracker.TotalTokens())
	}

	if tracker.TokensBySession("s1") != 600 {
		t.Errorf("expected s1 = 600, got %d", tracker.TokensBySession("s1"))
	}
	if tracker.TokensBySession("s2") != 900 {
		t.Errorf("expected s2 = 900, got %d", tracker.TokensBySession("s2"))
	}

	if tracker.TokensByTask("t1") != 300 {
		t.Errorf("expected t1 = 300, got %d", tracker.TokensByTask("t1"))
	}
	if tracker.TokensByTask("t2") != 300 {
		t.Errorf("expected t2 = 300, got %d", tracker.TokensByTask("t2"))
	}
	if tracker.TokensByTask("t3") != 900 {
		t.Errorf("expected t3 = 900, got %d", tracker.TokensByTask("t3"))
	}

	if tracker.TokensByAgent("a1") != 700 {
		t.Errorf("expected a1 = 700, got %d", tracker.TokensByAgent("a1"))
	}
	if tracker.TokensByAgent("a2") != 800 {
		t.Errorf("expected a2 = 800, got %d", tracker.TokensByAgent("a2"))
	}

	if tracker.TokensByProvider("p1") != 800 {
		t.Errorf("expected p1 = 800, got %d", tracker.TokensByProvider("p1"))
	}
	if tracker.TokensByProvider("p2") != 700 {
		t.Errorf("expected p2 = 700, got %d", tracker.TokensByProvider("p2"))
	}
}

func TestTrackerEmptyStrings(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{
		SessionID:   "",
		TaskID:      "",
		AgentType:   "",
		Provider:    "",
		TotalTokens: 100,
	})

	if tracker.TokensBySession("") != 100 {
		t.Errorf("expected empty session tokens = 100, got %d", tracker.TokensBySession(""))
	}
	if tracker.TokensByTask("") != 100 {
		t.Errorf("expected empty task tokens = 100, got %d", tracker.TokensByTask(""))
	}
	if tracker.TokensByAgent("") != 100 {
		t.Errorf("expected empty agent tokens = 100, got %d", tracker.TokensByAgent(""))
	}
	if tracker.TokensByProvider("") != 100 {
		t.Errorf("expected empty provider tokens = 100, got %d", tracker.TokensByProvider(""))
	}
}

func TestCalculateCostFromModel(t *testing.T) {
	tests := []struct {
		name         string
		inputTokens  int64
		outputTokens int64
		modelInfo    ModelInfo
		expectedCost float64
	}{
		{
			name:         "claude-3-opus pricing",
			inputTokens:  1000,
			outputTokens: 500,
			modelInfo:    ModelInfo{ID: "claude-3-opus", InputPricePerM: 15.0, OutputPricePerM: 75.0},
			expectedCost: 0.0525,
		},
		{
			name:         "gpt-4 pricing",
			inputTokens:  10000,
			outputTokens: 2000,
			modelInfo:    ModelInfo{ID: "gpt-4", InputPricePerM: 30.0, OutputPricePerM: 60.0},
			expectedCost: 0.42,
		},
		{
			name:         "zero tokens",
			inputTokens:  0,
			outputTokens: 0,
			modelInfo:    ModelInfo{ID: "test", InputPricePerM: 10.0, OutputPricePerM: 20.0},
			expectedCost: 0,
		},
		{
			name:         "free model",
			inputTokens:  1_000_000,
			outputTokens: 1_000_000,
			modelInfo:    ModelInfo{ID: "free", InputPricePerM: 0, OutputPricePerM: 0},
			expectedCost: 0,
		},
		{
			name:         "million tokens",
			inputTokens:  1_000_000,
			outputTokens: 1_000_000,
			modelInfo:    ModelInfo{ID: "test", InputPricePerM: 3.0, OutputPricePerM: 15.0},
			expectedCost: 18.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cost := CalculateCostFromModel(tc.inputTokens, tc.outputTokens, tc.modelInfo)
			tolerance := 0.0001
			if diff := cost - tc.expectedCost; diff > tolerance || diff < -tolerance {
				t.Errorf("CalculateCostFromModel() = %f, want %f", cost, tc.expectedCost)
			}
		})
	}
}

func TestCalculateCostFromModelMatchesCalculateCost(t *testing.T) {
	inputTokens := int64(5000)
	outputTokens := int64(2500)
	inputPrice := 15.0
	outputPrice := 75.0

	modelInfo := ModelInfo{
		ID:              "test-model",
		InputPricePerM:  inputPrice,
		OutputPricePerM: outputPrice,
	}

	costDirect := CalculateCost(inputTokens, outputTokens, inputPrice, outputPrice)
	costFromModel := CalculateCostFromModel(inputTokens, outputTokens, modelInfo)

	if costDirect != costFromModel {
		t.Errorf("CalculateCostFromModel() = %f, CalculateCost() = %f, should be equal", costFromModel, costDirect)
	}
}
