package agent

import "regexp"

var ansiPatternForTests = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPatternForTests.ReplaceAllString(s, "")
}
