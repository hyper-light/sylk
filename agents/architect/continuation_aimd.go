package architect

// continuationAIMD implements TCP-style additive-increase multiplicative-decrease
// for continuation round budgeting. Self-tunes to model speed and prompt complexity.
//
// Slow start: begin with conservative round budget, double on progress.
// Additive increase: add 1 round when making good progress (past slow-start).
// Multiplicative decrease: halve allowed rounds on stall (progress decay, truncation).
type continuationAIMD struct {
	allowedRounds int  // current congestion window (rounds)
	maxRounds     int  // hard ceiling from model params
	minRounds     int  // floor (always >= 1)
	ssThreshold   int  // slow-start threshold
	inSlowStart   bool // slow-start phase flag
}

func newContinuationAIMD(maxRounds int) *continuationAIMD {
	return &continuationAIMD{
		allowedRounds: max(1, maxRounds/2), // start conservative
		maxRounds:     maxRounds,
		minRounds:     1,
		ssThreshold:   maxRounds,
		inSlowStart:   true,
	}
}

// OnProgress is called when a continuation round produces good net-new content.
func (a *continuationAIMD) OnProgress() {
	if a.inSlowStart {
		a.allowedRounds = min(a.allowedRounds*2, a.ssThreshold) // exponential in slow-start
		if a.allowedRounds >= a.ssThreshold {
			a.inSlowStart = false
		}
	} else {
		a.allowedRounds = min(a.allowedRounds+1, a.maxRounds) // additive increase
	}
}

// OnStall is called when progress decays or truncation escalates.
func (a *continuationAIMD) OnStall() {
	a.ssThreshold = max(a.minRounds, a.allowedRounds/2)
	a.allowedRounds = max(a.minRounds, a.allowedRounds/2) // multiplicative decrease
	a.inSlowStart = false                                  // exit slow-start on loss
}

// AllowedRounds returns the current continuation budget.
func (a *continuationAIMD) AllowedRounds() int {
	return a.allowedRounds
}
