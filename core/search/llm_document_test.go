package search

import (
	"testing"
)

func TestStopReasonIsValid(t *testing.T) {
	tests := []struct {
		name     string
		reason   StopReason
		expected bool
	}{
		{"end_turn is valid", StopReasonEndTurn, true},
		{"max_tokens is valid", StopReasonMaxTokens, true},
		{"stop_sequence is valid", StopReasonStopSequence, true},
		{"tool_use is valid", StopReasonToolUse, true},
		{"empty string is invalid", StopReason(""), false},
		{"unknown value is invalid", StopReason("unknown"), false},
		{"random string is invalid", StopReason("random_value"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reason.IsValid(); got != tt.expected {
				t.Errorf("StopReason(%q).IsValid() = %v, want %v", tt.reason, got, tt.expected)
			}
		})
	}
}

func TestStopReasonString(t *testing.T) {
	tests := []struct {
		reason   StopReason
		expected string
	}{
		{StopReasonEndTurn, "end_turn"},
		{StopReasonMaxTokens, "max_tokens"},
		{StopReasonStopSequence, "stop_sequence"},
		{StopReasonToolUse, "tool_use"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.reason.String(); got != tt.expected {
				t.Errorf("StopReason.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestLLMPromptDocumentValidate(t *testing.T) {
	tests := []struct {
		name    string
		doc     *LLMPromptDocument
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid document",
			doc: &LLMPromptDocument{
				SessionID:  "session-123",
				AgentID:    "agent-456",
				AgentType:  "Engineer",
				Model:      "claude-3-opus",
				TokenCount: 1000,
				TurnNumber: 1,
			},
			wantErr: false,
		},
		{
			name: "valid with zero token count",
			doc: &LLMPromptDocument{
				SessionID:  "session-123",
				AgentID:    "agent-456",
				Model:      "claude-3-opus",
				TokenCount: 0,
				TurnNumber: 0,
			},
			wantErr: false,
		},
		{
			name: "valid with files",
			doc: &LLMPromptDocument{
				SessionID:      "session-123",
				AgentID:        "agent-456",
				Model:          "claude-3-opus",
				TokenCount:     500,
				TurnNumber:     2,
				PrecedingFiles: []string{"file1.go", "file2.go"},
				FollowingFiles: []string{"file3.go"},
			},
			wantErr: false,
		},
		{
			name: "missing session_id",
			doc: &LLMPromptDocument{
				AgentID:    "agent-456",
				Model:      "claude-3-opus",
				TokenCount: 1000,
			},
			wantErr: true,
			errMsg:  "session_id is required",
		},
		{
			name: "missing agent_id",
			doc: &LLMPromptDocument{
				SessionID:  "session-123",
				Model:      "claude-3-opus",
				TokenCount: 1000,
			},
			wantErr: true,
			errMsg:  "agent_id is required",
		},
		{
			name: "missing model",
			doc: &LLMPromptDocument{
				SessionID:  "session-123",
				AgentID:    "agent-456",
				TokenCount: 1000,
			},
			wantErr: true,
			errMsg:  "model is required",
		},
		{
			name: "negative token_count",
			doc: &LLMPromptDocument{
				SessionID:  "session-123",
				AgentID:    "agent-456",
				Model:      "claude-3-opus",
				TokenCount: -1,
			},
			wantErr: true,
			errMsg:  "token_count must be non-negative",
		},
		{
			name: "negative turn_number",
			doc: &LLMPromptDocument{
				SessionID:  "session-123",
				AgentID:    "agent-456",
				Model:      "claude-3-opus",
				TokenCount: 100,
				TurnNumber: -1,
			},
			wantErr: true,
			errMsg:  "turn_number must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.doc.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("LLMPromptDocument.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err.Error() != tt.errMsg {
				t.Errorf("LLMPromptDocument.Validate() error = %q, want %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestLLMResponseDocumentValidate(t *testing.T) {
	tests := []struct {
		name    string
		doc     *LLMResponseDocument
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid document",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: 2000,
				LatencyMs:  1500,
				StopReason: StopReasonEndTurn,
			},
			wantErr: false,
		},
		{
			name: "valid with empty stop_reason",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: 2000,
				LatencyMs:  1500,
				StopReason: "",
			},
			wantErr: false,
		},
		{
			name: "valid with code blocks",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: 2000,
				LatencyMs:  1500,
				StopReason: StopReasonEndTurn,
				CodeBlocks: []CodeBlock{
					{Language: "go", Content: "func main() {}", LineNum: 10},
				},
			},
			wantErr: false,
		},
		{
			name: "valid with files modified and tools used",
			doc: &LLMResponseDocument{
				PromptID:      "prompt-123",
				SessionID:     "session-456",
				AgentID:       "agent-789",
				Model:         "claude-3-opus",
				TokenCount:    2000,
				LatencyMs:     1500,
				StopReason:    StopReasonToolUse,
				FilesModified: []string{"main.go", "util.go"},
				ToolsUsed:     []string{"read_file", "write_file"},
			},
			wantErr: false,
		},
		{
			name: "missing prompt_id",
			doc: &LLMResponseDocument{
				SessionID:  "session-456",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: 2000,
			},
			wantErr: true,
			errMsg:  "prompt_id is required",
		},
		{
			name: "missing session_id",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: 2000,
			},
			wantErr: true,
			errMsg:  "session_id is required",
		},
		{
			name: "missing agent_id",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				Model:      "claude-3-opus",
				TokenCount: 2000,
			},
			wantErr: true,
			errMsg:  "agent_id is required",
		},
		{
			name: "missing model",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				AgentID:    "agent-789",
				TokenCount: 2000,
			},
			wantErr: true,
			errMsg:  "model is required",
		},
		{
			name: "negative token_count",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: -1,
			},
			wantErr: true,
			errMsg:  "token_count must be non-negative",
		},
		{
			name: "negative latency_ms",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: 2000,
				LatencyMs:  -1,
			},
			wantErr: true,
			errMsg:  "latency_ms must be non-negative",
		},
		{
			name: "invalid stop_reason",
			doc: &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: 2000,
				LatencyMs:  1500,
				StopReason: StopReason("invalid_reason"),
			},
			wantErr: true,
			errMsg:  "invalid stop_reason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.doc.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("LLMResponseDocument.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err.Error() != tt.errMsg {
				t.Errorf("LLMResponseDocument.Validate() error = %q, want %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestHasCodeBlocks(t *testing.T) {
	tests := []struct {
		name     string
		doc      *LLMResponseDocument
		expected bool
	}{
		{
			name:     "no code blocks (nil)",
			doc:      &LLMResponseDocument{},
			expected: false,
		},
		{
			name: "no code blocks (empty slice)",
			doc: &LLMResponseDocument{
				CodeBlocks: []CodeBlock{},
			},
			expected: false,
		},
		{
			name: "has one code block",
			doc: &LLMResponseDocument{
				CodeBlocks: []CodeBlock{
					{Language: "go", Content: "package main", LineNum: 1},
				},
			},
			expected: true,
		},
		{
			name: "has multiple code blocks",
			doc: &LLMResponseDocument{
				CodeBlocks: []CodeBlock{
					{Language: "go", Content: "package main", LineNum: 1},
					{Language: "python", Content: "print('hello')", LineNum: 10},
					{Language: "go", Content: "func foo() {}", LineNum: 20},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.doc.HasCodeBlocks(); got != tt.expected {
				t.Errorf("HasCodeBlocks() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetCodeBlocksByLanguage(t *testing.T) {
	doc := &LLMResponseDocument{
		CodeBlocks: []CodeBlock{
			{Language: "go", Content: "package main", LineNum: 1},
			{Language: "Python", Content: "print('hello')", LineNum: 10},
			{Language: "GO", Content: "func foo() {}", LineNum: 20},
			{Language: "typescript", Content: "const x = 1;", LineNum: 30},
			{Language: "go", Content: "type Bar struct {}", LineNum: 40},
		},
	}

	tests := []struct {
		name          string
		lang          string
		expectedCount int
	}{
		{"go lowercase", "go", 3},
		{"go uppercase", "GO", 3},
		{"go mixed case", "Go", 3},
		{"python lowercase", "python", 1},
		{"python titlecase", "Python", 1},
		{"typescript", "typescript", 1},
		{"nonexistent language", "rust", 0},
		{"empty language", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := doc.GetCodeBlocksByLanguage(tt.lang)
			if len(blocks) != tt.expectedCount {
				t.Errorf("GetCodeBlocksByLanguage(%q) returned %d blocks, want %d", tt.lang, len(blocks), tt.expectedCount)
			}
		})
	}
}

func TestGetCodeBlocksByLanguageEmptyDocument(t *testing.T) {
	t.Run("nil code blocks", func(t *testing.T) {
		doc := &LLMResponseDocument{}
		blocks := doc.GetCodeBlocksByLanguage("go")
		if blocks != nil {
			t.Errorf("GetCodeBlocksByLanguage() = %v, want nil", blocks)
		}
	})

	t.Run("empty code blocks", func(t *testing.T) {
		doc := &LLMResponseDocument{
			CodeBlocks: []CodeBlock{},
		}
		blocks := doc.GetCodeBlocksByLanguage("go")
		if blocks != nil {
			t.Errorf("GetCodeBlocksByLanguage() = %v, want nil", blocks)
		}
	})
}

func TestGetCodeBlocksByLanguageContentPreserved(t *testing.T) {
	doc := &LLMResponseDocument{
		CodeBlocks: []CodeBlock{
			{Language: "go", Content: "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}", LineNum: 5},
		},
	}

	blocks := doc.GetCodeBlocksByLanguage("go")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}

	expected := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}"
	if blocks[0].Content != expected {
		t.Errorf("Content = %q, want %q", blocks[0].Content, expected)
	}
	if blocks[0].LineNum != 5 {
		t.Errorf("LineNum = %d, want 5", blocks[0].LineNum)
	}
}

func TestCodeBlockStruct(t *testing.T) {
	block := CodeBlock{
		Language: "javascript",
		Content:  "console.log('test');",
		LineNum:  42,
	}

	if block.Language != "javascript" {
		t.Errorf("Language = %q, want %q", block.Language, "javascript")
	}
	if block.Content != "console.log('test');" {
		t.Errorf("Content = %q, want %q", block.Content, "console.log('test');")
	}
	if block.LineNum != 42 {
		t.Errorf("LineNum = %d, want 42", block.LineNum)
	}
}

func TestLLMPromptDocumentEmbedding(t *testing.T) {
	doc := &LLMPromptDocument{
		Document: Document{
			ID:       "doc-123",
			Path:     "/prompts/test-prompt",
			Type:     DocTypeLLMPrompt,
			Content:  "Write a function that...",
			Checksum: "abc123",
		},
		SessionID:  "session-123",
		AgentID:    "agent-456",
		AgentType:  "Engineer",
		Model:      "claude-3-opus",
		TokenCount: 500,
		TurnNumber: 1,
	}

	if doc.ID != "doc-123" {
		t.Errorf("embedded ID = %q, want %q", doc.ID, "doc-123")
	}
	if doc.Path != "/prompts/test-prompt" {
		t.Errorf("embedded Path = %q, want %q", doc.Path, "/prompts/test-prompt")
	}
	if doc.Content != "Write a function that..." {
		t.Errorf("embedded Content = %q, want %q", doc.Content, "Write a function that...")
	}
}

func TestLLMResponseDocumentEmbedding(t *testing.T) {
	doc := &LLMResponseDocument{
		Document: Document{
			ID:       "doc-456",
			Path:     "/responses/test-response",
			Type:     DocTypeLLMResponse,
			Content:  "Here is the implementation...",
			Checksum: "def456",
		},
		PromptID:   "prompt-123",
		SessionID:  "session-456",
		AgentID:    "agent-789",
		Model:      "claude-3-opus",
		TokenCount: 1500,
		LatencyMs:  2500,
		StopReason: StopReasonEndTurn,
	}

	if doc.ID != "doc-456" {
		t.Errorf("embedded ID = %q, want %q", doc.ID, "doc-456")
	}
	if doc.Path != "/responses/test-response" {
		t.Errorf("embedded Path = %q, want %q", doc.Path, "/responses/test-response")
	}
	if doc.Content != "Here is the implementation..." {
		t.Errorf("embedded Content = %q, want %q", doc.Content, "Here is the implementation...")
	}
}

func TestManyToolsUsed(t *testing.T) {
	tools := make([]string, 100)
	for i := 0; i < 100; i++ {
		tools[i] = "tool_" + string(rune('a'+i%26))
	}

	doc := &LLMResponseDocument{
		PromptID:   "prompt-123",
		SessionID:  "session-456",
		AgentID:    "agent-789",
		Model:      "claude-3-opus",
		TokenCount: 2000,
		LatencyMs:  5000,
		StopReason: StopReasonToolUse,
		ToolsUsed:  tools,
	}

	err := doc.Validate()
	if err != nil {
		t.Errorf("validation should pass with many tools, got error: %v", err)
	}

	if len(doc.ToolsUsed) != 100 {
		t.Errorf("ToolsUsed length = %d, want 100", len(doc.ToolsUsed))
	}
}

func TestManyFilesModified(t *testing.T) {
	files := make([]string, 50)
	for i := 0; i < 50; i++ {
		files[i] = "file_" + string(rune('a'+i%26)) + ".go"
	}

	doc := &LLMResponseDocument{
		PromptID:      "prompt-123",
		SessionID:     "session-456",
		AgentID:       "agent-789",
		Model:         "claude-3-opus",
		TokenCount:    2000,
		LatencyMs:     5000,
		StopReason:    StopReasonEndTurn,
		FilesModified: files,
	}

	err := doc.Validate()
	if err != nil {
		t.Errorf("validation should pass with many files modified, got error: %v", err)
	}

	if len(doc.FilesModified) != 50 {
		t.Errorf("FilesModified length = %d, want 50", len(doc.FilesModified))
	}
}

func TestManyCodeBlocks(t *testing.T) {
	blocks := make([]CodeBlock, 20)
	for i := 0; i < 20; i++ {
		blocks[i] = CodeBlock{
			Language: "go",
			Content:  "// Code block " + string(rune('0'+i%10)),
			LineNum:  i * 10,
		}
	}

	doc := &LLMResponseDocument{
		PromptID:   "prompt-123",
		SessionID:  "session-456",
		AgentID:    "agent-789",
		Model:      "claude-3-opus",
		TokenCount: 2000,
		LatencyMs:  5000,
		StopReason: StopReasonEndTurn,
		CodeBlocks: blocks,
	}

	err := doc.Validate()
	if err != nil {
		t.Errorf("validation should pass with many code blocks, got error: %v", err)
	}

	if !doc.HasCodeBlocks() {
		t.Error("HasCodeBlocks() should return true")
	}

	goBlocks := doc.GetCodeBlocksByLanguage("go")
	if len(goBlocks) != 20 {
		t.Errorf("GetCodeBlocksByLanguage(\"go\") returned %d blocks, want 20", len(goBlocks))
	}
}

func TestEmptyStringsInSlices(t *testing.T) {
	t.Run("empty preceding files", func(t *testing.T) {
		doc := &LLMPromptDocument{
			SessionID:      "session-123",
			AgentID:        "agent-456",
			Model:          "claude-3-opus",
			PrecedingFiles: []string{"", "file.go", ""},
		}
		err := doc.Validate()
		if err != nil {
			t.Errorf("validation should pass with empty strings in slices: %v", err)
		}
	})

	t.Run("empty tools used", func(t *testing.T) {
		doc := &LLMResponseDocument{
			PromptID:  "prompt-123",
			SessionID: "session-456",
			AgentID:   "agent-789",
			Model:     "claude-3-opus",
			ToolsUsed: []string{"", "read_file", ""},
		}
		err := doc.Validate()
		if err != nil {
			t.Errorf("validation should pass with empty strings in tools: %v", err)
		}
	})
}

func TestAllStopReasons(t *testing.T) {
	stopReasons := []StopReason{
		StopReasonEndTurn,
		StopReasonMaxTokens,
		StopReasonStopSequence,
		StopReasonToolUse,
	}

	for _, sr := range stopReasons {
		t.Run(sr.String(), func(t *testing.T) {
			doc := &LLMResponseDocument{
				PromptID:   "prompt-123",
				SessionID:  "session-456",
				AgentID:    "agent-789",
				Model:      "claude-3-opus",
				TokenCount: 1000,
				LatencyMs:  500,
				StopReason: sr,
			}

			err := doc.Validate()
			if err != nil {
				t.Errorf("validation should pass for stop_reason %q: %v", sr, err)
			}
		})
	}
}
