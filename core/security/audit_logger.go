package security

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrLoggerClosed    = errors.New("audit logger closed")
	ErrIntegrityFailed = errors.New("audit log integrity check failed")
)

type AuditLogConfig struct {
	LogPath           string `yaml:"log_path"`
	SignatureInterval int    `yaml:"signature_interval"`
	RotateSize        int64  `yaml:"rotate_size"`
	RetentionPolicy   string `yaml:"retention_policy"`
}

func DefaultAuditLogConfig() AuditLogConfig {
	return AuditLogConfig{
		SignatureInterval: 100,
		RotateSize:        100 * 1024 * 1024,
		RetentionPolicy:   "indefinite",
	}
}

type AuditLogger struct {
	mu sync.Mutex

	logFile      *os.File
	logPath      string
	sequence     uint64
	previousHash string

	signingKey       ed25519.PrivateKey
	publicKey        ed25519.PublicKey
	entriesSinceSign int

	config AuditLogConfig
	closed bool
}

func NewAuditLogger(cfg AuditLogConfig) (*AuditLogger, error) {
	cfg = normalizeAuditConfig(cfg)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}

	al := &AuditLogger{
		config:     cfg,
		signingKey: priv,
		publicKey:  pub,
		logPath:    cfg.LogPath,
	}

	if err := al.openLogFile(); err != nil {
		return nil, err
	}

	return al, nil
}

func normalizeAuditConfig(cfg AuditLogConfig) AuditLogConfig {
	if cfg.SignatureInterval == 0 {
		cfg.SignatureInterval = 100
	}
	if cfg.RotateSize == 0 {
		cfg.RotateSize = 100 * 1024 * 1024
	}
	return cfg
}

func (al *AuditLogger) openLogFile() error {
	if al.logPath == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(al.logPath), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(al.logPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	al.logFile = f

	return al.initializeFromExisting()
}

func (al *AuditLogger) initializeFromExisting() error {
	scanner := bufio.NewScanner(al.logFile)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "SIG:") {
			continue
		}
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		al.sequence = entry.Sequence
		al.previousHash = entry.EntryHash
	}
	return nil
}

func (al *AuditLogger) Log(entry AuditEntry) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	if al.closed {
		return ErrLoggerClosed
	}

	al.prepareEntry(&entry)

	if al.logFile == nil {
		return nil
	}

	return al.writeEntry(entry)
}

func (al *AuditLogger) prepareEntry(entry *AuditEntry) {
	al.sequence++
	entry.Sequence = al.sequence
	entry.PreviousHash = al.previousHash
	entry.ID = uuid.New().String()
	entry.Timestamp = time.Now().UTC()
	entry.EntryHash = al.computeEntryHash(*entry)
	al.previousHash = entry.EntryHash
}

func (al *AuditLogger) writeEntry(entry AuditEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	if _, err := al.logFile.Write(append(data, '\n')); err != nil {
		return err
	}

	return al.maybeSignAndRotate()
}

func (al *AuditLogger) maybeSignAndRotate() error {
	al.entriesSinceSign++
	if al.entriesSinceSign >= al.config.SignatureInterval {
		if err := al.writeSignature(); err != nil {
			return err
		}
		al.entriesSinceSign = 0
	}
	return al.checkRotation()
}

func (al *AuditLogger) computeEntryHash(entry AuditEntry) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d", entry.Sequence)))
	h.Write([]byte(entry.PreviousHash))
	h.Write([]byte(entry.Timestamp.Format(time.RFC3339Nano)))
	h.Write([]byte(entry.Category))
	h.Write([]byte(entry.EventType))
	h.Write([]byte(entry.SessionID))
	h.Write([]byte(entry.Action))
	h.Write([]byte(entry.Target))
	h.Write([]byte(entry.Outcome))

	if entry.Details != nil {
		detailsJSON, _ := json.Marshal(entry.Details)
		h.Write(detailsJSON)
	}

	return hex.EncodeToString(h.Sum(nil))
}

