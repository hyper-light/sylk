package forest_test

import (
	"testing"

	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/forest/testfixtures"
)

func BenchmarkPhase14FixtureSetup(b *testing.B) {
	b.ReportAllocs()
	clock := testfixtures.NewClock()
	ids := testfixtures.NewIDs("bench")
	for b.Loop() {
		nodeID := ids.Next("node")
		node := testfixtures.Node(nodeID, forest.ForestNodeValidation)
		artifact := testfixtures.Artifact("claim:" + node.ID)
		_ = testfixtures.Validation("claim:"+node.ID, artifact.ArtifactID)
		_ = testfixtures.ClaimSatisfiedDelta(clock, "claim:"+node.ID)
	}
}
