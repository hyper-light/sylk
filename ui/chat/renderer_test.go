package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

func TestRenderEntry_ThinkingPhaseWrapsLongText(t *testing.T) {
	entry := &ChatEntry{
		ID:           "e1",
		Timestamp:    time.Now(),
		Source:       SourceAgent,
		AgentType:    "guide",
		Streaming:    true,
		Content:      "",
		ThinkingText: "Consulting docs...\nwith multiline thought updates that should wrap properly",
	}

	lines, _ := RenderEntry(entry, 24, theme.DefaultDark(), nil)
	// Header (1) + wrapped thinking lines (>1) + spacer (1).
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (header + wrapped thinking + spacer), got %d", len(lines))
	}
	for i, line := range lines {
		if strings.Contains(line, "\n") || strings.Contains(line, "\r") {
			t.Fatalf("line %d contains raw newline: %q", i, line)
		}
	}
}

func TestRenderEntry_ThinkingPhaseNormalizesWhitespace(t *testing.T) {
	entry := &ChatEntry{
		ID:           "e2",
		Timestamp:    time.Now(),
		Source:       SourceAgent,
		AgentType:    "guide",
		Streaming:    true,
		Content:      "",
		ThinkingText: "line one\r\nline\t two",
	}

	lines, _ := RenderEntry(entry, 80, theme.DefaultDark(), nil)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + thinking + spacer), got %d", len(lines))
	}
	if !strings.Contains(lines[1], "line one line two") {
		t.Fatalf("expected normalized whitespace in thinking line, got %q", lines[1])
	}
}

func TestBadgeLabel_HumanizesPipelineCompositeAgentID(t *testing.T) {
	entry := &ChatEntry{
		Source:    SourceAgent,
		AgentType: "task_auth_checkout:inspector-pipeline",
	}

	if got := badgeLabel(entry); got != "Auth Checkout: Inspector" {
		t.Fatalf("badgeLabel = %q, want %q", got, "Auth Checkout: Inspector")
	}
	if got := badgeAgentType(entry); got != "inspector-pipeline" {
		t.Fatalf("badgeAgentType = %q, want %q", got, "inspector-pipeline")
	}
}

func TestBadgeLabel_UsesCanonicalPipelineAgentIDWhenTypeIsSemantic(t *testing.T) {
	entry := &ChatEntry{
		Source:    SourceAgent,
		AgentType: "inspector-pipeline",
		AgentID:   "task_payment_retry:inspector-pipeline",
	}

	if got := badgeLabel(entry); got != "Payment Retry: Inspector" {
		t.Fatalf("badgeLabel = %q, want %q", got, "Payment Retry: Inspector")
	}
}

func TestBadgeLabel_PrefersTaskMetadataOverRawPipelineIdentity(t *testing.T) {
	entry := &ChatEntry{
		Source:    SourceAgent,
		AgentType: "inspector-pipeline",
		AgentID:   "task_12__inspector-pipeline",
		TaskName:  "Payment retry",
		TaskSlug:  "payment_retry",
	}

	if got := badgeLabel(entry); got != "Payment Retry: Inspector" {
		t.Fatalf("badgeLabel = %q, want %q", got, "Payment Retry: Inspector")
	}
	if got := badgeAgentType(entry); got != "inspector-pipeline" {
		t.Fatalf("badgeAgentType = %q, want %q", got, "inspector-pipeline")
	}
}

func TestBadgeLabel_DropsNumericOnlyLegacyTaskScopedPipelinePrefix(t *testing.T) {
	entry := &ChatEntry{
		Source:    SourceAgent,
		AgentType: "task_12__inspector-pipeline",
		AgentID:   "task_12__inspector-pipeline",
	}

	if got := badgeLabel(entry); got != "Inspector" {
		t.Fatalf("badgeLabel = %q, want %q", got, "Inspector")
	}
	if got := badgeAgentType(entry); got != "inspector-pipeline" {
		t.Fatalf("badgeAgentType = %q, want %q", got, "inspector-pipeline")
	}
}

// TestStreamRenderHeightMatch demonstrates a height mismatch between the
// standard renderContent path and the streaming renderStreamingEntry path.
//
// The streaming path splits content at blank-line boundaries, renders the
// stable prefix and trailing tail independently, and merges them. Because
// renderMarkdownContent trims trailing empty lines from each fragment, the
// blank line(s) at the split boundary are lost. The merged output therefore
// has fewer lines than the single-pass render, causing the viewport height
// index to clip content.
func TestStreamRenderHeightMatch(t *testing.T) {
	th := theme.DefaultDark()
	width := 80
	bodyStyle := th.AgentMessage

	content := "Nice — landing pages are good for RSCs.\n" +
		"\n" +
		"A few things:\n" +
		"\n" +
		"**Content & structure:**\n" +
		"- What's the product?\n" +
		"- Do you have the content already?\n" +
		"\n" +
		"**The linked page:**\n" +
		"- What does the second page contain?\n" +
		"\n" +
		"**Interactivity:**\n" +
		"- Any interactive elements?\n"

	// Standard path: single-pass render of the full content.
	standardLines, _ := renderContent(content, width, bodyStyle, th, nil)

	// Streaming path: incremental render via renderStreamingEntry.
	state := &streamRenderState{}
	streamLines, _ := renderStreamingEntry(content, width, th, nil, state)

	t.Logf("standard path produced %d lines", len(standardLines))
	t.Logf("streaming path produced %d lines", len(streamLines))

	// Log all lines from both paths for comparison.
	maxLen := len(standardLines)
	if len(streamLines) > maxLen {
		maxLen = len(streamLines)
	}
	for i := 0; i < maxLen; i++ {
		stdLine := ""
		if i < len(standardLines) {
			stdLine = standardLines[i]
		}
		stmLine := ""
		if i < len(streamLines) {
			stmLine = streamLines[i]
		}
		match := "  "
		if stdLine != stmLine {
			match = "!="
		}
		t.Logf("[%2d] %s std=%q", i, match, stdLine)
		if stdLine != stmLine {
			t.Logf("[%2d] %s stm=%q", i, match, stmLine)
		}
	}

	// Identify which lines from the standard output are missing in stream output.
	if len(standardLines) != len(streamLines) {
		streamSet := make(map[int]string, len(streamLines))
		for i, l := range streamLines {
			streamSet[i] = l
		}

		// Walk through standard lines and find which ones are absent
		// at the corresponding position in the stream output.
		var missing []int
		si := 0
		for i, stdLine := range standardLines {
			if si < len(streamLines) && streamLines[si] == stdLine {
				si++
			} else {
				missing = append(missing, i)
			}
		}
		if len(missing) > 0 {
			t.Logf("lines present in standard but missing in streaming output: %v", missing)
			for _, idx := range missing {
				t.Logf("  missing line [%d]: %q", idx, standardLines[idx])
			}
		}
	}

	if len(standardLines) != len(streamLines) {
		t.Fatalf("height mismatch: standard path produced %d lines, streaming path produced %d lines",
			len(standardLines), len(streamLines))
	}
}

