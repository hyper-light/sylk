package guide

import (
	"fmt"
	"sync"
	"testing"
)

func TestParser_ConcurrentSetRoutingAndParse(t *testing.T) {
	parser := NewParser("@")
	routing := NewRoutingAggregator()

	const (
		writers    = 6
		readers    = 6
		iterations = 200
	)

	var wg sync.WaitGroup
	start := make(chan struct{})

	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			<-start
			for iter := 0; iter < iterations; iter++ {
				id := fmt.Sprintf("agent-%d-%d", writer, iter)
				alias := fmt.Sprintf("alias%d_%d", writer, iter)
				routing.RegisterAgent(&AgentRoutingInfo{
					ID:      id,
					Type:    "engineer",
					Name:    alias,
					Aliases: []string{alias + "_alt"},
					ActionShortcuts: []ActionShortcut{
						{Name: fmt.Sprintf("action%d_%d", writer, iter)},
					},
				})
				parser.SetRouting(routing)
			}
		}(writer)
	}

	for reader := 0; reader < readers; reader++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			<-start
			for iter := 0; iter < iterations; iter++ {
				inputs := []string{
					"@guide help",
					"@to:guide hi",
					"@guide:help:system",
				}
				for _, input := range inputs {
					if _, err := parser.Parse(input); err != nil {
						t.Errorf("Parse(%q): %v", input, err)
						return
					}
				}
			}
		}(reader)
	}

	close(start)
	wg.Wait()
}
