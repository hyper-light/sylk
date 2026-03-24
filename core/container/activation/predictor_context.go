package activation

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/container"
	"github.com/viterin/vek/vek32"
)

type contextArchetype struct {
	name                  string
	centroid              []float32
	norm                  float64
	preferredAgentTypes   map[string]struct{}
	headroomMultiplier    float64
	burstRatio            float64
	releaseHalfLife       time.Duration
	leaseDuration         time.Duration
	uncertaintyMultiplier float64
}

var (
	contextArchetypesOnce sync.Once
	contextArchetypes     []contextArchetype
)

func (ap *ActivationPredictor) MatchContextArchetypes(features []float32, agentType string, limit int) []container.ContextArchetypeMatch {
	if len(features) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 3
	}

	contextArchetypesOnce.Do(func() {
		contextArchetypes = defaultContextArchetypes()
	})

	queryNorm := math.Sqrt(float64(vek32.Dot(features, features)))
	if queryNorm == 0 {
		return nil
	}

	trimmedType := strings.TrimSpace(agentType)
	matches := make([]container.ContextArchetypeMatch, 0, len(contextArchetypes))
	for _, archetype := range contextArchetypes {
		score := cosineScore(features, queryNorm, archetype)
		if score <= 0 {
			continue
		}
		if trimmedType != "" {
			if _, ok := archetype.preferredAgentTypes[trimmedType]; ok {
				score *= 1.08
			} else if len(archetype.preferredAgentTypes) > 0 {
				score *= 0.96
			}
		}
		matches = append(matches, container.ContextArchetypeMatch{
			Name:                  archetype.name,
			Score:                 score,
			HeadroomMultiplier:    archetype.headroomMultiplier,
			BurstRatio:            archetype.burstRatio,
			ReleaseHalfLife:       archetype.releaseHalfLife,
			LeaseDuration:         archetype.leaseDuration,
			UncertaintyMultiplier: archetype.uncertaintyMultiplier,
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func cosineScore(features []float32, queryNorm float64, archetype contextArchetype) float64 {
	if len(features) != len(archetype.centroid) || queryNorm == 0 || archetype.norm == 0 {
		return 0
	}
	dot := float64(vek32.Dot(features, archetype.centroid))
	return dot / (queryNorm * archetype.norm)
}

func defaultContextArchetypes() []contextArchetype {
	raw := []contextArchetype{
		newContextArchetype(
			"engineer_steady_build",
			[]float32{0.78, 0.82, 0.74, 0.08, 0.18, 0.90, 0.34, 1.12, 0.70},
			[]string{"engineer"},
			1.15, 1.22, 12*time.Second, 8*time.Second, 1.06,
		),
		newContextArchetype(
			"engineer_test_burst",
			[]float32{1.08, 0.98, 0.82, 0.34, 0.48, 1.28, 0.44, 1.20, 0.92},
			[]string{"engineer", "tester-pipeline", "tester"},
			1.24, 1.36, 16*time.Second, 10*time.Second, 1.14,
		),
		newContextArchetype(
			"tester_bursty_validation",
			[]float32{0.94, 0.84, 0.66, 0.28, 0.62, 1.34, 0.56, 1.16, 0.82},
			[]string{"tester-pipeline", "tester"},
			1.28, 1.42, 18*time.Second, 11*time.Second, 1.18,
		),
		newContextArchetype(
			"inspector_spike_decay",
			[]float32{0.72, 0.62, 0.48, -0.12, 0.34, 1.10, 0.52, 1.02, 0.58},
			[]string{"inspector-pipeline", "inspector"},
			1.18, 1.25, 14*time.Second, 9*time.Second, 1.10,
		),
		newContextArchetype(
			"designer_long_plateau",
			[]float32{0.84, 0.92, 0.90, 0.04, 0.14, 0.96, 0.30, 1.26, 0.76},
			[]string{"designer", "architect"},
			1.20, 1.24, 20*time.Second, 12*time.Second, 1.12,
		),
		newContextArchetype(
			"compacted_rebound",
			[]float32{0.54, 0.48, 0.52, 0.18, 0.26, 0.88, 0.38, 1.08, 0.62},
			[]string{"engineer", "designer", "inspector-pipeline", "tester-pipeline"},
			1.16, 1.22, 10*time.Second, 7*time.Second, 1.05,
		),
	}
	return raw
}

func newContextArchetype(
	name string,
	centroid []float32,
	preferred []string,
	headroomMultiplier, burstRatio float64,
	releaseHalfLife, leaseDuration time.Duration,
	uncertaintyMultiplier float64,
) contextArchetype {
	preferredSet := make(map[string]struct{}, len(preferred))
	for _, agentType := range preferred {
		trimmed := strings.TrimSpace(agentType)
		if trimmed == "" {
			continue
		}
		preferredSet[trimmed] = struct{}{}
	}
	return contextArchetype{
		name:                  name,
		centroid:              centroid,
		norm:                  math.Sqrt(float64(vek32.Dot(centroid, centroid))),
		preferredAgentTypes:   preferredSet,
		headroomMultiplier:    headroomMultiplier,
		burstRatio:            burstRatio,
		releaseHalfLife:       releaseHalfLife,
		leaseDuration:         leaseDuration,
		uncertaintyMultiplier: uncertaintyMultiplier,
	}
}
