package mitigations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// CodeVerifier provides verification capabilities for code-related claims.
type CodeVerifier interface {
	VerifyFileExists(path string) (bool, error)
	VerifyFunctionExists(file, funcName string) (bool, error)
	VerifyPatternExists(pattern string) (bool, float64, error)
}

// DefaultCodeVerifier implements basic file system verification.
type DefaultCodeVerifier struct {
	basePath string
}

// NewDefaultCodeVerifier creates a new DefaultCodeVerifier.
func NewDefaultCodeVerifier(basePath string) *DefaultCodeVerifier {
	return &DefaultCodeVerifier{basePath: basePath}
}

// VerifyFileExists checks if a file exists at the given path.
func (v *DefaultCodeVerifier) VerifyFileExists(path string) (bool, error) {
	fullPath := v.resolvePath(path)
	_, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat file: %w", err)
	}
	return true, nil
}

func (v *DefaultCodeVerifier) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(v.basePath, path)
}

// VerifyFunctionExists checks if a function exists in the given file.
func (v *DefaultCodeVerifier) VerifyFunctionExists(file, funcName string) (bool, error) {
	fullPath := v.resolvePath(file)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return false, fmt.Errorf("read file: %w", err)
	}
	return v.containsFunction(string(content), funcName), nil
}

func (v *DefaultCodeVerifier) containsFunction(content, funcName string) bool {
	pattern := fmt.Sprintf(`func\s+(\([^)]+\)\s+)?%s\s*\(`, regexp.QuoteMeta(funcName))
	matched, _ := regexp.MatchString(pattern, content)
	return matched
}

// VerifyPatternExists searches the codebase for a pattern.
func (v *DefaultCodeVerifier) VerifyPatternExists(pattern string) (bool, float64, error) {
	matchCount, totalFiles, err := v.countPatternMatches(pattern)
	if err != nil {
		return false, 0, err
	}

	if matchCount == 0 {
		return false, 0, nil
	}
	return true, v.computePatternConfidence(matchCount, totalFiles), nil
}

func (v *DefaultCodeVerifier) countPatternMatches(pattern string) (int, int, error) {
	counter := &patternCounter{verifier: v, pattern: pattern}
	err := filepath.Walk(v.basePath, counter.processFile)
	return counter.matchCount, counter.totalFiles, err
}

type patternCounter struct {
	verifier   *DefaultCodeVerifier
	pattern    string
	matchCount int
	totalFiles int
}

func (p *patternCounter) processFile(path string, info os.FileInfo, err error) error {
	if p.shouldSkip(err, info, path) {
		return nil
	}
	p.totalFiles++
	p.checkMatch(path)
	return nil
}

func (p *patternCounter) shouldSkip(err error, info os.FileInfo, path string) bool {
	return err != nil || info.IsDir() || !p.verifier.isCodeFile(path)
}

func (p *patternCounter) checkMatch(path string) {
	if found, _ := p.verifier.searchInFile(path, p.pattern); found {
		p.matchCount++
	}
}

func (v *DefaultCodeVerifier) isCodeFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	codeExts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true,
		".java": true, ".c": true, ".cpp": true, ".rs": true,
	}
	return codeExts[ext]
}

func (v *DefaultCodeVerifier) searchInFile(path, pattern string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return strings.Contains(string(content), pattern), nil
}

func (v *DefaultCodeVerifier) computePatternConfidence(matches, total int) float64 {
	if total == 0 {
		return 0
	}
	ratio := float64(matches) / float64(total)
	if ratio > 0.3 {
		return 0.9
	}
	if ratio > 0.1 {
		return 0.7
	}
	return 0.5
}

// VerifyNode performs comprehensive verification of a node.
func VerifyNode(
	ctx context.Context,
	node *vectorgraphdb.GraphNode,
	verifier CodeVerifier,
	db *vectorgraphdb.VectorGraphDB,
) (*VerificationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := runChecks(node, verifier, db)
	finalizeResult(result)

	return result, nil
}

