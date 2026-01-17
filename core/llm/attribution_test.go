package llm

import (
	"sync"
	"testing"
	"time"
)

func makeTestRecord(sessionID, pipelineID, taskID, agentType, provider string, input, output, total int64, cost float64) UsageRecord {
	return UsageRecord{
		Timestamp:    time.Now(),
		SessionID:    sessionID,
		PipelineID:   pipelineID,
		TaskID:       taskID,
		AgentType:    agentType,
		Provider:     provider,
		InputTokens:  input,
		OutputTokens: output,
		TotalTokens:  total,
		Cost:         cost,
	}
}

func TestGenerateReportEmpty(t *testing.T) {
	tracker := NewUsageTracker()
	filter := UsageFilter{}

	report := tracker.GenerateReport(filter)

	if report == nil {
		t.Fatal("GenerateReport returned nil")
	}
	if report.TotalRequests != 0 {
		t.Errorf("expected 0 requests, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 0 {
		t.Errorf("expected 0 tokens, got %d", report.TotalTokens)
	}
	if report.TotalCost != 0 {
		t.Errorf("expected 0 cost, got %f", report.TotalCost)
	}
}

func TestGenerateReportBasic(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 200, 300, 0.01))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "architect", "anthropic", 200, 300, 500, 0.02))

	report := tracker.GenerateReport(UsageFilter{})

	if report.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 800 {
		t.Errorf("expected 800 tokens, got %d", report.TotalTokens)
	}
	tolerance := 0.0001
	if diff := report.TotalCost - 0.03; diff > tolerance || diff < -tolerance {
		t.Errorf("expected cost ~0.03, got %f", report.TotalCost)
	}
}

func TestGenerateReportFilterBySession(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s2", "p1", "t1", "engineer", "openai", 200, 200, 400, 0.02))
	tracker.Record(makeTestRecord("s1", "p2", "t2", "architect", "openai", 300, 300, 600, 0.03))

	report := tracker.GenerateReport(UsageFilter{SessionID: "s1"})

	if report.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 800 {
		t.Errorf("expected 800 tokens, got %d", report.TotalTokens)
	}
	if report.SessionID != "s1" {
		t.Errorf("expected session s1, got %s", report.SessionID)
	}
}

func TestGenerateReportFilterByPipeline(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s1", "p2", "t1", "engineer", "openai", 200, 200, 400, 0.02))

	report := tracker.GenerateReport(UsageFilter{PipelineID: "p1"})

	if report.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 200 {
		t.Errorf("expected 200 tokens, got %d", report.TotalTokens)
	}
}

func TestGenerateReportFilterByTask(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s1", "p1", "t2", "engineer", "openai", 200, 200, 400, 0.02))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "architect", "openai", 150, 150, 300, 0.015))

	report := tracker.GenerateReport(UsageFilter{TaskID: "t1"})

	if report.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 500 {
		t.Errorf("expected 500 tokens, got %d", report.TotalTokens)
	}
}

func TestGenerateReportFilterByAgentType(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "architect", "openai", 200, 200, 400, 0.02))
	tracker.Record(makeTestRecord("s1", "p1", "t2", "engineer", "openai", 150, 150, 300, 0.015))

	report := tracker.GenerateReport(UsageFilter{AgentType: "engineer"})

	if report.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 500 {
		t.Errorf("expected 500 tokens, got %d", report.TotalTokens)
	}
}

func TestGenerateReportFilterByProvider(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "anthropic", 200, 200, 400, 0.02))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 150, 150, 300, 0.015))

	report := tracker.GenerateReport(UsageFilter{Provider: "openai"})

	if report.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 500 {
		t.Errorf("expected 500 tokens, got %d", report.TotalTokens)
	}
}

