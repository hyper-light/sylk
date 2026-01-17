package parsers

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

type ESLintParser struct {
	textPattern *regexp.Regexp
}

func NewESLintParser() *ESLintParser {
	return &ESLintParser{
		textPattern: regexp.MustCompile(`^\s*(\d+):(\d+)\s+(error|warning)\s+(.+?)\s+(\S+)$`),
	}
}

type ESLintResult struct {
	Errors   []ParseError
	Warnings []ParseWarning
}

func (p *ESLintParser) Parse(stdout, stderr []byte) (any, error) {
	if json.Valid(stdout) {
		return p.parseJSON(stdout)
	}
	return p.parseText(string(stdout))
}

func (p *ESLintParser) parseJSON(data []byte) (*ESLintResult, error) {
	var files []eslintJSONFile
	if err := json.Unmarshal(data, &files); err != nil {
		return nil, err
	}

	result := &ESLintResult{}
	for _, file := range files {
		p.processJSONFile(file, result)
	}
	return result, nil
}

type eslintJSONFile struct {
	FilePath string          `json:"filePath"`
	Messages []eslintJSONMsg `json:"messages"`
}

type eslintJSONMsg struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
	RuleID   string `json:"ruleId"`
	Severity int    `json:"severity"`
}

func (p *ESLintParser) processJSONFile(file eslintJSONFile, result *ESLintResult) {
	for _, msg := range file.Messages {
		if msg.Severity == 2 {
			result.Errors = append(result.Errors, ParseError{
				File: file.FilePath, Line: msg.Line, Column: msg.Column,
				Message: msg.Message, Code: msg.RuleID,
			})
		} else {
			result.Warnings = append(result.Warnings, ParseWarning{
				File: file.FilePath, Line: msg.Line, Column: msg.Column,
				Message: msg.Message, Code: msg.RuleID,
			})
		}
	}
}

func (p *ESLintParser) parseText(output string) (*ESLintResult, error) {
	result := &ESLintResult{}
	var currentFile string

	for _, line := range strings.Split(output, "\n") {
		if newFile := p.tryExtractFilePath(line); newFile != "" {
			currentFile = newFile
			continue
		}
		p.parseTextLine(line, currentFile, result)
	}
	return result, nil
}

func (p *ESLintParser) tryExtractFilePath(line string) string {
	isPathLine := strings.HasPrefix(line, "/") || strings.Contains(line, ":")
	if !isPathLine {
		return ""
	}
	if p.textPattern.MatchString(line) {
		return ""
	}
	return strings.TrimSpace(line)
}

func (p *ESLintParser) parseTextLine(line, file string, result *ESLintResult) {
	matches := p.textPattern.FindStringSubmatch(line)
	if matches == nil {
		return
	}

	lineNum, _ := strconv.Atoi(matches[1])
	colNum, _ := strconv.Atoi(matches[2])
	severity := matches[3]
	message := matches[4]
	rule := matches[5]

	if severity == "error" {
		result.Errors = append(result.Errors, ParseError{
			File: file, Line: lineNum, Column: colNum, Message: message, Code: rule,
		})
	} else {
		result.Warnings = append(result.Warnings, ParseWarning{
			File: file, Line: lineNum, Column: colNum, Message: message, Code: rule,
		})
	}
}