func (al *AuditLogger) writeSignature() error {
	if al.logFile == nil {
		return nil
	}

	sigEntry := AuditSignature{
		Timestamp:    time.Now().UTC(),
		SequenceFrom: al.sequence - uint64(al.entriesSinceSign) + 1,
		SequenceTo:   al.sequence,
		ChainHash:    al.previousHash,
	}
	sigEntry.Signature = ed25519.Sign(al.signingKey, []byte(al.previousHash))

	data, _ := json.Marshal(sigEntry)
	_, err := al.logFile.Write(append([]byte("SIG:"), append(data, '\n')...))
	return err
}

func (al *AuditLogger) checkRotation() error {
	if al.logFile == nil {
		return nil
	}

	info, err := al.logFile.Stat()
	if err != nil {
		return nil
	}

	if info.Size() < al.config.RotateSize {
		return nil
	}

	return al.rotateLog()
}

func (al *AuditLogger) rotateLog() error {
	if err := al.writeSignature(); err != nil {
		return err
	}

	oldPath := al.logPath
	newPath := fmt.Sprintf("%s.%d", oldPath, time.Now().Unix())

	if err := al.logFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	return al.openLogFile()
}

func (al *AuditLogger) VerifyIntegrity() (*IntegrityReport, error) {
	al.mu.Lock()
	defer al.mu.Unlock()

	if al.logFile == nil {
		return &IntegrityReport{Valid: true}, nil
	}

	return al.scanAndVerify()
}

func (al *AuditLogger) scanAndVerify() (*IntegrityReport, error) {
	report := &IntegrityReport{StartTime: time.Now()}

	if _, err := al.logFile.Seek(0, 0); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(al.logFile)
	var prevHash string
	var lastSeq uint64

	for scanner.Scan() {
		al.verifyLine(scanner.Text(), &prevHash, &lastSeq, report)
	}

	report.EndTime = time.Now()
	report.Valid = len(report.Errors) == 0
	return report, nil
}

func (al *AuditLogger) verifyLine(line string, prevHash *string, lastSeq *uint64, report *IntegrityReport) {
	if strings.HasPrefix(line, "SIG:") {
		al.verifySignatureLine(line[4:], *prevHash, report)
		return
	}
	al.verifyEntryLine(line, prevHash, lastSeq, report)
}

func (al *AuditLogger) verifySignatureLine(sigJSON, prevHash string, report *IntegrityReport) {
	var sig AuditSignature
	if err := json.Unmarshal([]byte(sigJSON), &sig); err != nil {
		report.Errors = append(report.Errors, "invalid signature format")
		return
	}

	if !ed25519.Verify(al.publicKey, []byte(sig.ChainHash), sig.Signature) {
		report.Errors = append(report.Errors, "signature verification failed")
		return
	}

	report.SignaturesVerified++
}

func (al *AuditLogger) verifyEntryLine(line string, prevHash *string, lastSeq *uint64, report *IntegrityReport) {
	var entry AuditEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("parse error at seq %d", *lastSeq+1))
		return
	}

	al.checkEntryIntegrity(entry, *prevHash, *lastSeq, report)

	*prevHash = entry.EntryHash
	*lastSeq = entry.Sequence
	report.EntriesVerified++
}

func (al *AuditLogger) checkEntryIntegrity(entry AuditEntry, prevHash string, lastSeq uint64, report *IntegrityReport) {
	al.checkChainContinuity(entry, prevHash, report)
	al.checkHashMatch(entry, report)
	al.checkSequenceContinuity(entry, lastSeq, report)
}

func (al *AuditLogger) checkChainContinuity(entry AuditEntry, prevHash string, report *IntegrityReport) {
	if entry.PreviousHash != prevHash {
		report.Errors = append(report.Errors, fmt.Sprintf("chain break at seq %d", entry.Sequence))
	}
}

func (al *AuditLogger) checkHashMatch(entry AuditEntry, report *IntegrityReport) {
	computedHash := al.computeEntryHash(entry)
	if entry.EntryHash != computedHash {
		report.Errors = append(report.Errors, fmt.Sprintf("hash mismatch at seq %d", entry.Sequence))
	}
}