func TestGenerateReportFilterByTimePeriod(t *testing.T) {
	tracker := NewUsageTracker()

	now := time.Now()
	past := now.Add(-2 * time.Hour)
	future := now.Add(2 * time.Hour)

	tracker.Record(UsageRecord{
		Timestamp:   past,
		SessionID:   "s1",
		TotalTokens: 100,
	})
	tracker.Record(UsageRecord{
		Timestamp:   now,
		SessionID:   "s1",
		TotalTokens: 200,
	})
	tracker.Record(UsageRecord{
		Timestamp:   future,
		SessionID:   "s1",
		TotalTokens: 300,
	})

	startTime := now.Add(-1 * time.Hour)
	endTime := now.Add(1 * time.Hour)

	report := tracker.GenerateReport(UsageFilter{
		Period: &TimePeriod{
			Start: startTime,
			End:   endTime,
		},
	})

	if report.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 200 {
		t.Errorf("expected 200 tokens, got %d", report.TotalTokens)
	}
}

func TestGenerateReportMultipleFilters(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "architect", "openai", 200, 200, 400, 0.02))
	tracker.Record(makeTestRecord("s2", "p1", "t1", "engineer", "openai", 300, 300, 600, 0.03))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "anthropic", 400, 400, 800, 0.04))

	report := tracker.GenerateReport(UsageFilter{
		SessionID: "s1",
		AgentType: "engineer",
		Provider:  "openai",
	})

	if report.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 200 {
		t.Errorf("expected 200 tokens, got %d", report.TotalTokens)
	}
}

func TestGenerateReportBreakdownByProvider(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 200, 300, 0.01))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 150, 250, 400, 0.015))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "anthropic", 200, 300, 500, 0.02))

	report := tracker.GenerateReport(UsageFilter{})

	if len(report.ByProvider) != 2 {
		t.Errorf("expected 2 providers, got %d", len(report.ByProvider))
	}

	openai := report.ByProvider["openai"]
	if openai == nil {
		t.Fatal("openai breakdown missing")
	}
	if openai.Requests != 2 {
		t.Errorf("expected openai requests = 2, got %d", openai.Requests)
	}
	if openai.InputTokens != 250 {
		t.Errorf("expected openai input = 250, got %d", openai.InputTokens)
	}
	if openai.OutputTokens != 450 {
		t.Errorf("expected openai output = 450, got %d", openai.OutputTokens)
	}
	if openai.TotalTokens != 700 {
		t.Errorf("expected openai total = 700, got %d", openai.TotalTokens)
	}

	anthropic := report.ByProvider["anthropic"]
	if anthropic == nil {
		t.Fatal("anthropic breakdown missing")
	}
	if anthropic.Requests != 1 {
		t.Errorf("expected anthropic requests = 1, got %d", anthropic.Requests)
	}
}

func TestGenerateReportBreakdownByAgent(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "architect", "openai", 200, 200, 400, 0.02))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 150, 150, 300, 0.015))

	report := tracker.GenerateReport(UsageFilter{})

	if len(report.ByAgent) != 2 {
		t.Errorf("expected 2 agents, got %d", len(report.ByAgent))
	}

	engineer := report.ByAgent["engineer"]
	if engineer == nil {
		t.Fatal("engineer breakdown missing")
	}
	if engineer.Requests != 2 {
		t.Errorf("expected engineer requests = 2, got %d", engineer.Requests)
	}
	if engineer.TotalTokens != 500 {
		t.Errorf("expected engineer total = 500, got %d", engineer.TotalTokens)
	}
}

func TestGenerateReportBreakdownByPipeline(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s1", "p2", "t1", "engineer", "openai", 200, 200, 400, 0.02))
	tracker.Record(makeTestRecord("s1", "p1", "t2", "engineer", "openai", 150, 150, 300, 0.015))

	report := tracker.GenerateReport(UsageFilter{})

	if len(report.ByPipeline) != 2 {
		t.Errorf("expected 2 pipelines, got %d", len(report.ByPipeline))
	}

	p1 := report.ByPipeline["p1"]
	if p1 == nil {
		t.Fatal("p1 breakdown missing")
	}
	if p1.Requests != 2 {
		t.Errorf("expected p1 requests = 2, got %d", p1.Requests)
	}
	if p1.TotalTokens != 500 {
		t.Errorf("expected p1 total = 500, got %d", p1.TotalTokens)
	}
}

