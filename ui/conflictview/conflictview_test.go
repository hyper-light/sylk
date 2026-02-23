package conflictview

import (
	"strings"
	"testing"
)

// =============================================================================
// Feature A: Label Disambiguation
// =============================================================================

func TestDeriveConflictLabels_Rebase(t *testing.T) {
	m := &Model{data: ConflictData{
		Op:         1, // SeqRebase
		DestName:   "main",
		SourceName: "feature",
		Hash:       "abc1234",
		Subject:    "fix: thing",
	}}
	m.deriveConflictLabels()

	if m.oursLabel != "Curr" {
		t.Errorf("oursLabel = %q, want %q", m.oursLabel, "Curr")
	}
	if m.theirsLabel != "Incmg" {
		t.Errorf("theirsLabel = %q, want %q", m.theirsLabel, "Incmg")
	}
	if m.oursCtx != "main" {
		t.Errorf("oursCtx = %q, want %q", m.oursCtx, "main")
	}
	if m.theirsCtx != "fix: thing" {
		t.Errorf("theirsCtx = %q, want %q", m.theirsCtx, "fix: thing")
	}
}

func TestDeriveConflictLabels_CherryPick(t *testing.T) {
	m := &Model{data: ConflictData{
		Op:       0, // SeqCherryPick
		DestName: "develop",
		Hash:     "def5678",
		Subject:  "feat: new",
	}}
	m.deriveConflictLabels()

	if m.oursLabel != "Curr" {
		t.Errorf("oursLabel = %q, want %q", m.oursLabel, "Curr")
	}
	if m.theirsLabel != "Incmg" {
		t.Errorf("theirsLabel = %q, want %q", m.theirsLabel, "Incmg")
	}
	if m.oursCtx != "develop" {
		t.Errorf("oursCtx = %q, want %q", m.oursCtx, "develop")
	}
}

func TestDeriveConflictLabels_PullMerge(t *testing.T) {
	m := &Model{data: ConflictData{
		Op:         2, // SeqMerge
		DestName:   "main",
		SourceName: "origin/main",
	}}
	m.deriveConflictLabels()

	if m.oursLabel != "Locl" {
		t.Errorf("oursLabel = %q, want %q", m.oursLabel, "Locl")
	}
	if m.theirsLabel != "Remte" {
		t.Errorf("theirsLabel = %q, want %q", m.theirsLabel, "Remte")
	}
	if m.oursCtx != "main" {
		t.Errorf("oursCtx = %q, want %q", m.oursCtx, "main")
	}
	if m.theirsCtx != "origin/main" {
		t.Errorf("theirsCtx = %q, want %q", m.theirsCtx, "origin/main")
	}
}

func TestDeriveConflictLabels_LocalMerge(t *testing.T) {
	m := &Model{data: ConflictData{
		Op:         2, // SeqMerge
		DestName:   "main",
		SourceName: "feature",
	}}
	m.deriveConflictLabels()

	if m.oursLabel != "Dest" {
		t.Errorf("oursLabel = %q, want %q", m.oursLabel, "Dest")
	}
	if m.theirsLabel != "Source" {
		t.Errorf("theirsLabel = %q, want %q", m.theirsLabel, "Source")
	}
	if m.oursCtx != "main" {
		t.Errorf("oursCtx = %q, want %q", m.oursCtx, "main")
	}
	if m.theirsCtx != "feature" {
		t.Errorf("theirsCtx = %q, want %q", m.theirsCtx, "feature")
	}
}

// =============================================================================
// Feature E: Auto-Resolution
// =============================================================================

