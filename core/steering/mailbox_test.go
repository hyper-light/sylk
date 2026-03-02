package steering

import (
	"sync"
	"testing"
	"time"
)

func TestMailboxFIFO(t *testing.T) {
	var mb SteeringMailbox
	for i := range 5 {
		mb.Deposit(Command{
			Type:      CommandSteer,
			Text:      string(rune('a' + i)),
			Timestamp: time.Now(),
		})
	}

	cmds := mb.Drain()
	if len(cmds) != 5 {
		t.Fatalf("expected 5 commands, got %d", len(cmds))
	}
	for i, cmd := range cmds {
		want := string(rune('a' + i))
		if cmd.Text != want {
			t.Errorf("cmd[%d] text = %q, want %q", i, cmd.Text, want)
		}
	}
}

func TestMailboxDrainEmpty(t *testing.T) {
	var mb SteeringMailbox
	cmds := mb.Drain()
	if cmds != nil {
		t.Fatalf("expected nil from empty drain, got %v", cmds)
	}
}

func TestMailboxOverflowOverwritesOldest(t *testing.T) {
	var mb SteeringMailbox

	// Fill to capacity.
	for i := range mailboxCapacity {
		mb.Deposit(Command{
			Type:      CommandSteer,
			Text:      string(rune('A' + i)),
			Timestamp: time.Now(),
		})
	}

	// Add 3 more — should overwrite the oldest 3.
	for i := range 3 {
		mb.Deposit(Command{
			Type:      CommandSteer,
			Text:      string(rune('0' + i)),
			Timestamp: time.Now(),
		})
	}

	cmds := mb.Drain()
	if len(cmds) != mailboxCapacity {
		t.Fatalf("expected %d commands, got %d", mailboxCapacity, len(cmds))
	}

	// First should be 'D' (index 3, since first 3 were overwritten).
	if cmds[0].Text != "D" {
		t.Errorf("oldest surviving cmd = %q, want %q", cmds[0].Text, "D")
	}

	// Last 3 should be the overflow entries.
	for i := range 3 {
		idx := mailboxCapacity - 3 + i
		want := string(rune('0' + i))
		if cmds[idx].Text != want {
			t.Errorf("cmd[%d] text = %q, want %q", idx, cmds[idx].Text, want)
		}
	}
}

func TestMailboxPending(t *testing.T) {
	var mb SteeringMailbox
	if mb.Pending() != 0 {
		t.Fatalf("expected 0 pending, got %d", mb.Pending())
	}

	mb.Deposit(Command{Type: CommandSteer, Text: "a", Timestamp: time.Now()})
	mb.Deposit(Command{Type: CommandSteer, Text: "b", Timestamp: time.Now()})
	if mb.Pending() != 2 {
		t.Fatalf("expected 2 pending, got %d", mb.Pending())
	}

	mb.Drain()
	if mb.Pending() != 0 {
		t.Fatalf("expected 0 after drain, got %d", mb.Pending())
	}
}

func TestMailboxConcurrentDeposit(t *testing.T) {
	var mb SteeringMailbox
	var wg sync.WaitGroup

	producers := 8
	perProducer := 10
	wg.Add(producers)

	for p := range producers {
		go func(id int) {
			defer wg.Done()
			for i := range perProducer {
				mb.Deposit(Command{
					Type:      CommandSteer,
					Text:      string(rune('A' + id)),
					Timestamp: time.Now(),
					MessageIndex: i,
				})
			}
		}(p)
	}

	wg.Wait()

	cmds := mb.Drain()
	// Total deposits = 80, capacity = 16, so we should have exactly 16.
	if len(cmds) != mailboxCapacity {
		t.Fatalf("expected %d commands after concurrent overflow, got %d", mailboxCapacity, len(cmds))
	}
}

func TestMailboxDrainThenDeposit(t *testing.T) {
	var mb SteeringMailbox

	mb.Deposit(Command{Type: CommandSteer, Text: "first", Timestamp: time.Now()})
	mb.Drain()

	mb.Deposit(Command{Type: CommandSteer, Text: "second", Timestamp: time.Now()})
	cmds := mb.Drain()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Text != "second" {
		t.Errorf("text = %q, want %q", cmds[0].Text, "second")
	}
}