func TestRenderEntry_ToolCallLinesRespectViewportWidth(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inspector-1",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector-pipeline",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "inspect_workspace_state",
				ArgsSummary: `path="src/hello_cli/cli.py" include_tool_output=true`,
				Output:      strings.Repeat("tool output wrap ", 8),
				StartedAt:   time.Now().Add(-1500 * time.Millisecond),
				Duration:    1500 * time.Millisecond,
				Success:     true,
				Completed:   true,
				Expanded:    true,
			},
		},
	}

	const width = 28
	lines, _ := RenderEntry(entry, width, theme.DefaultDark(), nil)
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderEntry_InterAgentRowsUseTreeSpinnerAndEllipsis(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inspector-2",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "consult_academic_approach",
				StartedAt: time.Now().Add(-250 * time.Millisecond),
				Completed: true,
				Success:   true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "A table-driven harness would be cleaner and easier to extend than the current approach.",
					Status:     InterAgentToolDone,
				},
			},
			{
				ToolName:  "challenge_architect",
				StartedAt: time.Now().Add(-350 * time.Millisecond),
				Completed: true,
				Success:   true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"architect"},
					Summary:    "The testing scope is too weak for this checkpoint and likely needs to be revised before final sign-off.",
					Status:     InterAgentToolPending,
				},
			},
		},
	}

	const width = 72
	lines, _ := RenderEntry(entry, width, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "academic") {
		t.Fatalf("rendered inter-agent rows missing academic label: %q", joined)
	}
	if !strings.Contains(joined, "architect") {
		t.Fatalf("rendered inter-agent rows missing architect label: %q", joined)
	}
	if !strings.Contains(joined, "├─") || !strings.Contains(joined, "└─") {
		t.Fatalf("expected tree connectors in inter-agent rows: %q", joined)
	}
	if !strings.Contains(joined, "...") {
		t.Fatalf("expected ellipsis truncation in inter-agent rows: %q", joined)
	}
	if !containsAny(joined, interAgentSpinnerFrames[:]...) {
		t.Fatalf("expected ring-segment spinner in inter-agent rows: %q", joined)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderEntry_ThinkingEmojiStillFitsViewportWidth(t *testing.T) {
	entry := &ChatEntry{
		ID:           "archivalist-emoji",
		Timestamp:    time.Now(),
		Source:       SourceAgent,
		AgentType:    "archivalist",
		Streaming:    true,
		ThinkingText: "Searching archival traces 📚 for prior failures and decisions.",
	}

	const width = 24
	lines, _ := RenderEntry(entry, width, theme.DefaultDark(), nil)
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderEntry_ThinkingPhaseIncludesInterAgentRows(t *testing.T) {
	entry := &ChatEntry{
		ID:             "thinking-inter-agent",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "inspector",
		Streaming:      true,
		ThinkingText:   "⠋  0.5s",
		ThinkingStatus: "waiting: final inspector validation",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "challenge_architect",
				StartedAt: time.Now().Add(-250 * time.Millisecond),
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"architect"},
					Summary:    "testing scope is too weak for this checkpoint and likely needs revision",
					Status:     InterAgentToolPending,
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 72, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "architect") {
		t.Fatalf("thinking-phase render missing inter-agent row: %q", joined)
	}
	if !strings.Contains(joined, "waiting: final inspector validation") {
		t.Fatalf("thinking-phase render missing status line: %q", joined)
	}
}

func TestRenderEntry_FailedInterAgentRowShowsConcreteError(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-failure",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "consult_academic_approach",
				StartedAt: time.Now().Add(-250 * time.Millisecond),
				Completed: true,
				Success:   false,
				ErrorMsg:  "academic consultation failed: provider unavailable",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Assess whether the current approach is sound.",
					Status:     InterAgentToolFailed,
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 96, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "academic") {
		t.Fatalf("rendered row missing academic label: %q", joined)
	}
	if !strings.Contains(joined, "provider unavailable") {
		t.Fatalf("rendered row missing concrete error: %q", joined)
	}
}

func TestRenderEntry_OffsetsInterAgentSubregionsForLaterToolCalls(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-subregion-offset",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "read_file",
				ArgsSummary: "ui/chat/tool_render.go",
				FullArgs:    `{"path":"ui/chat/tool_render.go","start_line":1}`,
				Output:      "ok",
				StartedAt:   time.Now().Add(-time.Second),
				Completed:   true,
				Success:     true,
			},
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic-offset",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{ToolName: "read_file", ArgsSummary: "path=ui/chat/model.go", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "\"tool_call_key\"", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "ui/chat/model.go", Completed: true, Success: true},
								{ToolName: "apply_patch", ArgsSummary: "ui/chat/model.go", Completed: true, Success: true},
								{ToolName: "gofmt", ArgsSummary: "ui/chat/model.go", Completed: true, Success: true},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 88, theme.DefaultDark(), nil)
	if len(entry.ToolCallRegions) < 2 {
		t.Fatalf("expected two tool call regions, got %+v", entry.ToolCallRegions)
	}
	consultRegion := entry.ToolCallRegions[1]
	if len(consultRegion.Subregions) == 0 {
		t.Fatalf("expected inter-agent subregions for second tool call, got %+v", consultRegion)
	}
	overflow := consultRegion.Subregions[0]
	if overflow.Kind != ToolCallSubregionOverflow {
		t.Fatalf("expected first subregion to be overflow, got %+v", overflow)
	}
	if overflow.Start < consultRegion.Start || overflow.End > consultRegion.End {
		t.Fatalf("overflow subregion %+v must stay within consult region %+v", overflow, consultRegion)
	}
	if overflow.Start >= len(lines) || !strings.Contains(lines[overflow.Start], "earlier events") {
		t.Fatalf("expected overflow line at rendered index %d, got lines=%q", overflow.Start, strings.Join(lines, "\n"))
	}
}

func TestRenderEntry_ThinkingPhaseRendersToolCallsAboveSpinner(t *testing.T) {
	entry := &ChatEntry{
		ID:             "thinking-tool-order",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "engineer",
		Streaming:      true,
		ThinkingText:   "⠋  0.5s",
		ThinkingStatus: "waiting for tool completion",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "read_file",
				ArgsSummary: "path=README.md",
				StartedAt:   time.Now().Add(-250 * time.Millisecond),
				Completed:   true,
				Success:     true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 72, theme.DefaultDark(), nil)
	var toolLineIdx, spinnerLineIdx int = -1, -1
	for i, line := range lines {
		if toolLineIdx == -1 && strings.Contains(line, "read_file") {
			toolLineIdx = i
		}
		if spinnerLineIdx == -1 && strings.Contains(line, "0.5s") {
			spinnerLineIdx = i
		}
	}
	if toolLineIdx == -1 {
		t.Fatalf("thinking-phase render missing tool row: %q", strings.Join(lines, "\n"))
	}
	if spinnerLineIdx == -1 {
		t.Fatalf("thinking-phase render missing spinner row: %q", strings.Join(lines, "\n"))
	}
	if toolLineIdx >= spinnerLineIdx {
		t.Fatalf("expected tool row above spinner, got tool line %d and spinner line %d: %q", toolLineIdx, spinnerLineIdx, strings.Join(lines, "\n"))
	}
}

func TestRenderEntry_ThinkingPhaseClosesInterAgentTreeBeforeParentSpinner(t *testing.T) {
	entry := &ChatEntry{
		ID:             "thinking-tree-close",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "architect",
		Streaming:      true,
		ThinkingText:   "⠋  2.4s",
		ThinkingStatus: "Refining the patch plan...",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolPending,
				},
			},
			{
				ToolName:    "consult",
				ToolCallKey: "consult-2",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"guardian"},
					Summary:    "Checking safety assumptions",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-guardian",
							AgentType:     "guardian",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "go_test",
									ArgsSummary: "./ui/chat",
									StartedAt:   time.Now().Add(-700 * time.Millisecond),
								},
							},
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 72, theme.DefaultDark(), nil)
	var guardianLine, spinnerLine int = -1, -1
	for i, line := range lines {
		if guardianLine == -1 && strings.Contains(line, "guardian") {
			guardianLine = i
		}
		if spinnerLine == -1 && strings.Contains(line, "Refining the patch plan") {
			spinnerLine = i
		}
	}
	if guardianLine == -1 || spinnerLine == -1 {
		t.Fatalf("expected guardian branch and parent spinner in render: %q", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[guardianLine], "└─") {
		t.Fatalf("expected last inter-agent branch to use terminal connector, got %q", lines[guardianLine])
	}
	if guardianLine >= spinnerLine {
		t.Fatalf("expected inter-agent tree to render above parent spinner, got guardian line %d and spinner line %d", guardianLine, spinnerLine)
	}
}

