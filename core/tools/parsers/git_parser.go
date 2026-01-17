package parsers

import (
	"regexp"
	"strings"
)

type GitParser struct {
	statusPattern *regexp.Regexp
	diffPattern   *regexp.Regexp
	logPattern    *regexp.Regexp
}

func NewGitParser() *GitParser {
	return &GitParser{
		statusPattern: regexp.MustCompile(`^(.)(.)?\s+(.+)$`),
		diffPattern:   regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`),
		logPattern:    regexp.MustCompile(`^commit ([a-f0-9]+)`),
	}
}

type GitResult struct {
	Status *GitStatus
	Diff   *GitDiff
	Log    []GitCommit
}

type GitStatus struct {
	Staged    []string
	Unstaged  []string
	Untracked []string
}

type GitDiff struct {
	Files     []GitDiffFile
	Additions int
	Deletions int
}

type GitDiffFile struct {
	Path      string
	Additions int
	Deletions int
}

type GitCommit struct {
	Hash    string
	Author  string
	Message string
}

func (p *GitParser) Parse(stdout, stderr []byte) (any, error) {
	output := string(stdout)
	result := &GitResult{}

	if p.isStatusOutput(output) {
		result.Status = p.parseStatus(output)
	} else if p.isDiffOutput(output) {
		result.Diff = p.parseDiff(output)
	} else if p.isLogOutput(output) {
		result.Log = p.parseLog(output)
	}

	return result, nil
}

func (p *GitParser) isStatusOutput(output string) bool {
	return strings.Contains(output, "modified:") || strings.Contains(output, "new file:") || p.statusPattern.MatchString(output)
}

func (p *GitParser) isDiffOutput(output string) bool {
	return strings.HasPrefix(output, "diff --git")
}

func (p *GitParser) isLogOutput(output string) bool {
	return strings.HasPrefix(output, "commit ")
}

func (p *GitParser) parseStatus(output string) *GitStatus {
	status := &GitStatus{}
	for _, line := range strings.Split(output, "\n") {
		p.parseStatusLine(line, status)
	}
	return status
}

func (p *GitParser) parseStatusLine(line string, status *GitStatus) {
	if len(line) < 3 {
		return
	}

	matches := p.statusPattern.FindStringSubmatch(line)
	if matches == nil {
		return
	}

	idx, workTree := matches[1], matches[2]
	file := strings.TrimSpace(matches[3])
	p.categorizeStatusFile(idx, workTree, file, status)
}

func (p *GitParser) categorizeStatusFile(idx, workTree, file string, status *GitStatus) {
	if p.isUntracked(idx, workTree) {
		status.Untracked = append(status.Untracked, file)
	} else if p.isStaged(idx) {
		status.Staged = append(status.Staged, file)
	} else if p.isUnstaged(workTree) {
		status.Unstaged = append(status.Unstaged, file)
	}
}

func (p *GitParser) isUntracked(idx, workTree string) bool {
	return idx == "?" && workTree == "?"
}

func (p *GitParser) isStaged(idx string) bool {
	return idx != " " && idx != "?"
}

func (p *GitParser) isUnstaged(workTree string) bool {
	return workTree != " " && workTree != "?"
}

func (p *GitParser) parseDiff(output string) *GitDiff {
	diff := &GitDiff{}
	var currentFile *GitDiffFile

	for _, line := range strings.Split(output, "\n") {
		if newFile := p.tryParseDiffHeader(line); newFile != nil {
			currentFile = p.finishAndStartFile(diff, currentFile, newFile)
			continue
		}
		p.countDiffLine(line, currentFile, diff)
	}

	p.finishFile(diff, currentFile)
	return diff
}

func (p *GitParser) tryParseDiffHeader(line string) *GitDiffFile {
	matches := p.diffPattern.FindStringSubmatch(line)
	if matches == nil {
		return nil
	}
	return &GitDiffFile{Path: matches[2]}
}

func (p *GitParser) finishAndStartFile(diff *GitDiff, current, next *GitDiffFile) *GitDiffFile {
	p.finishFile(diff, current)
	return next
}

func (p *GitParser) finishFile(diff *GitDiff, file *GitDiffFile) {
	if file != nil {
		diff.Files = append(diff.Files, *file)
	}
}

func (p *GitParser) countDiffLine(line string, file *GitDiffFile, diff *GitDiff) {
	if file == nil {
		return
	}
	if p.isAdditionLine(line) {
		file.Additions++
		diff.Additions++
	} else if p.isDeletionLine(line) {
		file.Deletions++
		diff.Deletions++
	}
}

func (p *GitParser) isAdditionLine(line string) bool {
	return strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")
}

func (p *GitParser) isDeletionLine(line string) bool {
	return strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---")
}

func (p *GitParser) parseLog(output string) []GitCommit {
	var commits []GitCommit
	var current *GitCommit

	for _, line := range strings.Split(output, "\n") {
		if hash := p.tryParseCommitLine(line); hash != "" {
			current = p.finishAndStartCommit(&commits, current, hash)
			continue
		}
		p.parseCommitDetail(line, current)
	}

	p.finishCommit(&commits, current)
	return commits
}

func (p *GitParser) tryParseCommitLine(line string) string {
	matches := p.logPattern.FindStringSubmatch(line)
	if matches == nil {
		return ""
	}
	return matches[1]
}

func (p *GitParser) finishAndStartCommit(commits *[]GitCommit, current *GitCommit, hash string) *GitCommit {
	p.finishCommit(commits, current)
	return &GitCommit{Hash: hash}
}

func (p *GitParser) finishCommit(commits *[]GitCommit, commit *GitCommit) {
	if commit != nil {
		*commits = append(*commits, *commit)
	}
}

func (p *GitParser) parseCommitDetail(line string, commit *GitCommit) {
	if commit == nil {
		return
	}
	p.tryParseAuthor(line, commit)
	p.tryParseMessage(line, commit)
}

func (p *GitParser) tryParseAuthor(line string, commit *GitCommit) {
	if strings.HasPrefix(line, "Author:") {
		commit.Author = strings.TrimSpace(strings.TrimPrefix(line, "Author:"))
	}
}

func (p *GitParser) tryParseMessage(line string, commit *GitCommit) {
	if strings.HasPrefix(line, "    ") && commit.Message == "" {
		commit.Message = strings.TrimSpace(line)
	}
}
