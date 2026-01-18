package recovery

import (
	"sync"
	"testing"
	"time"
)

func TestRepetitionDetector_NoOperations(t *testing.T) {
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	score := rd.Score("agent-1")

	if score != 1.0 {
		t.Errorf("Score for nonexistent agent = %v, want 1.0", score)
	}
}

func TestRepetitionDetector_InsufficientData(t *testing.T) {
	rd := NewRepetitionDetector(DefaultRepetitionConfig())

	for i := 0; i < 5; i++ {
		rd.Record("agent-1", Operation{Hash: uint64(i)})
	}

	score := rd.Score("agent-1")
	if score != 1.0 {
		t.Errorf("Score with insufficient data = %v, want 1.0", score)
	}
}

func TestRepetitionDetector_DetectsSingleOpRepetition(t *testing.T) {
	rd := NewRepetitionDetector(RepetitionConfig{
		WindowSize:     50,
		MaxCycleLength: 5,
		MinRepetitions: 3,
	})

	for i := 0; i < 20; i++ {
		rd.Record("agent-1", Operation{Hash: 12345})
	}

	score := rd.Score("agent-1")
	if score >= 1.0 {
		t.Errorf("Score with same operation repeated = %v, want < 1.0", score)
	}
}

func TestRepetitionDetector_DetectsCycleOfTwo(t *testing.T) {
	rd := NewRepetitionDetector(RepetitionConfig{
		WindowSize:     50,
		MaxCycleLength: 5,
		MinRepetitions: 3,
	})

	for i := 0; i < 20; i++ {
		rd.Record("agent-1", Operation{Hash: uint64(i % 2)})
	}

	score := rd.Score("agent-1")
	if score >= 1.0 {
		t.Errorf("Score with A-B-A-B pattern = %v, want < 1.0", score)
	}
}

func TestRepetitionDetector_NoRepetitionForVariedOps(t *testing.T) {
	rd := NewRepetitionDetector(DefaultRepetitionConfig())

	for i := 0; i < 30; i++ {
		rd.Record("agent-1", Operation{Hash: uint64(i)})
	}

	score := rd.Score("agent-1")
	if score != 1.0 {
		t.Errorf("Score with varied operations = %v, want 1.0", score)
	}
}

func TestRepetitionDetector_RemoveAgent(t *testing.T) {
	rd := NewRepetitionDetector(DefaultRepetitionConfig())

	for i := 0; i < 20; i++ {
		rd.Record("agent-1", Operation{Hash: 12345})
	}

	rd.RemoveAgent("agent-1")

	score := rd.Score("agent-1")
	if score != 1.0 {
		t.Errorf("Score after remove = %v, want 1.0", score)
	}
}

func TestRepetitionDetector_MultipleAgents(t *testing.T) {
	rd := NewRepetitionDetector(DefaultRepetitionConfig())

	for i := 0; i < 20; i++ {
		rd.Record("agent-1", Operation{Hash: 12345})
		rd.Record("agent-2", Operation{Hash: uint64(i)})
	}

	score1 := rd.Score("agent-1")
	score2 := rd.Score("agent-2")

	if score1 >= score2 {
		t.Errorf("Repeating agent score %v should be < varied agent score %v", score1, score2)
	}
}

func TestRepetitionDetector_Concurrent(t *testing.T) {
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(agentNum int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rd.Record("agent-1", Operation{
					Hash:      uint64(j % 3),
					Timestamp: time.Now(),
				})
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = rd.Score("agent-1")
			}
		}()
	}

	wg.Wait()
}

func TestOperationLog_Recent(t *testing.T) {
	rd := NewRepetitionDetector(RepetitionConfig{
		WindowSize:     10,
		MaxCycleLength: 5,
		MinRepetitions: 3,
	})

	for i := 0; i < 5; i++ {
		rd.Record("agent-1", Operation{Action: string(rune('A' + i))})
	}

	log, _ := rd.operationLogs.Load("agent-1")
	recent := log.(*OperationLog).Recent(3)

	if len(recent) != 3 {
		t.Fatalf("Recent(3) len = %d, want 3", len(recent))
	}

	want := []string{"C", "D", "E"}
	for i, w := range want {
		if recent[i].Action != w {
			t.Errorf("Recent[%d].Action = %s, want %s", i, recent[i].Action, w)
		}
	}
}
