package sylkdir

import "github.com/adalundhe/sylk/core/search"

// ContentClass categorizes document content for chunker selection.
// Stored in ChunkSpan.Flags bits 2-5 (4 bits, max 15 values).
type ContentClass uint8

const (
	ContentClassSourceCode  ContentClass = 0
	ContentClassMarkdown    ContentClass = 1
	ContentClassWebContent  ContentClass = 2
	ContentClassLLMPrompt   ContentClass = 3
	ContentClassLLMResponse ContentClass = 4
	ContentClassGitCommit   ContentClass = 5
	ContentClassConfig      ContentClass = 6
	ContentClassNote        ContentClass = 7
	ContentClassAcademic    ContentClass = 8
)

// contentClassCount is the number of defined ContentClass values.
const contentClassCount = 9

// ContentClassFromDocType maps a search.DocumentType to a ContentClass.
func ContentClassFromDocType(dt search.DocumentType) ContentClass {
	switch dt {
	case search.DocTypeSourceCode:
		return ContentClassSourceCode
	case search.DocTypeMarkdown:
		return ContentClassMarkdown
	case search.DocTypeWebFetch:
		return ContentClassWebContent
	case search.DocTypeLLMPrompt:
		return ContentClassLLMPrompt
	case search.DocTypeLLMResponse:
		return ContentClassLLMResponse
	case search.DocTypeGitCommit:
		return ContentClassGitCommit
	case search.DocTypeConfig:
		return ContentClassConfig
	case search.DocTypeNote:
		return ContentClassNote
	default:
		return ContentClassNote // Conservative default: prose-like chunking
	}
}

// String returns the string representation of the ContentClass.
func (cc ContentClass) String() string {
	switch cc {
	case ContentClassSourceCode:
		return "source_code"
	case ContentClassMarkdown:
		return "markdown"
	case ContentClassWebContent:
		return "web_content"
	case ContentClassLLMPrompt:
		return "llm_prompt"
	case ContentClassLLMResponse:
		return "llm_response"
	case ContentClassGitCommit:
		return "git_commit"
	case ContentClassConfig:
		return "config"
	case ContentClassNote:
		return "note"
	case ContentClassAcademic:
		return "academic"
	default:
		return "unknown"
	}
}

// IsValid returns true if the ContentClass is a recognized value.
func (cc ContentClass) IsValid() bool {
	return cc < contentClassCount
}
