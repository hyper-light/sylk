package claims

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
)

const (
	ArtifactDataTypePlanMarkdown               = "sylk.plan_markdown.v1"
	ArtifactDataTypeExpectedToolInvocation     = "sylk.expected_tool_invocation.v1"
	ArtifactDataTypeExpectedToolOutput         = "sylk.expected_tool_output.v1"
	ArtifactDataTypeExpectedToolSkipped        = "sylk.expected_tool_skipped.v1"
	ArtifactDataTypeCarryForwardWorkingContext = "sylk.carry_forward.working_context.v1"
	ArtifactDataTypeCarryForwardEvidenceDigest = "sylk.carry_forward.evidence_digest.v1"
	ArtifactDataTypeCarryForwardSourceIndex    = "sylk.carry_forward.source_index.v1"
	ArtifactDataTypeCarryForwardContinuity     = "sylk.carry_forward.continuity_cursor.v1"
	ArtifactDataTypeCarryForwardSessionCursor  = "sylk.carry_forward.session_cursor.v1"
	ArtifactDataTypePresentationEvidence       = "sylk.presentation_evidence.v1"
)

var (
	ErrArtifactTypeDuplicate = errors.New("artifact data type duplicate")
	ErrArtifactTypeUnknown   = errors.New("artifact data type unknown")
	ErrArtifactCodecNil      = errors.New("artifact data codec is nil")
	ErrArtifactTypeInvalid   = errors.New("artifact data type invalid")
)

// ArtifactDataCodec serializes one registered artifact payload type.
type ArtifactDataCodec interface {
	Marshal(value any) ([]byte, error)
	Unmarshal(data []byte, target any) error
}

// RegisteredArtifactType is the immutable registry entry returned to callers.
type RegisteredArtifactType struct {
	DataType string
	GoType   reflect.Type
	Codec    ArtifactDataCodec
}

// ArtifactTypeError wraps registry and codec failures with operation context.
type ArtifactTypeError struct {
	DataType  string
	Operation string
	Err       error
}

func (e *ArtifactTypeError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("artifact type %q %s: %v", e.DataType, e.Operation, e.Err)
}

func (e *ArtifactTypeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// TypeRegistry maps stable artifact DataType strings to deterministic codecs.
type TypeRegistry struct {
	mu         sync.RWMutex
	byDataType map[string]RegisteredArtifactType
	byGoType   map[reflect.Type]string
}

func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{
		byDataType: make(map[string]RegisteredArtifactType),
		byGoType:   make(map[reflect.Type]string),
	}
}

func (r *TypeRegistry) Register(dataType string, sample any, codec ArtifactDataCodec) error {
	dataType = strings.TrimSpace(dataType)
	if dataType == "" {
		return artifactTypeError(dataType, "register", ErrArtifactTypeInvalid)
	}
	if codec == nil {
		return artifactTypeError(dataType, "register", ErrArtifactCodecNil)
	}
	goType, err := artifactGoTypeFromValue(sample)
	if err != nil {
		return artifactTypeError(dataType, "register", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byDataType[dataType]; exists {
		return artifactTypeError(dataType, "register", ErrArtifactTypeDuplicate)
	}
	if existing := strings.TrimSpace(r.byGoType[goType]); existing != "" {
		return artifactTypeError(dataType, "register", fmt.Errorf("%w: Go type %s already registered as %s", ErrArtifactTypeDuplicate, goType, existing))
	}
	entry := RegisteredArtifactType{DataType: dataType, GoType: goType, Codec: codec}
	r.byDataType[dataType] = entry
	r.byGoType[goType] = dataType
	return nil
}

func (r *TypeRegistry) LookupArtifactType(dataType string) (RegisteredArtifactType, error) {
	dataType = strings.TrimSpace(dataType)
	r.mu.RLock()
	entry, ok := r.byDataType[dataType]
	r.mu.RUnlock()
	if !ok {
		return RegisteredArtifactType{}, artifactTypeError(dataType, "lookup", ErrArtifactTypeUnknown)
	}
	return entry, nil
}

func (r *TypeRegistry) LookupArtifactTypeFor(goType reflect.Type) (RegisteredArtifactType, error) {
	goType = indirectArtifactGoType(goType)
	r.mu.RLock()
	dataType := r.byGoType[goType]
	entry, ok := r.byDataType[dataType]
	r.mu.RUnlock()
	if !ok {
		return RegisteredArtifactType{}, artifactTypeError("", "lookup_go_type", ErrArtifactTypeUnknown)
	}
	return entry, nil
}

func (r *TypeRegistry) ListArtifactTypes() []RegisteredArtifactType {
	r.mu.RLock()
	out := make([]RegisteredArtifactType, 0, len(r.byDataType))
	for _, entry := range r.byDataType {
		out = append(out, entry)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].DataType < out[j].DataType
	})
	return out
}

// JSONArtifactCodec is the built-in deterministic codec for JSON payloads.
type JSONArtifactCodec struct{}

func (JSONArtifactCodec) Marshal(value any) ([]byte, error) {
	if value == nil {
		return nil, fmt.Errorf("nil payload")
	}
	return json.Marshal(value)
}

func (JSONArtifactCodec) Unmarshal(data []byte, target any) error {
	if len(data) == 0 {
		return fmt.Errorf("empty payload")
	}
	if target == nil {
		return fmt.Errorf("nil target")
	}
	return json.Unmarshal(data, target)
}