func TestGenerateReportBreakdownByTask(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	tracker.Record(makeTestRecord("s1", "p1", "t2", "engineer", "openai", 200, 200, 400, 0.02))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "architect", "openai", 150, 150, 300, 0.015))

	report := tracker.GenerateReport(UsageFilter{})

	if len(report.ByTask) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(report.ByTask))
	}

	t1 := report.ByTask["t1"]
	if t1 == nil {
		t.Fatal("t1 breakdown missing")
	}
	if t1.Requests != 2 {
		t.Errorf("expected t1 requests = 2, got %d", t1.Requests)
	}
	if t1.TotalTokens != 500 {
		t.Errorf("expected t1 total = 500, got %d", t1.TotalTokens)
	}
}

func TestGenerateReportCostAccuracy(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 200, 300, 0.0123))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 150, 250, 400, 0.0456))
	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "anthropic", 200, 300, 500, 0.0789))

	report := tracker.GenerateReport(UsageFilter{})

	expectedCost := 0.0123 + 0.0456 + 0.0789
	tolerance := 0.0001
	if diff := report.TotalCost - expectedCost; diff > tolerance || diff < -tolerance {
		t.Errorf("expected cost = %f, got %f", expectedCost, report.TotalCost)
	}

	openai := report.ByProvider["openai"]
	expectedOpenaiCost := 0.0123 + 0.0456
	if diff := openai.TotalCost - expectedOpenaiCost; diff > tolerance || diff < -tolerance {
		t.Errorf("expected openai cost = %f, got %f", expectedOpenaiCost, openai.TotalCost)
	}
}

func TestTimePeriodBoundaries(t *testing.T) {
	tracker := NewUsageTracker()

	exactStart := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	exactEnd := time.Date(2024, 1, 1, 14, 0, 0, 0, time.UTC)
	middle := time.Date(2024, 1, 1, 13, 0, 0, 0, time.UTC)
	beforeStart := time.Date(2024, 1, 1, 11, 0, 0, 0, time.UTC)
	afterEnd := time.Date(2024, 1, 1, 15, 0, 0, 0, time.UTC)

	tracker.Record(UsageRecord{Timestamp: exactStart, TotalTokens: 100})
	tracker.Record(UsageRecord{Timestamp: exactEnd, TotalTokens: 200})
	tracker.Record(UsageRecord{Timestamp: middle, TotalTokens: 300})
	tracker.Record(UsageRecord{Timestamp: beforeStart, TotalTokens: 400})
	tracker.Record(UsageRecord{Timestamp: afterEnd, TotalTokens: 500})

	report := tracker.GenerateReport(UsageFilter{
		Period: &TimePeriod{Start: exactStart, End: exactEnd},
	})

	if report.TotalRequests != 3 {
		t.Errorf("expected 3 requests (inclusive boundaries), got %d", report.TotalRequests)
	}
	if report.TotalTokens != 600 {
		t.Errorf("expected 600 tokens, got %d", report.TotalTokens)
	}
}

func TestTimePeriodZeroValues(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{Timestamp: time.Now(), TotalTokens: 100})
	tracker.Record(UsageRecord{Timestamp: time.Now().Add(-24 * time.Hour), TotalTokens: 200})

	report := tracker.GenerateReport(UsageFilter{
		Period: &TimePeriod{},
	})

	if report.TotalRequests != 2 {
		t.Errorf("expected all records with zero period, got %d", report.TotalRequests)
	}
}

func TestAttributionReportConcurrency(t *testing.T) {
	tracker := NewUsageTracker()

	for i := 0; i < 100; i++ {
		tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))
	}

	var wg sync.WaitGroup
	numReaders := 50

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				report := tracker.GenerateReport(UsageFilter{})
				if report.TotalRequests != 100 {
					t.Errorf("expected 100 requests, got %d", report.TotalRequests)
				}
			}
		}()
	}

	wg.Wait()
}

func TestUsageBreakdownFields(t *testing.T) {
	breakdown := UsageBreakdown{
		Requests:     10,
		InputTokens:  1000,
		OutputTokens: 2000,
		TotalTokens:  3000,
		TotalCost:    0.05,
	}

	if breakdown.Requests != 10 {
		t.Error("requests mismatch")
	}
	if breakdown.InputTokens != 1000 {
		t.Error("input tokens mismatch")
	}
	if breakdown.OutputTokens != 2000 {
		t.Error("output tokens mismatch")
	}
	if breakdown.TotalTokens != 3000 {
		t.Error("total tokens mismatch")
	}
	if breakdown.TotalCost != 0.05 {
		t.Error("cost mismatch")
	}
}

