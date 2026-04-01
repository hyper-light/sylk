package sylkdir

import "testing"

func TestBuildSessionGroundTruthIncludesDocumentPaths(t *testing.T) {
	nodes := []*Node{
		{CanonicalKey: "doc:markdown:notes/runbook.md", NodeType: uint8(NodeTypeDocument), Path: "notes/runbook.md"},
		{CanonicalKey: "chunk:doc-1:0000", NodeType: uint8(NodeTypeChunk), Path: "notes/runbook.md"},
	}

	sessionKeys, indexedPaths := buildSessionGroundTruth(nodes)

	if !sessionKeys["doc:markdown:notes/runbook.md"] {
		t.Fatalf("document canonical key missing from session ground truth")
	}
	if !indexedPaths["notes/runbook.md"] {
		t.Fatalf("document path missing from indexed paths")
	}
}
