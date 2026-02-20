package git

import (
	"regexp"
	"strings"

	fdiff "github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// SecretFinding describes a potential credential found in staged diffs.
type SecretFinding struct {
	Path        string
	Line        int
	PatternName string
	Snippet     string // truncated to ~60 chars
}

// secretPattern pairs a compiled regex with a human-readable name.
type secretPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// secretPatterns is the compiled pattern table for credential detection.
// Each regex targets added lines in staged diffs.
var secretPatterns = []secretPattern{
	{Name: "AWS Access Key", Pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{Name: "AWS Secret Key", Pattern: regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]\s*\S{20,}`)},
	{Name: "GitHub Token", Pattern: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,}`)},
	{Name: "Private Key", Pattern: regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`)},
	{Name: "Generic API Key", Pattern: regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret[_-]?key)\s*[:=]\s*["']?\S{16,}`)},
	{Name: "Generic Token", Pattern: regexp.MustCompile(`(?i)(token|bearer)\s*[:=]\s*["']?[A-Za-z0-9_\-\.]{20,}`)},
}

// snippetMaxLen caps the length of secret finding snippets.
const snippetMaxLen = 60

// ScanStagedSecrets scans added lines in staged diffs (HEAD→index) for
// credential patterns. Only files in the provided paths set are checked.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) ScanStagedSecrets(paths []string) ([]SecretFinding, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	if len(paths) == 0 {
		return nil, nil
	}

	headTree, err := c.headTreeOrNil()
	if err != nil {
		return nil, err
	}

	idx, err := c.repo.Storer.Index()
	if err != nil {
		return nil, err
	}

	indexTreeHash, err := buildTreeFromIndex(c.repo.Storer, idx)
	if err != nil {
		return nil, err
	}

	indexTreeObj, err := object.GetTree(c.repo.Storer, indexTreeHash)
	if err != nil {
		return nil, err
	}

	changes, err := object.DiffTree(headTree, indexTreeObj)
	if err != nil {
		return nil, err
	}

	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	patch, err := changes.Patch()
	if err != nil {
		return nil, err
	}

	var findings []SecretFinding
	for _, fp := range patch.FilePatches() {
		from, to := fp.Files()
		path := diffFilePath(from, to)
		if path == "" {
			continue
		}
		if _, ok := pathSet[path]; !ok {
			continue
		}
		findings = append(findings, scanChunksForSecrets(fp, path)...)
	}

	return findings, nil
}

// headTreeOrNil returns the HEAD commit's tree, or nil for empty repos.
func (c *GitClient) headTreeOrNil() (*object.Tree, error) {
	head, err := c.repo.Head()
	if err != nil {
		return nil, nil // Empty repo.
	}
	commit, err := c.repo.CommitObject(head.Hash())
	if err != nil {
		return nil, err
	}
	return commit.Tree()
}

// diffFilePath extracts the file path from a diff's from/to files.
func diffFilePath(from, to fdiff.File) string {
	if to != nil {
		return to.Path()
	}
	if from != nil {
		return from.Path()
	}
	return ""
}

// scanChunksForSecrets scans added lines in a file patch for secret patterns.
func scanChunksForSecrets(fp fdiff.FilePatch, path string) []SecretFinding {
	var findings []SecretFinding
	lineNum := 0

	for _, chunk := range fp.Chunks() {
		lines := strings.Split(chunk.Content(), "\n")
		for _, line := range lines {
			lineNum++
			if chunk.Type() != fdiff.Add {
				continue
			}
			for _, sp := range secretPatterns {
				if sp.Pattern.MatchString(line) {
					findings = append(findings, SecretFinding{
						Path:        path,
						Line:        lineNum,
						PatternName: sp.Name,
						Snippet:     truncateSnippet(strings.TrimSpace(line)),
					})
					break // One finding per line is sufficient.
				}
			}
		}
	}

	return findings
}

// truncateSnippet limits a string to snippetMaxLen characters.
func truncateSnippet(s string) string {
	if len(s) <= snippetMaxLen {
		return s
	}
	return s[:snippetMaxLen-3] + "..."
}