func TestRenderEntry_InterAgentRowsRenderNestedChildLines(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-nested",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "path=ui/chat/model.go",
									StartedAt:   time.Now().Add(-100 * time.Millisecond),
									Duration:    100 * time.Millisecond,
									Completed:   true,
									Success:     true,
								},
							},
							ResultSummary: "A table-driven harness would be cleaner and easier to extend.",
							Completed:     true,
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 76, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "academic") {
		t.Fatalf("expected nested render to contain academic label: %q", joined)
	}
	if !strings.Contains(joined, "read_file") {
		t.Fatalf("expected nested render to contain child tool call: %q", joined)
	}
	if !strings.Contains(joined, "A table-driven harness would be cleaner") {
		t.Fatalf("expected nested render to contain child summary line: %q", joined)
	}
	if !strings.Contains(joined, "└─") {
		t.Fatalf("expected nested child connector lines: %q", joined)
	}
}

func TestRenderEntry_InterAgentRowsRenderNestedConsultChildAgents(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-nested-consult-child",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare UI pattern options",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-lib-1",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Find relevant UI patterns",
										Status:     InterAgentToolPending,
										Children: []InterAgentChildActivity{
											{
												CorrelationID:     "child-librarian",
												AgentType:         "librarian",
												ThinkingStatus:    "Inspecting existing UI patterns.",
												ThinkingText:      "⠋  0.2s",
												ThinkingColor:     "#7dcfff",
												ToolCalls:         nil,
												ResultSummary:     "",
												Completed:         false,
												Failed:            false,
												ToolCallsExpanded: false,
											},
										},
									},
									StartedAt: time.Now().Add(-200 * time.Millisecond),
								},
							},
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 92, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "academic") {
		t.Fatalf("expected nested render to contain academic label: %q", joined)
	}
	if !strings.Contains(joined, "librarian") {
		t.Fatalf("expected nested render to contain librarian child label: %q", joined)
	}
	if !strings.Contains(joined, "Inspecting existing UI patterns.") {
		t.Fatalf("expected nested render to contain librarian child progress: %q", joined)
	}
}

func TestRenderEntry_InterAgentChildRowsKeepActiveTimerVisibleUnderLongSummary(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-child-timer-visible",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare UI pattern options",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:  "child-academic",
							AgentType:      "academic",
							ThinkingStatus: "Evaluating a very long description of the current research direction that would normally crowd out the timer on the same line.",
							ThinkingText:   "⠋  12.3s",
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 84, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "⠋  12.3s") {
		t.Fatalf("expected child row to preserve the active timer on the right, got %q", joined)
	}
}

func TestRenderEntry_InterAgentRowsRenderDeepNestedTreeWithoutDuplicateBranchMarkers(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-deep-nested-tree",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-academic-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare landing-page framework options",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-librarian-1",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Collect grounded framework evidence",
										Status:     InterAgentToolPending,
										Children: []InterAgentChildActivity{
											{
												CorrelationID: "child-librarian",
												AgentType:     "librarian",
												ToolCalls: []ToolCallRecord{
													{
														ToolName:    "read_file",
														ArgsSummary: "path=docs/framework-benchmarks.md",
														StartedAt:   time.Now().Add(-250 * time.Millisecond),
													},
												},
												ThinkingStatus: "Collecting benchmark notes.",
											},
										},
									},
									StartedAt: time.Now().Add(-350 * time.Millisecond),
								},
							},
							ThinkingStatus: "Delegating evidence collection.",
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 96, theme.DefaultDark(), nil)
	academicLine := -1
	librarianLine := -1
	readFileLine := -1
	for idx, line := range lines {
		switch {
		case strings.Contains(line, "academic"):
			academicLine = idx
		case strings.Contains(line, "librarian"):
			librarianLine = idx
		case strings.Contains(line, "read_file"):
			readFileLine = idx
		}
	}
	if academicLine < 0 || librarianLine < 0 || readFileLine < 0 {
		t.Fatalf("expected academic, librarian, and read_file lines in deep nested render, got %q", strings.Join(lines, "\n"))
	}
	if !(academicLine < librarianLine && librarianLine < readFileLine) {
		t.Fatalf("expected deep nested lines in academic -> librarian -> read_file order, got %q", strings.Join(lines, "\n"))
	}
	for _, idx := range []int{librarianLine, readFileLine} {
		line := lines[idx]
		branches := strings.Count(line, "└─") + strings.Count(line, "├─")
		if branches != 1 {
			t.Fatalf("expected exactly one branch marker on nested line %q, got %d", line, branches)
		}
	}
}

func TestFormatToolDuration_SubMillisecondUsesLessThanOneMillisecond(t *testing.T) {
	if got := formatToolDuration(750 * time.Microsecond); got != "<1ms" {
		t.Fatalf("formatToolDuration(750µs) = %q, want <1ms", got)
	}
}

func TestFormatToolCallDuration_ChallengePendingUsesLiveElapsedAfterDispatchCompletion(t *testing.T) {
	// Challenge rows deliberately stay Pending after Complete=true while the
	// challenged agent is still composing its response, so the duration must
	// keep ticking. Other inter-agent kinds reach a terminal status at
	// dispatch and freeze instead.
	dispatchDuration := 100 * time.Millisecond
	got := formatToolCallDuration(ToolCallRecord{
		StartedAt: time.Now().Add(-1500 * time.Millisecond),
		Duration:  dispatchDuration,
		Completed: true,
		InterAgent: &InterAgentTool{
			Kind:   InterAgentToolChallenge,
			Status: InterAgentToolPending,
		},
	})
	if got == formatToolDuration(dispatchDuration) {
		t.Fatalf("expected pending challenge duration to stay live after dispatch completion, got %q", got)
	}
	if got == "" {
		t.Fatal("expected pending challenge duration to remain visible")
	}
}

func TestFormatToolCallDuration_NonChallengePendingFreezesOnCompletion(t *testing.T) {
	// Approval/consult/store rows that somehow land in Completed=true with a
	// stale Pending status (a missed terminal Status update — historically
	// the "guardian - approved by user" stuck row) must freeze on the
	// captured Duration so the row doesn't appear to keep growing forever.
	dispatchDuration := 100 * time.Millisecond
	for _, kind := range []InterAgentToolKind{
		InterAgentToolApproval,
		InterAgentToolConsult,
		InterAgentToolStore,
		"", // unknown kind defaults to freezing too
	} {
		got := formatToolCallDuration(ToolCallRecord{
			StartedAt: time.Now().Add(-1500 * time.Millisecond),
			Duration:  dispatchDuration,
			Completed: true,
			InterAgent: &InterAgentTool{
				Kind:   kind,
				Status: InterAgentToolPending,
			},
		})
		if got != formatToolDuration(dispatchDuration) {
			t.Fatalf("kind %q: expected frozen captured duration on completion, got %q", kind, got)
		}
	}
}

