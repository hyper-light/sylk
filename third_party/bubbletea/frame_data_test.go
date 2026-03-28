package tea

import "testing"

type frameDataTestModel struct{}

func (frameDataTestModel) Init() Cmd               { return nil }
func (frameDataTestModel) Update(Msg) (Model, Cmd) { return frameDataTestModel{}, nil }
func (frameDataTestModel) View() string            { return "view" }
func (frameDataTestModel) FrameData() FrameData {
	return FrameData{
		Lines:     []string{"a", "b"},
		DirtyRows: []int{1},
		Version:   2,
		Full:      false,
	}
}

type snapshotLike struct {
	Lines     []string
	DirtyRows []int
	Version   uint64
	Full      bool
}

type snapshotLikeModel struct{}

func (snapshotLikeModel) Init() Cmd               { return nil }
func (snapshotLikeModel) Update(Msg) (Model, Cmd) { return snapshotLikeModel{}, nil }
func (snapshotLikeModel) View() string            { return "view" }
func (snapshotLikeModel) FrameData() snapshotLike {
	return snapshotLike{
		Lines:     []string{"x", "y"},
		DirtyRows: []int{0, 0, 1, 9},
		Version:   3,
		Full:      false,
	}
}

func TestExtractModelFrameData_TeaFrameData(t *testing.T) {
	frame, ok := extractModelFrameData(frameDataTestModel{})
	if !ok {
		t.Fatal("extractModelFrameData() = false, want true")
	}
	if frame.Version != 2 || frame.Full {
		t.Fatalf("frame = %+v", frame)
	}
	if len(frame.Lines) != 2 || frame.Lines[0] != "a" || frame.DirtyRows[0] != 1 {
		t.Fatalf("frame = %+v", frame)
	}
}

func TestExtractModelFrameData_StructuralSnapshot(t *testing.T) {
	frame, ok := extractModelFrameData(snapshotLikeModel{})
	if !ok {
		t.Fatal("extractModelFrameData() = false, want true")
	}
	if frame.Version != 3 {
		t.Fatalf("frame.Version = %d, want 3", frame.Version)
	}
	rows := normalizeDirtyRows(frame.DirtyRows, len(frame.Lines))
	if len(rows) != 2 || rows[0] != 0 || rows[1] != 1 {
		t.Fatalf("normalizeDirtyRows = %v, want [0 1]", rows)
	}
}

func TestNormalizeFrameData_ZeroValueFallsBack(t *testing.T) {
	if _, ok := normalizeFrameData(FrameData{}); ok {
		t.Fatal("normalizeFrameData(FrameData{}) = true, want false")
	}
}
