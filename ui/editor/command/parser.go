// Package command implements the command-line parser, registry, and built-in
// commands for the editor's : command mode.
package command

import "strings"

// ParsedCommand is the result of parsing a colon command line.
type ParsedCommand struct {
	Name  string
	Bang  bool
	Range *RangeSpec
	Args  string
}

// charClass classifies characters during command-line parsing.
type charClass int

const (
	classWhitespace charClass = iota
	classAlpha
	classBang
	classOther
)

// charClassEntry maps a predicate to its class.
type charClassEntry struct {
	test  func(r rune) bool
	class charClass
}

// charClassTable is checked in order; first match wins.
var charClassTable = []charClassEntry{
	{test: func(r rune) bool { return r == ' ' || r == '\t' }, class: classWhitespace},
	{test: isAlphaRune, class: classAlpha},
	{test: func(r rune) bool { return r == '!' }, class: classBang},
}

func isAlphaRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// classifyChar returns the character class for a rune.
func classifyChar(r rune) charClass {
	for _, entry := range charClassTable {
		if entry.test(r) {
			return entry.class
		}
	}
	return classOther
}

// Parse parses a colon command string (without the leading `:`) into its
// constituent parts: optional range, command name, optional bang, and
// arguments. The parser uses table-driven character classification.
func Parse(input string) (*ParsedCommand, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return &ParsedCommand{}, nil
	}
	// Handle bare ! as shell command.
	if input[0] == '!' {
		return &ParsedCommand{
			Name: "!",
			Args: strings.TrimSpace(input[1:]),
		}, nil
	}
	// Parse optional range prefix.
	rangeSpec, rest := ParseRangeSpec(input)
	rest = strings.TrimSpace(rest)
	// Extract command name.
	name, rest := extractName([]rune(rest))
	// Check for bang.
	bang := false
	runes := []rune(rest)
	if len(runes) > 0 && runes[0] == '!' {
		bang = true
		runes = runes[1:]
	}
	args := strings.TrimSpace(string(runes))
	return &ParsedCommand{
		Name:  name,
		Bang:  bang,
		Range: rangeSpec,
		Args:  args,
	}, nil
}

// extractName reads the command name from the beginning of the rune slice.
// Command names consist of alphabetic characters. Special single-character
// commands (like s for substitute) are handled by the caller through the
// registry.
func extractName(runes []rune) (string, string) {
	end := 0
	for end < len(runes) {
		c := classifyChar(runes[end])
		if c != classAlpha {
			break
		}
		end++
	}
	return string(runes[:end]), string(runes[end:])
}
