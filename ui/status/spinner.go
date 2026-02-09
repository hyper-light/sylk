package status

// DefaultFrames is the Braille dot animation sequence for the spinner.
var DefaultFrames = []string{
	"\u280B", "\u2819", "\u2839", "\u2838",
	"\u283C", "\u2834", "\u2826", "\u2827",
	"\u2807", "\u280F",
}

// Spinner cycles through animation frames on each tick.
type Spinner struct {
	frames  []string
	current int
}

// NewSpinner creates a Spinner initialized to the first frame of DefaultFrames.
func NewSpinner() *Spinner {
	return &Spinner{
		frames: DefaultFrames,
	}
}

// Tick advances the spinner to the next frame and returns it.
func (s *Spinner) Tick() string {
	frame := s.frames[s.current]
	s.current = (s.current + 1) % len(s.frames)
	return frame
}

// Current returns the current frame without advancing.
func (s *Spinner) Current() string {
	return s.frames[s.current]
}

// Reset returns the spinner to the first frame.
func (s *Spinner) Reset() {
	s.current = 0
}
