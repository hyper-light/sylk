package shared

import (
	"testing"
	"time"
)

// TestSessionConsultationCache_FreshHitOnIdenticalQuery pins the
// canonical cached-serve path: identical query, fresh entry, response
// covers the query terms — pressure math denies re-asking, lookup
// returns Hit=true with the stored response.
func TestSessionConsultationCache_FreshHitOnIdenticalQuery(t *testing.T) {
	cache := NewSessionConsultationCache()
	StoreSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"does the repo have any existing python files",
		map[string]any{"summary": "no python files in this repo, this is a pure go codebase"},
		15*time.Minute,
		0,
	)
	hit := LookupSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"does the repo have any existing python files",
		ResearchDepthStandard,
		2,
	)
	if !hit.Hit {
		t.Fatalf("expected hit on identical query, got reason=%q", hit.Reason)
	}
	resp, ok := hit.Response.(map[string]any)
	if !ok {
		t.Fatalf("response should be the stored map, got %T", hit.Response)
	}
	if resp["summary"] != "no python files in this repo, this is a pure go codebase" {
		t.Fatalf("response payload corrupted, got %+v", resp)
	}
}

// TestSessionConsultationCache_StaleEntryForcesReAsk pins the
// freshness gate: entry past its FreshnessHorizon must NOT be served
// even when the query matches and pressure denies re-asking. The
// world may have moved; force a fresh ask.
func TestSessionConsultationCache_StaleEntryForcesReAsk(t *testing.T) {
	cache := NewSessionConsultationCache()
	StoreSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"does the repo have any existing python files",
		map[string]any{"summary": "no python files"},
		1*time.Nanosecond, // expires effectively immediately
		0,
	)
	time.Sleep(2 * time.Millisecond)
	hit := LookupSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"does the repo have any existing python files",
		ResearchDepthStandard,
		2,
	)
	if hit.Hit {
		t.Fatalf("stale entry must not be served, got hit with reason=%q", hit.Reason)
	}
	if hit.Reason != "horizon expired" {
		t.Fatalf("expected reason=horizon expired, got %q", hit.Reason)
	}
}

// TestSessionConsultationCache_MissingHorizonNeverServed pins the
// agent contract: an agent that returns an answer without committing
// a freshness horizon does NOT get its answer cached for re-use.
// This is the explicit "missing contract == no cache" rule.
func TestSessionConsultationCache_MissingHorizonNeverServed(t *testing.T) {
	cache := NewSessionConsultationCache()
	StoreSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"does the repo have any existing python files",
		map[string]any{"summary": "no python files"},
		0, // no horizon committed
		0,
	)
	hit := LookupSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"does the repo have any existing python files",
		ResearchDepthStandard,
		2,
	)
	if hit.Hit {
		t.Fatalf("entry without freshness horizon must not be served, got hit reason=%q", hit.Reason)
	}
}

// TestSessionConsultationCache_DifferentQueryAdmittedReAsk pins the
// pressure-gate: a meaningfully different query for the same target
// must NOT serve the cached entry (the queries are different enough
// that re-asking is the right call). Verifies the existing
// evaluateAdmission math is the dedup signal — no separate threshold.
func TestSessionConsultationCache_DifferentQueryAdmittedReAsk(t *testing.T) {
	cache := NewSessionConsultationCache()
	StoreSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"does the repo have any existing python files",
		map[string]any{"summary": "no python files in repo"},
		15*time.Minute,
		0,
	)
	hit := LookupSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"what is the typical commit cadence in this repo",
		ResearchDepthStandard,
		2,
	)
	if hit.Hit {
		t.Fatalf("differing query must not hit cache, got reason=%q", hit.Reason)
	}
}

// TestSessionConsultationCache_SessionScopingIsolatesEntries pins
// that entries cached against one session are NOT served to another
// session — the cache is session-scoped to prevent cross-context
// pollution. The same query on a different session must miss.
func TestSessionConsultationCache_SessionScopingIsolatesEntries(t *testing.T) {
	cache := NewSessionConsultationCache()
	StoreSessionConsultationCache(
		cache,
		"sess-A",
		"librarian",
		"does the repo have python files",
		map[string]any{"summary": "session A answer"},
		15*time.Minute,
		0,
	)
	hit := LookupSessionConsultationCache(
		cache,
		"sess-B",
		"librarian",
		"does the repo have python files",
		ResearchDepthStandard,
		2,
	)
	if hit.Hit {
		t.Fatalf("cross-session lookup must miss, got reason=%q", hit.Reason)
	}
}

// TestSessionConsultationCache_TargetScopingIsolatesEntries pins that
// entries cached against one target are NOT served when a different
// target is being consulted, even with similar queries. Each target
// has its own bucket; cross-target reuse would leak agent identity.
func TestSessionConsultationCache_TargetScopingIsolatesEntries(t *testing.T) {
	cache := NewSessionConsultationCache()
	StoreSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"prior preferences for python packaging",
		map[string]any{"summary": "librarian answer"},
		15*time.Minute,
		0,
	)
	hit := LookupSessionConsultationCache(
		cache,
		"sess-1",
		"archivalist",
		"prior preferences for python packaging",
		ResearchDepthStandard,
		2,
	)
	if hit.Hit {
		t.Fatalf("cross-target lookup must miss, got reason=%q", hit.Reason)
	}
}

