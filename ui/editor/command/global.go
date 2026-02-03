package command

import (
	"errors"
	"regexp"
)

// GlobalCmd represents a parsed :g or :v command.
type GlobalCmd struct {
	Pattern string
	Command string
	Invert  bool // true for :v (match lines that do NOT match)
}

// ParseGlobal parses the argument portion of a :g/pattern/cmd or
// :v/pattern/cmd command. The first character of args is the delimiter.
// Alternate delimiters are supported (e.g. :g#pattern#cmd).
func ParseGlobal(args string, invert bool) (*GlobalCmd, error) {
	runes := []rune(args)
	if len(runes) == 0 {
		return nil, errors.New("global: empty argument")
	}
	delim := runes[0]
	pattern, cmd, err := splitGlobalParts(runes[1:], delim)
	if err != nil {
		return nil, err
	}
	return &GlobalCmd{
		Pattern: pattern,
		Command: cmd,
		Invert:  invert,
	}, nil
}

// splitGlobalParts splits the rune slice on the delimiter into pattern and
// command. The delimiter may appear at most twice (opening and closing the
// pattern); everything after the second delimiter is the command.
func splitGlobalParts(runes []rune, delim rune) (string, string, error) {
	end := 0
	for end < len(runes) && runes[end] != delim {
		end++
	}
	if end == 0 {
		return "", "", errors.New("global: empty pattern")
	}
	pattern := string(runes[:end])
	cmd := ""
	if end < len(runes) {
		// Skip closing delimiter.
		cmd = trimLeadingSpace(string(runes[end+1:]))
	}
	return pattern, cmd, nil
}

// trimLeadingSpace removes leading whitespace without importing strings
// (already imported in other files, but keeping this self-contained for
// readability).
func trimLeadingSpace(s string) string {
	runes := []rune(s)
	i := 0
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	return string(runes[i:])
}

// ExecuteGlobal runs the global command against the provided lines, invoking
// exec for each matching (or non-matching, if inverted) line. Lines are
// bounded to the length of the input slice. Returns the count of lines
// processed.
func ExecuteGlobal(cmd *GlobalCmd, lines []string, exec func(lineNum int, line string) error) (int, error) {
	re, err := regexp.Compile(cmd.Pattern)
	if err != nil {
		return 0, errors.New("global: invalid pattern: " + err.Error())
	}
	count := 0
	for i, line := range lines {
		matched := re.MatchString(line)
		if shouldProcess(matched, cmd.Invert) {
			if err := exec(i, line); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

// shouldProcess determines whether a line should be processed based on
// match status and inversion.
func shouldProcess(matched, invert bool) bool {
	return matched != invert
}
