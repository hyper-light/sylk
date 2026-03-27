package commandapproval

import (
	"context"
	"strings"
	"time"
)

type Decision string

const (
	DecisionAllow  Decision = "allow"
	DecisionDeny   Decision = "deny"
	DecisionPrompt Decision = "prompt"
)

type RuleAction string

const (
	RuleActionAllow RuleAction = "allow"
	RuleActionDeny  RuleAction = "deny"
)

type PathScope string

const (
	PathScopeNone              PathScope = "none"
	PathScopeWorkspaceRoot     PathScope = "workspace_root"
	PathScopeWorkspaceRelative PathScope = "workspace_relative"
	PathScopeWorkspaceAbsolute PathScope = "workspace_absolute"
	PathScopeOutsideWorkspace  PathScope = "outside_workspace"
)

type MatchSource string

const (
	MatchSourceBuiltinAllow MatchSource = "builtin_allow"
	MatchSourceBuiltinDeny  MatchSource = "builtin_deny"
	MatchSourceStoredAllow  MatchSource = "stored_allow"
	MatchSourceStoredDeny   MatchSource = "stored_deny"
	MatchSourceInteractive  MatchSource = "interactive"
)

type ApprovalPolicy string

const (
	ApprovalPolicyDefault ApprovalPolicy = "default"
	ApprovalPolicyExact   ApprovalPolicy = "exact"
)

func IsFetchToolName(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "web_fetch", "fetch_document", "crawl_links":
		return true
	default:
		return false
	}
}

type Request struct {
	Command        string         `json:"command"`
	WorkingDir     string         `json:"working_dir,omitempty"`
	WorkspaceRoot  string         `json:"workspace_root,omitempty"`
	ToolName       string         `json:"tool_name,omitempty"`
	Domain         string         `json:"domain,omitempty"`
	Justification  string         `json:"justification,omitempty"`
	AgentID        string         `json:"agent_id,omitempty"`
	AgentType      string         `json:"agent_type,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	DAGID          string         `json:"dag_id,omitempty"`
	NodeID         string         `json:"node_id,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	PipelineID     string         `json:"pipeline_id,omitempty"`
	ApprovalPolicy ApprovalPolicy `json:"approval_policy,omitempty"`
}

type PathArg struct {
	Value string    `json:"value"`
	Scope PathScope `json:"scope"`
	Zone  string    `json:"zone,omitempty"`
}

type Analysis struct {
	RawCommand       string         `json:"raw_command"`
	Normalized       string         `json:"normalized"`
	Tokens           []string       `json:"tokens,omitempty"`
	Program          string         `json:"program"`
	Verb             string         `json:"verb,omitempty"`
	WorkingDir       string         `json:"working_dir,omitempty"`
	WorkingDirScope  PathScope      `json:"working_dir_scope"`
	WorkingDirZone   string         `json:"working_dir_zone,omitempty"`
	PathArgs         []PathArg      `json:"path_args,omitempty"`
	Flags            []string       `json:"flags,omitempty"`
	TemplateKey      string         `json:"template_key"`
	ExactKey         string         `json:"exact_key"`
	PersistKey       string         `json:"persist_key"`
	PersistLabel     string         `json:"persist_label"`
	RuleLabel        string         `json:"rule_label"`
	Summary          string         `json:"summary"`
	Risk             string         `json:"risk"`
	OutsideWorkspace bool           `json:"outside_workspace"`
	Mutating         bool           `json:"mutating"`
	ApprovalPolicy   ApprovalPolicy `json:"approval_policy,omitempty"`
}

type Rule struct {
	MatchKey  string     `yaml:"match_key" json:"match_key"`
	Action    RuleAction `yaml:"action" json:"action"`
	RuleLabel string     `yaml:"rule_label,omitempty" json:"rule_label,omitempty"`
	Summary   string     `yaml:"summary,omitempty" json:"summary,omitempty"`
	CreatedAt time.Time  `yaml:"created_at,omitempty" json:"created_at,omitempty"`
}

type Evaluation struct {
	Decision     Decision    `json:"decision"`
	Source       MatchSource `json:"source"`
	Reason       string      `json:"reason,omitempty"`
	UserDecision string      `json:"user_decision,omitempty"`
	Analysis     Analysis    `json:"analysis"`
	Rule         *Rule       `json:"rule,omitempty"`
}

type Proposal struct {
	CorrelationID  string         `json:"correlation_id"`
	TargetAgentID  string         `json:"target_agent_id"`
	AgentID        string         `json:"agent_id,omitempty"`
	AgentType      string         `json:"agent_type,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	DAGID          string         `json:"dag_id,omitempty"`
	NodeID         string         `json:"node_id,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	PipelineID     string         `json:"pipeline_id,omitempty"`
	ToolName       string         `json:"tool_name,omitempty"`
	Command        string         `json:"command"`
	Domain         string         `json:"domain,omitempty"`
	Justification  string         `json:"justification,omitempty"`
	WorkingDir     string         `json:"working_dir,omitempty"`
	WorkspaceRoot  string         `json:"workspace_root,omitempty"`
	TemplateKey    string         `json:"template_key"`
	PersistKey     string         `json:"persist_key"`
	PersistLabel   string         `json:"persist_label"`
	RuleLabel      string         `json:"rule_label"`
	Summary        string         `json:"summary"`
	Risk           string         `json:"risk"`
	Timestamp      time.Time      `json:"timestamp"`
	ApprovalPolicy ApprovalPolicy `json:"approval_policy,omitempty"`
}

func (p *Proposal) IsFetchApproval() bool {
	if p == nil {
		return false
	}
	return IsFetchToolName(p.ToolName)
}

type Gate interface {
	Authorize(ctx context.Context, req Request) (Evaluation, error)
}
