package git

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	fdiff "github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	godiff "github.com/go-git/go-git/v5/utils/diff"

	dmp "github.com/sergi/go-diff/diffmatchpatch"
)

// =============================================================================
// Diff status constants
// =============================================================================

const (
	DiffStatusAdded    = "A"
	DiffStatusModified = "M"
	DiffStatusDeleted  = "D"
	DiffStatusRenamed  = "R"
	DiffStatusCopied   = "C"
)

// hunkHeaderRegex matches diff hunk headers like "@@ -1,3 +1,4 @@".
var hunkHeaderRegex = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)

// binaryDetectLen is the number of leading bytes inspected for NUL to
// classify a file as binary. Matches git's heuristic.
const binaryDetectLen = 8000

// =============================================================================
// Commit-to-Commit Diff
// =============================================================================

// GetDiff returns diff between two commits.
func (c *GitClient) GetDiff(fromHash, toHash string) ([]FileDiff, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	fromTree, err := c.resolveTreeFromRef(fromHash)
	if err != nil {
		return nil, err
	}

	toTree, err := c.resolveTreeFromRef(toHash)
	if err != nil {
		return nil, err
	}

	changes, err := object.DiffTreeWithOptions(
		context.Background(), fromTree, toTree, object.DefaultDiffTreeOptions,
	)
	if err != nil {
		return nil, err
	}

	patch, err := changes.Patch()
	if err != nil {
		return nil, err
	}

	return fileDiffsFromPatch(patch), nil
}

// =============================================================================
// Working Tree Diff (index vs working directory)
// =============================================================================

// GetWorkingTreeDiff returns diff of unstaged changes.
func (c *GitClient) GetWorkingTreeDiff() ([]FileDiff, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return nil, err
	}

	status, err := wt.Status()
	if err != nil {
		return nil, err
	}

	idx, err := c.repo.Storer.Index()
	if err != nil {
		return nil, err
	}
	idxMap := indexEntryMap(idx)

	var diffs []FileDiff
	for path, fs := range status {
		if fs.Worktree != gogit.Modified && fs.Worktree != gogit.Deleted {
			continue
		}

		fd := c.workingTreeFileDiff(path, fs.Worktree, idxMap)
		diffs = append(diffs, fd)
	}

	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Path < diffs[j].Path })
	return diffs, nil
}

// workingTreeFileDiff builds a FileDiff for a single working tree change.
func (c *GitClient) workingTreeFileDiff(path string, wtStatus gogit.StatusCode, idxMap map[string]*index.Entry) FileDiff {
	fd := FileDiff{Path: path}

	switch wtStatus {
	case gogit.Modified:
		fd.Status = DiffStatusModified
	case gogit.Deleted:
		fd.Status = DiffStatusDeleted
	}

	oldContent := readBlobFromIndexMap(c.repo.Storer, idxMap, path)
	var newContent string
	if wtStatus != gogit.Deleted {
		newContent, _ = readDiskFile(c.repoPath, path)
	}

	if isBinary(oldContent) || isBinary(newContent) {
		fd.Binary = true
		return fd
	}

	fd.Hunks = buildHunksFromText(path, oldContent, newContent)
	fd.Additions, fd.Deletions = countChanges(fd.Hunks)
	return fd
}

// =============================================================================
// Staged Diff (HEAD vs index)
// =============================================================================

// GetStagedDiff returns diff of staged changes.
func (c *GitClient) GetStagedDiff() ([]FileDiff, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return nil, err
	}

	status, err := wt.Status()
	if err != nil {
		return nil, err
	}

	headTree, _ := c.headTree() // nil for empty repos

	idx, err := c.repo.Storer.Index()
	if err != nil {
		return nil, err
	}
	idxMap := indexEntryMap(idx)

	var diffs []FileDiff
	for path, fs := range status {
		if fs.Staging == gogit.Unmodified || fs.Staging == gogit.Untracked {
			continue
		}

		fd := c.stagedFileDiff(path, fs.Staging, headTree, idxMap)
		diffs = append(diffs, fd)
	}

	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Path < diffs[j].Path })
	return diffs, nil
}