type PlanMarkdownArtifactData struct {
	Markdown string `json:"markdown"`
	Title    string `json:"title,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

type ExpectedToolInvocationArtifactData struct {
	Call         ExpectedToolCall `json:"call"`
	ValidationID string           `json:"validation_id,omitempty"`
	AgentID      string           `json:"agent_id,omitempty"`
}

type ExpectedToolOutputArtifactData struct {
	CallID       string                      `json:"call_id,omitempty"`
	Tool         string                      `json:"tool,omitempty"`
	Status       ExpectedToolExecutionStatus `json:"status,omitempty"`
	Summary      string                      `json:"summary,omitempty"`
	Output       any                         `json:"output,omitempty"`
	Metadata     map[string]any              `json:"metadata,omitempty"`
	Artifacts    []*Artifact                 `json:"artifacts,omitempty"`
	ValidationID string                      `json:"validation_id,omitempty"`
}

type ExpectedToolSkippedArtifactData struct {
	CallID       string `json:"call_id,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Required     bool   `json:"required,omitempty"`
	ValidationID string `json:"validation_id,omitempty"`
}

type CarryForwardWorkingContextData struct {
	Topic          string         `json:"topic"`
	AgentID        string         `json:"agent_id,omitempty"`
	WorkingContext string         `json:"working_context"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type CarryForwardEvidenceDigestData struct {
	Topic    string         `json:"topic"`
	AgentID  string         `json:"agent_id,omitempty"`
	Digest   string         `json:"digest"`
	Findings any            `json:"findings,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type CarryForwardSourceIndexData struct {
	Topic    string         `json:"topic"`
	AgentID  string         `json:"agent_id,omitempty"`
	Sources  any            `json:"sources,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type CarryForwardContinuityCursorData struct {
	Topic       string         `json:"topic"`
	AgentID     string         `json:"agent_id,omitempty"`
	FromSeq     uint64         `json:"from_seq,omitempty"`
	ThroughSeq  uint64         `json:"through_seq,omitempty"`
	PreviousID  string         `json:"previous_id,omitempty"`
	CursorLabel string         `json:"cursor_label,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type CarryForwardSessionCursorData struct {
	SessionID  string         `json:"session_id,omitempty"`
	BoardID    string         `json:"board_id,omitempty"`
	ThroughSeq uint64         `json:"through_seq,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type PresentationEvidenceArtifactData struct {
	Kind      string         `json:"kind"`
	Reference string         `json:"reference,omitempty"`
	Title     string         `json:"title,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func RegisterBuiltinArtifactDataTypes(registry *TypeRegistry) error {
	if registry == nil {
		return fmt.Errorf("type registry is required")
	}
	codec := JSONArtifactCodec{}
	entries := []struct {
		dataType string
		sample   any
	}{
		{ArtifactDataTypePlanMarkdown, PlanMarkdownArtifactData{}},
		{ArtifactDataTypeExpectedToolInvocation, ExpectedToolInvocationArtifactData{}},
		{ArtifactDataTypeExpectedToolOutput, ExpectedToolOutputArtifactData{}},
		{ArtifactDataTypeExpectedToolSkipped, ExpectedToolSkippedArtifactData{}},
		{ArtifactDataTypeCarryForwardWorkingContext, CarryForwardWorkingContextData{}},
		{ArtifactDataTypeCarryForwardEvidenceDigest, CarryForwardEvidenceDigestData{}},
		{ArtifactDataTypeCarryForwardSourceIndex, CarryForwardSourceIndexData{}},
		{ArtifactDataTypeCarryForwardContinuity, CarryForwardContinuityCursorData{}},
		{ArtifactDataTypeCarryForwardSessionCursor, CarryForwardSessionCursorData{}},
		{ArtifactDataTypePresentationEvidence, PresentationEvidenceArtifactData{}},
	}
	for _, entry := range entries {
		if err := registry.Register(entry.dataType, entry.sample, codec); err != nil {
			return err
		}
	}
	return nil
}

func DefaultTypeRegistry() *TypeRegistry {
	defaultTypeRegistryOnce.Do(func() {
		defaultTypeRegistry = NewTypeRegistry()
		defaultTypeRegistryErr = RegisterBuiltinArtifactDataTypes(defaultTypeRegistry)
	})
	return defaultTypeRegistry
}

func DefaultTypeRegistryError() error {
	_ = DefaultTypeRegistry()
	return defaultTypeRegistryErr
}

var (
	defaultTypeRegistry     *TypeRegistry
	defaultTypeRegistryErr  error
	defaultTypeRegistryOnce sync.Once
)

func artifactGoTypeFromValue(value any) (reflect.Type, error) {
	if value == nil {
		return nil, ErrArtifactTypeInvalid
	}
	goType := indirectArtifactGoType(reflect.TypeOf(value))
	if goType == nil {
		return nil, ErrArtifactTypeInvalid
	}
	return goType, nil
}

func indirectArtifactGoType(goType reflect.Type) reflect.Type {
	for goType != nil && goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	return goType
}

func artifactTypeError(dataType, op string, err error) error {
	return &ArtifactTypeError{
		DataType:  strings.TrimSpace(dataType),
		Operation: strings.TrimSpace(op),
		Err:       err,
	}
}
