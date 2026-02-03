package sylkdir

import (
	"math"
	"testing"
)

func TestWALNodeData_RoundTrip(t *testing.T) {
	cases := []WALNodeData{
		{NodeID: 42, Domain: 1, NodeType: 3, Key: "file:/src/main.go", Name: "main", Path: "/src/main.go"},
		{NodeID: 0, Domain: 0, NodeType: 0, Key: "", Name: "", Path: ""},
		{NodeID: math.MaxUint32, Domain: 255, NodeType: 255, Key: "symbol:/a/b.go:Foo:func", Name: "Foo", Path: "/a/b.go"},
	}

	for _, tc := range cases {
		data, err := EncodeNodeData(&tc)
		if err != nil {
			t.Fatalf("EncodeNodeData(%+v): %v", tc, err)
		}

		decoded, err := DecodeNodeData(data)
		if err != nil {
			t.Fatalf("DecodeNodeData: %v", err)
		}

		if decoded.NodeID != tc.NodeID {
			t.Errorf("NodeID = %d, want %d", decoded.NodeID, tc.NodeID)
		}
		if decoded.Domain != tc.Domain {
			t.Errorf("Domain = %d, want %d", decoded.Domain, tc.Domain)
		}
		if decoded.NodeType != tc.NodeType {
			t.Errorf("NodeType = %d, want %d", decoded.NodeType, tc.NodeType)
		}
		if decoded.Key != tc.Key {
			t.Errorf("Key = %q, want %q", decoded.Key, tc.Key)
		}
		if decoded.Name != tc.Name {
			t.Errorf("Name = %q, want %q", decoded.Name, tc.Name)
		}
		if decoded.Path != tc.Path {
			t.Errorf("Path = %q, want %q", decoded.Path, tc.Path)
		}
	}
}

func TestWALNodeData_Truncated(t *testing.T) {
	d := &WALNodeData{NodeID: 1, Key: "k", Name: "n", Path: "p"}
	data, _ := EncodeNodeData(d)

	// Truncate at various points.
	for _, cutoff := range []int{0, 3, 5, 8, len(data) - 1} {
		if _, err := DecodeNodeData(data[:cutoff]); err != ErrWALCodecTruncated {
			t.Errorf("DecodeNodeData(truncated at %d) = %v, want ErrWALCodecTruncated", cutoff, err)
		}
	}
}

func TestWALEdgeData_RoundTrip(t *testing.T) {
	cases := []WALEdgeData{
		{SourceID: 1, TargetID: 2, EdgeType: 3, Weight: 0.75, SessionID: 100},
		{SourceID: 0, TargetID: 0, EdgeType: 0, Weight: 0, SessionID: 0},
		{SourceID: math.MaxUint32, TargetID: math.MaxUint32, EdgeType: 255, Weight: -1.5, SessionID: math.MaxUint32},
		{SourceID: 10, TargetID: 20, EdgeType: 1, Weight: math.Float32frombits(0x7FC00000), SessionID: 5}, // NaN weight
	}

	for _, tc := range cases {
		data := EncodeEdgeData(&tc)
		decoded, err := DecodeEdgeData(data)
		if err != nil {
			t.Fatalf("DecodeEdgeData: %v", err)
		}

		if decoded.SourceID != tc.SourceID {
			t.Errorf("SourceID = %d, want %d", decoded.SourceID, tc.SourceID)
		}
		if decoded.TargetID != tc.TargetID {
			t.Errorf("TargetID = %d, want %d", decoded.TargetID, tc.TargetID)
		}
		if decoded.EdgeType != tc.EdgeType {
			t.Errorf("EdgeType = %d, want %d", decoded.EdgeType, tc.EdgeType)
		}
		if math.Float32bits(decoded.Weight) != math.Float32bits(tc.Weight) {
			t.Errorf("Weight bits = %x, want %x", math.Float32bits(decoded.Weight), math.Float32bits(tc.Weight))
		}
		if decoded.SessionID != tc.SessionID {
			t.Errorf("SessionID = %d, want %d", decoded.SessionID, tc.SessionID)
		}
	}
}

func TestWALEdgeData_Truncated(t *testing.T) {
	for _, cutoff := range []int{0, 8, 12, 16} {
		if _, err := DecodeEdgeData(make([]byte, cutoff)); err != ErrWALCodecTruncated {
			t.Errorf("DecodeEdgeData(len=%d) = %v, want ErrWALCodecTruncated", cutoff, err)
		}
	}
}

func TestWALVectorData_RoundTrip(t *testing.T) {
	cases := []WALVectorData{
		{NodeID: 1, Vector: []float32{1.0, 2.0, 3.0, 4.0}},
		{NodeID: 0, Vector: nil},
		{NodeID: 0, Vector: []float32{}},
		{NodeID: 99, Vector: []float32{-1.5, 0, math.MaxFloat32, math.SmallestNonzeroFloat32}},
	}

	for _, tc := range cases {
		data := EncodeVectorData(&tc)
		decoded, err := DecodeVectorData(data)
		if err != nil {
			t.Fatalf("DecodeVectorData: %v", err)
		}

		if decoded.NodeID != tc.NodeID {
			t.Errorf("NodeID = %d, want %d", decoded.NodeID, tc.NodeID)
		}
		if len(decoded.Vector) != len(tc.Vector) {
			t.Fatalf("Vector len = %d, want %d", len(decoded.Vector), len(tc.Vector))
		}
		for i, v := range decoded.Vector {
			if math.Float32bits(v) != math.Float32bits(tc.Vector[i]) {
				t.Errorf("Vector[%d] bits = %x, want %x", i, math.Float32bits(v), math.Float32bits(tc.Vector[i]))
			}
		}
	}
}

