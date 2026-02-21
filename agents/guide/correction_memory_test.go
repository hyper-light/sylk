package guide

import (
	"testing"
	"time"
)

func TestCorrectionMemory_SelectForPrompt_ExactMatch(t *testing.T) {
	memory := newCorrectionMemory(32)
	now := time.Now()
	memory.add(CorrectionRecord{
		Input:         "where is auth middleware",
		WrongIntent:   IntentRecall,
		WrongDomain:   DomainGeneral,
		WrongTarget:   TargetGuide,
		CorrectIntent: IntentFind,
		CorrectDomain: DomainLocal,
		CorrectTarget: TargetLibrarian,
		CorrectedAt:   now,
	})
	memory.add(CorrectionRecord{
		Input:         "where is auth middleware",
		WrongIntent:   IntentFind,
		WrongDomain:   DomainHistory,
		WrongTarget:   TargetArchivalist,
		CorrectIntent: IntentFind,
		CorrectDomain: DomainLocal,
		CorrectTarget: TargetLibrarian,
		CorrectedAt:   now.Add(time.Second),
	})

	selected := memory.selectForPrompt("where is auth middleware", 4)
	if len(selected) != 2 {
		t.Fatalf("selected corrections = %d, want 2", len(selected))
	}
}

func TestCorrectionMemory_SelectForPrompt_FuzzyMatch(t *testing.T) {
	memory := newCorrectionMemory(32)
	memory.add(CorrectionRecord{
		Input:         "find the auth middleware file",
		WrongIntent:   IntentRecall,
		WrongDomain:   DomainGeneral,
		WrongTarget:   TargetGuide,
		CorrectIntent: IntentFind,
		CorrectDomain: DomainLocal,
		CorrectTarget: TargetLibrarian,
		CorrectedAt:   time.Now(),
	})
	memory.add(CorrectionRecord{
		Input:         "what did we do last week on caching",
		WrongIntent:   IntentFind,
		WrongDomain:   DomainLocal,
		WrongTarget:   TargetLibrarian,
		CorrectIntent: IntentRecall,
		CorrectDomain: DomainHistory,
		CorrectTarget: TargetArchivalist,
		CorrectedAt:   time.Now(),
	})

	selected := memory.selectForPrompt("locate auth middleware implementation", 4)
	if len(selected) == 0 {
		t.Fatal("expected fuzzy correction selection")
	}
	if selected[0].CorrectTarget != TargetLibrarian {
		t.Fatalf("selected target = %s, want %s", selected[0].CorrectTarget, TargetLibrarian)
	}
}

func TestCorrectionMemory_FormatExamplesBounded(t *testing.T) {
	memory := newCorrectionMemory(128)
	for i := 0; i < 10; i++ {
		memory.add(CorrectionRecord{
			Input:         "route this to librarian",
			WrongIntent:   IntentRecall,
			WrongDomain:   DomainGeneral,
			WrongTarget:   TargetGuide,
			CorrectIntent: IntentFind,
			CorrectDomain: DomainLocal,
			CorrectTarget: TargetLibrarian,
			CorrectedAt:   time.Now().Add(time.Duration(i) * time.Second),
		})
	}
	selected := memory.selectForPrompt("route this to librarian", 3)
	if len(selected) != 3 {
		t.Fatalf("selected corrections = %d, want 3", len(selected))
	}
	examples := formatCorrectionExamples(selected)
	if examples == "" {
		t.Fatal("expected formatted examples output")
	}
}