func runChecks(node *vectorgraphdb.GraphNode, verifier CodeVerifier, db *vectorgraphdb.VectorGraphDB) *VerificationResult {
	result := &VerificationResult{Checks: make([]VerificationCheck, 0)}

	for _, checkFn := range selectChecksForNode(node) {
		check := checkFn(node, verifier, db)
		result.Checks = append(result.Checks, check)
		if !check.Passed {
			result.FailedChecks = append(result.FailedChecks, check.CheckType)
		}
	}
	return result
}

func finalizeResult(result *VerificationResult) {
	result.Confidence = computeOverallConfidence(result.Checks)
	result.Verified = result.Confidence >= 0.6
	result.ShouldQueue = shouldQueueForReview(result)
	if result.ShouldQueue {
		result.QueueReason = determineQueueReason(result)
	}
}

type checkFunc func(*vectorgraphdb.GraphNode, CodeVerifier, *vectorgraphdb.VectorGraphDB) VerificationCheck

func selectChecksForNode(node *vectorgraphdb.GraphNode) []checkFunc {
	switch node.Domain {
	case vectorgraphdb.DomainCode:
		return []checkFunc{checkFileReference, checkMetadataPresent}
	case vectorgraphdb.DomainHistory:
		return []checkFunc{checkMetadataPresent, checkTimestampValid}
	case vectorgraphdb.DomainAcademic:
		return []checkFunc{checkMetadataPresent, checkSourceURL}
	default:
		return []checkFunc{checkMetadataPresent}
	}
}

func checkFileReference(node *vectorgraphdb.GraphNode, verifier CodeVerifier, _ *vectorgraphdb.VectorGraphDB) VerificationCheck {
	check := VerificationCheck{
		CheckType: "file_reference",
		Target:    node.ID,
	}

	path, ok := node.Metadata["path"].(string)
	if !ok || path == "" {
		check.Passed = false
		check.Confidence = 0
		check.Details = "no file path in metadata"
		return check
	}

	exists, err := verifier.VerifyFileExists(path)
	if err != nil {
		check.Passed = false
		check.Confidence = 0
		check.Details = fmt.Sprintf("verification error: %v", err)
		return check
	}

	check.Passed = exists
	check.Confidence = boolToConfidence(exists)
	return check
}

func checkMetadataPresent(node *vectorgraphdb.GraphNode, _ CodeVerifier, _ *vectorgraphdb.VectorGraphDB) VerificationCheck {
	check := VerificationCheck{
		CheckType: "metadata_present",
		Target:    node.ID,
	}

	if len(node.Metadata) == 0 {
		check.Passed = false
		check.Confidence = 0.3
		check.Details = "empty metadata"
		return check
	}

	check.Passed = true
	check.Confidence = 0.8
	return check
}

func checkTimestampValid(node *vectorgraphdb.GraphNode, _ CodeVerifier, _ *vectorgraphdb.VectorGraphDB) VerificationCheck {
	check := VerificationCheck{
		CheckType: "timestamp_valid",
		Target:    node.ID,
	}

	check.Passed = !node.CreatedAt.IsZero()
	check.Confidence = boolToConfidence(check.Passed)
	return check
}

func checkSourceURL(node *vectorgraphdb.GraphNode, _ CodeVerifier, _ *vectorgraphdb.VectorGraphDB) VerificationCheck {
	check := VerificationCheck{
		CheckType: "source_url",
		Target:    node.ID,
	}

	url, ok := node.Metadata["url"].(string)
	if !ok || url == "" {
		check.Passed = false
		check.Confidence = 0.5
		check.Details = "no source URL"
		return check
	}

	check.Passed = strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	check.Confidence = boolToConfidence(check.Passed)
	return check
}

func boolToConfidence(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

func computeOverallConfidence(checks []VerificationCheck) float64 {
	if len(checks) == 0 {
		return 0.5
	}

	var sum float64
	for _, c := range checks {
		sum += c.Confidence
	}
	return sum / float64(len(checks))
}

func shouldQueueForReview(result *VerificationResult) bool {
	return result.Confidence >= 0.4 && result.Confidence < 0.6
}

func determineQueueReason(result *VerificationResult) string {
	if len(result.FailedChecks) > 0 {
		return fmt.Sprintf("failed checks: %s", strings.Join(result.FailedChecks, ", "))
	}
	return "low confidence score"
}
