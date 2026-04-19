package quality

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// SCORE-04 Bayesian WAL entries. Observations and GP updates are logged
// BEFORE applying them to the in-memory posterior — durability-first so a
// crash between Append and Apply leaves the WAL as the authoritative
// truth. On recovery, the registered replayer decodes each entry and
// re-applies it to the session's SessionWeightManager.
//
// Dependency direction: this file lives in core/quality and imports
// core/concurrency for WAL primitives. core/concurrency does NOT import
// core/quality — the EntryType constants are a schema enum; the payload
// structs and encode/decode logic are here. Replay is wired via the
// RegisterWALEntryReplayer mechanism in core/concurrency/recovery.go.

// WeightObservationPayload is the WAL-durable record for a single Beta
// posterior update. It mirrors ObservationRecord but drops fields that
// are reconstructable from the other fields (PriorMean / PosteriorMean
// are derivable by replaying on top of the session's current state).
type WeightObservationPayload struct {
	Timestamp       time.Time  `json:"timestamp"`
	SessionID       string     `json:"session_id"`
	Domain          string     `json:"domain"`
	Signal          string     `json:"signal"`
	Success         bool       `json:"success"`
	EffectiveWeight float64    `json:"effective_weight"`
}

// GPObservationPayload captures one Gaussian-process observation — the
// agent quality GP's input tuple (context_size, output_tokens, tool_calls,
// quality). Fields mirror core/handoff's GPObservation for straight
// replay into the handoff profile learner.
type GPObservationPayload struct {
	Timestamp    time.Time `json:"timestamp"`
	SessionID    string    `json:"session_id"`
	AgentType    string    `json:"agent_type"`
	ModelID      string    `json:"model_id"`
	ContextSize  int       `json:"context_size"`
	OutputTokens int       `json:"output_tokens"`
	ToolCalls    int       `json:"tool_calls"`
	Quality      float64   `json:"quality"`
}

// ThresholdFeedbackPayload records a threshold-learning observation — a
// user or system decision that either endorsed or rejected the current
// threshold choice. Bayesian decision theory folds these into the
// threshold distribution.
type ThresholdFeedbackPayload struct {
	Timestamp    time.Time `json:"timestamp"`
	SessionID    string    `json:"session_id"`
	ThresholdKey string    `json:"threshold_key"`
	ChosenValue  float64   `json:"chosen_value"`
	WasOptimal   bool      `json:"was_optimal"`
	UtilityDelta float64   `json:"utility_delta"`
}

// DecayFeedbackPayload records an observation of a count-domain decay
// rate (e.g. "3 staleness events observed over a 100-hour window"). Used
// by LearnedDecayRate.Update on replay.
type DecayFeedbackPayload struct {
	Timestamp    time.Time `json:"timestamp"`
	SessionID    string    `json:"session_id"`
	Domain       string    `json:"domain"`
	EventCount   float64   `json:"event_count"`
	Interval     float64   `json:"interval"`
}

// WALWriter abstracts the append surface so core/quality does not tie
// itself to a specific WAL implementation. The production wiring passes a
// thin adapter around *concurrency.WriteAheadLog; tests pass a capturing
// fake.
type WALWriter interface {
	Append(entryType concurrency.EntryType, payload []byte) (uint64, error)
}

// WALSink fans observations out to the WAL. Registering a WALSink on a
// SessionWeightManager (via SetObservationHook) writes every observation
// to the WAL before it touches in-memory state. If the WAL append fails,
// the observation is still applied to memory but the caller is informed
// via the error hook — durability over availability.
type WALSink struct {
	writer WALWriter
	// errorHook receives append errors so callers can escalate or log
	// them without losing durability context. Nil is permitted — errors
	// are swallowed but the observation is still applied downstream.
	errorHook func(error)
}

// NewWALSink wires a writer into an observation hook.
func NewWALSink(writer WALWriter) *WALSink {
	return &WALSink{writer: writer}
}

// SetErrorHook installs a callback invoked when the WAL append fails.
// Optional; absent a hook, errors are silently dropped (the in-memory
// observation still applies, which matches availability-over-durability
// fallback behavior callers expect during soft outages).
func (s *WALSink) SetErrorHook(hook func(error)) {
	s.errorHook = hook
}

