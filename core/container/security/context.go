package security

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrActionDenied    = errors.New("action denied by security context")
	ErrSecretNotFound  = errors.New("secret not found")
	ErrEscalationDenied = errors.New("capability escalation not permitted")
)

// ActionType classifies a security-relevant action for authorization.
type ActionType int

const (
	ActionFileRead ActionType = iota
	ActionFileWrite
	ActionFileDelete
	ActionPublish
	ActionSubscribe
	ActionSignalSend
	ActionSignalReceive
	ActionSecretAccess
	ActionEscalate
	ActionNetworkEgress  // Outbound HTTP/network requests
	ActionNetworkIngress // Inbound data from external sources
)

// actionTypeNames maps action types to their string representation.
var actionTypeNames = map[ActionType]string{
	ActionFileRead:      "file_read",
	ActionFileWrite:     "file_write",
	ActionFileDelete:    "file_delete",
	ActionPublish:       "publish",
	ActionSubscribe:     "subscribe",
	ActionSignalSend:    "signal_send",
	ActionSignalReceive: "signal_receive",
	ActionSecretAccess:  "secret_access",
	ActionEscalate:      "escalate",
	ActionNetworkEgress:  "network_egress",
	ActionNetworkIngress: "network_ingress",
}

// String returns the string representation of an action type.
func (a ActionType) String() string {
	if name, ok := actionTypeNames[a]; ok {
		return name
	}
	return "unknown"
}

// AuthorizationRequest describes an action requiring authorization.
type AuthorizationRequest struct {
	Action ActionType
	Target string // Path, topic, signal name, secret ref, etc.
}

// AuthorizationResult is the outcome of an authorization check.
type AuthorizationResult struct {
	Allowed   bool
	Reason    string
	Timestamp time.Time
}

// AuditSink receives security audit events. Optional — nil-safe.
type AuditSink interface {
	LogContainerAction(containerID, agentID string, action ActionType, target, outcome string)
}

// SecurityContext enforces runtime security for a container.
// It combines a CapabilitySet (what the container can do) with
// an AuditSink (observability) and optional secrets storage.
type SecurityContext struct {
	mu          sync.RWMutex
	containerID string
	agentID     string
	role        string // e.g. "worker", "supervisor" — maps to security.AgentRole
	capabilities *CapabilitySet
	readOnly    bool
	auditSink   AuditSink
	secrets     map[string][]byte // bounded by SecretRefs in spec
}

// SecurityContextConfig provides construction parameters.
type SecurityContextConfig struct {
	ContainerID  string
	AgentID      string
	Role         string
	Capabilities *CapabilitySet
	ReadOnly     bool
	AuditSink    AuditSink
	Secrets      map[string][]byte
}

// NewSecurityContext creates a new security context from config.
func NewSecurityContext(cfg SecurityContextConfig) *SecurityContext {
	caps := cfg.Capabilities
	if caps == nil {
		caps = EmptyCapabilitySet()
	}
	secrets := cfg.Secrets
	if secrets == nil {
		secrets = make(map[string][]byte)
	}
	return &SecurityContext{
		containerID:  cfg.ContainerID,
		agentID:      cfg.AgentID,
		role:         cfg.Role,
		capabilities: caps,
		readOnly:     cfg.ReadOnly,
		auditSink:    cfg.AuditSink,
		secrets:      secrets,
	}
}

// Authorize checks whether the given action is permitted.
func (sc *SecurityContext) Authorize(_ context.Context, req AuthorizationRequest) AuthorizationResult {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	result := sc.evaluateAction(req)
	sc.audit(req, result)
	return result
}

func (sc *SecurityContext) evaluateAction(req AuthorizationRequest) AuthorizationResult {
	allowed, reason := sc.checkCapability(req)
	return AuthorizationResult{
		Allowed:   allowed,
		Reason:    reason,
		Timestamp: time.Now(),
	}
}

func (sc *SecurityContext) checkCapability(req AuthorizationRequest) (bool, string) {
	switch req.Action {
	case ActionFileRead:
		return sc.capabilities.CanReadPath(req.Target), "path_read_check"
	case ActionFileWrite:
		return sc.checkWriteCapability(req.Target)
	case ActionFileDelete:
		return sc.checkDeleteCapability(req.Target)
	case ActionPublish:
		return sc.capabilities.CanPublish(req.Target), "publish_check"
	case ActionSubscribe:
		return sc.capabilities.CanSubscribe(req.Target), "subscribe_check"
	case ActionSignalSend:
		return sc.capabilities.CanSignal(req.Target), "signal_send_check"
	case ActionSignalReceive:
		return sc.capabilities.CanSignal(req.Target), "signal_receive_check"
	case ActionSecretAccess:
		return sc.checkSecretAccess(req.Target)
	case ActionEscalate:
		return sc.capabilities.CanEscalate(), "escalation_check"
	case ActionNetworkEgress:
		return sc.capabilities.CanNetworkEgress(req.Target), "network_egress_check"
	case ActionNetworkIngress:
		return sc.capabilities.CanNetworkIngress(req.Target), "network_ingress_check"
	default:
		return false, "unknown_action"
	}
}

func (sc *SecurityContext) checkWriteCapability(target string) (bool, string) {
	if sc.readOnly {
		return false, "read_only_filesystem"
	}
	return sc.capabilities.CanWritePath(target), "path_write_check"
}

func (sc *SecurityContext) checkDeleteCapability(target string) (bool, string) {
	if sc.readOnly {
		return false, "read_only_filesystem"
	}
	return sc.capabilities.CanWritePath(target), "path_delete_check"
}

func (sc *SecurityContext) checkSecretAccess(ref string) (bool, string) {
	_, exists := sc.secrets[ref]
	return exists, "secret_access_check"
}

func (sc *SecurityContext) audit(req AuthorizationRequest, result AuthorizationResult) {
	if sc.auditSink == nil {
		return
	}
	outcome := "denied"
	if result.Allowed {
		outcome = "allowed"
	}
	sc.auditSink.LogContainerAction(sc.containerID, sc.agentID, req.Action, req.Target, outcome)
}

// GetSecret retrieves a secret by reference name.
func (sc *SecurityContext) GetSecret(ref string) ([]byte, error) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	val, exists := sc.secrets[ref]
	if !exists {
		return nil, ErrSecretNotFound
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	return cp, nil
}

// Capabilities returns the capability set for inspection.
func (sc *SecurityContext) Capabilities() *CapabilitySet {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.capabilities
}

// Role returns the security role string.
func (sc *SecurityContext) Role() string {
	return sc.role
}

// ContainerID returns the container ID this context belongs to.
func (sc *SecurityContext) ContainerID() string {
	return sc.containerID
}

// IsReadOnly reports whether the container filesystem is read-only.
func (sc *SecurityContext) IsReadOnly() bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.readOnly
}