// stagedFileDiff builds a FileDiff for a single staged change.
func (c *GitClient) stagedFileDiff(path string, stagingStatus gogit.StatusCode, headTree *object.Tree, idxMap map[string]*index.Entry) FileDiff {
	fd := FileDiff{Path: path}

	switch stagingStatus {
	case gogit.Added:
		fd.Status = DiffStatusAdded
	case gogit.Modified:
		fd.Status = DiffStatusModified
	case gogit.Deleted:
		fd.Status = DiffStatusDeleted
	case gogit.Renamed:
		fd.Status = DiffStatusRenamed
	case gogit.Copied:
		fd.Status = DiffStatusCopied
	}

	var oldContent string
	if headTree != nil && stagingStatus != gogit.Added {
		oldContent = readBlobFromTree(headTree, path)
	}

	var newContent string
	if stagingStatus != gogit.Deleted {
		newContent = readBlobFromIndexMap(c.repo.Storer, idxMap, path)
	}

	if isBinary(oldContent) || isBinary(newContent) {
		fd.Binary = true
		return fd
	}

	fd.Hunks = buildHunksFromText(path, oldContent, newContent)
	fd.Additions, fd.Deletions = countChanges(fd.Hunks)
	return fd
}

// =============================================================================
// Single File Diff
// =============================================================================

// GetFileDiff returns diff for a specific file between commits.
// Returns nil if the file was not changed.
func (c *GitClient) GetFileDiff(path, fromHash, toHash string) (*FileDiff, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getFileDiffInternal(path, fromHash, toHash)
}

// getFileDiffInternal performs the file diff without locking.
func (c *GitClient) getFileDiffInternal(path, fromHash, toHash string) (*FileDiff, error) {
	fromTree, err := c.resolveTreeFromRef(fromHash)
	if err != nil {
		return nil, err
	}

	toTree, err := c.resolveTreeFromRef(toHash)
	if err != nil {
		return nil, err
	}

	changes, err := object.DiffTreeWithOptions(
		context.Background(), fromTree, toTree, object.DefaultDiffTreeOptions,
	)
	if err != nil {
		return nil, err
	}

	// Find the change for the specified path (check both From and To names
	// to handle renames).
	for _, ch := range changes {
		if ch.To.Name != path && ch.From.Name != path {
			continue
		}

		patch, err := ch.Patch()
		if err != nil {
			return nil, err
		}

		fps := patch.FilePatches()
		if len(fps) == 0 {
			return nil, nil
		}

		fd := fileDiffFromFilePatch(fps[0])
		return &fd, nil
	}

	return nil, nil
}

// =============================================================================
// Patch → FileDiff Conversion
// =============================================================================

// fileDiffsFromPatch converts a go-git Patch to a slice of FileDiff.
func fileDiffsFromPatch(patch *object.Patch) []FileDiff {
	fps := patch.FilePatches()
	diffs := make([]FileDiff, 0, len(fps))

	for _, fp := range fps {
		diffs = append(diffs, fileDiffFromFilePatch(fp))
	}

	return diffs
}

// fileDiffFromFilePatch converts a single go-git FilePatch to our FileDiff.
func fileDiffFromFilePatch(fp fdiff.FilePatch) FileDiff {
	from, to := fp.Files()

	fd := FileDiff{
		Binary: fp.IsBinary(),
	}

	switch {
	case from == nil && to != nil:
		fd.Status = DiffStatusAdded
		fd.Path = to.Path()
	case from != nil && to == nil:
		fd.Status = DiffStatusDeleted
		fd.Path = from.Path()
	case from != nil && to != nil:
		fd.Path = to.Path()
		if from.Path() != to.Path() {
			fd.Status = DiffStatusRenamed
			fd.OldPath = from.Path()
		} else {
			fd.Status = DiffStatusModified
		}
	}

	if !fp.IsBinary() {
		fd.Hunks = hunksFromFilePatch(fp)
		fd.Additions, fd.Deletions = countChanges(fd.Hunks)
	}

	return fd
}

