package forest_test

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/forest/testfixtures"
)

func TestPhase14FixturesAreDeterministicAndReusable(t *testing.T) {
	clock := testfixtures.NewClock()
	first := clock.Now()
	second := clock.Advance(time.Second)
	if !second.After(first) {
		t.Fatalf("fixture clock did not advance: %s then %s", first, second)
	}

	ids := testfixtures.NewIDs("fixture")
	id := ids.Next("node")
	if id == ids.Next("node") {
		t.Fatal("fixture IDs did not advance")
	}

	delta := testfixtures.ClaimSatisfiedDelta(clock, "claim-fixture")
	if delta.Action != claims.DeltaActionClaimSatisfied || delta.SessionID == "" {
		t.Fatalf("canonical delta fixture invalid: %+v", delta)
	}

	node := testfixtures.Node("node-fixture", forest.ForestNodeValidation)
	clone := testfixtures.CloneNode(node)
	clone.Metadata["fixture"] = false
	if node.Metadata["fixture"] != true {
		t.Fatal("fixture node clone mutated original metadata")
	}

	artifact := testfixtures.Artifact("claim-fixture")
	validation := testfixtures.Validation("claim-fixture", artifact.ArtifactID)
	if validation.TargetArtifactID != artifact.ArtifactID || validation.Status == "" {
		t.Fatalf("artifact/validation lifecycle fixtures invalid: %+v %+v", artifact, validation)
	}
}