func TestRenderEntry_InterAgentChildRowsRenderSubMillisecondDurations(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-sub-ms-child",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_librarian_style",
				ToolCallKey: "consult-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"librarian"},
					Summary:    "Checking existing codebase patterns",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-librarian",
							AgentType:     "librarian",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "path=README.md",
									StartedAt:   time.Now().Add(-750 * time.Microsecond),
									Duration:    750 * time.Microsecond,
									Completed:   true,
									Success:     true,
								},
							},
							ResultSummary: "Found an existing project layout to follow.",
							Completed:     true,
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 76, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "<1ms") {
		t.Fatalf("expected nested child tool row to render <1ms instead of 0ms: %q", joined)
	}
	if strings.Contains(joined, "0ms") {
		t.Fatalf("unexpected 0ms duration in nested child tool row: %q", joined)
	}
}

func TestRenderEntry_DoneInterAgentParentWithPendingChildStillAdvancesSpinner(t *testing.T) {
	prevSpinner := interAgentSpinnerIndex
	interAgentSpinnerIndex = 0
	defer func() {
		interAgentSpinnerIndex = prevSpinner
	}()

	entry := &ChatEntry{
		ID:        "inter-agent-done-parent-pending-child",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector-pipeline",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "challenge_agent",
				ToolCallKey: "challenge-1",
				StartedAt:   time.Now().Add(-600 * time.Millisecond),
				Completed:   true,
				Success:     true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"tester-pipeline"},
					Summary:    "Prepare the pipeline tester handoff.",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "corr-pending-tester",
							AgentType:     "tester-pipeline",
							Completed:     false,
						},
					},
				},
			},
		},
	}

	firstLines, _ := RenderEntry(entry, 88, theme.DefaultDark(), nil)
	secondLines, _ := RenderEntry(entry, 88, theme.DefaultDark(), nil)
	first := strings.Join(firstLines, "\n")
	second := strings.Join(secondLines, "\n")
	if first == second {
		t.Fatalf("expected spinner frame to advance for pending child under completed parent, got %q", first)
	}
	if !containsAny(first, interAgentSpinnerFrames[:]...) {
		t.Fatalf("expected first render to contain an inter-agent spinner, got %q", first)
	}
	if !containsAny(second, interAgentSpinnerFrames[:]...) {
		t.Fatalf("expected second render to contain an inter-agent spinner, got %q", second)
	}
}

func TestRenderEntry_InterAgentRowsRenderSeparateChildSectionsPerAgent(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-multi-child",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the research plan.",
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_research_support",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"librarian", "archivalist"},
					Summary:    "Gathering references and archived context",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-librarian",
							AgentType:     "librarian",
							ResultSummary: "Found the reference set for the migration notes.",
							Completed:     true,
							ToolCalls: []ToolCallRecord{
								{ToolName: "search_library", ArgsSummary: "migration notes", Completed: true, Success: true},
								{ToolName: "read_file", ArgsSummary: "docs/migration.md", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "\"migration\"", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "docs/migration.md", Completed: true, Success: true},
								{ToolName: "write_notes", ArgsSummary: "migration references", Completed: true, Success: true},
							},
						},
						{
							CorrelationID: "child-archivalist",
							AgentType:     "archivalist",
							ResultSummary: "Recovered the prior archived plan snapshot.",
							Completed:     true,
							ToolCalls: []ToolCallRecord{
								{ToolName: "list_archives", ArgsSummary: "project=sylk", Completed: true, Success: true},
								{ToolName: "read_archive", ArgsSummary: "plan-v3", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "\"handoff\"", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "archive/plan-v3.md", Completed: true, Success: true},
								{ToolName: "write_notes", ArgsSummary: "archived plan", Completed: true, Success: true},
							},
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 88, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Found the reference set") {
		t.Fatalf("expected dedicated librarian child header/summary, got %q", joined)
	}
	if !strings.Contains(joined, "Recovered the prior archived plan snapshot") {
		t.Fatalf("expected dedicated archivalist child header/summary, got %q", joined)
	}
	if strings.Contains(joined, "librarian, archivalist") {
		t.Fatalf("expected aggregate target row to be omitted once child sections exist, got %q", joined)
	}
	if got := strings.Count(joined, "earlier events"); got != 2 {
		t.Fatalf("expected one overflow row per child section, got %d in %q", got, joined)
	}
}

func TestRenderEntry_InterAgentRowsRenderOverflowControlRowWithSpecialConnector(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-overflow-control-collapsed",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_research_support",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Gathering references",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic-overflow-control",
							AgentType:     "academic",
							Completed:     true,
							ToolCalls: []ToolCallRecord{
								{ToolName: "read_file", ArgsSummary: "one", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "two", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "three", Completed: true, Success: true},
								{ToolName: "apply_patch", ArgsSummary: "four", Completed: true, Success: true},
								{ToolName: "gofmt", ArgsSummary: "five", Completed: true, Success: true},
							},
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 88, theme.DefaultDark(), nil)
	found := ""
	for _, line := range lines {
		stripped := stripANSITest(line)
		if strings.Contains(stripped, "Show 2 earlier events") {
			found = stripped
			break
		}
	}
	if found == "" {
		t.Fatalf("expected collapsed overflow control row, got %q", strings.Join(lines, "\n"))
	}
	if !strings.Contains(found, "╰─ ▸ Show 2 earlier events") {
		t.Fatalf("expected collapsed overflow control to use special connector and icon, got %q", found)
	}
}

func TestRenderEntry_InterAgentRowsRenderExpandedOverflowControlAsHide(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-overflow-control-expanded",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_research_support",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Gathering references",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:     "child-academic-overflow-hide",
							AgentType:         "academic",
							Completed:         true,
							ToolCallsExpanded: true,
							ToolCalls: []ToolCallRecord{
								{ToolName: "read_file", ArgsSummary: "one", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "two", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "three", Completed: true, Success: true},
								{ToolName: "apply_patch", ArgsSummary: "four", Completed: true, Success: true},
								{ToolName: "gofmt", ArgsSummary: "five", Completed: true, Success: true},
							},
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 88, theme.DefaultDark(), nil)
	found := ""
	for _, line := range lines {
		stripped := stripANSITest(line)
		if strings.Contains(stripped, "hide") {
			found = stripped
			break
		}
	}
	if found == "" {
		t.Fatalf("expected expanded hide control row, got %q", strings.Join(lines, "\n"))
	}
	if !strings.Contains(found, "╰─ ▾ hide") {
		t.Fatalf("expected expanded overflow control to use special connector and hide label, got %q", found)
	}
	if strings.Contains(stripANSITest(strings.Join(lines, "\n")), "earlier events") {
		t.Fatalf("expected expanded branch to replace earlier-events label with hide, got %q", strings.Join(lines, "\n"))
	}
}

func TestRenderEntry_InterAgentRowsRenderMissingTargetsAsIndependentBranches(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inter-agent-missing-target-branches",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Refining the research plan.",
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_research_support",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"librarian", "archivalist"},
					Summary:    "Checking prior patterns",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:  "child-librarian-only",
							AgentType:      "librarian",
							ThinkingStatus: "Checking prior patterns",
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 88, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "librarian - Checking prior patterns") {
		t.Fatalf("expected librarian branch, got %q", joined)
	}
	if !strings.Contains(joined, "archivalist - Checking prior patterns") {
		t.Fatalf("expected archivalist placeholder branch, got %q", joined)
	}
	if strings.Contains(joined, "librarian, archivalist") {
		t.Fatalf("expected no aggregate combined branch, got %q", joined)
	}
}