// hunksFromFilePatch encodes a single FilePatch as unified diff and parses
// it back into our DiffHunk representation. This reuses go-git's battle-tested
// UnifiedEncoder and our existing hunk parser.
func hunksFromFilePatch(fp fdiff.FilePatch) []DiffHunk {
	if len(fp.Chunks()) == 0 {
		return nil
	}

	p := &singlePatch{fp: fp}
	var buf bytes.Buffer
	enc := fdiff.NewUnifiedEncoder(&buf, fdiff.DefaultContextLines)
	if err := enc.Encode(p); err != nil {
		return nil
	}

	return parseHunksFromOutput(buf.String())
}

// =============================================================================
// Text Diff Helpers
// =============================================================================

// buildHunksFromText generates diff hunks from raw old/new file content using
// the Myers diff algorithm (via go-git's diff.Do) and the UnifiedEncoder.
func buildHunksFromText(path, oldContent, newContent string) []DiffHunk {
	if oldContent == newContent {
		return nil
	}

	diffs := godiff.Do(oldContent, newContent)
	chunks := chunksFromDiffs(diffs)

	if len(chunks) == 0 {
		return nil
	}

	// Build a synthetic FilePatch so we can use the UnifiedEncoder.
	var from, to fdiff.File
	if oldContent != "" {
		from = &localFile{path: path, mode: filemode.Regular}
	}
	if newContent != "" {
		to = &localFile{path: path, mode: filemode.Regular}
	}

	fp := &localFilePatch{from: from, to: to, chunks: chunks}
	return hunksFromFilePatch(fp)
}

// chunksFromDiffs converts diffmatchpatch diffs to fdiff.Chunk objects.
func chunksFromDiffs(diffs []dmp.Diff) []fdiff.Chunk {
	chunks := make([]fdiff.Chunk, 0, len(diffs))
	for _, d := range diffs {
		var op fdiff.Operation
		switch d.Type {
		case dmp.DiffEqual:
			op = fdiff.Equal
		case dmp.DiffInsert:
			op = fdiff.Add
		case dmp.DiffDelete:
			op = fdiff.Delete
		}
		chunks = append(chunks, &localChunk{content: d.Text, op: op})
	}
	return chunks
}

// =============================================================================
// Tree & Blob Helpers
// =============================================================================

// resolveTreeFromRef resolves a commit reference to its tree.
func (c *GitClient) resolveTreeFromRef(ref string) (*object.Tree, error) {
	commit, err := c.resolveCommit(ref)
	if err != nil {
		return nil, err
	}
	return commit.Tree()
}

// headTree returns the HEAD commit's tree. Returns (nil, nil) for empty repos.
func (c *GitClient) headTree() (*object.Tree, error) {
	ref, err := c.repo.Head()
	if err != nil {
		return nil, nil
	}

	commit, err := c.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, err
	}

	return commit.Tree()
}

// readBlobFromTree reads file content from a tree. Returns "" if not found.
func readBlobFromTree(tree *object.Tree, path string) string {
	if tree == nil {
		return ""
	}
	f, err := tree.File(path)
	if err != nil {
		return ""
	}
	content, err := f.Contents()
	if err != nil {
		return ""
	}
	return content
}

// readBlobFromIndexMap reads file content from the index via a pre-built map.
// Returns "" if the entry is not found.
func readBlobFromIndexMap(storer storer.EncodedObjectStorer, idxMap map[string]*index.Entry, path string) string {
	entry, ok := idxMap[path]
	if !ok {
		return ""
	}
	return readBlobByHash(storer, entry.Hash)
}

// readBlobByHash reads blob content by its hash. Returns "" on error.
func readBlobByHash(storer storer.EncodedObjectStorer, hash plumbing.Hash) string {
	blob, err := object.GetBlob(storer, hash)
	if err != nil {
		return ""
	}
	reader, err := blob.Reader()
	if err != nil {
		return ""
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return ""
	}
	return string(data)
}

// readDiskFile reads file content from the working directory.
func readDiskFile(repoPath, path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoPath, path))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// indexEntryMap builds a lookup map from path → index entry.
func indexEntryMap(idx *index.Index) map[string]*index.Entry {
	m := make(map[string]*index.Entry, len(idx.Entries))
	for _, e := range idx.Entries {
		m[e.Name] = e
	}
	return m
}

// isBinary reports whether content is binary by checking for NUL bytes
// in the first binaryDetectLen bytes. Matches git's heuristic.
func isBinary(content string) bool {
	end := len(content)
	if end > binaryDetectLen {
		end = binaryDetectLen
	}
	return strings.Contains(content[:end], "\x00")
}

