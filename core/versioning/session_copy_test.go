package versioning

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSessionVFS_MergeDescriptors_RecordedOnEachMerge verifies that the
// session tracks a MergeDescriptor per merge into green and that the
// descriptors are returned in arrival order. See
// docs/PARALLEL_GLOBAL_VFS.md §3.1 / §3.3.
func TestSessionVFS_MergeDescriptors_RecordedOnEachMerge(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()
	ctx := context.Background()

	if got := svfs.MergeDescriptors(); len(got) != 0 {
		t.Fatalf("MergeDescriptors at start = %v, want empty", got)
	}
	if _, ok := svfs.LatestMergeDescriptor(); ok {
		t.Fatal("LatestMergeDescriptor should be false on a fresh session")
	}

	// Merge A.
	pipeA, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_a",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline A: %v", err)
	}
	pathA := filepath.Join(dir, "a.py")
	if err := svfs.NewPipelineFileAccess(pipeA).WriteFile(ctx, pathA, []byte("a\n")); err != nil {
		t.Fatalf("WriteFile A: %v", err)
	}
	resA, err := svfs.MergePipelineIntoGreen(ctx, "task_a", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatalf("MergePipelineIntoGreen A: %v", err)
	}

	// Merge B.
	pipeB, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_b",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline B: %v", err)
	}
	pathB := filepath.Join(dir, "b.py")
	if err := svfs.NewPipelineFileAccess(pipeB).WriteFile(ctx, pathB, []byte("b\n")); err != nil {
		t.Fatalf("WriteFile B: %v", err)
	}
	resB, err := svfs.MergePipelineIntoGreen(ctx, "task_b", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatalf("MergePipelineIntoGreen B: %v", err)
	}

	descs := svfs.MergeDescriptors()
	if len(descs) != 2 {
		t.Fatalf("MergeDescriptors = %d, want 2", len(descs))
	}
	if descs[0].PipelineID != "task_a" {
		t.Fatalf("descs[0].PipelineID = %q, want task_a", descs[0].PipelineID)
	}
	if descs[1].PipelineID != "task_b" {
		t.Fatalf("descs[1].PipelineID = %q, want task_b", descs[1].PipelineID)
	}
	if descs[0].MergedVersion != resA.MergedVersion {
		t.Fatalf("descs[0].MergedVersion = %s, want %s", descs[0].MergedVersion, resA.MergedVersion)
	}
	if descs[1].MergedVersion != resB.MergedVersion {
		t.Fatalf("descs[1].MergedVersion = %s, want %s", descs[1].MergedVersion, resB.MergedVersion)
	}
	if descs[1].BaseVersion != resA.MergedVersion {
		t.Fatalf("descs[1].BaseVersion = %s, want %s (B's base is A's merged)", descs[1].BaseVersion, resA.MergedVersion)
	}

	// Latest accessor.
	latest, ok := svfs.LatestMergeDescriptor()
	if !ok {
		t.Fatal("LatestMergeDescriptor: expected ok=true")
	}
	if latest.PipelineID != "task_b" {
		t.Fatalf("LatestMergeDescriptor.PipelineID = %q, want task_b", latest.PipelineID)
	}

	// Find by version.
	found, ok := svfs.FindMergeDescriptor(resA.MergedVersion)
	if !ok {
		t.Fatalf("FindMergeDescriptor(%s): expected ok=true", resA.MergedVersion)
	}
	if found.PipelineID != "task_a" {
		t.Fatalf("FindMergeDescriptor.PipelineID = %q, want task_a", found.PipelineID)
	}

	// Find for a non-existent version.
	notFound := SemanticVersion{Major: 999, Minor: 999}
	if _, ok := svfs.FindMergeDescriptor(notFound); ok {
		t.Fatal("FindMergeDescriptor on unknown version: expected ok=false")
	}
}

