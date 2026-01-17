package concurrency

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalidCheckpointVersion = errors.New("invalid checkpoint version")
	ErrCorruptedCheckpoint      = errors.New("corrupted checkpoint")
	ErrCheckpointNotFound       = errors.New("checkpoint not found")
	ErrCheckpointerClosed       = errors.New("checkpointer is closed")
)

type CheckpointVersion int

const (
	CheckpointV1 CheckpointVersion = 1
)

type PipelineState string

const (
	PipelineActive  PipelineState = "active"
	PipelineWaiting PipelineState = "waiting"
	PipelineReady   PipelineState = "ready"
)

type Checkpoint struct {
	Version     CheckpointVersion           `json:"version"`
	ID          string                      `json:"id"`
	CreatedAt   time.Time                   `json:"created_at"`
	WALSequence uint64                      `json:"wal_sequence"`
	ContentHash string                      `json:"content_hash"`
	Pipelines   map[string]PipelineSnapshot `json:"pipelines"`
	LLMQueues   LLMQueueSnapshot            `json:"llm_queues"`
	StagingDir  StagingSnapshot             `json:"staging_dir"`
	Agents      map[string]AgentSnapshot    `json:"agents"`
}

type PipelineSnapshot struct {
	ID    string         `json:"id"`
	State PipelineState  `json:"state"`
	Phase string         `json:"phase"`
	Extra map[string]any `json:"extra,omitempty"`
}

type LLMQueueSnapshot struct {
	UserQueueSize     int `json:"user_queue_size"`
	PipelineQueueSize int `json:"pipeline_queue_size"`
}

type StagingSnapshot struct {
	Files []StagingFile `json:"files"`
}

type StagingFile struct {
	Path     string    `json:"path"`
	Hash     string    `json:"hash"`
	Modified time.Time `json:"modified"`
}

type AgentSnapshot struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	CurrentTask    string         `json:"current_task,omitempty"`
	ContextSummary string         `json:"context_summary,omitempty"`
	Extra          map[string]any `json:"extra,omitempty"`
}

func NewCheckpoint(id string, walSeq uint64) *Checkpoint {
	return &Checkpoint{
		Version:     CheckpointV1,
		ID:          id,
		CreatedAt:   time.Now(),
		WALSequence: walSeq,
		Pipelines:   make(map[string]PipelineSnapshot),
		Agents:      make(map[string]AgentSnapshot),
	}
}

func (c *Checkpoint) Serialize() ([]byte, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return data, nil
}

func DeserializeCheckpoint(data []byte) (*Checkpoint, error) {
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

func (c *Checkpoint) ComputeHash() string {
	hashData := struct {
		WALSequence uint64                      `json:"wal_sequence"`
		Pipelines   map[string]PipelineSnapshot `json:"pipelines"`
		LLMQueues   LLMQueueSnapshot            `json:"llm_queues"`
		Agents      map[string]AgentSnapshot    `json:"agents"`
	}{
		WALSequence: c.WALSequence,
		Pipelines:   c.Pipelines,
		LLMQueues:   c.LLMQueues,
		Agents:      c.Agents,
	}

	data, _ := json.Marshal(hashData)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (c *Checkpoint) Validate() error {
	if c.Version != CheckpointV1 {
		return ErrInvalidCheckpointVersion
	}

	if c.ContentHash == "" {
		return nil
	}

	computed := c.ComputeHash()
	if computed != c.ContentHash {
		return ErrCorruptedCheckpoint
	}

	return nil
}

func (c *Checkpoint) Finalize() {
	c.ContentHash = c.ComputeHash()
}

func (c *Checkpoint) AddPipeline(id string, state PipelineState, phase string) {
	c.Pipelines[id] = PipelineSnapshot{
		ID:    id,
		State: state,
		Phase: phase,
	}
}

func (c *Checkpoint) AddAgent(id, agentType, task, context string) {
	c.Agents[id] = AgentSnapshot{
		ID:             id,
		Type:           agentType,
		CurrentTask:    task,
		ContextSummary: context,
	}
}

func (c *Checkpoint) SetLLMQueues(userSize, pipelineSize int) {
	c.LLMQueues = LLMQueueSnapshot{
		UserQueueSize:     userSize,
		PipelineQueueSize: pipelineSize,
	}
}

func (c *Checkpoint) AddStagingFile(path, hash string, modified time.Time) {
	c.StagingDir.Files = append(c.StagingDir.Files, StagingFile{
		Path:     path,
		Hash:     hash,
		Modified: modified,
	})
}
