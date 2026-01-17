package lsp

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/detect"
)

type LSPSelector struct {
	servers []*LanguageServerDefinition
}

func NewLSPSelector() *LSPSelector {
	return &LSPSelector{
		servers: BuiltinServers,
	}
}

func NewLSPSelectorWith(servers []*LanguageServerDefinition) *LSPSelector {
	return &LSPSelector{
		servers: servers,
	}
}

func (s *LSPSelector) SelectServer(root, filePath string) *LanguageServerDefinition {
	servers := s.SelectServers(root, filePath)
	if len(servers) == 0 {
		return nil
	}
	return servers[0]
}

func (s *LSPSelector) SelectServers(root, filePath string) []*LanguageServerDefinition {
	ext := normalizeExt(filePath)
	var matches []*LanguageServerDefinition

	for _, srv := range s.servers {
		if matchesExt(srv, ext) && isServerEnabled(srv) && hasRootMarker(srv, root) {
			matches = append(matches, srv)
		}
	}

	return matches
}

func (s *LSPSelector) SelectServerByLanguageID(langID string) *LanguageServerDefinition {
	servers := s.SelectServersByLanguageID(langID)
	if len(servers) == 0 {
		return nil
	}
	return servers[0]
}

func (s *LSPSelector) SelectServersByLanguageID(langID string) []*LanguageServerDefinition {
	var matches []*LanguageServerDefinition

	for _, srv := range s.servers {
		if matchesLanguageID(srv, langID) && isServerEnabled(srv) {
			matches = append(matches, srv)
		}
	}

	return matches
}

func (s *LSPSelector) DetectServers(root, filePath string) []DetectionResult {
	ext := normalizeExt(filePath)
	var results []DetectionResult

	for _, srv := range s.servers {
		if !matchesExt(srv, ext) {
			continue
		}
		result := buildServerDetection(srv, root)
		results = append(results, result)
	}

	sortDetectionByConfidence(results)
	return results
}

func normalizeExt(filePath string) string {
	return strings.ToLower(filepath.Ext(filePath))
}

func matchesExt(srv *LanguageServerDefinition, ext string) bool {
	for _, supported := range srv.Extensions {
		if supported == ext {
			return true
		}
	}
	return false
}

func matchesLanguageID(srv *LanguageServerDefinition, langID string) bool {
	for _, supported := range srv.LanguageIDs {
		if supported == langID {
			return true
		}
	}
	return false
}

func isServerEnabled(srv *LanguageServerDefinition) bool {
	if srv.Enabled == nil {
		return true
	}
	return srv.Enabled()
}

func hasRootMarker(srv *LanguageServerDefinition, root string) bool {
	if len(srv.RootMarkers) == 0 {
		return true
	}
	return detect.FileExists(root, srv.RootMarkers...)
}

func buildServerDetection(srv *LanguageServerDefinition, root string) DetectionResult {
	available := isServerEnabled(srv)
	hasMarker := hasRootMarker(srv, root)
	confidence := computeConfidence(available, hasMarker)

	return DetectionResult{
		ServerID:   srv.ID,
		Confidence: confidence,
		Reason:     computeReason(available, hasMarker),
		RootPath:   root,
	}
}

func computeConfidence(available, hasMarker bool) float64 {
	if !available {
		return 0.0
	}
	if hasMarker {
		return 1.0
	}
	return 0.5
}

func computeReason(available, hasMarker bool) string {
	if !available {
		return "binary not found"
	}
	if hasMarker {
		return "binary available and root marker found"
	}
	return "binary available"
}

func sortDetectionByConfidence(results []DetectionResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Confidence > results[j].Confidence
	})
}