// =============================================================================
// Local fdiff Interface Implementations
// =============================================================================

// singlePatch wraps a single FilePatch as a Patch for encoding.
type singlePatch struct{ fp fdiff.FilePatch }

func (p *singlePatch) FilePatches() []fdiff.FilePatch { return []fdiff.FilePatch{p.fp} }
func (p *singlePatch) Message() string                { return "" }

// localChunk implements fdiff.Chunk.
type localChunk struct {
	content string
	op      fdiff.Operation
}

func (c *localChunk) Content() string      { return c.content }
func (c *localChunk) Type() fdiff.Operation { return c.op }

// localFile implements fdiff.File.
type localFile struct {
	hash plumbing.Hash
	mode filemode.FileMode
	path string
}

func (f *localFile) Hash() plumbing.Hash      { return f.hash }
func (f *localFile) Mode() filemode.FileMode   { return f.mode }
func (f *localFile) Path() string              { return f.path }

// localFilePatch implements fdiff.FilePatch.
type localFilePatch struct {
	isBinary   bool
	from, to   fdiff.File
	chunks     []fdiff.Chunk
}

func (fp *localFilePatch) IsBinary() bool                  { return fp.isBinary }
func (fp *localFilePatch) Files() (fdiff.File, fdiff.File) { return fp.from, fp.to }
func (fp *localFilePatch) Chunks() []fdiff.Chunk           { return fp.chunks }

// =============================================================================
// Hunk Parsing (retained from original — used by all diff paths)
// =============================================================================

// parseHunksFromOutput parses unified diff output into a slice of hunks.
func parseHunksFromOutput(output string) []DiffHunk {
	var hunks []DiffHunk
	var currentHunk *DiffHunk

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		currentHunk, hunks = processHunkLine(scanner.Text(), currentHunk, hunks)
	}

	return appendFinalHunk(hunks, currentHunk)
}

// processHunkLine processes a single line during hunk parsing.
func processHunkLine(line string, current *DiffHunk, hunks []DiffHunk) (*DiffHunk, []DiffHunk) {
	if hunk := parseHunkHeader(line); hunk != nil {
		hunks = appendFinalHunk(hunks, current)
		return hunk, hunks
	}

	if current != nil && isHunkContentLine(line) {
		current.Lines = append(current.Lines, line)
	}

	return current, hunks
}

// appendFinalHunk appends the current hunk to the list if it exists.
func appendFinalHunk(hunks []DiffHunk, current *DiffHunk) []DiffHunk {
	if current != nil {
		return append(hunks, *current)
	}
	return hunks
}

// parseHunkHeader parses a hunk header line.
func parseHunkHeader(line string) *DiffHunk {
	matches := hunkHeaderRegex.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}

	return &DiffHunk{
		OldStart: parseIntOrDefault(matches[1], 1),
		OldLines: parseIntOrDefault(matches[2], 1),
		NewStart: parseIntOrDefault(matches[3], 1),
		NewLines: parseIntOrDefault(matches[4], 1),
	}
}

// parseIntOrDefault parses an integer string or returns a default value.
func parseIntOrDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return n
}

// hunkContentPrefixes contains valid prefix characters for hunk content lines.
var hunkContentPrefixes = map[byte]bool{
	'+':  true,
	'-':  true,
	' ':  true,
	'\\': true,
}

// isHunkContentLine checks if a line is part of a hunk's content.
func isHunkContentLine(line string) bool {
	if len(line) == 0 {
		return false
	}
	return hunkContentPrefixes[line[0]]
}

// countChanges counts additions and deletions from hunk lines.
func countChanges(hunks []DiffHunk) (additions, deletions int) {
	for _, hunk := range hunks {
		a, d := countHunkChanges(hunk)
		additions += a
		deletions += d
	}
	return
}

// countHunkChanges counts additions and deletions in a single hunk.
func countHunkChanges(hunk DiffHunk) (additions, deletions int) {
	for _, line := range hunk.Lines {
		if len(line) > 0 && line[0] == '+' {
			additions++
		}
		if len(line) > 0 && line[0] == '-' {
			deletions++
		}
	}
	return
}
