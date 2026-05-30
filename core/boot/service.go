package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

const (
	BootToolPhase    = "boot_phase"
	BootToolSetup    = "boot_setup"
	BootToolDetect   = "boot_detect"
	BootToolAllocate = "boot_allocate"
	BootToolIngest   = "boot_ingest"
	BootToolCommit   = "boot_commit"
	BootToolFinalize = "boot_finalize"
)

var ErrBootSequencerServiceInvalid = errors.New("boot sequencer service invalid")

type BootSequencerServiceConfig struct {
	ProcessUID string
	MaxRecords int
	Clock      func() time.Time
}

type BootSequencerService struct {
	mu         sync.Mutex
	processUID string
	maxRecords int
	clock      func() time.Time
	records    map[string]claims.BootPhaseArtifactData
	order      []string
}

func NewBootSequencerService(cfg BootSequencerServiceConfig) *BootSequencerService {
	return &BootSequencerService{
		processUID: strings.TrimSpace(cfg.ProcessUID),
		maxRecords: bootSequencerMaxRecords(cfg.MaxRecords),
		clock:      firstBootServiceClock(cfg.Clock),
		records:    make(map[string]claims.BootPhaseArtifactData, bootSequencerMaxRecords(cfg.MaxRecords)),
	}
}

func (s *BootSequencerService) HandleServiceClaim(ctx context.Context, req claims.ServiceClaimRequest) (claims.ServiceClaimResult, error) {
	if s == nil {
		return claims.ServiceClaimResult{}, ErrBootSequencerServiceInvalid
	}
	call, err := bootSequencerClaimCall(req.Claim)
	if err != nil {
		return claims.ServiceClaimResult{}, err
	}
	data, err := s.bootPhaseDataFromCall(ctx, call, req)
	if err != nil {
		data = bootPhaseFailureData(call, s.processUID, err)
	}
	return s.RecordPhase(ctx, data, nil)
}

func (s *BootSequencerService) RecordPhase(ctx context.Context, data claims.BootPhaseArtifactData, extra []*claims.Artifact) (claims.ServiceClaimResult, error) {
	if s == nil {
		return claims.ServiceClaimResult{}, ErrBootSequencerServiceInvalid
	}
	data = s.normalizeBootPhaseData(ctx, data)
	data = s.recordBootPhaseData(data)
	artifact, err := claims.NewBootPhaseArtifact(data)
	if err != nil {
		return claims.ServiceClaimResult{}, err
	}
	artifacts := make([]*claims.Artifact, 0, 1+len(extra))
	artifacts = append(artifacts, artifact)
	artifacts = append(artifacts, extra...)
	return claims.ServiceClaimResult{
		Summary:   "boot " + data.Phase + " " + data.Status,
		Artifacts: artifacts,
	}, nil
}

func (s *BootSequencerService) bootPhaseDataFromCall(ctx context.Context, call claims.ExpectedToolCall, req claims.ServiceClaimRequest) (claims.BootPhaseArtifactData, error) {
	data, err := decodeBootPhaseArguments(call.Arguments)
	if err != nil {
		return claims.BootPhaseArtifactData{}, err
	}
	data.Phase = firstBootServiceString(data.Phase, bootPhaseStringArg(call.Arguments, "phase"), bootPhaseForTool(call.Tool))
	data.ProcessUID = firstBootServiceString(data.ProcessUID, s.processUID, bootProcessUIDFromRequest(req))
	return s.normalizeBootPhaseData(ctx, data), nil
}