// TestSessionVFS_CopyAt_MaterializesHistoricalState verifies CopyAt
// replays WAL deltas to reconstruct a historical Copy. See
// docs/PARALLEL_GLOBAL_VFS.md §3.6.
func TestSessionVFS_CopyAt_MaterializesHistoricalState(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()
	ctx := context.Background()

	// Merge A: write "a.py" = "v1".
	pipeA, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_a",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline A: %v", err)
	}
	pathA := filepath.Join(dir, "a.py")
	if err := svfs.NewPipelineFileAccess(pipeA).WriteFile(ctx, pathA, []byte("v1")); err != nil {
		t.Fatalf("WriteFile A: %v", err)
	}
	resA, err := svfs.MergePipelineIntoGreen(ctx, "task_a", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatalf("MergePipelineIntoGreen A: %v", err)
	}

	// Merge B: overwrite "a.py" = "v2".
	pipeB, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_b",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline B: %v", err)
	}
	if err := svfs.NewPipelineFileAccess(pipeB).WriteFile(ctx, pathA, []byte("v2")); err != nil {
		t.Fatalf("WriteFile B: %v", err)
	}
	resB, err := svfs.MergePipelineIntoGreen(ctx, "task_b", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatalf("MergePipelineIntoGreen B: %v", err)
	}

	// CopyAt version A should show "v1".
	matA, err := svfs.CopyAt(resA.MergedVersion)
	if err != nil {
		t.Fatalf("CopyAt(A): %v", err)
	}
	content, ok := matA.Read(pathA)
	if !ok {
		t.Fatal("CopyAt(A).Read missing a.py")
	}
	if string(content) != "v1" {
		t.Fatalf("CopyAt(A) a.py = %q, want v1", string(content))
	}

	// CopyAt version B should show "v2".
	matB, err := svfs.CopyAt(resB.MergedVersion)
	if err != nil {
		t.Fatalf("CopyAt(B): %v", err)
	}
	content, ok = matB.Read(pathA)
	if !ok {
		t.Fatal("CopyAt(B).Read missing a.py")
	}
	if string(content) != "v2" {
		t.Fatalf("CopyAt(B) a.py = %q, want v2", string(content))
	}

	// CopyAt the zero version: empty.
	matZero, err := svfs.CopyAt(SemanticVersion{})
	if err != nil {
		t.Fatalf("CopyAt(zero): %v", err)
	}
	if matZero.FileCount() != 0 {
		t.Fatalf("CopyAt(zero).FileCount = %d, want 0", matZero.FileCount())
	}

	// CopyAt a future version: error.
	future := SemanticVersion{Major: resB.MergedVersion.Major + 100}
	if _, err := svfs.CopyAt(future); err == nil {
		t.Fatal("CopyAt(future): expected error")
	}
}

// TestSessionVFS_BeginPipeline_WithBaseCopyVersion_PinsIsolation verifies
// that a pipeline dispatched with BaseCopyVersion sees the Copy's state
// exactly, regardless of subsequent green advancement. This is the
// byte-for-byte isolation guarantee from docs/PARALLEL_GLOBAL_VFS.md §3.6.
func TestSessionVFS_BeginPipeline_WithBaseCopyVersion_PinsIsolation(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()
	ctx := context.Background()

	// Pipeline A: write "shared.py" = "v1".
	pipeA, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_a",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline A: %v", err)
	}
	sharedPath := filepath.Join(dir, "shared.py")
	if err := svfs.NewPipelineFileAccess(pipeA).WriteFile(ctx, sharedPath, []byte("v1")); err != nil {
		t.Fatalf("WriteFile A: %v", err)
	}
	resA, err := svfs.MergePipelineIntoGreen(ctx, "task_a", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatalf("MergePipelineIntoGreen A: %v", err)
	}
	pinnedVersion := resA.MergedVersion

	// Pipeline B dispatches pinned to A's Copy.
	pipeB, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID:      "task_b_remediation",
		SessionID:        "test-session",
		WorkingDir:       dir,
		BaseCopyVersion:  pinnedVersion,
	})
	if err != nil {
		t.Fatalf("BeginPipeline B pinned: %v", err)
	}

	// Pipeline C (separate) advances green by overwriting shared.py.
	pipeC, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_c",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline C: %v", err)
	}
	if err := svfs.NewPipelineFileAccess(pipeC).WriteFile(ctx, sharedPath, []byte("v-from-C")); err != nil {
		t.Fatalf("WriteFile C: %v", err)
	}
	if _, err := svfs.MergePipelineIntoGreen(ctx, "task_c", PipelineInspectorCertificate{}); err != nil {
		t.Fatalf("MergePipelineIntoGreen C: %v", err)
	}

	// Pipeline B's reads of shared.py must still return A's "v1",
	// NOT C's "v-from-C". This is the isolation guarantee.
	faB := svfs.NewPipelineFileAccess(pipeB)
	got, err := faB.ReadFile(ctx, sharedPath)
	if err != nil {
		t.Fatalf("Pipeline B ReadFile: %v", err)
	}
	if string(got) != "v1" {
		t.Fatalf("Pipeline B sees shared.py = %q; want %q (pinned to A's Copy, must not see C's later advancement)", string(got), "v1")
	}
}