// TestExtractFreshnessHorizon_DurationStringsAndNumeric pins the
// contract for how agents communicate freshness horizon. Accepts Go
// duration strings ("15m"), float64 seconds, int seconds. Anything
// else returns 0 (no horizon committed → cache won't serve).
func TestExtractFreshnessHorizon_DurationStringsAndNumeric(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want time.Duration
	}{
		{"nil", nil, 0},
		{"duration string 15m",
			map[string]any{"freshness_horizon": "15m"},
			15 * time.Minute,
		},
		{"duration string 2h",
			map[string]any{"freshness_horizon": "2h"},
			2 * time.Hour,
		},
		{"float seconds",
			map[string]any{"freshness_horizon": float64(900)},
			900 * time.Second,
		},
		{"int seconds",
			map[string]any{"freshness_horizon": 60},
			60 * time.Second,
		},
		{"unparseable string returns zero",
			map[string]any{"freshness_horizon": "tomorrow"},
			0,
		},
		{"missing key returns zero",
			map[string]any{"summary": "no horizon here"},
			0,
		},
		{"non-map returns zero", "raw string response", 0},
		{"typed ConsultResponsePayload pointer",
			&ConsultResponsePayload{Summary: "x", FreshnessHorizon: 15 * time.Minute},
			15 * time.Minute,
		},
		{"typed ConsultResponsePayload value",
			ConsultResponsePayload{Summary: "x", FreshnessHorizon: 2 * time.Hour},
			2 * time.Hour,
		},
		{"nil typed pointer returns zero",
			(*ConsultResponsePayload)(nil),
			0,
		},
		{"typed payload without horizon returns zero",
			&ConsultResponsePayload{Summary: "no horizon committed"},
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractFreshnessHorizon(tc.in); got != tc.want {
				t.Fatalf("ExtractFreshnessHorizon(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestSessionConsultationCache_TypedPayloadRoundTrip pins the
// agent contract end-to-end: an agent emitting the canonical typed
// ConsultResponsePayload with FreshnessHorizon set must be cached
// and then served back when an identical query arrives.
func TestSessionConsultationCache_TypedPayloadRoundTrip(t *testing.T) {
	cache := NewSessionConsultationCache()
	payload := &ConsultResponsePayload{
		Summary:          "no python files in repo",
		Details:          "scanned the workspace, only Go sources present",
		FreshnessHorizon: 15 * time.Minute,
	}
	// Mirror what the architect's consultation_bus.go does at write
	// time: extract the horizon from the response and store it.
	StoreSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"are there any python files in this repo",
		payload,
		ExtractFreshnessHorizon(payload),
		0,
	)
	hit := LookupSessionConsultationCache(
		cache,
		"sess-1",
		"librarian",
		"are there any python files in this repo",
		ResearchDepthStandard,
		2,
	)
	if !hit.Hit {
		t.Fatalf("typed-payload round-trip should hit cache, got reason=%q", hit.Reason)
	}
	served, ok := hit.Response.(*ConsultResponsePayload)
	if !ok {
		t.Fatalf("served response should be *ConsultResponsePayload, got %T", hit.Response)
	}
	if served.Summary != "no python files in repo" {
		t.Fatalf("payload corrupted on round-trip, got %+v", served)
	}
}

// TestSessionConsultationCache_LRUBoundsPerTargetEntries pins the
// per-(session,target) LRU cap. Storing more entries than the bound
// drops the oldest, keeping memory growth bounded across long
// sessions with many distinct questions to a single target.
func TestSessionConsultationCache_LRUBoundsPerTargetEntries(t *testing.T) {
	cache := NewSessionConsultationCache()
	for i := 0; i < sessionCacheLRUSize+10; i++ {
		StoreSessionConsultationCache(
			cache,
			"sess-1",
			"librarian",
			"distinct query "+timeToken(i),
			map[string]any{"summary": "answer " + timeToken(i)},
			15*time.Minute,
			0,
		)
	}
	bucket := cache.bucket("sess-1", false)
	if bucket == nil {
		t.Fatal("bucket should exist")
	}
	bucket.mu.Lock()
	got := len(bucket.perTarget["librarian"])
	bucket.mu.Unlock()
	if got > sessionCacheLRUSize {
		t.Fatalf("per-target entries %d exceeded LRU cap %d", got, sessionCacheLRUSize)
	}
}

// TestInvalidateSessionConsultationCacheForSession_DropsAllSessionEntries
// pins explicit invalidation: clearing a session removes every entry
// for that session across all targets. Used at session-end.
func TestInvalidateSessionConsultationCacheForSession_DropsAllSessionEntries(t *testing.T) {
	cache := NewSessionConsultationCache()
	StoreSessionConsultationCache(cache, "sess-1", "librarian", "q1", map[string]any{"summary": "a1"}, 15*time.Minute, 0)
	StoreSessionConsultationCache(cache, "sess-1", "archivalist", "q2", map[string]any{"summary": "a2"}, 15*time.Minute, 0)
	StoreSessionConsultationCache(cache, "sess-2", "librarian", "q3", map[string]any{"summary": "a3"}, 15*time.Minute, 0)

	InvalidateSessionConsultationCacheForSession(cache, "sess-1")

	if hit := LookupSessionConsultationCache(cache, "sess-1", "librarian", "q1", ResearchDepthStandard, 2); hit.Hit {
		t.Fatalf("sess-1 librarian entry should be invalidated, got hit reason=%q", hit.Reason)
	}
	if hit := LookupSessionConsultationCache(cache, "sess-1", "archivalist", "q2", ResearchDepthStandard, 2); hit.Hit {
		t.Fatalf("sess-1 archivalist entry should be invalidated, got hit reason=%q", hit.Reason)
	}
	if hit := LookupSessionConsultationCache(cache, "sess-2", "librarian", "q3", ResearchDepthStandard, 2); !hit.Hit {
		t.Fatalf("sess-2 entries must NOT be touched by sess-1 invalidation, got reason=%q", hit.Reason)
	}
}

func timeToken(i int) string {
	return time.Now().Add(time.Duration(i) * time.Microsecond).Format("150405.000000")
}