// ObservationHook returns an ObservationHook that writes each record to
// the WAL. Install via SessionWeightManager.SetObservationHook.
func (s *WALSink) ObservationHook() ObservationHook {
	return func(rec ObservationRecord) {
		payload := WeightObservationPayload{
			Timestamp:       time.Now().UTC(),
			SessionID:       rec.SessionID,
			Domain:          rec.Domain.String(),
			Signal:          rec.Signal.String(),
			Success:         rec.Success,
			EffectiveWeight: rec.EffectiveWeight,
		}
		data, err := json.Marshal(payload)
		if err != nil {
			s.reportError(fmt.Errorf("encode weight observation: %w", err))
			return
		}
		if _, err := s.writer.Append(concurrency.EntryWeightObservation, data); err != nil {
			s.reportError(fmt.Errorf("append weight observation: %w", err))
		}
	}
}

// AppendGPObservation writes a GP observation to the WAL. Called by the
// handoff profile learner's durability hook when SCORE-04 wiring lands.
func (s *WALSink) AppendGPObservation(p GPObservationPayload) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode gp observation: %w", err)
	}
	_, err = s.writer.Append(concurrency.EntryGPObservation, data)
	return err
}

// AppendThresholdFeedback writes a threshold-learning observation.
func (s *WALSink) AppendThresholdFeedback(p ThresholdFeedbackPayload) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode threshold feedback: %w", err)
	}
	_, err = s.writer.Append(concurrency.EntryThresholdFeedback, data)
	return err
}

// AppendDecayFeedback writes a decay-rate observation.
func (s *WALSink) AppendDecayFeedback(p DecayFeedbackPayload) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode decay feedback: %w", err)
	}
	_, err = s.writer.Append(concurrency.EntryDecayFeedback, data)
	return err
}

func (s *WALSink) reportError(err error) {
	if s.errorHook != nil {
		s.errorHook(err)
	}
}

// DecodeWeightObservation parses a WAL payload produced by ObservationHook.
// Public so tests and replay paths share the same decoder.
func DecodeWeightObservation(payload []byte) (WeightObservationPayload, error) {
	var p WeightObservationPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return WeightObservationPayload{}, fmt.Errorf("decode weight observation: %w", err)
	}
	return p, nil
}

// DecodeGPObservation parses a GP observation payload.
func DecodeGPObservation(payload []byte) (GPObservationPayload, error) {
	var p GPObservationPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return GPObservationPayload{}, fmt.Errorf("decode gp observation: %w", err)
	}
	return p, nil
}

// DecodeThresholdFeedback parses a threshold-feedback payload.
func DecodeThresholdFeedback(payload []byte) (ThresholdFeedbackPayload, error) {
	var p ThresholdFeedbackPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ThresholdFeedbackPayload{}, fmt.Errorf("decode threshold feedback: %w", err)
	}
	return p, nil
}

// DecodeDecayFeedback parses a decay-feedback payload.
func DecodeDecayFeedback(payload []byte) (DecayFeedbackPayload, error) {
	var p DecayFeedbackPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return DecayFeedbackPayload{}, fmt.Errorf("decode decay feedback: %w", err)
	}
	return p, nil
}

// ApplyWeightObservation replays a weight-observation payload against the
// named session's manager. The registered replayer (Replayer type, below)
// holds a registry of live session managers so recovery knows where to
// route each entry.
func ApplyWeightObservation(manager *SessionWeightManager, p WeightObservationPayload) error {
	if manager == nil {
		return fmt.Errorf("cannot apply weight observation: session manager not registered for %s", p.SessionID)
	}
	domain, ok := DomainFromString(p.Domain)
	if !ok {
		return fmt.Errorf("unknown domain %q in WAL entry", p.Domain)
	}
	signal, ok := SignalKindFromString(p.Signal)
	if !ok {
		return fmt.Errorf("unknown signal %q in WAL entry", p.Signal)
	}
	manager.Observe(SignalObservation{
		Kind:            signal,
		Success:         p.Success,
		EffectiveWeight: p.EffectiveWeight,
	}, domain)
	return nil
}

// Replayer is the concurrency.WALEntryReplayer implementation for all
// four Bayesian entry types. It holds a registry of (session_id →
// SessionWeightManager) so recovery can route each replay to the right
// session. GPObservation / ThresholdFeedback / DecayFeedback carry
// session ID too; consumers register their sinks via their respective
// handoff/threshold/decay surfaces.
type Replayer struct {
	mu           sync.RWMutex
	sessions     map[string]*SessionWeightManager
	gpHandler    func(GPObservationPayload) error
	thresholdH   func(ThresholdFeedbackPayload) error
	decayHandler func(DecayFeedbackPayload) error
}