func TestRenderEntry_ToolCallLinesDoNotEmbedRawNewlines(t *testing.T) {
	entry := &ChatEntry{
		ID:        "tool-newlines",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "write_file\nunsafe",
				ArgsSummary: "path=README.md\ncontent=hello",
				FullArgs:    "{\n  \"path\": \"README.md\",\n  \"content\": \"hello\\nworld\"\n}",
				Output:      "wrote file successfully",
				StartedAt:   time.Now().Add(-time.Second),
				Duration:    time.Second,
				Success:     true,
				Completed:   true,
				Expanded:    true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 32, theme.DefaultDark(), nil)
	for i, line := range lines {
		if strings.Contains(line, "\n") || strings.Contains(line, "\r") {
			t.Fatalf("line %d contains raw newline: %q", i, line)
		}
	}
}

func TestRenderEntry_CollapsedToolCallsStaySingleLineInChatPanel(t *testing.T) {
	entry := &ChatEntry{
		ID:        "tool-single-line",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "write_file",
				ArgsSummary: "path=README.md\ncontent=line one that keeps going long enough to require truncation in a narrow viewport",
				FullArgs:    "{\n  \"path\": \"README.md\",\n  \"content\": \"line one\\nline two\\nline three\"\n}",
				Output:      "line one\nline two\nline three\nline four",
				StartedAt:   time.Now().Add(-time.Second),
				Duration:    time.Second,
				Success:     true,
				Completed:   true,
			},
		},
	}

	const width = 34
	lines, _ := RenderEntry(entry, width, theme.DefaultDark(), nil)
	if len(lines) != 4 {
		t.Fatalf("rendered line count = %d, want 4 (header + single tool row + tool spacer + entry spacer): %q", len(lines), strings.Join(lines, "\n"))
	}
	if strings.TrimSpace(lines[2]) != "" {
		t.Fatalf("expected tool spacer on line 2, got %q", lines[2])
	}
	if !strings.Contains(lines[1], "...") {
		t.Fatalf("expected tool row to ellipsize long description, got %q", lines[1])
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderEntry_ExpandedToolCallsRenderDetailBlock(t *testing.T) {
	entry := &ChatEntry{
		ID:        "tool-expanded-detail",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "write_file",
				ArgsSummary: "path=README.md",
				FullArgs:    "{\n  \"path\": \"README.md\",\n  \"content\": \"line one\\nline two\\nline three\"\n}",
				Output:      "line one\nline two\nline three\nline four",
				StartedAt:   time.Now().Add(-time.Second),
				Duration:    time.Second,
				Success:     true,
				Completed:   true,
				Expanded:    true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 48, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "args - path=README.md") {
		t.Fatalf("expected expanded tool render to include compact args detail, got %q", joined)
	}
	if !strings.Contains(joined, "output - line one line two") {
		t.Fatalf("expected expanded tool render to include compact output detail, got %q", joined)
	}
	if len(lines) <= 4 {
		t.Fatalf("expected expanded tool render to use more than a single tool row, got %q", joined)
	}
}

func TestRenderEntry_ExpandedToolCallsAvoidRoutingNoiseAndRawPlanJSON(t *testing.T) {
	entry := &ChatEntry{
		ID:        "tool-expanded-structured",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "plan",
				FullArgs:  `{"query":"Refine the plan around packaging support","session_id":"sess-1","plan":{"tasks":[{"id":"task_1","title":"Implement CLI"},{"id":"task_2","title":"Add pyproject"}]}}`,
				Output:    `{"response":"Plan revised to add packaging support.","artifact_version":"v1.1","plan":{"tasks":[{"id":"task_1"}]}}`,
				StartedAt: time.Now().Add(-time.Second),
				Duration:  time.Second,
				Success:   true,
				Completed: true,
				Expanded:  true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 72, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "args - query=Refine the plan around packaging support") {
		t.Fatalf("expected expanded render to prefer the query summary, got %q", joined)
	}
	if !strings.Contains(joined, "output - response=Plan revised to add packaging support.") {
		t.Fatalf("expected expanded render to prefer the response summary, got %q", joined)
	}
	if strings.Contains(joined, "session_id") {
		t.Fatalf("expected expanded render to omit routing noise, got %q", joined)
	}
	if strings.Contains(joined, "\"tasks\"") || strings.Contains(joined, `"plan"`) {
		t.Fatalf("expected expanded render to avoid raw plan JSON, got %q", joined)
	}
}

func TestRenderEntry_ExpandedToolCallsAreStableAcrossRerenders(t *testing.T) {
	entry := &ChatEntry{
		ID:        "tool-expanded-stable",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "plan",
				FullArgs:  `{"query":"Refine the plan around packaging support","session_id":"sess-1","plan":{"tasks":[{"id":"task_1"}]}}`,
				Output:    `{"response":"Plan revised to add packaging support.","artifact_version":"v1.1"}`,
				StartedAt: time.Now().Add(-time.Second),
				Duration:  time.Second,
				Success:   true,
				Completed: true,
				Expanded:  true,
			},
		},
	}

	first, _ := RenderEntry(entry, 72, theme.DefaultDark(), nil)
	second, _ := RenderEntry(entry, 72, theme.DefaultDark(), nil)
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatalf("expected expanded tool render to remain stable across rerenders\nfirst:\n%q\nsecond:\n%q", strings.Join(first, "\n"), strings.Join(second, "\n"))
	}
}

func TestRenderEntry_NestedExpandedChildToolCallUsesCompactDetailRows(t *testing.T) {
	entry := &ChatEntry{
		ID:        "nested-child-tool-expanded",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "write_file",
									ArgsSummary: "path=README.md",
									FullArgs:    "{\n  \"path\": \"README.md\",\n  \"content\": \"line one\\nline two\\nline three\"\n}",
									Output:      "{\n  \"ok\": true,\n  \"updated_lines\": 3,\n  \"path\": \"README.md\"\n}",
									StartedAt:   time.Now().Add(-time.Second),
									Duration:    time.Second,
									Completed:   true,
									Success:     true,
									Expanded:    true,
								},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	}

	lines, _ := RenderEntry(entry, 52, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	var argsLine, outputLine string
	for _, line := range lines {
		switch {
		case strings.Contains(line, "args - path=README.md"):
			argsLine = line
		case strings.Contains(line, "output - ok=true"):
			outputLine = line
		}
	}
	if !strings.Contains(joined, "args - path=README.md") {
		t.Fatalf("expected compact args detail row, got %q", joined)
	}
	if !strings.Contains(joined, "output - ok=true") {
		t.Fatalf("expected compact output detail row, got %q", joined)
	}
	if strings.Contains(joined, "\"content\":") || strings.Contains(joined, "\"updated_lines\":") {
		t.Fatalf("expected nested child expanded tool call to avoid raw JSON blocks, got %q", joined)
	}
	if strings.Contains(joined, "│   args -") || strings.Contains(joined, "│   output -") {
		t.Fatalf("expected nested child expanded detail rows to avoid extra inner branch stems, got %q", joined)
	}
	if argsLine == "" || strings.Count(argsLine, "│") < 1 {
		t.Fatalf("expected args detail line to keep a continuation stem, got %q", argsLine)
	}
	if outputLine == "" || !strings.Contains(outputLine, "└─") {
		t.Fatalf("expected final expanded detail line to terminate with a curved connector, got %q", outputLine)
	}
	if len(lines) > 8 {
		t.Fatalf("expected nested child expanded tool call to stay compact, got %d lines: %q", len(lines), joined)
	}
}

