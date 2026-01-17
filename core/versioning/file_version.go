package versioning

import (
	"time"
)

type FileVersion struct {
	ID             VersionID
	FilePath       string
	Parents        []VersionID
	Operations     []OperationID
	ContentHash    ContentHash
	ContentSize    int64
	Clock          VectorClock
	Timestamp      time.Time
	PipelineID     string
	SessionID      SessionID
	IsMerge        bool
	VariantGroupID *string
	VariantLabel   string
}

func NewFileVersion(
	filePath string,
	content []byte,
	parents []VersionID,
	operations []OperationID,
	pipelineID string,
	sessionID SessionID,
	clock VectorClock,
) FileVersion {
	contentHash := ComputeContentHash(content)
	metadata := buildMetadata(filePath, parents, pipelineID, sessionID)

	return FileVersion{
		ID:          ComputeVersionID(content, metadata),
		FilePath:    filePath,
		Parents:     cloneVersionIDs(parents),
		Operations:  cloneOperationIDs(operations),
		ContentHash: contentHash,
		ContentSize: int64(len(content)),
		Clock:       clock.Clone(),
		Timestamp:   time.Now(),
		PipelineID:  pipelineID,
		SessionID:   sessionID,
		IsMerge:     len(parents) > 1,
	}
}

func buildMetadata(filePath string, parents []VersionID, pipelineID string, sessionID SessionID) []byte {
	size := len(filePath) + len(pipelineID) + len(sessionID) + len(parents)*HashSize
	metadata := make([]byte, 0, size)
	metadata = append(metadata, []byte(filePath)...)
	for _, p := range parents {
		metadata = append(metadata, p[:]...)
	}
	metadata = append(metadata, []byte(pipelineID)...)
	metadata = append(metadata, []byte(sessionID)...)
	return metadata
}

func cloneVersionIDs(ids []VersionID) []VersionID {
	if ids == nil {
		return nil
	}
	result := make([]VersionID, len(ids))
	copy(result, ids)
	return result
}

func cloneOperationIDs(ids []OperationID) []OperationID {
	if ids == nil {
		return nil
	}
	result := make([]OperationID, len(ids))
	copy(result, ids)
	return result
}

func (fv *FileVersion) Clone() FileVersion {
	clone := *fv
	clone.Parents = cloneVersionIDs(fv.Parents)
	clone.Operations = cloneOperationIDs(fv.Operations)
	clone.Clock = fv.Clock.Clone()
	if fv.VariantGroupID != nil {
		vgid := *fv.VariantGroupID
		clone.VariantGroupID = &vgid
	}
	return clone
}

func (fv *FileVersion) HasParent(id VersionID) bool {
	for _, p := range fv.Parents {
		if p == id {
			return true
		}
	}
	return false
}

func (fv *FileVersion) IsRoot() bool {
	return len(fv.Parents) == 0
}

func (fv *FileVersion) IsVariant() bool {
	return fv.VariantGroupID != nil
}

func (fv *FileVersion) SetVariant(groupID, label string) {
	fv.VariantGroupID = &groupID
	fv.VariantLabel = label
}

func (fv *FileVersion) AddOperation(opID OperationID) {
	fv.Operations = append(fv.Operations, opID)
}

func (fv *FileVersion) Equal(other *FileVersion) bool {
	if fv == nil || other == nil {
		return fv == other
	}
	return fv.ID == other.ID
}

func (fv *FileVersion) HappensBefore(other *FileVersion) bool {
	if fv == nil || other == nil {
		return false
	}
	return fv.Clock.HappensBefore(other.Clock)
}

func (fv *FileVersion) Concurrent(other *FileVersion) bool {
	if fv == nil || other == nil {
		return false
	}
	return fv.Clock.Concurrent(other.Clock)
}
