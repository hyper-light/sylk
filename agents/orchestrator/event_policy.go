package orchestrator

// eventDisposition tells the LLM loop whether to process or drop an event.
type eventDisposition int

const (
	dispDrop    eventDisposition = iota // Handled deterministically — skip LLM.
	dispProcess                         // Requires LLM judgment.
)

// classifyEvent determines whether a bus event needs LLM analysis.
//
// Classification rules (CC = 3):
//
//	Critical severity  → always process (immediate analysis required)
//	agents.registry    → drop (handled by knownAgents + healthMonitor)
//	everything else    → process (tasks, pipelines, workflows, warnings)
func classifyEvent(evt *busEvent) eventDisposition {
	if evt.Severity == severityCritical {
		return dispProcess
	}
	if evt.Topic == "agents.registry" {
		return dispDrop
	}
	return dispProcess
}

// filterBatch removes events classified as dispDrop.
// Returns nil when all events are filtered out.
func filterBatch(batch []*busEvent) []*busEvent {
	n := 0
	for _, evt := range batch {
		if classifyEvent(evt) == dispProcess {
			batch[n] = evt
			n++
		}
	}
	// Clear trailing references to allow GC.
	for i := n; i < len(batch); i++ {
		batch[i] = nil
	}
	if n == 0 {
		return nil
	}
	return batch[:n]
}

// deduplicateBatch collapses events with identical topic+subject, keeping the
// latest by timestamp. Preserves relative order of surviving events.
func deduplicateBatch(batch []*busEvent) []*busEvent {
	if len(batch) <= 1 {
		return batch
	}

	type dedupKey struct {
		topic   string
		summary string
	}

	// Map from key → index of the latest event in the output slice.
	seen := make(map[dedupKey]int, len(batch))
	n := 0

	for _, evt := range batch {
		key := dedupKey{topic: evt.Topic, summary: evt.Summary}
		if idx, exists := seen[key]; exists {
			// Replace earlier duplicate with latest timestamp.
			if evt.Timestamp.After(batch[idx].Timestamp) {
				batch[idx] = evt
			}
			continue
		}
		seen[key] = n
		batch[n] = evt
		n++
	}

	// Clear trailing references.
	for i := n; i < len(batch); i++ {
		batch[i] = nil
	}
	if n == 0 {
		return nil
	}
	return batch[:n]
}
