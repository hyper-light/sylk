package boot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/embedder"
)

var hybridTier = embedder.TierHybridLocal

func TestFindGitRoot(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}

	root, err := FindGitRoot(cwd)
	if err != nil {
		t.Skipf("not in git repo: %v", err)
	}

	gitDir := filepath.Join(root, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		t.Errorf("expected .git at %s, got error: %v", gitDir, err)
	}
}

func TestMetaRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()

	if err := EnsureSylkDirs(tmpDir); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	meta := NewMeta(true, "hybrid-local")
	meta.SetFileHash("test.go", "sha256:abc123")
	meta.UpdateStats(100, 200, 100)

	if err := meta.Save(tmpDir); err != nil {
		t.Fatalf("save meta: %v", err)
	}

	loaded, err := LoadMeta(tmpDir)
	if err != nil {
		t.Fatalf("load meta: %v", err)
	}

	if loaded.SchemaVersion != SchemaVersion {
		t.Errorf("schema version: got %d, want %d", loaded.SchemaVersion, SchemaVersion)
	}
	if !loaded.IsGitRepo {
		t.Error("expected is_git_repo to be true")
	}
	if loaded.EmbedderSource != "hybrid-local" {
		t.Errorf("embedder source: got %s, want hybrid-local", loaded.EmbedderSource)
	}
	if loaded.NodeCount != 100 {
		t.Errorf("node count: got %d, want 100", loaded.NodeCount)
	}
	if hash, ok := loaded.GetFileHash("test.go"); !ok || hash != "sha256:abc123" {
		t.Errorf("file hash: got %s, want sha256:abc123", hash)
	}
}

func TestSimpleSerializer(t *testing.T) {
	s := NewSimpleSerializer()

	result := s.Serialize(
		"core/main.go",
		"function",
		"main",
		"func main()",
		"func main() {\n\tfmt.Println(\"hello\")\n}",
	)

	expected := `# path: core/main.go
# kind: function
# name: main
# signature: func main()
# body:
func main() {
	fmt.Println("hello")
}`

	if result != expected {
		t.Errorf("serialized output mismatch:\ngot:\n%s\n\nwant:\n%s", result, expected)
	}
}

