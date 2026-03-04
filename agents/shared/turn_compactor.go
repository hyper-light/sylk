package shared

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/tools"
)

// TurnGroup represents a single LLM turn: one assistant message (with tool
// calls) followed by N tool-result messages.
type TurnGroup struct {
	AssistantIdx int
	ToolIdxs     []int
	Tokens       int
}

// IdentifyTurnGroups walks messages and groups each assistant message (that
// contains tool calls) with its subsequent tool-result messages. Groups are
// returned oldest-first. Messages before the first assistant message are
// protected (initial user prompt).
func IdentifyTurnGroups(messages []providers.Message, counter providers.TokenCounter) []TurnGroup {
	var groups []TurnGroup
	i := 0
	n := len(messages)

	for i < n {
		msg := messages[i]
		if msg.Role == providers.RoleAssistant && len(msg.ToolCalls) > 0 {
			g := TurnGroup{AssistantIdx: i}
			assistantTokens, _ := counter.Count([]providers.Message{msg})
			g.Tokens = assistantTokens

			// Collect subsequent tool-result messages.
			j := i + 1
			for j < n && messages[j].Role == providers.RoleTool {
				toolTokens, _ := counter.Count([]providers.Message{messages[j]})
				g.Tokens += toolTokens
				g.ToolIdxs = append(g.ToolIdxs, j)
				j++
			}
			groups = append(groups, g)
			i = j
			continue
		}
		i++
	}
	return groups
}

// CompactTurnGroups replaces tool-result message content in the oldest turn
// groups with compact summaries. The last preserveRecent groups are left
// untouched. Assistant messages are never modified. Returns estimated tokens
// freed.
func CompactTurnGroups(
	messages []providers.Message,
	groups []TurnGroup,
	preserveRecent int,
	counter providers.TokenCounter,
) int {
	compactable := len(groups) - preserveRecent
	if compactable <= 0 {
		return 0
	}

	freed := 0
	for _, g := range groups[:compactable] {
		for _, idx := range g.ToolIdxs {
			msg := &messages[idx]
			if isAlreadyCompacted(msg.Content) {
				continue
			}
			oldTokens, _ := counter.CountText(msg.Content)
			msg.Content = SummarizeToolResult(msg.ToolName, msg.Content)
			newTokens, _ := counter.CountText(msg.Content)
			freed += oldTokens - newTokens
		}
	}
	return freed
}

// EvictTurnGroup removes an entire turn group (assistant + tool results) from
// the message slice and returns the new slice. Indices are processed
// high-to-low to avoid shifting issues.
func EvictTurnGroup(messages []providers.Message, group TurnGroup) []providers.Message {
	// Collect all indices to remove.
	remove := make(map[int]struct{}, 1+len(group.ToolIdxs))
	remove[group.AssistantIdx] = struct{}{}
	for _, idx := range group.ToolIdxs {
		remove[idx] = struct{}{}
	}

	result := make([]providers.Message, 0, len(messages)-len(remove))
	for i, msg := range messages {
		if _, skip := remove[i]; !skip {
			result = append(result, msg)
		}
	}
	return result
}

// SummarizeToolResult produces a compact summary of a tool result. It
// preserves lines matched by the importance detector (errors, warnings,
// file:line references) and produces a header with line/error/warning counts.
func SummarizeToolResult(toolName, content string) string {
	if len(content) == 0 {
		return "[" + toolName + ": empty]"
	}

	detector := tools.NewImportanceDetector(tools.DefaultImportancePatterns())
	lines := strings.Split(content, "\n")

	var important []string
	var errCount, warnCount int
	for _, line := range lines {
		if detector.IsImportant(line) {
			important = append(important, line)
			lower := strings.ToLower(line)
			if strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "panic") {
				errCount++
			}
			if strings.Contains(lower, "warning") {
				warnCount++
			}
		}
	}

	header := fmt.Sprintf("[%s: %d lines", toolName, len(lines))
	if errCount > 0 {
		header += fmt.Sprintf(", %d errors", errCount)
	}
	if warnCount > 0 {
		header += fmt.Sprintf(", %d warnings", warnCount)
	}
	header += "]"

	if len(important) == 0 {
		return header
	}

	// Cap preserved lines to avoid a summary larger than the original.
	const maxPreserved = 30
	if len(important) > maxPreserved {
		important = important[:maxPreserved]
	}

	return header + "\n" + strings.Join(important, "\n")
}

const compactedPrefix = "["

func isAlreadyCompacted(content string) bool {
	return strings.HasPrefix(content, compactedPrefix) && strings.Contains(content, " lines")
}
