// Package macro implements vim-style macro recording and playback.
// Macros record sequences of keystrokes into named registers and replay
// them on demand.
package macro

import "sync"

// maxMacroKeyBuffer is the maximum number of runes that can be recorded in a
// single macro. Derived from a conservative upper bound on useful macro
// length -- longer macros indicate a logic error or runaway recording.
const maxMacroKeyBuffer = 4096

// Recorder captures keystrokes into a register during macro recording.
type Recorder struct {
	mu        sync.Mutex
	recording bool
	register  rune
	keys      []rune
}

// NewRecorder creates a Recorder ready for use.
func NewRecorder() *Recorder {
	return &Recorder{
		keys: make([]rune, 0, maxMacroKeyBuffer),
	}
}

// StartRecording begins recording keystrokes into the given register.
// If a recording is already active, it is discarded.
func (r *Recorder) StartRecording(register rune) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recording = true
	r.register = register
	r.keys = r.keys[:0]
}

// StopRecording ends the active recording and returns the target register
// and the captured key sequence. Returns zero values if no recording was
// active.
func (r *Recorder) StopRecording() (rune, []rune) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording {
		return 0, nil
	}
	r.recording = false
	reg := r.register
	result := make([]rune, len(r.keys))
	copy(result, r.keys)
	r.keys = r.keys[:0]
	return reg, result
}

// IsRecording reports whether a macro recording is currently active.
func (r *Recorder) IsRecording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recording
}

// RecordKey appends a key to the active recording buffer. The key is
// silently dropped if no recording is active or the buffer is full.
func (r *Recorder) RecordKey(key rune) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording {
		return
	}
	if len(r.keys) >= maxMacroKeyBuffer {
		return
	}
	r.keys = append(r.keys, key)
}

// Register returns the register being recorded into. Returns 0 if no
// recording is active.
func (r *Recorder) Register() rune {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording {
		return 0
	}
	return r.register
}
