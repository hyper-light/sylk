package graph

import (
	"encoding/binary"
	"os"
	"sync/atomic"
)

type IDAllocator struct {
	counter  atomic.Uint32
	filePath string
}

func NewIDAllocator(filePath string) (*IDAllocator, error) {
	alloc := &IDAllocator{filePath: filePath}

	if filePath != "" {
		if err := alloc.load(); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	return alloc, nil
}

func (a *IDAllocator) NextID() uint32 {
	return a.counter.Add(1) - 1
}

func (a *IDAllocator) Current() uint32 {
	return a.counter.Load()
}

func (a *IDAllocator) load() error {
	data, err := os.ReadFile(a.filePath)
	if err != nil {
		return err
	}
	if len(data) < 4 {
		return nil
	}
	a.counter.Store(binary.LittleEndian.Uint32(data[:4]))
	return nil
}

func (a *IDAllocator) Persist() error {
	if a.filePath == "" {
		return nil
	}
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, a.counter.Load())
	return os.WriteFile(a.filePath, buf, 0644)
}

func (a *IDAllocator) Reset() {
	a.counter.Store(0)
}
