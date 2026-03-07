package fetch

import (
	"fmt"
	"sync"
	"time"
)

// QuarantineVerdict is the Guardian's assessment of quarantined content.
type QuarantineVerdict int

const (
	VerdictPending  QuarantineVerdict = iota // Awaiting inspection
	VerdictClean                             // Safe to ingest
	VerdictFlagged                           // Suspicious — present to user
	VerdictBlocked                           // Malicious — reject
)

func (v QuarantineVerdict) String() string {
	names := [...]string{"pending", "clean", "flagged", "blocked"}
	if int(v) < len(names) {
		return names[v]
	}
	return "unknown"
}

// QuarantineEntry holds fetched content awaiting Guardian inspection.
type QuarantineEntry struct {
	ID            string            `json:"id"`
	URL           string            `json:"url"`
	Domain        string            `json:"domain"`
	ContentType   string            `json:"content_type"`
	Content       []byte            `json:"-"` // Raw bytes, not serialized
	ContentHash   string            `json:"content_hash"`
	ContentLength int               `json:"content_length"`
	FetchedAt     time.Time         `json:"fetched_at"`
	SourceAgent   string            `json:"source_agent"`
	ConsentID     string            `json:"consent_id"` // Links to consent decision
	Metadata      map[string]string `json:"metadata,omitempty"`

	// Inspection results (populated by Guardian).
	Verdict    QuarantineVerdict  `json:"verdict"`
	Findings   []InspectionFinding `json:"findings,omitempty"`
	InspectedAt *time.Time         `json:"inspected_at,omitempty"`
	InspectedBy string             `json:"inspected_by,omitempty"`
}

// InspectionFinding represents a single finding from content inspection.
type InspectionFinding struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`       // "credential", "injection", "malicious_code", "supply_chain", "exfiltration", "polyglot"
	Severity   string  `json:"severity"`   // "info", "low", "medium", "high", "critical"
	Confidence float64 `json:"confidence"` // 0.0–1.0
	Title      string  `json:"title"`
	Detail     string  `json:"detail"`
	Line       int     `json:"line,omitempty"`
	Pattern    string  `json:"pattern,omitempty"`
}

// Provenance tracks the full audit trail from fetch to ingestion.
type Provenance struct {
	FetchURL       string    `json:"fetch_url"`
	FetchedAt      time.Time `json:"fetched_at"`
	ContentHash    string    `json:"content_hash"`
	ConsentID      string    `json:"consent_id"`
	InspectionID   string    `json:"inspection_id"`
	Verdict        string    `json:"verdict"`
	FindingCount   int       `json:"finding_count"`
	IngestedAt     time.Time `json:"ingested_at"`
	SourceAgent    string    `json:"source_agent"`
	ApprovedBy     string    `json:"approved_by"` // "user", "auto", "config"
}

// QuarantineBuffer holds entries awaiting inspection. Bounded to prevent
// unbounded memory growth.
type QuarantineBuffer struct {
	mu       sync.Mutex
	entries  map[string]*QuarantineEntry
	maxItems int
	maxBytes int64
	curBytes int64
}

// NewQuarantineBuffer creates a bounded quarantine buffer.
func NewQuarantineBuffer(maxItems int, maxBytes int64) *QuarantineBuffer {
	return &QuarantineBuffer{
		entries:  make(map[string]*QuarantineEntry, maxItems),
		maxItems: maxItems,
		maxBytes: maxBytes,
	}
}

// Hold places content in quarantine. Returns an error if the buffer
// is full or the content exceeds size limits.
func (q *QuarantineBuffer) Hold(entry *QuarantineEntry) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.entries) >= q.maxItems {
		return fmt.Errorf("quarantine buffer full (%d items)", q.maxItems)
	}

	entrySize := int64(len(entry.Content))
	if q.curBytes+entrySize > q.maxBytes {
		return fmt.Errorf("quarantine buffer size exceeded (%d + %d > %d bytes)",
			q.curBytes, entrySize, q.maxBytes)
	}

	entry.Verdict = VerdictPending
	q.entries[entry.ID] = entry
	q.curBytes += entrySize
	return nil
}

// Get retrieves an entry by ID.
func (q *QuarantineBuffer) Get(id string) (*QuarantineEntry, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.entries[id]
	return e, ok
}

// SetVerdict updates the verdict and findings for an entry.
func (q *QuarantineBuffer) SetVerdict(id string, verdict QuarantineVerdict, findings []InspectionFinding, inspector string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	entry, ok := q.entries[id]
	if !ok {
		return false
	}
	now := time.Now()
	entry.Verdict = verdict
	entry.Findings = findings
	entry.InspectedAt = &now
	entry.InspectedBy = inspector
	return true
}

// Release removes an entry from quarantine and returns it. The caller
// is responsible for ingestion or disposal.
func (q *QuarantineBuffer) Release(id string) (*QuarantineEntry, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	entry, ok := q.entries[id]
	if !ok {
		return nil, false
	}
	q.curBytes -= int64(len(entry.Content))
	delete(q.entries, id)
	return entry, true
}

// PendingCount returns how many entries await inspection.
func (q *QuarantineBuffer) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	count := 0
	for _, e := range q.entries {
		if e.Verdict == VerdictPending {
			count++
		}
	}
	return count
}

// Stats returns quarantine buffer statistics.
func (q *QuarantineBuffer) Stats() QuarantineStats {
	q.mu.Lock()
	defer q.mu.Unlock()

	stats := QuarantineStats{
		TotalEntries: len(q.entries),
		TotalBytes:   q.curBytes,
		MaxItems:     q.maxItems,
		MaxBytes:     q.maxBytes,
	}
	for _, e := range q.entries {
		switch e.Verdict {
		case VerdictPending:
			stats.Pending++
		case VerdictClean:
			stats.Clean++
		case VerdictFlagged:
			stats.Flagged++
		case VerdictBlocked:
			stats.Blocked++
		}
	}
	return stats
}

// QuarantineStats summarizes buffer state.
type QuarantineStats struct {
	TotalEntries int   `json:"total_entries"`
	TotalBytes   int64 `json:"total_bytes"`
	MaxItems     int   `json:"max_items"`
	MaxBytes     int64 `json:"max_bytes"`
	Pending      int   `json:"pending"`
	Clean        int   `json:"clean"`
	Flagged      int   `json:"flagged"`
	Blocked      int   `json:"blocked"`
}
