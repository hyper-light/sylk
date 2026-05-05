package bridge

// This file provides recordingProgram, a TeaProgram fake used by
// bridge tests that exercise the program-Send interaction. The
// production bridges deliver to a tea.Program; tests inject this
// fake to capture the dispatched messages.

type recordingProgram struct {
	messages []any
}

func (r *recordingProgram) Send(m any) {
	r.messages = append(r.messages, m)
}
