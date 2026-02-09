package sylkdir

import (
	"testing"

	"github.com/adalundhe/sylk/core/search"
)

func TestContentClassFromDocType(t *testing.T) {
	tests := []struct {
		dt   search.DocumentType
		want ContentClass
	}{
		{search.DocTypeSourceCode, ContentClassSourceCode},
		{search.DocTypeMarkdown, ContentClassMarkdown},
		{search.DocTypeWebFetch, ContentClassWebContent},
		{search.DocTypeLLMPrompt, ContentClassLLMPrompt},
		{search.DocTypeLLMResponse, ContentClassLLMResponse},
		{search.DocTypeGitCommit, ContentClassGitCommit},
		{search.DocTypeConfig, ContentClassConfig},
		{search.DocTypeNote, ContentClassNote},
	}

	for _, tt := range tests {
		got := ContentClassFromDocType(tt.dt)
		if got != tt.want {
			t.Errorf("ContentClassFromDocType(%q): got %d (%s), want %d (%s)",
				tt.dt, got, got.String(), tt.want, tt.want.String())
		}
	}
}

func TestContentClassFromDocTypeUnknown(t *testing.T) {
	got := ContentClassFromDocType("unknown_type")
	if got != ContentClassNote {
		t.Errorf("unknown doc type: got %d, want %d (ContentClassNote)", got, ContentClassNote)
	}
}

func TestContentClassString(t *testing.T) {
	tests := []struct {
		cc   ContentClass
		want string
	}{
		{ContentClassSourceCode, "source_code"},
		{ContentClassMarkdown, "markdown"},
		{ContentClassWebContent, "web_content"},
		{ContentClassLLMPrompt, "llm_prompt"},
		{ContentClassLLMResponse, "llm_response"},
		{ContentClassGitCommit, "git_commit"},
		{ContentClassConfig, "config"},
		{ContentClassNote, "note"},
		{ContentClassAcademic, "academic"},
		{ContentClass(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.cc.String(); got != tt.want {
			t.Errorf("ContentClass(%d).String(): got %q, want %q", tt.cc, got, tt.want)
		}
	}
}

func TestContentClassIsValid(t *testing.T) {
	for cc := ContentClass(0); cc < contentClassCount; cc++ {
		if !cc.IsValid() {
			t.Errorf("ContentClass(%d) should be valid", cc)
		}
	}

	if ContentClass(contentClassCount).IsValid() {
		t.Errorf("ContentClass(%d) should be invalid", contentClassCount)
	}
}
