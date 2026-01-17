package llm

import "time"

// UsageBreakdown contains token and cost metrics for a category.
type UsageBreakdown struct {
	Requests     int64
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	TotalCost    float64
}

// AttributionReport provides usage attribution across multiple dimensions.
type AttributionReport struct {
	SessionID     string
	Period        TimePeriod
	TotalRequests int64
	TotalTokens   int64
	TotalCost     float64
	ByProvider    map[string]*UsageBreakdown
	ByAgent       map[string]*UsageBreakdown
	ByPipeline    map[string]*UsageBreakdown
	ByTask        map[string]*UsageBreakdown
}

// TimePeriod defines the time range for a report.
type TimePeriod struct {
	Start time.Time
	End   time.Time
}

// UsageFilter specifies criteria for filtering usage records.
type UsageFilter struct {
	SessionID  string
	PipelineID string
	TaskID     string
	AgentType  string
	Provider   string
	Period     *TimePeriod
}

// GenerateReport creates an AttributionReport from filtered usage records.
func (t *UsageTracker) GenerateReport(filter UsageFilter) *AttributionReport {
	t.mu.RLock()
	defer t.mu.RUnlock()

	report := newAttributionReport(filter)
	for i := range t.records {
		if matchesFilter(&t.records[i], &filter) {
			addRecordToReport(report, &t.records[i])
		}
	}
	return report
}

func newAttributionReport(filter UsageFilter) *AttributionReport {
	return &AttributionReport{
		SessionID:  filter.SessionID,
		Period:     periodFromFilter(&filter),
		ByProvider: make(map[string]*UsageBreakdown),
		ByAgent:    make(map[string]*UsageBreakdown),
		ByPipeline: make(map[string]*UsageBreakdown),
		ByTask:     make(map[string]*UsageBreakdown),
	}
}

func periodFromFilter(filter *UsageFilter) TimePeriod {
	if filter.Period != nil {
		return *filter.Period
	}
	return TimePeriod{}
}

func matchesFilter(record *UsageRecord, filter *UsageFilter) bool {
	return matchesStringFilters(record, filter) && matchesPeriodFilter(record.Timestamp, filter.Period)
}

func matchesStringFilters(record *UsageRecord, filter *UsageFilter) bool {
	checks := []bool{
		matchesStringFilter(record.SessionID, filter.SessionID),
		matchesStringFilter(record.PipelineID, filter.PipelineID),
		matchesStringFilter(record.TaskID, filter.TaskID),
		matchesStringFilter(record.AgentType, filter.AgentType),
		matchesStringFilter(record.Provider, filter.Provider),
	}
	return allTrue(checks)
}

func allTrue(checks []bool) bool {
	for _, check := range checks {
		if !check {
			return false
		}
	}
	return true
}

func matchesStringFilter(value, filter string) bool {
	return filter == "" || value == filter
}

func matchesPeriodFilter(timestamp time.Time, period *TimePeriod) bool {
	if period == nil {
		return true
	}
	return isAfterStart(timestamp, period.Start) && isBeforeEnd(timestamp, period.End)
}

func isAfterStart(timestamp, start time.Time) bool {
	return start.IsZero() || !timestamp.Before(start)
}

func isBeforeEnd(timestamp, end time.Time) bool {
	return end.IsZero() || !timestamp.After(end)
}

func addRecordToReport(report *AttributionReport, record *UsageRecord) {
	report.TotalRequests++
	report.TotalTokens += record.TotalTokens
	report.TotalCost += record.Cost

	addToBreakdown(report.ByProvider, record.Provider, record)
	addToBreakdown(report.ByAgent, record.AgentType, record)
	addToBreakdown(report.ByPipeline, record.PipelineID, record)
	addToBreakdown(report.ByTask, record.TaskID, record)
}

func addToBreakdown(breakdowns map[string]*UsageBreakdown, key string, record *UsageRecord) {
	breakdown := getOrCreateBreakdown(breakdowns, key)
	breakdown.Requests++
	breakdown.InputTokens += record.InputTokens
	breakdown.OutputTokens += record.OutputTokens
	breakdown.TotalTokens += record.TotalTokens
	breakdown.TotalCost += record.Cost
}

func getOrCreateBreakdown(breakdowns map[string]*UsageBreakdown, key string) *UsageBreakdown {
	if breakdown, exists := breakdowns[key]; exists {
		return breakdown
	}
	breakdowns[key] = &UsageBreakdown{}
	return breakdowns[key]
}