func (s *BootSequencerService) normalizeBootPhaseData(ctx context.Context, data claims.BootPhaseArtifactData) claims.BootPhaseArtifactData {
	now := s.clock()
	data.Phase = strings.TrimSpace(data.Phase)
	data.ProcessUID = firstBootServiceString(data.ProcessUID, s.processUID)
	data.PhaseOrder = firstBootServiceInt(data.PhaseOrder, bootPhaseOrder(data.Phase))
	data.StartedAt = firstBootServiceTime(data.StartedAt, now)
	data.EndedAt = firstBootServiceTime(data.EndedAt, data.StartedAt)
	data.Duration = firstBootServiceDuration(data.Duration, data.EndedAt.Sub(data.StartedAt))
	data.ArtifactRefs = normalizeBootStringList(data.ArtifactRefs)
	data.PriorPhases = normalizeBootStringList(data.PriorPhases)
	data.Metadata = cloneBootMetadata(data.Metadata)
	if data.FailureReason == "" {
		data.Ready = true
	}
	data.Status = bootStatus(data)
	if err := ctx.Err(); err != nil {
		data.Ready = false
		data.Status = claims.InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func (s *BootSequencerService) recordBootPhaseData(data claims.BootPhaseArtifactData) claims.BootPhaseArtifactData {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(data.PriorPhases) == 0 {
		data.PriorPhases = s.priorPhasesLocked(data.ProcessUID)
	}
	key := bootPhaseRecordKey(data.ProcessUID, data.Phase)
	if _, exists := s.records[key]; !exists && len(s.order) >= s.maxRecords {
		data.Ready = false
		data.Status = claims.InfrastructureStatusFailed
		data.FailureReason = "boot sequencer record capacity reached"
		return data
	}
	if _, exists := s.records[key]; !exists {
		s.order = append(s.order, key)
	}
	s.records[key] = data
	return data
}

func (s *BootSequencerService) priorPhasesLocked(processUID string) []string {
	out := make([]string, 0, len(s.order))
	for _, key := range s.order {
		data := s.records[key]
		if strings.TrimSpace(data.ProcessUID) == strings.TrimSpace(processUID) {
			out = append(out, data.Phase)
		}
	}
	return out
}

func bootSequencerClaimCall(claim *claims.Claim) (claims.ExpectedToolCall, error) {
	if claim == nil {
		return claims.ExpectedToolCall{}, fmt.Errorf("%w: claim is required", ErrBootSequencerServiceInvalid)
	}
	for _, call := range claim.ExpectedToolCalls {
		if bootSequencerToolAllowed(call.Tool) {
			return call, nil
		}
	}
	return claims.ExpectedToolCall{}, fmt.Errorf("%w: expected boot phase tool call is required", ErrBootSequencerServiceInvalid)
}

func decodeBootPhaseArguments(args map[string]any) (claims.BootPhaseArtifactData, error) {
	var data claims.BootPhaseArtifactData
	raw, err := json.Marshal(args)
	if err != nil {
		return data, err
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return data, err
	}
	return data, nil
}

func bootPhaseFailureData(call claims.ExpectedToolCall, processUID string, cause error) claims.BootPhaseArtifactData {
	phase := firstBootServiceString(bootPhaseStringArg(call.Arguments, "phase"), bootPhaseForTool(call.Tool), call.Tool)
	return claims.BootPhaseArtifactData{
		Phase:         phase,
		PhaseOrder:    bootPhaseOrder(phase),
		ProcessUID:    strings.TrimSpace(processUID),
		Ready:         false,
		Status:        claims.InfrastructureStatusFailed,
		FailureReason: cause.Error(),
	}
}

func bootSequencerToolAllowed(tool string) bool {
	switch strings.TrimSpace(tool) {
	case BootToolPhase, BootToolSetup, BootToolDetect, BootToolAllocate, BootToolIngest, BootToolCommit, BootToolFinalize:
		return true
	case "setup", "detect", "allocate", "ingest", "commit", "finalize":
		return true
	default:
		return false
	}
}

func BootPhaseToolFor(phase string) string {
	switch strings.TrimSpace(phase) {
	case "setup":
		return BootToolSetup
	case "detect":
		return BootToolDetect
	case "allocate":
		return BootToolAllocate
	case "ingest":
		return BootToolIngest
	case "commit":
		return BootToolCommit
	case "finalize":
		return BootToolFinalize
	default:
		return BootToolPhase
	}
}

func bootPhaseForTool(tool string) string {
	switch strings.TrimSpace(tool) {
	case BootToolSetup, "setup":
		return "setup"
	case BootToolDetect, "detect":
		return "detect"
	case BootToolAllocate, "allocate":
		return "allocate"
	case BootToolIngest, "ingest":
		return "ingest"
	case BootToolCommit, "commit":
		return "commit"
	case BootToolFinalize, "finalize":
		return "finalize"
	default:
		return ""
	}
}

func bootPhaseOrder(phase string) int {
	for idx, candidate := range bootPhaseSequence() {
		if strings.TrimSpace(phase) == candidate {
			return idx + 1
		}
	}
	return 0
}

func bootPhaseSequence() []string {
	return []string{"setup", "detect", "allocate", "ingest", "commit", "finalize"}
}

func bootSequencerMaxRecords(configured int) int {
	if configured > 0 {
		return configured
	}
	return len(bootPhaseSequence())
}

func bootStatus(data claims.BootPhaseArtifactData) string {
	if data.FailureReason != "" {
		return claims.InfrastructureStatusFailed
	}
	return claims.InfrastructureStatusOK
}

func bootPhaseRecordKey(processUID, phase string) string {
	return strings.TrimSpace(processUID) + "\x00" + strings.TrimSpace(phase)
}

func bootProcessUIDFromRequest(req claims.ServiceClaimRequest) string {
	if req.Participant.ScopeKeys != nil {
		if uid := strings.TrimSpace(req.Participant.ScopeKeys["process_uid"]); uid != "" {
			return uid
		}
	}
	if req.Board != nil {
		return req.Board.SessionID()
	}
	return ""
}

func bootPhaseStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func normalizeBootStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func cloneBootMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[strings.TrimSpace(key)] = value
	}
	return out
}

func firstBootServiceClock(clock func() time.Time) func() time.Time {
	if clock != nil {
		return clock
	}
	return func() time.Time { return time.Now().UTC() }
}

func firstBootServiceString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstBootServiceInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstBootServiceTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

func firstBootServiceDuration(values ...time.Duration) time.Duration {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