func TestRenderEntry_NestedExpandedChildToolCallDetailsUseContinuationAndTerminalConnectorsAtDepth(t *testing.T) {
	entry := &ChatEntry{
		ID:        "nested-child-tool-expanded-ancestor-branches",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName: "consult_research_support",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic", "archivalist"},
					Summary:    "Compare evidence sources",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "write_file",
									ArgsSummary: "path=README.md",
									FullArgs:    "{\n  \"path\": \"README.md\",\n  \"content\": \"line one\\nline two\\nline three\"\n}",
									Output:      "{\n  \"ok\": true,\n  \"updated_lines\": 3,\n  \"path\": \"README.md\"\n}",
									StartedAt:   time.Now().Add(-time.Second),
									Duration:    time.Second,
									Completed:   true,
									Success:     true,
									Expanded:    true,
								},
								{
									ToolName:    "grep",
									ArgsSummary: "\"comparison matrix\"",
									StartedAt:   time.Now().Add(-500 * time.Millisecond),
								},
							},
							ThinkingStatus: "Evaluating comparison structure.",
						},
						{
							CorrelationID:  "child-archivalist",
							AgentType:      "archivalist",
							ThinkingStatus: "Checking archived notes.",
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 72, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	var argsLine, outputLine string
	for _, line := range lines {
		switch {
		case strings.Contains(line, "args - path=README.md"):
			argsLine = line
		case strings.Contains(line, "output - ok=true"):
			outputLine = line
		}
	}
	if argsLine == "" || outputLine == "" {
		t.Fatalf("expected expanded child detail lines, got %q", joined)
	}
	if got := strings.Count(argsLine, "│"); got != 3 {
		t.Fatalf("expected args detail to keep architect, academic, and write_file continuation stems, got %d in %q", got, argsLine)
	}
	if got := strings.Count(outputLine, "│"); got != 2 {
		t.Fatalf("expected terminal output detail to keep only ancestor stems, got %d in %q", got, outputLine)
	}
	if !strings.Contains(outputLine, "└─") {
		t.Fatalf("expected terminal output detail to use a curved connector, got %q", outputLine)
	}
	if !strings.Contains(joined, "grep") || !strings.Contains(joined, "archivalist") {
		t.Fatalf("expected sibling tool row and sibling child branch to remain visible, got %q", joined)
	}
}

func TestRenderEntry_LongPipelineHeaderRespectsViewportWidth(t *testing.T) {
	entry := &ChatEntry{
		ID:             "engineer-1",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "engineer",
		TaskName:       "Very long pipeline task title that would otherwise wrap the engineer badge line",
		TaskSlug:       "very_long_pipeline_task_title_that_would_otherwise_wrap_the_engineer_badge_line",
		Streaming:      true,
		ThinkingText:   "⠋  0.0s",
		ThinkingStatus: "Reviewing task criteria, active test failures, and task-local workspace before implementing changes.",
	}

	const width = 32
	lines, _ := RenderEntry(entry, width, theme.DefaultDark(), nil)
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderStreamingEntryFull_PipelineStatusFooterRespectsViewportWidth(t *testing.T) {
	entry := &ChatEntry{
		ID:             "pipeline-stream",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "inspector-pipeline",
		AgentID:        "task_auth_checkout:inspector-pipeline",
		TaskName:       "Auth checkout",
		TaskSlug:       "auth-checkout",
		Content:        "Reviewing acceptance criteria before applying the next patch.",
		Streaming:      true,
		ThinkingText:   "Analyzing current pipeline phase",
		ThinkingStatus: "Reviewing task criteria, active test failures, and task-local workspace before implementing changes.",
	}

	const width = 28
	lines, _ := renderStreamingEntryFull(entry, width, theme.DefaultDark(), nil, &streamRenderState{})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Analyzing current pipeline") || !strings.Contains(joined, "phase") {
		t.Fatalf("stream footer missing thinking text: %q", joined)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderEntry_CompletedProgressOnlyEntryRetainsStatusSummary(t *testing.T) {
	entry := &ChatEntry{
		ID:              "inspector-progress-only-complete",
		Timestamp:       time.Now(),
		Source:          SourceAgent,
		AgentType:       "inspector-pipeline",
		AgentID:         "task_auth_checkout:inspector-pipeline",
		TaskName:        "Auth checkout",
		TaskSlug:        "auth-checkout",
		Content:         "",
		Streaming:       false,
		ThinkingStatus:  "Validation accepted. Proceeding to closure gate.",
		ThinkingElapsed: 3*time.Second + 400*time.Millisecond,
	}

	lines, _ := RenderEntry(entry, 88, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "thought for 3.4s") {
		t.Fatalf("expected collapsed thinking summary, got %q", joined)
	}
	if !strings.Contains(joined, "Validation accepted. Proceeding to closure gate.") {
		t.Fatalf("expected preserved progress-only status, got %q", joined)
	}
}

func TestRenderEntry_KeepsParentContentVisibleWhileInterAgentChallengePending(t *testing.T) {
	entry := &ChatEntry{
		ID:        "architect-with-pending-challenge",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "This should stay hidden until the challenge completes.",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "challenge_engineer",
				StartedAt: time.Now().Add(-500 * time.Millisecond),
				Completed: true,
				Success:   true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"engineer"},
					Summary:    "Implement the accepted patch plan.",
					Status:     InterAgentToolPending,
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 80, theme.DefaultDark(), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "This should stay hidden until the challenge completes.") {
		t.Fatalf("expected parent content to remain visible while challenge is pending, got %q", joined)
	}
	if !strings.Contains(joined, "Implement the accepted patch plan.") {
		t.Fatalf("expected pending challenge row to remain visible, got %q", joined)
	}

	entry.ToolCalls[0].InterAgent.Status = InterAgentToolDone
	lines, _ = RenderEntry(entry, 80, theme.DefaultDark(), nil)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "This should stay hidden until the challenge completes.") {
		t.Fatalf("expected parent content to appear after challenge completion, got %q", joined)
	}
}

func TestRenderStreamingEntryFull_KeepsParentContentVisibleWhileInterAgentConsultPending(t *testing.T) {
	entry := &ChatEntry{
		ID:             "inspector-with-pending-consult",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "inspector",
		Content:        "The final audit summary should wait for the consult result.",
		Streaming:      true,
		ThinkingText:   "Waiting on academic consult",
		ThinkingStatus: "Researching the packaging guidance before finalizing the audit.",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "consult_academic_approach",
				StartedAt: time.Now().Add(-400 * time.Millisecond),
				Completed: true,
				Success:   true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare current packaging guidance and cite the best sources.",
					Status:     InterAgentToolPending,
				},
			},
		},
	}

	lines, _ := renderStreamingEntryFull(entry, 88, theme.DefaultDark(), nil, &streamRenderState{})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "The final audit summary should wait for the consult result.") {
		t.Fatalf("expected parent streaming content to remain visible while consult is pending, got %q", joined)
	}
	if !strings.Contains(joined, "Compare current packaging guidance") {
		t.Fatalf("expected consult row to remain visible, got %q", joined)
	}
	if !strings.Contains(joined, "Waiting on academic consult") {
		t.Fatalf("expected streaming footer to remain visible, got %q", joined)
	}

	entry.ToolCalls[0].InterAgent.Status = InterAgentToolDone
	lines, _ = renderStreamingEntryFull(entry, 88, theme.DefaultDark(), nil, &streamRenderState{})
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "The final audit summary should wait for the consult result.") {
		t.Fatalf("expected parent streaming content to appear after consult completion, got %q", joined)
	}
}