func TestBootPipeline_SmallProject(t *testing.T) {
	tmpDir := t.TempDir()

	testFiles := map[string]string{
		"main.go": `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}

func helper() string {
	return "helper"
}
`,
		"util.go": `package main

type Config struct {
	Name string
	Port int
}

func NewConfig(name string) *Config {
	return &Config{Name: name, Port: 8080}
}
`,
	}

	for name, content := range testFiles {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := Boot(ctx, tmpDir)
	if err != nil {
		t.Fatalf("boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result)

	if result.FilesProcessed != 2 {
		t.Errorf("files processed: got %d, want 2", result.FilesProcessed)
	}

	if result.NodesCreated == 0 {
		t.Error("expected nodes to be created")
	}

	if result.VectorsCreated == 0 {
		t.Error("expected vectors to be created")
	}

	if result.DocsCreated == 0 {
		t.Error("expected docs to be created")
	}

	if result.CommitVersion == "" {
		t.Error("expected commit version to be set")
	}

	if result.EmbedderSource != "hybrid-local" {
		t.Errorf("embedder source: got %s, want hybrid-local", result.EmbedderSource)
	}

	if !MetaExists(tmpDir) {
		t.Error("expected meta.json to exist after boot")
	}

	meta, err := LoadMeta(tmpDir)
	if err != nil {
		t.Fatalf("load meta after boot: %v", err)
	}

	if meta.NodeCount == 0 {
		t.Error("expected node count > 0 in meta")
	}

	// Verify data persisted: global stores should have nodes
	sd := sylkdir.New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("init sylkdir for verification: %v", err)
	}

	gm := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("load global meta: %v", err)
	}

	headVersion := gm.GetHead()
	nodeStore, err := sylkdir.NewGlobalVersionNodeStore(sd, headVersion)
	if err != nil {
		t.Fatalf("open global node store: %v", err)
	}
	defer nodeStore.Close()

	nodes, err := nodeStore.ReadAll()
	if err != nil {
		t.Fatalf("read all nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Error("expected global node store to contain nodes after boot")
	}

	t.Logf("Boot completed in %v", result.Duration)
	t.Logf("Files: %d, Nodes: %d, Edges: %d, Docs: %d, Vectors: %d, ChunkRefs: %d",
		result.FilesProcessed, result.NodesCreated, result.EdgesCreated,
		result.DocsCreated, result.VectorsCreated, result.ChunkRefsCreated)
	t.Logf("Commit version: %s", result.CommitVersion)
	t.Logf("Global nodes persisted: %d", len(nodes))
}

func TestBootPipeline_Incremental(t *testing.T) {
	tmpDir := t.TempDir()

	initialFile := `package main

func foo() {}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(initialFile), 0644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	ctx := context.Background()

	result1, err := Boot(ctx, tmpDir)
	if err != nil {
		t.Fatalf("first boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result1)
	t.Logf("First boot: %d files, %d nodes in %v",
		result1.FilesProcessed, result1.NodesCreated, result1.Duration)

	if result1.IsIncremental {
		t.Error("first boot should not be incremental")
	}

	// Wait for background file hashing to complete — delta detection uses these hashes.
	result1.BackgroundHasher.Wait()

	updatedFile := `package main

func foo() {}
func bar() {}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(updatedFile), 0644); err != nil {
		t.Fatalf("write updated file: %v", err)
	}

	result2, err := Boot(ctx, tmpDir)
	if err != nil {
		t.Fatalf("second boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result2)
	t.Logf("Second boot: %d files, %d nodes in %v (incremental: %v)",
		result2.FilesProcessed, result2.NodesCreated, result2.Duration, result2.IsIncremental)

	if !result2.IsIncremental {
		t.Error("second boot should be incremental")
	}
}

func TestBootPipeline_RealProject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real project test in short mode")
	}

	projectRoot := findProjectRoot(t)
	t.Logf("Testing boot on project: %s", projectRoot)

	tmpSylk := filepath.Join(t.TempDir(), ".sylk")
	projectTmp := t.TempDir()

	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		t.Fatalf("read project dir: %v", err)
	}

	for _, entry := range entries {
		if entry.Name() == ".sylk" || entry.Name() == ".git" {
			continue
		}
		src := filepath.Join(projectRoot, entry.Name())
		dst := filepath.Join(projectTmp, entry.Name())
		if err := copyPath(src, dst); err != nil {
			t.Logf("skip copy %s: %v", entry.Name(), err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := Boot(ctx, projectTmp)
	if err != nil {
		t.Fatalf("boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result)

	t.Logf("Boot completed in %v", result.Duration)
	t.Logf("Project root: %s", result.ProjectRoot)
	t.Logf("Is git repo: %v", result.IsGitRepo)
	t.Logf("Files processed: %d", result.FilesProcessed)
	t.Logf("Nodes created: %d", result.NodesCreated)
	t.Logf("Vectors created: %d", result.VectorsCreated)
	t.Logf("Embedder: %s", result.EmbedderSource)

	if result.FilesProcessed == 0 {
		t.Error("expected files to be processed")
	}

	if result.NodesCreated == 0 {
		t.Error("expected nodes to be created")
	}

	if result.EmbedderSource != "hybrid-local" {
		t.Errorf("expected hybrid-local embedder, got: %s", result.EmbedderSource)
	}

	_ = tmpSylk
}

func TestBootPipeline_RealProjectIncremental(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real project incremental test in short mode")
	}

	projectRoot := findProjectRoot(t)
	projectTmp := t.TempDir()

	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		t.Fatalf("read project dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() == ".sylk" || entry.Name() == ".git" {
			continue
		}
		src := filepath.Join(projectRoot, entry.Name())
		dst := filepath.Join(projectTmp, entry.Name())
		if err := copyPath(src, dst); err != nil {
			t.Logf("skip copy %s: %v", entry.Name(), err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Cold-start boot: processes all files.
	result1, err := Boot(ctx, projectTmp)
	if err != nil {
		t.Fatalf("cold boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result1)
	t.Logf("Cold boot: %d files, %d nodes, %d docs in %v",
		result1.FilesProcessed, result1.NodesCreated, result1.DocsCreated, result1.Duration)

	if result1.FilesProcessed == 0 {
		t.Fatal("cold boot should process files")
	}

	// Wait for background file hashing to complete — delta detection uses these hashes.
	result1.BackgroundHasher.Wait()

	// Modify a single file to trigger delta detection.
	targetFile := filepath.Join(projectTmp, "core", "boot", "meta.go")
	original, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	modified := append(original, "\n// delta-test-marker\n"...)
	if err := os.WriteFile(targetFile, modified, 0644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	// Incremental delta boot: should process only the modified file.
	result2, err := Boot(ctx, projectTmp)
	if err != nil {
		t.Fatalf("delta boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result2)
	t.Logf("Delta boot: %d files, %d nodes, %d docs in %v",
		result2.FilesProcessed, result2.NodesCreated, result2.DocsCreated, result2.Duration)

	if result2.FilesProcessed != 1 {
		t.Errorf("delta boot should process 1 file, got %d", result2.FilesProcessed)
	}

	if result2.Duration >= result1.Duration {
		t.Errorf("delta boot (%v) should be faster than cold boot (%v)",
			result2.Duration, result1.Duration)
	}

	// Verify global state is intact after delta commit.
	sd := sylkdir.New(projectTmp)
	if err := sd.Init(); err != nil {
		t.Fatalf("init sylkdir: %v", err)
	}
	gm := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("load global meta: %v", err)
	}

	head := gm.GetHead()
	nodeStore, err := sylkdir.NewGlobalVersionNodeStore(sd, head)
	if err != nil {
		t.Fatalf("open global node store: %v", err)
	}
	defer nodeStore.Close()

	globalNodeCount := nodeStore.CountForVersion(head)
	if globalNodeCount == 0 {
		t.Fatal("global node store empty after delta commit")
	}
	t.Logf("Global state: version=%s, nodes=%d", head.String(), globalNodeCount)
}

func TestBootPipeline_DataPersisted(t *testing.T) {
	tmpDir := t.TempDir()

	testFiles := map[string]string{
		"app.go": `package app

func Run() error {
	return nil
}

type Server struct {
	Port int
}
`,
	}

	for name, content := range testFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := Boot(ctx, tmpDir)
	if err != nil {
		t.Fatalf("boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result)

	// Verify SylkDir state
	sd := sylkdir.New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("init sylkdir: %v", err)
	}

	gm := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("load global meta: %v", err)
	}

	head := gm.GetHead()
	if head.String() == "0.0.0" {
		t.Error("head version should not be 0.0.0 after boot commit")
	}

	// Verify nodes persisted
	nodeStore, err := sylkdir.NewGlobalVersionNodeStore(sd, head)
	if err != nil {
		t.Fatalf("open global node store: %v", err)
	}
	defer nodeStore.Close()

	nodes, err := nodeStore.ReadAll()
	if err != nil {
		t.Fatalf("read all nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("no nodes in global store after boot")
	}

	// Verify edges persisted
	edgeStore, err := sylkdir.NewGlobalVersionEdgeStore(sd, head)
	if err != nil {
		t.Fatalf("open global edge store: %v", err)
	}
	defer edgeStore.Close()

	edges, err := edgeStore.ReadAll()
	if err != nil {
		t.Fatalf("read all edges: %v", err)
	}
	if len(edges) == 0 {
		t.Error("no edges in global store after boot")
	}

	// Verify canonical index has file keys
	canonIdx := sylkdir.NewCanonicalKeyIndexFromSylkDir(sd)
	if err := canonIdx.Init(); err != nil {
		t.Fatalf("init canonical index: %v", err)
	}

	if canonIdx.Count() == 0 {
		t.Error("canonical index is empty after boot")
	}

	t.Logf("Persisted: %d nodes, %d edges, %d canonical keys, version %s",
		len(nodes), len(edges), canonIdx.Count(), result.CommitVersion)
}

func TestBootPipeline_NoChangesSkip(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ctx := context.Background()

	result1, err := Boot(ctx, tmpDir)
	if err != nil {
		t.Fatalf("first boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result1)
	if result1.NodesCreated == 0 {
		t.Fatal("first boot should create nodes")
	}

	// Wait for background file hashing to complete — change detection uses these hashes.
	result1.BackgroundHasher.Wait()

	// Second boot with no changes should skip
	result2, err := Boot(ctx, tmpDir)
	if err != nil {
		t.Fatalf("second boot failed: %v", err)
	}
	defer closeBackgroundIndexer(result2)

	if result2.FilesProcessed != 0 {
		t.Errorf("second boot with no changes should process 0 files, got %d", result2.FilesProcessed)
	}
	if result2.NodesCreated != 0 {
		t.Errorf("second boot with no changes should create 0 nodes, got %d", result2.NodesCreated)
	}

	t.Logf("First boot: %d nodes. Second boot (no changes): %d nodes, skipped=%v",
		result1.NodesCreated, result2.NodesCreated, result2.FilesProcessed == 0)
}

func findProjectRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}

	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == ".git" {
			continue
		}

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// closeBackgroundIndexer cancels and waits for the background indexer
// and background hasher goroutines to finish. Safe to call with nil result.
func closeBackgroundIndexer(result *PipelineResult) {
	if result == nil {
		return
	}
	if result.BackgroundIndexer != nil {
		result.BackgroundIndexer.Close()
	}
	if result.BackgroundHasher != nil {
		result.BackgroundHasher.Wait()
	}
}
