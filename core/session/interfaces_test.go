package session_test

import (
	"testing"

	"github.com/adalundhe/sylk/core/session"
)

func TestInterfaceImplementations(t *testing.T) {
	var _ session.SessionManagerService = (*session.Manager)(nil)
	var _ session.SessionContextService = (*session.Context)(nil)
	var _ session.PersisterService = (*session.MemoryPersister)(nil)
	var _ session.PersisterService = (*session.FilePersister)(nil)
}