func TestAttributionReportPeriodFromFilter(t *testing.T) {
	tracker := NewUsageTracker()

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)

	report := tracker.GenerateReport(UsageFilter{
		Period: &TimePeriod{Start: start, End: end},
	})

	if report.Period.Start != start {
		t.Errorf("expected start = %v, got %v", start, report.Period.Start)
	}
	if report.Period.End != end {
		t.Errorf("expected end = %v, got %v", end, report.Period.End)
	}
}

func TestGenerateReportEmptyFilter(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))

	report := tracker.GenerateReport(UsageFilter{})

	if report.SessionID != "" {
		t.Errorf("expected empty session ID, got %s", report.SessionID)
	}
}

func TestGenerateReportNoMatches(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(makeTestRecord("s1", "p1", "t1", "engineer", "openai", 100, 100, 200, 0.01))

	report := tracker.GenerateReport(UsageFilter{SessionID: "nonexistent"})

	if report.TotalRequests != 0 {
		t.Errorf("expected 0 requests, got %d", report.TotalRequests)
	}
	if report.TotalTokens != 0 {
		t.Errorf("expected 0 tokens, got %d", report.TotalTokens)
	}
}

func TestGenerateReportEmptyStrings(t *testing.T) {
	tracker := NewUsageTracker()

	tracker.Record(UsageRecord{
		SessionID:   "",
		PipelineID:  "",
		TaskID:      "",
		AgentType:   "",
		Provider:    "",
		TotalTokens: 100,
	})

	report := tracker.GenerateReport(UsageFilter{})

	if report.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", report.TotalRequests)
	}
	if _, exists := report.ByProvider[""]; !exists {
		t.Error("empty provider key should exist")
	}
}

func TestFilterCombinations(t *testing.T) {
	tracker := NewUsageTracker()

	now := time.Now()

	records := []UsageRecord{
		{Timestamp: now, SessionID: "s1", PipelineID: "p1", TaskID: "t1", AgentType: "engineer", Provider: "openai", TotalTokens: 100},
		{Timestamp: now, SessionID: "s1", PipelineID: "p1", TaskID: "t2", AgentType: "architect", Provider: "openai", TotalTokens: 200},
		{Timestamp: now, SessionID: "s1", PipelineID: "p2", TaskID: "t1", AgentType: "engineer", Provider: "anthropic", TotalTokens: 300},
		{Timestamp: now, SessionID: "s2", PipelineID: "p1", TaskID: "t1", AgentType: "engineer", Provider: "openai", TotalTokens: 400},
	}

	for _, r := range records {
		tracker.Record(r)
	}

	tests := []struct {
		name           string
		filter         UsageFilter
		expectedCount  int64
		expectedTokens int64
	}{
		{"no filter", UsageFilter{}, 4, 1000},
		{"session only", UsageFilter{SessionID: "s1"}, 3, 600},
		{"pipeline only", UsageFilter{PipelineID: "p1"}, 3, 700},
		{"task only", UsageFilter{TaskID: "t1"}, 3, 800},
		{"agent only", UsageFilter{AgentType: "engineer"}, 3, 800},
		{"provider only", UsageFilter{Provider: "openai"}, 3, 700},
		{"session + provider", UsageFilter{SessionID: "s1", Provider: "openai"}, 2, 300},
		{"all filters match one", UsageFilter{SessionID: "s1", PipelineID: "p1", TaskID: "t1", AgentType: "engineer", Provider: "openai"}, 1, 100},
		{"no match", UsageFilter{SessionID: "nonexistent"}, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := tracker.GenerateReport(tc.filter)
			if report.TotalRequests != tc.expectedCount {
				t.Errorf("expected %d requests, got %d", tc.expectedCount, report.TotalRequests)
			}
			if report.TotalTokens != tc.expectedTokens {
				t.Errorf("expected %d tokens, got %d", tc.expectedTokens, report.TotalTokens)
			}
		})
	}
}