func TestWALVectorData_Truncated(t *testing.T) {
	d := &WALVectorData{NodeID: 1, Vector: []float32{1.0, 2.0}}
	data := EncodeVectorData(d)

	// Header truncated.
	if _, err := DecodeVectorData(data[:4]); err != ErrWALCodecTruncated {
		t.Errorf("DecodeVectorData(header truncated) = %v, want ErrWALCodecTruncated", err)
	}

	// Vector data truncated.
	if _, err := DecodeVectorData(data[:12]); err != ErrWALCodecTruncated {
		t.Errorf("DecodeVectorData(vector truncated) = %v, want ErrWALCodecTruncated", err)
	}
}

func TestWALDocData_RoundTrip(t *testing.T) {
	cases := []WALDocData{
		{ID: "doc-1", Path: "/src/main.go", DocType: "source", Content: "package main\n\nfunc main() {}", Language: "go"},
		{ID: "", Path: "", DocType: "", Content: "", Language: ""},
		{ID: "doc-2", Path: "/long/path/to/file.rs", DocType: "source", Content: "fn main() {}\n", Language: "rust"},
	}

	for _, tc := range cases {
		data, err := EncodeDocData(&tc)
		if err != nil {
			t.Fatalf("EncodeDocData(%+v): %v", tc, err)
		}

		decoded, err := DecodeDocData(data)
		if err != nil {
			t.Fatalf("DecodeDocData: %v", err)
		}

		if decoded.ID != tc.ID {
			t.Errorf("ID = %q, want %q", decoded.ID, tc.ID)
		}
		if decoded.Path != tc.Path {
			t.Errorf("Path = %q, want %q", decoded.Path, tc.Path)
		}
		if decoded.DocType != tc.DocType {
			t.Errorf("DocType = %q, want %q", decoded.DocType, tc.DocType)
		}
		if decoded.Content != tc.Content {
			t.Errorf("Content = %q, want %q", decoded.Content, tc.Content)
		}
		if decoded.Language != tc.Language {
			t.Errorf("Language = %q, want %q", decoded.Language, tc.Language)
		}
	}
}

func TestWALDocData_Truncated(t *testing.T) {
	d := &WALDocData{ID: "x", Path: "p", DocType: "t", Content: "c", Language: "l"}
	data, _ := EncodeDocData(d)

	for _, cutoff := range []int{0, 1, 4, 8, len(data) - 1} {
		if _, err := DecodeDocData(data[:cutoff]); err != ErrWALCodecTruncated {
			t.Errorf("DecodeDocData(truncated at %d) = %v, want ErrWALCodecTruncated", cutoff, err)
		}
	}
}

func TestWALCommitData_RoundTrip(t *testing.T) {
	cases := []WALCommitData{
		{SessionID: 1, Major: 0, Minor: 1, Patch: 0},
		{SessionID: 0, Major: 0, Minor: 0, Patch: 0},
		{SessionID: math.MaxUint32, Major: math.MaxUint16, Minor: math.MaxUint16, Patch: math.MaxUint16},
		{SessionID: 42, Major: 2, Minor: 3, Patch: 7},
	}

	for _, tc := range cases {
		data := EncodeCommitData(&tc)
		decoded, err := DecodeCommitData(data)
		if err != nil {
			t.Fatalf("DecodeCommitData: %v", err)
		}

		if decoded.SessionID != tc.SessionID {
			t.Errorf("SessionID = %d, want %d", decoded.SessionID, tc.SessionID)
		}
		if decoded.Major != tc.Major {
			t.Errorf("Major = %d, want %d", decoded.Major, tc.Major)
		}
		if decoded.Minor != tc.Minor {
			t.Errorf("Minor = %d, want %d", decoded.Minor, tc.Minor)
		}
		if decoded.Patch != tc.Patch {
			t.Errorf("Patch = %d, want %d", decoded.Patch, tc.Patch)
		}
	}
}

func TestWALCommitData_Truncated(t *testing.T) {
	for _, cutoff := range []int{0, 4, 6, 8, 9} {
		if _, err := DecodeCommitData(make([]byte, cutoff)); err != ErrWALCodecTruncated {
			t.Errorf("DecodeCommitData(len=%d) = %v, want ErrWALCodecTruncated", cutoff, err)
		}
	}
}

func TestWALCodec_EncodedSizes(t *testing.T) {
	// Verify encoded sizes match expected wire format.
	node := &WALNodeData{NodeID: 1, Domain: 2, NodeType: 3, Key: "abc", Name: "de", Path: "f"}
	nodeData, _ := EncodeNodeData(node)
	// 4+1+1 + (2+3) + (2+2) + (2+1) = 18
	if len(nodeData) != 18 {
		t.Errorf("node encoded size = %d, want 18", len(nodeData))
	}

	edge := &WALEdgeData{SourceID: 1, TargetID: 2, EdgeType: 3, Weight: 1.0, SessionID: 4}
	edgeData := EncodeEdgeData(edge)
	if len(edgeData) != walEdgeFixedSize {
		t.Errorf("edge encoded size = %d, want %d", len(edgeData), walEdgeFixedSize)
	}

	vec := &WALVectorData{NodeID: 1, Vector: []float32{1.0, 2.0, 3.0}}
	vecData := EncodeVectorData(vec)
	if expected := walVectorHeaderSize + 3*4; len(vecData) != expected {
		t.Errorf("vector encoded size = %d, want %d", len(vecData), expected)
	}

	commit := &WALCommitData{SessionID: 1, Major: 0, Minor: 1, Patch: 0}
	commitData := EncodeCommitData(commit)
	if len(commitData) != walCommitFixedSize {
		t.Errorf("commit encoded size = %d, want %d", len(commitData), walCommitFixedSize)
	}
}