func TestRenderStreamingEntryFull_KeepsParentContentVisibleWhileNestedChildPendingAfterApproval(t *testing.T) {
	entry := &ChatEntry{
		ID:             "architect-with-pending-academic-after-approval",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "architect",
		Content:        "The architect should not speak yet.",
		Streaming:      true,
		ThinkingText:   "⠋  0.5s",
		ThinkingStatus: "Waiting on academic follow-up.",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "consult_academic_approach",
				StartedAt: time.Now().Add(-400 * time.Millisecond),
				Completed: true,
				Success:   true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Fetch and analyze the official source.",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "corr-child-academic-after-approval",
							AgentType:     "academic",
							Completed:     false,
							ToolCalls: []ToolCallRecord{
								{
									ToolName:  "approval_guardian",
									Completed: true,
									Success:   true,
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolApproval,
										AgentTypes: []string{"guardian"},
										Summary:    "Requesting Guardian approval for raw.githubusercontent.com",
										Status:     InterAgentToolDone,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	lines, _ := renderStreamingEntryFull(entry, 88, theme.DefaultDark(), nil, &streamRenderState{})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "The architect should not speak yet.") {
		t.Fatalf("expected parent streaming content to remain visible while academic child remains pending, got %q", joined)
	}
	if !strings.Contains(joined, "academic") || !strings.Contains(joined, "guardian") {
		t.Fatalf("expected nested academic and guardian rows to remain visible, got %q", joined)
	}
	if !strings.Contains(joined, "Waiting on academic follow-up.") {
		t.Fatalf("expected parent streaming footer to remain visible, got %q", joined)
	}

	entry.ToolCalls[0].InterAgent.Children[0].Completed = true
	lines, _ = renderStreamingEntryFull(entry, 88, theme.DefaultDark(), nil, &streamRenderState{})
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "The architect should not speak yet.") {
		t.Fatalf("expected parent streaming content to appear after academic child completion, got %q", joined)
	}
}

func TestRenderStreamingEntryFull_KeepsNestedConsultedAgentAndToolRowsVisibleAlongsideParentContent(t *testing.T) {
	entry := &ChatEntry{
		ID:             "architect-with-nested-consulted-agent-tools",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "architect",
		Content:        "The architect has draft text that must stay hidden until research settles.",
		Streaming:      true,
		ThinkingText:   "⠋  0.7s",
		ThinkingStatus: "Waiting on research details.",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "consult_academic_approach",
				StartedAt: time.Now().Add(-400 * time.Millisecond),
				Completed: true,
				Success:   true,
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Establish the strongest evidence base.",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:  "corr-child-academic-with-nested-consult",
							AgentType:      "academic",
							Completed:      false,
							ThinkingStatus: "Reviewing the initial evidence frame.",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-lib-1",
									StartedAt:   time.Now().Add(-250 * time.Millisecond),
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Collect benchmark and methodology sources.",
										Status:     InterAgentToolPending,
										Children: []InterAgentChildActivity{
											{
												CorrelationID:  "corr-child-librarian-with-tool",
												AgentType:      "librarian",
												Completed:      false,
												ThinkingStatus: "Collecting benchmark evidence.",
												ToolCalls: []ToolCallRecord{
													{
														ToolName:    "web_search",
														ArgsSummary: "framework benchmark methodology",
														StartedAt:   time.Now().Add(-150 * time.Millisecond),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	lines, _ := renderStreamingEntryFull(entry, 100, theme.DefaultDark(), nil, &streamRenderState{})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "The architect has draft text that must stay hidden until research settles.") {
		t.Fatalf("expected parent streaming content to remain visible while nested consult work is active, got %q", joined)
	}
	for _, needle := range []string{
		"The architect has draft text that must stay hidden until research settles.",
		"academic",
		"librarian",
		"Collect benchmark and methodology sources.",
		"web_search",
		"framework benchmark methodology",
		"Waiting on research details.",
	} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("expected streaming nested consult render to contain %q, got %q", needle, joined)
		}
	}
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func TestRenderEntry_InterAgentRowsRenderSiblingNestedConsultAgentsAsSiblings(t *testing.T) {
	entry := &ChatEntry{
		ID:        "academic-nested-consults-siblings",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-academic-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Build evidence plan",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:  "child-academic",
							AgentType:      "academic",
							ThinkingStatus: "Evaluating evidence",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-librarian-1",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Collect framework benchmarks",
										Status:     InterAgentToolPending,
									},
									StartedAt: time.Now().Add(-200 * time.Millisecond),
								},
								{
									ToolName:    "consult",
									ToolCallKey: "consult-archivalist-1",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"archivalist"},
										Summary:    "Look for historical comparisons",
										Status:     InterAgentToolPending,
									},
									StartedAt: time.Now().Add(-100 * time.Millisecond),
								},
							},
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 96, theme.DefaultDark(), nil)
	var academicLine, librarianLine, archivalistLine string
	for _, line := range lines {
		stripped := stripANSITest(line)
		switch {
		case strings.Contains(stripped, "academic"):
			academicLine = stripped
		case strings.Contains(stripped, "librarian"):
			librarianLine = stripped
		case strings.Contains(stripped, "archivalist"):
			archivalistLine = stripped
		}
	}
	if academicLine == "" || librarianLine == "" || archivalistLine == "" {
		t.Fatalf("expected academic, librarian, and archivalist rows, got %q", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(strings.TrimLeft(academicLine, " "), "└─ academic") {
		t.Fatalf("expected academic to remain the terminal child of architect, got %q", academicLine)
	}
	if !strings.HasPrefix(strings.TrimLeft(librarianLine, " "), "├─ librarian") {
		t.Fatalf("expected librarian to render as a sibling row under academic, got %q", librarianLine)
	}
	if !strings.HasPrefix(strings.TrimLeft(archivalistLine, " "), "└─ archivalist") {
		t.Fatalf("expected archivalist to render as the terminal sibling under academic, got %q", archivalistLine)
	}
}

func TestRenderEntry_InterAgentRowsRenderSiblingNestedConsultAgentsWithOwnToolCalls(t *testing.T) {
	entry := &ChatEntry{
		ID:        "academic-nested-consults-sibling-toolcalls",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-academic-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Build evidence plan",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:  "child-academic",
							AgentType:      "academic",
							ThinkingStatus: "Evaluating evidence",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-librarian-1",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Collect framework benchmarks",
										Status:     InterAgentToolPending,
										Children: []InterAgentChildActivity{
											{
												CorrelationID: "child-librarian",
												AgentType:     "librarian",
												ToolCalls: []ToolCallRecord{
													{
														ToolName:    "web_search",
														ArgsSummary: "framework benchmarks",
														StartedAt:   time.Now().Add(-200 * time.Millisecond),
													},
												},
											},
										},
									},
									StartedAt: time.Now().Add(-300 * time.Millisecond),
								},
								{
									ToolName:    "consult",
									ToolCallKey: "consult-archivalist-1",
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"archivalist"},
										Summary:    "Look for historical comparisons",
										Status:     InterAgentToolPending,
										Children: []InterAgentChildActivity{
											{
												CorrelationID: "child-archivalist",
												AgentType:     "archivalist",
												ToolCalls: []ToolCallRecord{
													{
														ToolName:    "search_archive",
														ArgsSummary: "prior comparisons",
														StartedAt:   time.Now().Add(-100 * time.Millisecond),
													},
												},
											},
										},
									},
									StartedAt: time.Now().Add(-150 * time.Millisecond),
								},
							},
						},
					},
				},
			},
		},
	}

	lines, _ := RenderEntry(entry, 100, theme.DefaultDark(), nil)
	var librarianLine, webSearchLine, archivalistLine, searchArchiveLine string
	for _, line := range lines {
		stripped := stripANSITest(line)
		switch {
		case strings.Contains(stripped, "librarian"):
			librarianLine = stripped
		case strings.Contains(stripped, "web_search"):
			webSearchLine = stripped
		case strings.Contains(stripped, "archivalist"):
			archivalistLine = stripped
		case strings.Contains(stripped, "search_archive"):
			searchArchiveLine = stripped
		}
	}
	if librarianLine == "" || webSearchLine == "" || archivalistLine == "" || searchArchiveLine == "" {
		t.Fatalf("expected nested sibling consult rows and tool calls, got %q", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(strings.TrimLeft(librarianLine, " "), "├─ librarian") {
		t.Fatalf("expected librarian consult row to be a sibling under academic, got %q", librarianLine)
	}
	if !strings.HasPrefix(strings.TrimLeft(webSearchLine, " "), "│  └─ ") || !strings.Contains(webSearchLine, "web_search") {
		t.Fatalf("expected librarian child tool call to keep the sibling stem, got %q", webSearchLine)
	}
	if !strings.HasPrefix(strings.TrimLeft(archivalistLine, " "), "└─ archivalist") {
		t.Fatalf("expected archivalist consult row to terminate the sibling set, got %q", archivalistLine)
	}
	if !strings.HasPrefix(strings.TrimLeft(searchArchiveLine, " "), "└─ ") || !strings.Contains(searchArchiveLine, "search_archive") {
		t.Fatalf("expected archivalist child tool call to terminate without a dangling sibling stem, got %q", searchArchiveLine)
	}
}

func stripANSITest(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSITerminator(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// TestToolCallVisuallyOrphaned_FlagsLongPendingWithSubsequentActivity covers
// the (E) heuristic: a tool call that has been pending for more than the
// orphan threshold AND has at least N completed peers after it in the same
// list is treated as visually abandoned. The renderer swaps the live spinner
// for a `?` so users are not misled by a still-spinning timer for a row whose
// Complete event has very likely been lost.
func TestToolCallVisuallyOrphaned_FlagsLongPendingWithSubsequentActivity(t *testing.T) {
	now := time.Now()
	calls := []ToolCallRecord{
		{ToolCallKey: "stuck", ToolName: "summarize_workspace_state", StartedAt: now.Add(-2 * time.Minute)},
		{ToolCallKey: "p1", ToolName: "coord_query_view", StartedAt: now.Add(-90 * time.Second), Completed: true, Success: true},
		{ToolCallKey: "p2", ToolName: "coord_claim_scope", StartedAt: now.Add(-80 * time.Second), Completed: true, Success: true},
		{ToolCallKey: "p3", ToolName: "prepare_pipeline_write_context", StartedAt: now.Add(-70 * time.Second), Completed: true, Success: true},
	}
	if !toolCallVisuallyOrphaned(calls, 0, now) {
		t.Fatal("expected the long-pending call with 3 completed peers after it to be flagged orphaned")
	}
}

// TestToolCallVisuallyOrphaned_DoesNotFlagFreshLongPending confirms a recent
// pending call is never flagged, even after later peers complete. Many
// genuinely slow tools (build runs, large summarizations) can outlast a few
// quick peers.
func TestToolCallVisuallyOrphaned_DoesNotFlagFreshLongPending(t *testing.T) {
	now := time.Now()
	calls := []ToolCallRecord{
		{ToolCallKey: "fresh", ToolName: "summarize_workspace_state", StartedAt: now.Add(-5 * time.Second)},
		{ToolCallKey: "p1", ToolName: "coord_query_view", StartedAt: now.Add(-4 * time.Second), Completed: true, Success: true},
		{ToolCallKey: "p2", ToolName: "coord_claim_scope", StartedAt: now.Add(-3 * time.Second), Completed: true, Success: true},
		{ToolCallKey: "p3", ToolName: "prepare_pipeline_write_context", StartedAt: now.Add(-2 * time.Second), Completed: true, Success: true},
	}
	if toolCallVisuallyOrphaned(calls, 0, now) {
		t.Fatal("a 5s-old pending call must not be flagged orphaned regardless of peer completions")
	}
}

// TestToolCallVisuallyOrphaned_DoesNotFlagWithoutSubsequentActivity confirms
// the "agent has moved on" signal is required: a long-pending call with no
// later peers stays unflagged because we cannot tell from one row alone
// whether the tool is genuinely working or stuck.
func TestToolCallVisuallyOrphaned_DoesNotFlagWithoutSubsequentActivity(t *testing.T) {
	now := time.Now()
	calls := []ToolCallRecord{
		{ToolCallKey: "lone", ToolName: "summarize_workspace_state", StartedAt: now.Add(-2 * time.Minute)},
	}
	if toolCallVisuallyOrphaned(calls, 0, now) {
		t.Fatal("a lone long-pending call must not be flagged orphaned without subsequent activity")
	}
}

// TestToolCallVisuallyOrphaned_SkipsCompletedRows confirms completed rows
// never get flagged regardless of age or surrounding peers.
func TestToolCallVisuallyOrphaned_SkipsCompletedRows(t *testing.T) {
	now := time.Now()
	calls := []ToolCallRecord{
		{ToolCallKey: "done", ToolName: "summarize_workspace_state", StartedAt: now.Add(-2 * time.Minute), Completed: true, Success: true},
		{ToolCallKey: "p1", ToolName: "coord_query_view", StartedAt: now.Add(-90 * time.Second), Completed: true, Success: true},
		{ToolCallKey: "p2", ToolName: "coord_claim_scope", StartedAt: now.Add(-80 * time.Second), Completed: true, Success: true},
		{ToolCallKey: "p3", ToolName: "prepare_pipeline_write_context", StartedAt: now.Add(-70 * time.Second), Completed: true, Success: true},
	}
	if toolCallVisuallyOrphaned(calls, 0, now) {
		t.Fatal("completed rows must never be flagged orphaned")
	}
}

// TestToolCallVisuallyOrphaned_SkipsPendingChallengeRows confirms inter-agent
// challenge rows that legitimately wait for a peer response are exempt from
// the orphan heuristic — they are *expected* to remain pending after dispatch
// until the response thread arrives, sometimes for many minutes.
func TestToolCallVisuallyOrphaned_SkipsPendingChallengeRows(t *testing.T) {
	now := time.Now()
	calls := []ToolCallRecord{
		{
			ToolCallKey: "challenge",
			ToolName:    "challenge_agent",
			StartedAt:   now.Add(-2 * time.Minute),
			InterAgent:  &InterAgentTool{Kind: InterAgentToolChallenge, Status: InterAgentToolPending},
		},
		{ToolCallKey: "p1", Completed: true, Success: true, StartedAt: now.Add(-90 * time.Second)},
		{ToolCallKey: "p2", Completed: true, Success: true, StartedAt: now.Add(-80 * time.Second)},
		{ToolCallKey: "p3", Completed: true, Success: true, StartedAt: now.Add(-70 * time.Second)},
	}
	if toolCallVisuallyOrphaned(calls, 0, now) {
		t.Fatal("challenge rows must be exempt from the orphan heuristic — they are expected to wait")
	}
}

// TestFormatToolCallDuration_OrphanedSwapsLiveSpinnerForGlyph confirms the
// formatter renders the orphan glyph alongside the elapsed string when the
// transient OrphanedAtRender flag is set on a render-time copy. The persisted
// row is unaffected — flag is render-only.
func TestFormatToolCallDuration_OrphanedSwapsLiveSpinnerForGlyph(t *testing.T) {
	tc := ToolCallRecord{
		ToolCallKey:      "stuck",
		ToolName:         "summarize_workspace_state",
		StartedAt:        time.Now().Add(-2 * time.Minute),
		OrphanedAtRender: true,
	}
	got := formatToolCallDuration(tc)
	if !strings.Contains(got, orphanGlyph) {
		t.Fatalf("orphaned duration = %q, want it to contain the orphan glyph %q", got, orphanGlyph)
	}
}
