package parsers

import (
	"regexp"
	"strconv"
	"strings"
)

type GoParser struct {
	errorPattern *regexp.Regexp
	testPattern  *regexp.Regexp
}

func NewGoParser() *GoParser {
	return &GoParser{
		errorPattern: regexp.MustCompile(`^(.+):(\d+):(\d+):\s*(.+)$`),
		testPattern:  regexp.MustCompile(`^---\s*(PASS|FAIL|SKIP):\s*(\S+)\s*\(([0-9.]+)s\)$`),
	}
}

type GoParseResult struct {
	Errors   []ParseError
	Warnings []ParseWarning
	Tests    []TestResult
	Coverage float64
}

func (p *GoParser) Parse(stdout, stderr []byte) (any, error) {
	result := &GoParseResult{}
	p.parseOutput(string(stdout), result)
	p.parseOutput(string(stderr), result)
	return result, nil
}

func (p *GoParser) parseOutput(output string, result *GoParseResult) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		p.parseLine(line, result)
	}
}

func (p *GoParser) parseLine(line string, result *GoParseResult) {
	if p.parseErrorLine(line, result) {
		return
	}
	p.parseTestLine(line, result)
}

func (p *GoParser) parseErrorLine(line string, result *GoParseResult) bool {
	matches := p.errorPattern.FindStringSubmatch(line)
	if matches == nil {
		return false
	}

	lineNum, _ := strconv.Atoi(matches[2])
	colNum, _ := strconv.Atoi(matches[3])
	result.Errors = append(result.Errors, ParseError{
		File:    matches[1],
		Line:    lineNum,
		Column:  colNum,
		Message: matches[4],
	})
	return true
}

func (p *GoParser) parseTestLine(line string, result *GoParseResult) bool {
	matches := p.testPattern.FindStringSubmatch(line)
	if matches == nil {
		return false
	}

	duration, _ := strconv.ParseFloat(matches[3], 64)
	result.Tests = append(result.Tests, TestResult{
		Name:     matches[2],
		Status:   p.parseTestStatus(matches[1]),
		Duration: duration,
	})
	return true
}

func (p *GoParser) parseTestStatus(s string) TestStatus {
	switch s {
	case "PASS":
		return TestStatusPass
	case "FAIL":
		return TestStatusFail
	case "SKIP":
		return TestStatusSkip
	default:
		return TestStatusFail
	}
}
