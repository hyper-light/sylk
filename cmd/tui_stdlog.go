package cmd

import (
	"bytes"
	"errors"
	"io"
	"log"
	"log/slog"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/agentlog"
)

const (
	tuiStdLogAgentID = "tui"
)

// installTUIStdLogSink redirects stdlib logger output away from stdout/stderr
// while the TUI is active. Benign shutdown noise is suppressed; all other lines
// are persisted to the TUI WAL.
func installTUIStdLogSink() func() error {
	sink, sinkCloser := newTUIStdLogSink()
	log.SetOutput(sink)

	return func() error {
		// Keep stdlib logging off stdout/stderr during teardown and process exit.
		log.SetOutput(io.Discard)
		flushErr := sink.Close()
		if sinkCloser == nil {
			return flushErr
		}
		return errors.Join(flushErr, sinkCloser.Close())
	}
}

func newTUIStdLogSink() (*tuiStdLogSink, io.Closer) {
	logger, closer, err := agentlog.NewWALLogger(tuiStdLogAgentID)
	if err != nil {
		return &tuiStdLogSink{fallback: io.Discard}, nil
	}
	return &tuiStdLogSink{
		logger:   logger,
		fallback: io.Discard,
	}, closer
}

type tuiStdLogSink struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	logger   *slog.Logger
	fallback io.Writer
}

func (s *tuiStdLogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.buffer.Write(p)
	s.flushLinesLocked(false)
	return len(p), nil
}

func (s *tuiStdLogSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLinesLocked(true)
	return nil
}

func (s *tuiStdLogSink) flushLinesLocked(final bool) {
	for {
		data := s.buffer.Bytes()
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			break
		}
		line := strings.TrimRight(string(data[:newline]), "\r")
		s.buffer.Next(newline + 1)
		s.routeLine(line)
	}
	if final && s.buffer.Len() > 0 {
		line := strings.TrimSpace(s.buffer.String())
		s.buffer.Reset()
		s.routeLine(line)
	}
}

func (s *tuiStdLogSink) routeLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	message := stripStdLogPrefix(line)
	if isBenignContextShutdownLog(message) {
		if s.logger != nil {
			s.logger.Info("stdlib log suppressed", "message", message)
		}
		return
	}
	if s.logger != nil {
		s.logger.Warn("stdlib log", "message", message)
		return
	}
	if s.fallback != nil {
		_, _ = io.WriteString(s.fallback, line+"\n")
	}
}

func stripStdLogPrefix(line string) string {
	trimmed := strings.TrimSpace(line)
	fields := strings.Fields(trimmed)
	if len(fields) < 3 {
		return trimmed
	}
	if !looksLikeStdLogDate(fields[0]) || !looksLikeStdLogTime(fields[1]) {
		return trimmed
	}
	return strings.Join(fields[2:], " ")
}

func looksLikeStdLogDate(token string) bool {
	return len(token) == 10 &&
		isDigits(token[0:4]) &&
		token[4] == '/' &&
		isDigits(token[5:7]) &&
		token[7] == '/' &&
		isDigits(token[8:10])
}

func looksLikeStdLogTime(token string) bool {
	return len(token) == 8 &&
		isDigits(token[0:2]) &&
		token[2] == ':' &&
		isDigits(token[3:5]) &&
		token[5] == ':' &&
		isDigits(token[6:8])
}

func isDigits(token string) bool {
	for i := range token {
		if token[i] < '0' || token[i] > '9' {
			return false
		}
	}
	return true
}

func isBenignContextShutdownLog(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if !strings.HasPrefix(normalized, "error context") {
		return false
	}
	return strings.Contains(normalized, "deadline exceeded") ||
		strings.Contains(normalized, "canceled")
}
