package gitpanel

import (
	"testing"
	"time"
)

type testListEntry struct {
	filterText string
	sortKey    string
	sortTime   time.Time
}

func (e testListEntry) FilterText() string  { return e.filterText }
func (e testListEntry) SortKey() string     { return e.sortKey }
func (e testListEntry) SortTime() time.Time { return e.sortTime }

func TestComputeWidthsCapsUserWidthAgainstElasticMinimums(t *testing.T) {
	defs := []*ColumnDef{
		{ID: "fixed", Label: "Fixed", Strategy: WidthFixed, Fixed: 4},
		{ID: "user", Label: "User", Strategy: WidthFlex},
		{ID: "other", Label: "Other", Strategy: WidthMaxContent},
	}
	layout := NewColumnLayout(defs)
	layout.Columns[1].UserWidth = 20

	layout.ComputeWidths(nil, 20)

	if got, want := layout.Columns[0].Width, 4; got != want {
		t.Fatalf("fixed width = %d, want %d", got, want)
	}
	if got, want := layout.Columns[1].Width, 4; got != want {
		t.Fatalf("user width = %d, want %d", got, want)
	}
	if got, want := layout.Columns[2].Width, colMinWidth(defs[2]); got != want {
		t.Fatalf("other width = %d, want %d", got, want)
	}
}

func TestComputeWidthsFlexColumnAbsorbsRemainingBudget(t *testing.T) {
	defs := []*ColumnDef{
		{
			ID:       "subject",
			Label:    "Subject",
			Strategy: WidthMaxContent,
			Measure:  func(listEntry) int { return 12 },
		},
		{
			ID:       "body",
			Label:    "Body",
			Strategy: WidthFlex,
			Measure:  func(listEntry) int { return 6 },
		},
	}
	layout := NewColumnLayout(defs)
	entries := []listEntry{testListEntry{filterText: "x", sortKey: "x", sortTime: time.Unix(0, 0)}}

	layout.ComputeWidths(entries, 30)

	if got, want := layout.Columns[0].Width, 12; got != want {
		t.Fatalf("subject width = %d, want %d", got, want)
	}
	if got, want := layout.Columns[1].Width, 10; got != want {
		t.Fatalf("flex width = %d, want %d", got, want)
	}
}