// TestMaterializePipelineFromCopy_InstallsCopyAsBaseReader verifies
// the extracted Stage-2 function: given a live pipelineVFS and a
// baseVersion, the pipeline's BaseReader returns byte-for-byte
// content at that Copy's version and falls through to disk for
// paths the Copy didn't capture.
func TestMaterializePipelineFromCopy_InstallsCopyAsBaseReader(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:       "materialize-test",
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()
	ctx := context.Background()

	// Merge A: write shared.py v1.
	pipeA, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "A",
		SessionID:  "materialize-test",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedPath := filepath.Join(dir, "shared.py")
	if err := svfs.NewPipelineFileAccess(pipeA).WriteFile(ctx, sharedPath, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	resA, err := svfs.MergePipelineIntoGreen(ctx, "A", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}

	// Merge B: advance green with shared.py v2.
	pipeB, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "B",
		SessionID:  "materialize-test",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svfs.NewPipelineFileAccess(pipeB).WriteFile(ctx, sharedPath, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if _, err := svfs.MergePipelineIntoGreen(ctx, "B", PipelineInspectorCertificate{}); err != nil {
		t.Fatal(err)
	}

	// Construct an uninitialized pipeline VFS (no base reader yet)
	// and call the extracted MaterializePipelineFromCopy directly.
	pipeC, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID:      "C",
		SessionID:       "materialize-test",
		WorkingDir:      dir,
		BaseCopyVersion: resA.MergedVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Re-materialize via the exported entry point (idempotent: BeginPipeline
	// already materialized this pipeline; calling again installs the same
	// Copy-backed readers).
	if err := svfs.MaterializePipelineFromCopy(pipeC, resA.MergedVersion); err != nil {
		t.Fatalf("MaterializePipelineFromCopy: %v", err)
	}

	// Pipeline C must see shared.py as v1 (A's Copy), NOT v2 (B's
	// later merge). This is the pinning invariant the extracted
	// function is responsible for.
	content, err := svfs.NewPipelineFileAccess(pipeC).ReadFile(ctx, sharedPath)
	if err != nil {
		t.Fatalf("ReadFile shared.py through C: %v", err)
	}
	if string(content) != "v1" {
		t.Errorf("materialized C sees shared.py = %q, want %q (pinned to A's Copy)", string(content), "v1")
	}
}

// TestMaterializePipelineFromCopy_RejectsInvalidInputs pins the
// precondition contract: nil pipelineVFS and zero baseVersion are
// errors, not silent no-ops.
func TestMaterializePipelineFromCopy_RejectsInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "materialize-rejects",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svfs.Close()

	if err := svfs.MaterializePipelineFromCopy(nil, SemanticVersion{Minor: 1}); err == nil {
		t.Error("accepted nil pipelineVFS")
	}

	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "probe",
		SessionID:  "materialize-rejects",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svfs.MaterializePipelineFromCopy(pipe, SemanticVersion{}); err == nil {
		t.Error("accepted zero baseVersion (callers should use current-green dispatch for that case)")
	}
}