func (al *AuditLogger) checkSequenceContinuity(entry AuditEntry, lastSeq uint64, report *IntegrityReport) {
	if lastSeq > 0 && entry.Sequence != lastSeq+1 {
		report.Errors = append(report.Errors, fmt.Sprintf("sequence gap: %d to %d", lastSeq, entry.Sequence))
	}
}

func (al *AuditLogger) Close() error {
	al.mu.Lock()
	defer al.mu.Unlock()

	if al.closed {
		return nil
	}
	al.closed = true

	al.finalizeLog()

	if al.logFile != nil {
		return al.logFile.Close()
	}
	return nil
}

func (al *AuditLogger) finalizeLog() {
	if al.entriesSinceSign > 0 && al.logFile != nil {
		_ = al.writeSignature()
	}
}

func (al *AuditLogger) LogPermissionGranted(agentID string, action PermissionAction, source string) {
	entry := NewAuditEntry(AuditCategoryPermission, "permission_granted", "grant")
	entry.AgentID = agentID
	entry.Target = action.Target
	entry.Outcome = "allowed"
	entry.Details = map[string]interface{}{
		"action_type": string(action.Type),
		"source":      source,
	}
	_ = al.Log(entry)
}

func (al *AuditLogger) LogPermissionDenied(agentID string, action PermissionAction, reason string) {
	entry := NewAuditEntry(AuditCategoryPermission, "permission_denied", "deny")
	entry.Severity = AuditSeveritySecurity
	entry.AgentID = agentID
	entry.Target = action.Target
	entry.Outcome = "denied"
	entry.Details = map[string]interface{}{
		"action_type": string(action.Type),
		"reason":      reason,
	}
	_ = al.Log(entry)
}

func (al *AuditLogger) LogFileOperation(op, path, agentID string, size int64, hash string) {
	entry := NewAuditEntry(AuditCategoryFile, "file_"+op, op)
	entry.AgentID = agentID
	entry.Target = path
	entry.Outcome = "success"
	entry.Details = map[string]interface{}{
		"size": size,
		"hash": hash,
	}
	_ = al.Log(entry)
}

func (al *AuditLogger) LogProcessExecution(cmd, agentID string, exitCode int) {
	entry := NewAuditEntry(AuditCategoryProcess, "process_execution", "execute")
	entry.AgentID = agentID
	entry.Target = cmd
	entry.Outcome = fmt.Sprintf("exit_%d", exitCode)
	entry.Details = map[string]interface{}{
		"exit_code": exitCode,
	}
	_ = al.Log(entry)
}

func (al *AuditLogger) LogNetworkAllowed(domain, url string) {
	entry := NewAuditEntry(AuditCategoryNetwork, "network_allowed", "allow")
	entry.Target = domain
	entry.Outcome = "allowed"
	entry.Details = map[string]interface{}{
		"url": url,
	}
	_ = al.Log(entry)
}

func (al *AuditLogger) LogNetworkBlocked(domain, url string) {
	entry := NewAuditEntry(AuditCategoryNetwork, "network_blocked", "block")
	entry.Severity = AuditSeveritySecurity
	entry.Target = domain
	entry.Outcome = "blocked"
	entry.Details = map[string]interface{}{
		"url": url,
	}
	_ = al.Log(entry)
}

func (al *AuditLogger) LogLLMCall(provider, model string, tokens int, cost float64) {
	entry := NewAuditEntry(AuditCategoryLLM, "llm_call", "call")
	entry.Target = provider
	entry.Outcome = "success"
	entry.Details = map[string]interface{}{
		"model":  model,
		"tokens": tokens,
		"cost":   cost,
	}
	_ = al.Log(entry)
}

func (al *AuditLogger) LogSessionEvent(event, sessionID string) {
	entry := NewAuditEntry(AuditCategorySession, "session_"+event, event)
	entry.SessionID = sessionID
	entry.Outcome = "success"
	_ = al.Log(entry)
}

func (al *AuditLogger) Sequence() uint64 {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.sequence
}

func (al *AuditLogger) PreviousHash() string {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.previousHash
}