func TestAutoResolveHunks_ImportConflict(t *testing.T) {
	lines := []string{
		"package main",
		"<<<<<<< HEAD",
		"import \"fmt\"",
		"=======",
		"import \"os\"",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	autos := autoResolveHunks(hunks, lines, "main.go")

	if len(autos) != 1 {
		t.Fatalf("got %d auto resolutions, want 1", len(autos))
	}
	if autos[0].Kind != AutoImportMerge {
		t.Errorf("kind = %d, want AutoImportMerge", autos[0].Kind)
	}
	if autos[0].Result != ResBoth {
		t.Errorf("result = %d, want ResBoth", autos[0].Result)
	}
}

func TestAutoResolveHunks_WhitespaceOnly(t *testing.T) {
	lines := []string{
		"<<<<<<< HEAD",
		"  hello  ",
		"=======",
		"hello",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	autos := autoResolveHunks(hunks, lines, "file.txt")

	if len(autos) != 1 {
		t.Fatalf("got %d auto resolutions, want 1", len(autos))
	}
	if autos[0].Kind != AutoWhitespaceOnly {
		t.Errorf("kind = %d, want AutoWhitespaceOnly", autos[0].Kind)
	}
}

func TestAutoResolveHunks_AdjacentAdd(t *testing.T) {
	lines := []string{
		"<<<<<<< HEAD",
		"func foo() {}",
		"=======",
		"func bar() {}",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	autos := autoResolveHunks(hunks, lines, "main.go")

	if len(autos) != 1 {
		t.Fatalf("got %d auto resolutions, want 1", len(autos))
	}
	if autos[0].Kind != AutoAdjacentAdd {
		t.Errorf("kind = %d, want AutoAdjacentAdd", autos[0].Kind)
	}
}

func TestAutoResolveHunks_NoAutoResolve(t *testing.T) {
	// Both sides share "y = shared()" so isAdjacentAdd returns false.
	// Content differs so isWhitespaceOnly is false. Not imports.
	lines := []string{
		"<<<<<<< HEAD",
		"x = 1",
		"y = shared()",
		"=======",
		"x = 2",
		"y = shared()",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	autos := autoResolveHunks(hunks, lines, "main.py")

	if len(autos) != 0 {
		t.Errorf("got %d auto resolutions, want 0", len(autos))
	}
}

func TestIsImportConflict_GoImports(t *testing.T) {
	ours := []string{`import "fmt"`}
	theirs := []string{`import "os"`}
	if !isImportConflict(ours, theirs, "main.go") {
		t.Error("expected Go imports to be detected")
	}
}

func TestIsImportConflict_TSImports(t *testing.T) {
	ours := []string{`import React from 'react'`}
	theirs := []string{`import Vue from 'vue'`}
	if !isImportConflict(ours, theirs, "app.tsx") {
		t.Error("expected TS imports to be detected")
	}
}

func TestIsImportConflict_PythonImports(t *testing.T) {
	ours := []string{`import os`}
	theirs := []string{`from sys import path`}
	if !isImportConflict(ours, theirs, "main.py") {
		t.Error("expected Python imports to be detected")
	}
}

func TestIsImportConflict_NonImport(t *testing.T) {
	ours := []string{`x = 1`}
	theirs := []string{`import os`}
	if isImportConflict(ours, theirs, "main.py") {
		t.Error("expected mixed content to not be detected as import conflict")
	}
}

func TestIsWhitespaceOnly(t *testing.T) {
	ours := []string{"  hello  ", " world "}
	theirs := []string{"hello", "world"}
	if !isWhitespaceOnly(ours, theirs) {
		t.Error("expected whitespace-only diff")
	}
}

func TestIsWhitespaceOnly_DifferentContent(t *testing.T) {
	ours := []string{"hello"}
	theirs := []string{"world"}
	if isWhitespaceOnly(ours, theirs) {
		t.Error("expected different content to not be whitespace-only")
	}
}

func TestIsAdjacentAdd(t *testing.T) {
	ours := []string{"func foo() {}"}
	theirs := []string{"func bar() {}"}
	if !isAdjacentAdd(ours, theirs) {
		t.Error("expected adjacent add detection")
	}
}

func TestIsAdjacentAdd_Overlap(t *testing.T) {
	ours := []string{"func foo() {}"}
	theirs := []string{"func foo() {}", "func bar() {}"}
	if isAdjacentAdd(ours, theirs) {
		t.Error("expected overlapping lines to not be adjacent add")
	}
}

// =============================================================================
// Feature F: Regression Detection
// =============================================================================

func TestCheckResolution_DiscardedChanges(t *testing.T) {
	lines := []string{
		"<<<<<<< HEAD",
		"line1",
		"line2",
		"line3",
		"line4",
		"line5",
		"=======",
		"other",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	warnings := checkResolution(lines, hunks[0], ResTheirs)

	found := false
	for _, w := range warnings {
		if w.Kind == RegressionDiscardedChanges {
			found = true
		}
	}
	if !found {
		t.Error("expected RegressionDiscardedChanges warning")
	}
}

func TestCheckResolution_DroppedAll(t *testing.T) {
	lines := []string{
		"<<<<<<< HEAD",
		"",
		"  ",
		"=======",
		"real content",
		"more content",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	warnings := checkResolution(lines, hunks[0], ResOurs)

	found := false
	for _, w := range warnings {
		if w.Kind == RegressionDroppedAll {
			found = true
		}
	}
	if !found {
		t.Error("expected RegressionDroppedAll warning")
	}
}

func TestCheckResolution_DuplicateCode(t *testing.T) {
	lines := []string{
		"<<<<<<< HEAD",
		"func foo() {",
		"  return 1",
		"}",
		"=======",
		"func foo() {",
		"  return 1",
		"}",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	warnings := checkResolution(lines, hunks[0], ResBoth)

	found := false
	for _, w := range warnings {
		if w.Kind == RegressionDuplicateCode {
			found = true
		}
	}
	if !found {
		t.Error("expected RegressionDuplicateCode warning")
	}
}

func TestCheckResolution_NoWarnings(t *testing.T) {
	lines := []string{
		"<<<<<<< HEAD",
		"x = 1",
		"=======",
		"x = 2",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	warnings := checkResolution(lines, hunks[0], ResOurs)

	if len(warnings) != 0 {
		t.Errorf("got %d warnings, want 0", len(warnings))
	}
}

func TestKeptAndDropped(t *testing.T) {
	ours := []string{"a", "b"}
	theirs := []string{"c", "d"}

	kept, dropped := keptAndDropped(ours, theirs, ResOurs)
	if kept[0] != "a" || dropped[0] != "c" {
		t.Error("ResOurs should keep ours, drop theirs")
	}

	kept, dropped = keptAndDropped(ours, theirs, ResTheirs)
	if kept[0] != "c" || dropped[0] != "a" {
		t.Error("ResTheirs should keep theirs, drop ours")
	}
}

// =============================================================================
// Feature I: Complexity Scoring
// =============================================================================

func TestScoreAllEntries_SingleSimple(t *testing.T) {
	entries := []ConflictFileEntry{{
		Hunks: []ConflictHunk{{StartLine: 0, DivLine: 2, EndLine: 4}},
	}}
	scores := scoreAllEntries(entries)

	if len(scores) != 1 {
		t.Fatalf("got %d scores, want 1", len(scores))
	}
	if scores[0].Level != ComplexitySimple {
		t.Errorf("level = %d, want ComplexitySimple", scores[0].Level)
	}
}

func TestScoreAllEntries_DeleteModifyIsComplex(t *testing.T) {
	entries := []ConflictFileEntry{{
		Type:  TypeDeleteModify,
		Hunks: nil,
	}}
	scores := scoreAllEntries(entries)

	if scores[0].Level != ComplexityComplex {
		t.Errorf("level = %d, want ComplexityComplex", scores[0].Level)
	}
}

func TestScoreAllEntries_BinaryIsComplex(t *testing.T) {
	entries := []ConflictFileEntry{{
		Type:  TypeBinary,
		Hunks: nil,
	}}
	scores := scoreAllEntries(entries)

	if scores[0].Level != ComplexityComplex {
		t.Errorf("level = %d, want ComplexityComplex", scores[0].Level)
	}
}

func TestSortByComplexity(t *testing.T) {
	// Use pre-computed scores to test sort logic directly,
	// independent of data-driven threshold calculations.
	entries := []ConflictFileEntry{
		{Path: "complex.go"},
		{Path: "simple.go"},
		{Path: "moderate.go"},
	}
	scores := []ComplexityScore{
		{Level: ComplexityComplex},
		{Level: ComplexitySimple},
		{Level: ComplexityModerate},
	}
	sortByComplexity(entries, scores)

	want := []string{"simple.go", "moderate.go", "complex.go"}
	for i, w := range want {
		if entries[i].Path != w {
			t.Errorf("entries[%d].Path = %q, want %q", i, entries[i].Path, w)
		}
	}
}

func TestMedianInt(t *testing.T) {
	tests := []struct {
		vals []int
		want int
	}{
		{nil, 0},
		{[]int{5}, 5},
		{[]int{1, 3, 5}, 3},
		{[]int{5, 1, 3}, 3},
		{[]int{1, 2, 3, 4}, 3},
	}
	for _, tt := range tests {
		got := medianInt(tt.vals)
		if got != tt.want {
			t.Errorf("medianInt(%v) = %d, want %d", tt.vals, got, tt.want)
		}
	}
}

func TestPercentileInt(t *testing.T) {
	vals := []int{10, 20, 30, 40, 50}
	got := percentileInt(vals, 75)
	if got != 40 {
		t.Errorf("percentileInt(75) = %d, want 40", got)
	}
}

// =============================================================================
// Feature D: Syntax Validation (type/struct tests only — tree-sitter optional)
// =============================================================================

func TestSyntaxWarningType(t *testing.T) {
	w := SyntaxWarning{Path: "test.go"}
	if w.Path != "test.go" {
		t.Errorf("Path = %q, want test.go", w.Path)
	}
}

// =============================================================================
// Feature G: Step Preview Types
// =============================================================================

func TestStepPreviewTypes(t *testing.T) {
	sp := StepPreview{
		Hash:          "abc1234",
		Subject:       "fix: thing",
		FileDiffs:     []FileDiffSummary{{Path: "main.go", Additions: 5, Deletions: 2}},
		ConflictPaths: map[string]bool{"main.go": true},
	}
	if sp.Hash != "abc1234" {
		t.Errorf("Hash = %q, want abc1234", sp.Hash)
	}
	if len(sp.FileDiffs) != 1 {
		t.Fatalf("FileDiffs len = %d, want 1", len(sp.FileDiffs))
	}
	if !sp.ConflictPaths["main.go"] {
		t.Error("expected main.go in ConflictPaths")
	}
}

// =============================================================================
// Feature B: Base Context Helpers
// =============================================================================

func TestClipToHunkRegion(t *testing.T) {
	m := &Model{
		hunks: []ConflictHunk{
			{StartLine: 5, DivLine: 8, EndLine: 10},
		},
	}
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = strings.Repeat("x", i)
	}

	clipped := m.clipToHunkRegion(lines, 0)
	// start = max(5-1, 0) = 4, end = min(10+1, 20) = 11
	if len(clipped) != 7 {
		t.Errorf("clipped len = %d, want 7", len(clipped))
	}
}

func TestClipToHunkRegion_InvalidIndex(t *testing.T) {
	m := &Model{}
	lines := []string{"a", "b", "c"}
	clipped := m.clipToHunkRegion(lines, -1)
	if len(clipped) != 3 {
		t.Errorf("invalid index should return original, got len %d", len(clipped))
	}
}

func TestCapSlice(t *testing.T) {
	s := []string{"a", "b", "c", "d", "e"}
	capped := capSlice(s, 3)
	if len(capped) != 3 {
		t.Errorf("capSlice(5, 3) len = %d, want 3", len(capped))
	}
	capped = capSlice(s, 10)
	if len(capped) != 5 {
		t.Errorf("capSlice(5, 10) len = %d, want 5", len(capped))
	}
}

// =============================================================================
// Hunk Detection
// =============================================================================

func TestDetectHunks(t *testing.T) {
	lines := []string{
		"normal line",
		"<<<<<<< HEAD",
		"ours content",
		"=======",
		"theirs content",
		">>>>>>> feature",
		"more normal",
	}
	hunks := detectHunks(lines)

	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(hunks))
	}
	h := hunks[0]
	if h.StartLine != 1 {
		t.Errorf("StartLine = %d, want 1", h.StartLine)
	}
	if h.DivLine != 3 {
		t.Errorf("DivLine = %d, want 3", h.DivLine)
	}
	if h.EndLine != 5 {
		t.Errorf("EndLine = %d, want 5", h.EndLine)
	}
}

func TestDetectHunks_Multiple(t *testing.T) {
	lines := []string{
		"<<<<<<< HEAD",
		"a",
		"=======",
		"b",
		">>>>>>> feature",
		"gap",
		"<<<<<<< HEAD",
		"c",
		"=======",
		"d",
		">>>>>>> feature",
	}
	hunks := detectHunks(lines)
	if len(hunks) != 2 {
		t.Fatalf("got %d hunks, want 2", len(hunks))
	}
}

// =============================================================================
// Splice / Resolution
// =============================================================================

func TestSpliceHunk_Ours(t *testing.T) {
	lines := []string{
		"before",
		"<<<<<<< HEAD",
		"ours line",
		"=======",
		"theirs line",
		">>>>>>> feature",
		"after",
	}
	hunks := detectHunks(lines)
	result := spliceHunk(lines, hunks[0], ResOurs)

	joined := strings.Join(result, "\n")
	if !strings.Contains(joined, "ours line") {
		t.Error("expected ours content preserved")
	}
	if strings.Contains(joined, "theirs line") {
		t.Error("expected theirs content removed")
	}
	if strings.Contains(joined, "<<<<<<<") {
		t.Error("expected markers removed")
	}
}

func TestSpliceHunk_Theirs(t *testing.T) {
	lines := []string{
		"before",
		"<<<<<<< HEAD",
		"ours line",
		"=======",
		"theirs line",
		">>>>>>> feature",
		"after",
	}
	hunks := detectHunks(lines)
	result := spliceHunk(lines, hunks[0], ResTheirs)

	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "ours line") {
		t.Error("expected ours content removed")
	}
	if !strings.Contains(joined, "theirs line") {
		t.Error("expected theirs content preserved")
	}
}

func TestSpliceHunk_Both(t *testing.T) {
	lines := []string{
		"before",
		"<<<<<<< HEAD",
		"ours line",
		"=======",
		"theirs line",
		">>>>>>> feature",
		"after",
	}
	hunks := detectHunks(lines)
	result := spliceHunk(lines, hunks[0], ResBoth)

	joined := strings.Join(result, "\n")
	if !strings.Contains(joined, "ours line") {
		t.Error("expected ours content preserved")
	}
	if !strings.Contains(joined, "theirs line") {
		t.Error("expected theirs content preserved")
	}
	if strings.Contains(joined, "<<<<<<<") {
		t.Error("expected markers removed")
	}
}

// =============================================================================
// Chord Handling
// =============================================================================

func TestChordPrefixes(t *testing.T) {
	if !chordPrefixes["alt+M"] {
		t.Error("alt+M should be a chord prefix")
	}
	if !chordPrefixes["alt+D"] {
		t.Error("alt+D should be a chord prefix")
	}
	if chordPrefixes["a"] {
		t.Error("'a' should not be a chord prefix")
	}
}

// =============================================================================
// Utility functions
// =============================================================================

func TestAppendBounded(t *testing.T) {
	dst := []string{"existing"}
	result := appendBounded(dst, "a\nb\nc", 3)
	if len(result) != 3 {
		t.Errorf("len = %d, want 3", len(result))
	}
	if result[0] != "existing" {
		t.Errorf("result[0] = %q, want existing", result[0])
	}
}

func TestAppendBounded_Empty(t *testing.T) {
	dst := []string{"existing"}
	result := appendBounded(dst, "", 5)
	if len(result) != 1 {
		t.Errorf("empty text should not add lines, got len %d", len(result))
	}
}

func TestPadToHeight(t *testing.T) {
	lines := []string{"a", "b"}
	result := padToHeight(lines, 5, 10)
	if len(result) != 5 {
		t.Errorf("len = %d, want 5", len(result))
	}
	if result[2] != strings.Repeat(" ", 10) {
		t.Errorf("padded line should be blank")
	}
}

func TestExtractLines(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	got := extractLines(lines, 1, 3)
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("extractLines(1,3) = %v, want [b c]", got)
	}
}

func TestExtractLines_OutOfRange(t *testing.T) {
	lines := []string{"a"}
	got := extractLines(lines, 5, 10)
	if len(got) != 0 {
		t.Errorf("out-of-range should return nil, got len %d", len(got))
	}
}

func TestHasDuplicateBlock(t *testing.T) {
	lines := []string{"a", "b", "c", "a", "b", "c"}
	if !hasDuplicateBlock(lines, 3) {
		t.Error("expected duplicate block detected")
	}
}

func TestHasDuplicateBlock_NoDuplicate(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f"}
	if hasDuplicateBlock(lines, 3) {
		t.Error("expected no duplicate block")
	}
}

func TestHasDuplicateBlock_TooShort(t *testing.T) {
	lines := []string{"a", "b"}
	if hasDuplicateBlock(lines, 3) {
		t.Error("expected no duplicate for short input")
	}
}

// =============================================================================
// Display line mapping
// =============================================================================

func TestDisplayToContent(t *testing.T) {
	m := &Model{
		mergedLines: make([]string, 10),
		hunks: []ConflictHunk{
			{StartLine: 3, DivLine: 5, EndLine: 7},
		},
	}

	// Action line at display position 3 (StartLine + 0 hunks before).
	contentLine, isAction, hunkIdx := m.displayToContent(3)
	if !isAction || hunkIdx != 0 {
		t.Errorf("d=3: isAction=%v hunkIdx=%d, want action hunk 0", isAction, hunkIdx)
	}
	_ = contentLine

	// Content line after the action line.
	contentLine, isAction, _ = m.displayToContent(4)
	if isAction {
		t.Error("d=4 should be content, not action")
	}
	if contentLine != 3 {
		t.Errorf("d=4: contentLine=%d, want 3", contentLine)
	}
}

func TestContentToDisplay(t *testing.T) {
	m := &Model{
		hunks: []ConflictHunk{
			{StartLine: 3, DivLine: 5, EndLine: 7},
		},
	}

	// Content line 3 has one action line before it (at display 3).
	d := m.contentToDisplay(3)
	if d != 4 {
		t.Errorf("contentToDisplay(3) = %d, want 4", d)
	}

	// Content line 0 has no action lines before it.
	d = m.contentToDisplay(0)
	if d != 0 {
		t.Errorf("contentToDisplay(0) = %d, want 0", d)
	}
}

// =============================================================================
// ClassifyLine
// =============================================================================

func TestClassifyLine(t *testing.T) {
	hunks := []ConflictHunk{
		{StartLine: 2, DivLine: 4, EndLine: 6},
	}

	tests := []struct {
		line int
		want lineRegion
	}{
		{0, lineNormal},
		{2, lineMarkerOurs},
		{3, lineOursContent},
		{4, lineMarkerDiv},
		{5, lineTheirsContent},
		{6, lineMarkerTheirs},
		{7, lineNormal},
	}
	for _, tt := range tests {
		got := classifyLine(tt.line, hunks)
		if got != tt.want {
			t.Errorf("classifyLine(%d) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

// =============================================================================
// Import prefix detection
// =============================================================================

func TestImportPrefixes(t *testing.T) {
	tests := []struct {
		path    string
		wantLen int
	}{
		{"main.go", 4},
		{"app.ts", 2},
		{"app.tsx", 2},
		{"main.py", 2},
		{"App.java", 1},
		{"main.rs", 1},
		{"unknown.xyz", 0},
	}
	for _, tt := range tests {
		got := importPrefixes(tt.path)
		if len(got) != tt.wantLen {
			t.Errorf("importPrefixes(%q) len = %d, want %d", tt.path, len(got), tt.wantLen)
		}
	}
}

// =============================================================================
// Complexity helpers
// =============================================================================

func TestTotalHunkLines(t *testing.T) {
	hunks := []ConflictHunk{
		{StartLine: 0, DivLine: 3, EndLine: 6},  // ours=2, theirs=2
		{StartLine: 10, DivLine: 12, EndLine: 15}, // ours=1, theirs=2
	}
	got := totalHunkLines(hunks)
	if got != 7 {
		t.Errorf("totalHunkLines = %d, want 7", got)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		score    ComplexityScore
		medHunks int
		medLines int
		p75Lines int
		want     ComplexityLevel
	}{
		{
			name:     "delete is always complex",
			score:    ComplexityScore{HasDelete: true, HunkCount: 1, TotalLines: 1},
			medHunks: 5, medLines: 50, p75Lines: 100,
			want: ComplexityComplex,
		},
		{
			name:     "single small hunk is simple",
			score:    ComplexityScore{HunkCount: 1, TotalLines: 5},
			medHunks: 3, medLines: 10, p75Lines: 20,
			want: ComplexitySimple,
		},
		{
			name:     "many hunks is complex",
			score:    ComplexityScore{HunkCount: 10, TotalLines: 50},
			medHunks: 3, medLines: 10, p75Lines: 20,
			want: ComplexityComplex,
		},
	}
	for _, tt := range tests {
		got := classify(tt.score, tt.medHunks, tt.medLines, tt.p75Lines)
		if got != tt.want {
			t.Errorf("%s: classify() = %d, want %d", tt.name, got, tt.want)
		}
	}
}
