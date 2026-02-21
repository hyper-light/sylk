package architect

func emptyCodebasePatterns() *CodebasePatterns {
	return &CodebasePatterns{
		Patterns:           []PatternInfo{},
		RelevantFiles:      []string{},
		ExistingComponents: []string{},
	}
}

func codebasePatternsFromEvidence(evidence *ConsultationEvidence) *CodebasePatterns {
	if evidence == nil {
		return emptyCodebasePatterns()
	}
	if typed, ok := evidence.Data.(*CodebasePatterns); ok && typed != nil {
		return typed
	}
	payload, ok := evidence.Data.(map[string]any)
	if !ok {
		return emptyCodebasePatterns()
	}
	return parseCodebasePatterns(payload)
}

func parseCodebasePatterns(payload map[string]any) *CodebasePatterns {
	patterns := emptyCodebasePatterns()
	patterns.RelevantFiles = parseStringSlice(payload["relevant_files"])
	patterns.ExistingComponents = parseStringSlice(payload["existing_components"])
	patterns.Patterns = parsePatternInfos(payload["patterns"])
	return patterns
}

func parsePatternInfos(value any) []PatternInfo {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]PatternInfo, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, PatternInfo{
			Name:        stringValue(entry["name"]),
			Description: stringValue(entry["description"]),
			Example:     stringValue(entry["example"]),
			FilePath:    stringValue(entry["file_path"]),
			Category:    stringValue(entry["category"]),
		})
	}
	return result
}

func parseStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

