package pool_test

import (
	"testing"

	"github.com/adalundhe/sylk/core/pool"
)

func TestInterfaceImplementations(t *testing.T) {
	var _ pool.PriorityPoolService = (*pool.PriorityPool)(nil)
	var _ pool.SessionPoolService = (*pool.SessionPool)(nil)
	var _ pool.FairnessControllerService = (*pool.FairnessController)(nil)
}