// NewReplayer constructs a replayer with no sessions registered. Register
// sessions via RegisterSession before recovery begins.
func NewReplayer() *Replayer {
	return &Replayer{
		sessions: make(map[string]*SessionWeightManager),
	}
}

// RegisterSession wires a session manager so WeightObservation entries
// whose SessionID matches will be routed to it during replay.
func (r *Replayer) RegisterSession(sessionID string, manager *SessionWeightManager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sessionID] = manager
}

// UnregisterSession drops the session from the registry. Replay entries
// whose SessionID matches will be logged and skipped rather than erroring,
// so an already-shut-down session does not block recovery of the rest.
func (r *Replayer) UnregisterSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

// SetGPHandler installs the handler for EntryGPObservation replay. Nil
// clears the handler.
func (r *Replayer) SetGPHandler(handler func(GPObservationPayload) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gpHandler = handler
}

// SetThresholdHandler installs the EntryThresholdFeedback replay handler.
func (r *Replayer) SetThresholdHandler(handler func(ThresholdFeedbackPayload) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.thresholdH = handler
}

// SetDecayHandler installs the EntryDecayFeedback replay handler.
func (r *Replayer) SetDecayHandler(handler func(DecayFeedbackPayload) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.decayHandler = handler
}

// ReplayEntry implements concurrency.WALEntryReplayer.
func (r *Replayer) ReplayEntry(entry *concurrency.WALEntry) error {
	if entry == nil {
		return nil
	}
	switch entry.Type {
	case concurrency.EntryWeightObservation:
		return r.replayWeight(entry.Payload)
	case concurrency.EntryGPObservation:
		return r.replayGP(entry.Payload)
	case concurrency.EntryThresholdFeedback:
		return r.replayThreshold(entry.Payload)
	case concurrency.EntryDecayFeedback:
		return r.replayDecay(entry.Payload)
	}
	return nil
}

func (r *Replayer) replayWeight(payload []byte) error {
	p, err := DecodeWeightObservation(payload)
	if err != nil {
		return err
	}
	r.mu.RLock()
	manager := r.sessions[p.SessionID]
	r.mu.RUnlock()
	if manager == nil {
		// Session is not registered (e.g. already shut down, or replay is
		// for a different session whose manager was never wired). Skip
		// silently — the rest of the WAL should still replay.
		return nil
	}
	return ApplyWeightObservation(manager, p)
}

func (r *Replayer) replayGP(payload []byte) error {
	p, err := DecodeGPObservation(payload)
	if err != nil {
		return err
	}
	r.mu.RLock()
	handler := r.gpHandler
	r.mu.RUnlock()
	if handler == nil {
		return nil
	}
	return handler(p)
}

func (r *Replayer) replayThreshold(payload []byte) error {
	p, err := DecodeThresholdFeedback(payload)
	if err != nil {
		return err
	}
	r.mu.RLock()
	handler := r.thresholdH
	r.mu.RUnlock()
	if handler == nil {
		return nil
	}
	return handler(p)
}

func (r *Replayer) replayDecay(payload []byte) error {
	p, err := DecodeDecayFeedback(payload)
	if err != nil {
		return err
	}
	r.mu.RLock()
	handler := r.decayHandler
	r.mu.RUnlock()
	if handler == nil {
		return nil
	}
	return handler(p)
}

// Register installs this replayer against all four Bayesian entry types
// in the process-wide registry. Call once at startup after constructing
// the SessionWeightManager for the current session.
func (r *Replayer) Register() {
	concurrency.RegisterWALEntryReplayer(concurrency.EntryWeightObservation, r)
	concurrency.RegisterWALEntryReplayer(concurrency.EntryGPObservation, r)
	concurrency.RegisterWALEntryReplayer(concurrency.EntryThresholdFeedback, r)
	concurrency.RegisterWALEntryReplayer(concurrency.EntryDecayFeedback, r)
}

// Unregister removes the replayer from the registry. Useful for tests and
// for graceful session shutdown.
func (r *Replayer) Unregister() {
	concurrency.RegisterWALEntryReplayer(concurrency.EntryWeightObservation, nil)
	concurrency.RegisterWALEntryReplayer(concurrency.EntryGPObservation, nil)
	concurrency.RegisterWALEntryReplayer(concurrency.EntryThresholdFeedback, nil)
	concurrency.RegisterWALEntryReplayer(concurrency.EntryDecayFeedback, nil)
}
