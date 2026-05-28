package claims

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var (
	ErrArtifactDataNil          = errors.New("artifact is nil")
	ErrArtifactDataMismatch     = errors.New("artifact data type mismatch")
	ErrArtifactDataEmpty        = errors.New("artifact data is empty")
	ErrArtifactDataHashMismatch = errors.New("artifact data hash mismatch")
	ErrArtifactDataSizeMismatch = errors.New("artifact data size mismatch")
	ErrArtifactCodecPanic       = errors.New("artifact codec panic")
)

// ArtifactDataError wraps typed artifact data helper failures.
type ArtifactDataError struct {
	ArtifactID string
	DataType   string
	Operation  string
	Err        error
}

func (e *ArtifactDataError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("artifact %q data type %q %s: %v", e.ArtifactID, e.DataType, e.Operation, e.Err)
}

func (e *ArtifactDataError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func SetArtifactData[T any](artifact *Artifact, value T) error {
	return SetArtifactDataWithRegistry(DefaultTypeRegistry(), artifact, value)
}

func SetArtifactDataWithRegistry[T any](registry *TypeRegistry, artifact *Artifact, value T) error {
	if artifact == nil {
		return artifactDataError("", "", "set", ErrArtifactDataNil)
	}
	entry, err := artifactTypeForValue(registry, value)
	if err != nil {
		return artifactDataError(artifact.ID, "", "set", err)
	}
	data, err := safeCodecMarshal(entry.Codec, value)
	if err != nil {
		return artifactDataError(artifact.ID, entry.DataType, "marshal", err)
	}
	if len(data) == 0 {
		return artifactDataError(artifact.ID, entry.DataType, "marshal", ErrArtifactDataEmpty)
	}
	artifact.DataType = entry.DataType
	artifact.Data = append([]byte(nil), data...)
	artifact.ContentHash = ArtifactContentHash(data)
	artifact.Size = int64(len(data))
	return nil
}

func ArtifactData[T any](artifact *Artifact) (T, error) {
	return ArtifactDataWithRegistry[T](DefaultTypeRegistry(), artifact)
}

func ArtifactDataWithRegistry[T any](registry *TypeRegistry, artifact *Artifact) (T, error) {
	var zero T
	if artifact == nil {
		return zero, artifactDataError("", "", "get", ErrArtifactDataNil)
	}
	entry, err := artifactTypeForGeneric[T](registry)
	if err != nil {
		return zero, artifactDataError(artifact.ID, artifact.DataType, "get", err)
	}
	if strings.TrimSpace(artifact.DataType) != entry.DataType {
		return zero, artifactDataError(artifact.ID, artifact.DataType, "get", ErrArtifactDataMismatch)
	}
	if len(artifact.Data) == 0 {
		return zero, artifactDataError(artifact.ID, artifact.DataType, "get", ErrArtifactDataEmpty)
	}
	if err := validateArtifactContentHash(artifact); err != nil {
		return zero, artifactDataError(artifact.ID, artifact.DataType, "get", err)
	}
	if err := safeCodecUnmarshal(entry.Codec, append([]byte(nil), artifact.Data...), &zero); err != nil {
		return zero, artifactDataError(artifact.ID, artifact.DataType, "unmarshal", err)
	}
	return zero, nil
}

func ArtifactContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ApplyBuiltinArtifactDataBridge encodes legacy reference/metadata
// artifacts into typed payloads for tests and migration tooling. It is
// intentionally opt-in and does not alter production board ingestion.
func ApplyBuiltinArtifactDataBridge(artifact *Artifact) error {
	if artifact == nil {
		return artifactDataError("", "", "bridge", ErrArtifactDataNil)
	}
	if strings.TrimSpace(artifact.DataType) != "" || len(artifact.Data) != 0 {
		return nil
	}
	switch strings.TrimSpace(artifact.Kind) {
	case ArtifactKindPlanMarkdown:
		return SetArtifactData(artifact, PlanMarkdownArtifactData{
			Markdown: artifact.Reference,
			Title:    presentationTitle(artifact.Presentation),
		})
	case ArtifactKindExpectedToolInvocation:
		return SetArtifactData(artifact, ExpectedToolInvocationArtifactData{
			ValidationID: metadataString(artifact.Metadata, ExpectedToolMetadataValidationID),
			AgentID:      artifact.AgentID,
		})
	case ArtifactKindExpectedToolOutput:
		return SetArtifactData(artifact, ExpectedToolOutputArtifactData{
			Summary:      artifact.Reference,
			Output:       artifact.Metadata[ExpectedToolMetadataOutput],
			Metadata:     cloneAnyMap(artifact.Metadata),
			ValidationID: metadataString(artifact.Metadata, ExpectedToolMetadataValidationID),
		})
	case ArtifactKindExpectedToolSkipped:
		return SetArtifactData(artifact, ExpectedToolSkippedArtifactData{
			Reason:       artifact.Reference,
			Required:     metadataBool(artifact.Metadata, ExpectedToolMetadataRequired),
			ValidationID: metadataString(artifact.Metadata, ExpectedToolMetadataValidationID),
		})
	case ArtifactKindReadiness:
		return SetArtifactData(artifact, KnowledgeReadinessArtifactData{
			Component:  metadataString(artifact.Metadata, "component"),
			QualityBar: metadataString(artifact.Metadata, "quality_bar"),
			Reference:  artifact.Reference,
			Metadata:   cloneAnyMap(artifact.Metadata),
		})
	default:
		return nil
	}
}

func artifactTypeForValue[T any](registry *TypeRegistry, value T) (RegisteredArtifactType, error) {
	if registry == nil {
		return RegisteredArtifactType{}, ErrArtifactTypeUnknown
	}
	if artifactValueIsNil(value) {
		return RegisteredArtifactType{}, ErrArtifactTypeInvalid
	}
	return registry.LookupArtifactTypeFor(indirectArtifactGoType(reflect.TypeOf(value)))
}

func artifactTypeForGeneric[T any](registry *TypeRegistry) (RegisteredArtifactType, error) {
	if registry == nil {
		return RegisteredArtifactType{}, ErrArtifactTypeUnknown
	}
	return registry.LookupArtifactTypeFor(indirectArtifactGoType(reflect.TypeFor[T]()))
}

func validateArtifactContentHash(artifact *Artifact) error {
	hash := strings.TrimSpace(artifact.ContentHash)
	if hash == "" {
		return ErrArtifactDataHashMismatch
	}
	if hash != ArtifactContentHash(artifact.Data) {
		return ErrArtifactDataHashMismatch
	}
	if artifact.Size != 0 && artifact.Size != int64(len(artifact.Data)) {
		return ErrArtifactDataSizeMismatch
	}
	return nil
}

func safeCodecMarshal(codec ArtifactDataCodec, value any) (data []byte, err error) {
	defer recoverCodecPanic(&err)
	return codec.Marshal(value)
}

func safeCodecUnmarshal(codec ArtifactDataCodec, data []byte, target any) (err error) {
	defer recoverCodecPanic(&err)
	return codec.Unmarshal(data, target)
}

func recoverCodecPanic(err *error) {
	if recovered := recover(); recovered != nil {
		*err = fmt.Errorf("%w: %v", ErrArtifactCodecPanic, recovered)
	}
}

func artifactDataError(artifactID, dataType, op string, err error) error {
	return &ArtifactDataError{
		ArtifactID: strings.TrimSpace(artifactID),
		DataType:   strings.TrimSpace(dataType),
		Operation:  strings.TrimSpace(op),
		Err:        err,
	}
}

func presentationTitle(p *Presentation) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Title)
}
