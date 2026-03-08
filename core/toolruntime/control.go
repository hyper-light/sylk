package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

type Invocation struct {
	ToolCall        providers.ToolCall
	AgentID         string
	CorrelationID   string
	CapabilityScope string
}

type GuardianControlRequest struct {
	AgentID           string         `json:"agent_id"`
	CorrelationID     string         `json:"correlation_id"`
	CapabilityScope   string         `json:"capability_scope"`
	ToolName          string         `json:"tool_name"`
	ToolID            string         `json:"tool_id,omitempty"`
	Arguments         string         `json:"arguments"`
	ArgumentsHash     string         `json:"arguments_hash,omitempty"`
	Input             map[string]any `json:"input,omitempty"`
	Policy            ToolPolicy     `json:"policy"`
	PolicyFingerprint string         `json:"policy_fingerprint,omitempty"`
	Timestamp         time.Time      `json:"timestamp"`
}

type GuardianControlGrant struct {
	GrantID           string    `json:"grant_id"`
	AgentID           string    `json:"agent_id"`
	CorrelationID     string    `json:"correlation_id"`
	CapabilityScope   string    `json:"capability_scope"`
	ToolName          string    `json:"tool_name"`
	ArgumentsHash     string    `json:"arguments_hash,omitempty"`
	PolicyFingerprint string    `json:"policy_fingerprint,omitempty"`
	Approved          bool      `json:"approved"`
	Reason            string    `json:"reason,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type GuardianControlPlane interface {
	RequestGrant(ctx context.Context, req GuardianControlRequest) (*GuardianControlGrant, error)
}

func (g *GuardianControlGrant) Validate(req GuardianControlRequest) error {
	if g == nil {
		return fmt.Errorf("guardian control grant is required")
	}
	if !g.Approved {
		if reason := strings.TrimSpace(g.Reason); reason != "" {
			return fmt.Errorf("guardian denied tool execution: %s", reason)
		}
		return fmt.Errorf("guardian denied tool execution")
	}
	if g.AgentID != req.AgentID {
		return fmt.Errorf("guardian grant agent mismatch: %q != %q", g.AgentID, req.AgentID)
	}
	if g.CorrelationID != req.CorrelationID {
		return fmt.Errorf("guardian grant correlation mismatch: %q != %q", g.CorrelationID, req.CorrelationID)
	}
	if g.CapabilityScope != req.CapabilityScope {
		return fmt.Errorf("guardian grant capability scope mismatch: %q != %q", g.CapabilityScope, req.CapabilityScope)
	}
	if g.ToolName != req.ToolName {
		return fmt.Errorf("guardian grant tool mismatch: %q != %q", g.ToolName, req.ToolName)
	}
	if req.ArgumentsHash != "" && g.ArgumentsHash != req.ArgumentsHash {
		return fmt.Errorf("guardian grant arguments hash mismatch")
	}
	if g.PolicyFingerprint != "" && req.PolicyFingerprint != "" && g.PolicyFingerprint != req.PolicyFingerprint {
		return fmt.Errorf("guardian grant fingerprint mismatch")
	}
	if !g.ExpiresAt.IsZero() && time.Now().After(g.ExpiresAt) {
		return fmt.Errorf("guardian grant expired")
	}
	return nil
}

func HashArguments(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}
