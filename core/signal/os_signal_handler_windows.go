//go:build windows

package signal

import (
	"os"
	"syscall"
)

func handledSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func isInterruptSignal(sig os.Signal) bool {
	return sig == os.Interrupt
}

func isTerminateSignal(sig os.Signal) bool {
	return sig == syscall.SIGTERM
}

func isSuspendSignal(os.Signal) bool {
	return false
}
