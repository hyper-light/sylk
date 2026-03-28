package shared

import (
	"fmt"
	"strings"
)

type ResearchDepth string

const (
	ResearchDepthUnset         ResearchDepth = ""
	ResearchDepthMinimal       ResearchDepth = "minimal"
	ResearchDepthQuick         ResearchDepth = "quick"
	ResearchDepthStandard      ResearchDepth = "standard"
	ResearchDepthDeep          ResearchDepth = "deep"
	ResearchDepthComprehensive ResearchDepth = "comprehensive"
)

const ConsultationMetadataResearchDepthKey = "research_depth"

func NormalizeResearchDepth(raw string) ResearchDepth {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ResearchDepthMinimal):
		return ResearchDepthMinimal
	case string(ResearchDepthQuick):
		return ResearchDepthQuick
	case string(ResearchDepthStandard):
		return ResearchDepthStandard
	case string(ResearchDepthDeep):
		return ResearchDepthDeep
	case string(ResearchDepthComprehensive):
		return ResearchDepthComprehensive
	default:
		return ResearchDepthUnset
	}
}

func ResearchDepthEnumValues() []string {
	return []string{
		string(ResearchDepthMinimal),
		string(ResearchDepthQuick),
		string(ResearchDepthStandard),
		string(ResearchDepthDeep),
		string(ResearchDepthComprehensive),
	}
}

func EffectiveResearchDepth(raw string) ResearchDepth {
	if depth := NormalizeResearchDepth(raw); depth != ResearchDepthUnset {
		return depth
	}
	return ResearchDepthStandard
}

func ConsultationResearchDepth(metadata map[string]any) ResearchDepth {
	if len(metadata) == 0 {
		return ResearchDepthUnset
	}
	raw, ok := metadata[ConsultationMetadataResearchDepthKey]
	if !ok || raw == nil {
		return ResearchDepthUnset
	}
	return NormalizeResearchDepth(fmt.Sprint(raw))
}

func ConsultationMetadataWithResearchDepth(metadata map[string]any, depth string) map[string]any {
	normalized := NormalizeResearchDepth(depth)
	if normalized == ResearchDepthUnset {
		return CloneMetadataMap(metadata)
	}
	cloned := CloneMetadataMap(metadata)
	if cloned == nil {
		cloned = make(map[string]any, 1)
	}
	cloned[ConsultationMetadataResearchDepthKey] = string(normalized)
	return cloned
}

func CloneMetadataMap(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}
