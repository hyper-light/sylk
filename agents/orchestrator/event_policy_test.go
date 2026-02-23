package orchestrator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyEvent_CriticalAlwaysProcessed(t *testing.T) {
	evt := &busEvent{Topic: "agents.registry", Severity: severityCritical}
	assert.Equal(t, dispProcess, classifyEvent(evt))
}

func TestClassifyEvent_RegistryInfoDropped(t *testing.T) {
	evt := &busEvent{Topic: "agents.registry", Severity: severityInfo}
	assert.Equal(t, dispDrop, classifyEvent(evt))
}

func TestClassifyEvent_RegistryWarningDropped(t *testing.T) {
	evt := &busEvent{Topic: "agents.registry", Severity: severityWarning}
	assert.Equal(t, dispDrop, classifyEvent(evt))
}

func TestClassifyEvent_TaskDispatchProcessed(t *testing.T) {
	evt := &busEvent{Topic: "tasks.dispatch", Severity: severityInfo}
	assert.Equal(t, dispProcess, classifyEvent(evt))
}

func TestClassifyEvent_WorkflowWarningProcessed(t *testing.T) {
	evt := &busEvent{Topic: "workflows.status", Severity: severityWarning}
	assert.Equal(t, dispProcess, classifyEvent(evt))
}

func TestClassifyEvent_PipelineInfoProcessed(t *testing.T) {
	evt := &busEvent{Topic: "pipeline.update", Severity: severityInfo}
	assert.Equal(t, dispProcess, classifyEvent(evt))
}

func TestFilterBatch_AllDropped(t *testing.T) {
	batch := []*busEvent{
		{Topic: "agents.registry", Severity: severityInfo, Summary: "a"},
		{Topic: "agents.registry", Severity: severityInfo, Summary: "b"},
	}
	result := filterBatch(batch)
	assert.Nil(t, result)
}

func TestFilterBatch_MixedBatch(t *testing.T) {
	batch := []*busEvent{
		{Topic: "agents.registry", Severity: severityInfo, Summary: "reg"},
		{Topic: "tasks.failed", Severity: severityCritical, Summary: "fail"},
		{Topic: "agents.registry", Severity: severityInfo, Summary: "reg2"},
		{Topic: "tasks.dispatch", Severity: severityInfo, Summary: "dispatch"},
	}
	result := filterBatch(batch)
	require.Len(t, result, 2)
	assert.Equal(t, "fail", result[0].Summary)
	assert.Equal(t, "dispatch", result[1].Summary)
}

func TestFilterBatch_Empty(t *testing.T) {
	result := filterBatch(nil)
	assert.Nil(t, result)
}

func TestDeduplicateBatch_NoDuplicates(t *testing.T) {
	now := time.Now()
	batch := []*busEvent{
		{Topic: "tasks.dispatch", Summary: "a", Timestamp: now},
		{Topic: "tasks.complete", Summary: "b", Timestamp: now},
	}
	result := deduplicateBatch(batch)
	require.Len(t, result, 2)
}

func TestDeduplicateBatch_DuplicatesKeepLatest(t *testing.T) {
	t1 := time.Now()
	t2 := t1.Add(time.Second)

	batch := []*busEvent{
		{Topic: "tasks.dispatch", Summary: "same", Timestamp: t1, Data: map[string]any{"v": "old"}},
		{Topic: "tasks.complete", Summary: "unique", Timestamp: t1},
		{Topic: "tasks.dispatch", Summary: "same", Timestamp: t2, Data: map[string]any{"v": "new"}},
	}
	result := deduplicateBatch(batch)
	require.Len(t, result, 2)

	// First entry should be the latest "same" event.
	assert.Equal(t, "same", result[0].Summary)
	assert.Equal(t, "new", result[0].Data["v"])
	assert.Equal(t, "unique", result[1].Summary)
}

func TestDeduplicateBatch_SingleEvent(t *testing.T) {
	batch := []*busEvent{
		{Topic: "x", Summary: "y", Timestamp: time.Now()},
	}
	result := deduplicateBatch(batch)
	require.Len(t, result, 1)
}

func TestDeduplicateBatch_Empty(t *testing.T) {
	result := deduplicateBatch(nil)
	assert.Nil(t, result)
}

func TestDeduplicateBatch_AllDuplicates(t *testing.T) {
	t1 := time.Now()
	t2 := t1.Add(time.Second)
	t3 := t2.Add(time.Second)

	batch := []*busEvent{
		{Topic: "t", Summary: "s", Timestamp: t1, Data: map[string]any{"v": "1"}},
		{Topic: "t", Summary: "s", Timestamp: t3, Data: map[string]any{"v": "3"}},
		{Topic: "t", Summary: "s", Timestamp: t2, Data: map[string]any{"v": "2"}},
	}
	result := deduplicateBatch(batch)
	require.Len(t, result, 1)
	assert.Equal(t, "3", result[0].Data["v"])
}
