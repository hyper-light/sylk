//go:build !windows

package signal

import (
	"os"
	"syscall"
)

func handledSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGTSTP}
}

func isInterruptSignal(sig os.Signal) bool {
	return sig == syscall.SIGINT
}

func isTerminateSignal(sig os.Signal) bool {
	return sig == syscall.SIGTERM
}

func isSuspendSignal(sig os.Signal) bool {
	return sig == syscall.SIGTSTP
}
