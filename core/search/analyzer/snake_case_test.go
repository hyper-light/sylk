package analyzer

import (
	"testing"

	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSnakeCaseFilter(t *testing.T) {
	filter, err := NewSnakeCaseFilter(nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, filter)
	assert.IsType(t, &SnakeCaseFilter{}, filter)
}

func TestSnakeCaseFilter_Filter(t *testing.T) {
	filter := &SnakeCaseFilter{}

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple snake_case",
			input:    "get_user",
			expected: []string{"get", "user", "get_user"},
		},
		{
			name:     "multiple parts",
			input:    "get_user_by_id",
			expected: []string{"get", "user", "by", "id", "get_user_by_id"},
		},
		{
			name:     "python dunder __init__",
			input:    "__init__",
			expected: []string{"init", "__init__"},
		},
		{
			name:     "kebab-case",
			input:    "get-user-id",
			expected: []string{"get", "user", "id", "get-user-id"},
		},
		{
			name:     "mixed separators",
			input:    "get_user-id",
			expected: []string{"get", "user", "id", "get_user-id"},
		},
		{
			name:     "no separator - single word",
			input:    "getuser",
			expected: []string{"getuser"},
		},
		{
			name:     "leading underscore _private",
			input:    "_private",
			expected: []string{"private", "_private"},
		},
		{
			name:     "trailing underscore value_",
			input:    "value_",
			expected: []string{"value", "value_"},
		},
		{
			name:     "consecutive underscores",
			input:    "get__user",
			expected: []string{"get", "user", "get__user"},
		},
		{
			name:     "single underscore only",
			input:    "_",
			expected: []string{"_"},
		},
		{
			name:     "double underscore only",
			input:    "__",
			expected: []string{"__"},
		},
		{
			name:     "single hyphen only",
			input:    "-",
			expected: []string{"-"},
		},
		{
			name:     "three consecutive underscores",
			input:    "a___b",
			expected: []string{"a", "b", "a___b"},
		},
		{
			name:     "leading and trailing",
			input:    "_value_",
			expected: []string{"value", "_value_"},
		},
		{
			name:     "double leading underscore",
			input:    "__private",
			expected: []string{"private", "__private"},
		},
		{
			name:     "triple underscore method",
			input:    "___magic___",
			expected: []string{"magic", "___magic___"},
		},
		{
			name:     "single character parts",
			input:    "a_b_c",
			expected: []string{"a", "b", "c", "a_b_c"},
		},
		{
			name:     "mixed with numbers",
			input:    "user_123_id",
			expected: []string{"user", "123", "id", "user_123_id"},
		},
		{
			name:     "kebab with numbers",
			input:    "api-v2-client",
			expected: []string{"api", "v2", "client", "api-v2-client"},
		},
		{
			name:     "all caps snake",
			input:    "HTTP_STATUS_CODE",
			expected: []string{"HTTP", "STATUS", "CODE", "HTTP_STATUS_CODE"},
		},
		{
			name:     "mixed case parts",
			input:    "Get_User_ById",
			expected: []string{"Get", "User", "ById", "Get_User_ById"},
		},
		{
			name:     "unicode preserved",
			input:    "get_用户_id",
			expected: []string{"get", "用户", "id", "get_用户_id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputStream := analysis.TokenStream{
				&analysis.Token{
					Term:     []byte(tc.input),
					Position: 1,
					Start:    0,
					End:      len(tc.input),
					Type:     analysis.AlphaNumeric,
				},
			}

			output := filter.Filter(inputStream)

			got := make([]string, len(output))
			for i, token := range output {
				got[i] = string(token.Term)
			}

			assert.Equal(t, tc.expected, got, "for input %q", tc.input)
		})
	}
}

func TestSnakeCaseFilter_EmptyInput(t *testing.T) {
	filter := &SnakeCaseFilter{}

	// Empty token stream
	output := filter.Filter(analysis.TokenStream{})
	assert.Empty(t, output)
}

func TestSnakeCaseFilter_EmptyToken(t *testing.T) {
	filter := &SnakeCaseFilter{}

	inputStream := analysis.TokenStream{
		&analysis.Token{
			Term:     []byte(""),
			Position: 1,
			Start:    0,
			End:      0,
			Type:     analysis.AlphaNumeric,
		},
	}

	output := filter.Filter(inputStream)
	require.Len(t, output, 1)
	assert.Equal(t, "", string(output[0].Term))
}

func TestSnakeCaseFilter_PreservesTokenMetadata(t *testing.T) {
	filter := &SnakeCaseFilter{}

	inputStream := analysis.TokenStream{
		&analysis.Token{
			Term:     []byte("get_user"),
			Position: 5,
			Start:    10,
			End:      18,
			Type:     analysis.AlphaNumeric,
		},
	}

	output := filter.Filter(inputStream)

	// Should have 3 tokens: "get", "user", "get_user"
	require.Len(t, output, 3)

	// All tokens should preserve position metadata
	for _, token := range output {
		assert.Equal(t, 5, token.Position, "position should be preserved")
		assert.Equal(t, 10, token.Start, "start should be preserved")
		assert.Equal(t, 18, token.End, "end should be preserved")
		assert.Equal(t, analysis.AlphaNumeric, token.Type, "type should be preserved")
	}
}

func TestSnakeCaseFilter_MultipleTokens(t *testing.T) {
	filter := &SnakeCaseFilter{}

	inputStream := analysis.TokenStream{
		&analysis.Token{
			Term:     []byte("get_user"),
			Position: 1,
			Start:    0,
			End:      8,
			Type:     analysis.AlphaNumeric,
		},
		&analysis.Token{
			Term:     []byte("by_id"),
			Position: 2,
			Start:    9,
			End:      14,
			Type:     analysis.AlphaNumeric,
		},
	}

	output := filter.Filter(inputStream)

	// get_user -> get, user, get_user (3)
	// by_id -> by, id, by_id (3)
	// Total: 6 tokens
	require.Len(t, output, 6)

	expected := []string{"get", "user", "get_user", "by", "id", "by_id"}
	for i, token := range output {
		assert.Equal(t, expected[i], string(token.Term))
	}
}

func TestSnakeCaseFilter_OriginalTokenLast(t *testing.T) {
	filter := &SnakeCaseFilter{}

	inputStream := analysis.TokenStream{
		&analysis.Token{
			Term:     []byte("get_user_by_id"),
			Position: 1,
			Start:    0,
			End:      14,
			Type:     analysis.AlphaNumeric,
		},
	}

	output := filter.Filter(inputStream)

	// Original token should be last
	lastToken := output[len(output)-1]
	assert.Equal(t, "get_user_by_id", string(lastToken.Term))
}

func TestSplitOnSeparators(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple underscore",
			input:    "a_b",
			expected: []string{"a", "b"},
		},
		{
			name:     "simple hyphen",
			input:    "a-b",
			expected: []string{"a", "b"},
		},
		{
			name:     "mixed separators",
			input:    "a_b-c",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "no separator",
			input:    "abc",
			expected: []string{"abc"},
		},
		{
			name:     "leading underscore",
			input:    "_abc",
			expected: []string{"abc"},
		},
		{
			name:     "trailing underscore",
			input:    "abc_",
			expected: []string{"abc"},
		},
		{
			name:     "consecutive separators",
			input:    "a__b",
			expected: []string{"a", "b"},
		},
		{
			name:     "only separator",
			input:    "_",
			expected: []string{},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitOnSeparators(tc.input)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestSnakeCaseFilterName(t *testing.T) {
	assert.Equal(t, "snake_case", SnakeCaseFilterName)
}
