package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

func loadedDashboard(t *testing.T, width int) *dashboard {
	t.Helper()
	d := newDashboard(nil, "is:open")
	d.SetSize(width, 30)
	d.Update(changesLoadedMsg{changes: []gerrit.ChangeInfo{
		{Number: 1, Subject: "first"},
		{Number: 2, Subject: "second"},
	}})
	return d
}

func TestDashboardPreview(t *testing.T) {
	d := loadedDashboard(t, 200)
	if !d.split() || d.previewFor != 1 {
		t.Fatalf("wide dashboard should preview the first change, previewing %d", d.previewFor)
	}
	if !strings.Contains(d.View(), "loading…") {
		t.Fatal("preview should show a placeholder while loading")
	}

	// A stale tick, from a cursor position left since, is ignored.
	if _, cmd := d.Update(previewTickMsg{seq: d.previewSeq - 1, number: 1}); cmd != nil {
		t.Fatal("stale preview tick should not fetch")
	}

	d.Update(previewLoadedMsg{number: 1, change: &gerrit.ChangeInfo{Number: 1, Subject: "first", Status: "NEW"}})
	if !strings.Contains(d.View(), "1: first") {
		t.Fatalf("preview should render the loaded change:\n%s", d.View())
	}

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyDown})
	if d.previewFor != 2 || cmd == nil {
		t.Fatalf("moving the cursor should schedule the next preview, previewing %d", d.previewFor)
	}
	d.Update(previewLoadedMsg{number: 1, change: &gerrit.ChangeInfo{Number: 1, Subject: "first"}})
	if strings.Contains(d.View(), "1: first") {
		t.Fatal("a preview arriving for another change should not replace the current one")
	}

	_, cmd = d.Update(tea.KeyMsg{Type: tea.KeyUp})
	if d.previewFor != 1 || cmd != nil || !strings.Contains(d.View(), "1: first") {
		t.Fatal("going back to a cached change should render it without fetching")
	}
}

func TestDashboardGroupsOwnChangesFirst(t *testing.T) {
	me := gerrit.AccountInfo{AccountID: 7, Name: "Me"}
	other := gerrit.AccountInfo{AccountID: 8, Name: "Bob"}
	d := newDashboard(nil, "is:open")
	d.SetSize(100, 30)
	d.Update(changesLoadedMsg{
		self: &me,
		changes: []gerrit.ChangeInfo{
			{Number: 1, Subject: "bob's", Owner: other},
			{Number: 2, Subject: "mine", Owner: me},
			{Number: 3, Subject: "bob's again", Owner: other},
		},
	})

	var order []string
	for _, it := range d.items {
		if it.change == nil {
			order = append(order, it.header)
		} else {
			order = append(order, it.change.Subject)
		}
	}
	want := []string{"▾ My changes (1)", "mine", "▾ Reviews (2)", "bob's", "bob's again"}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", order, want)
	}

	if n, _ := d.selected(); n != 2 {
		t.Fatalf("cursor should start on the first own change, got %d", n)
	}
	down := tea.KeyMsg{Type: tea.KeyDown}
	up := tea.KeyMsg{Type: tea.KeyUp}

	d.Update(down)
	if n, _ := d.selected(); n != 1 {
		t.Fatalf("moving down should skip the Reviews header, got %d", n)
	}
	d.Update(up)
	if n, _ := d.selected(); n != 2 {
		t.Fatalf("moving up should skip the Reviews header, got %d", n)
	}
	d.Update(up)
	if n, _ := d.selected(); n != 2 || d.table.Cursor() != 1 {
		t.Fatalf("moving up onto the top header should bounce back, got %d at row %d", n, d.table.Cursor())
	}
	if !strings.Contains(d.View(), "My changes (1)") {
		t.Fatalf("headers should be rendered:\n%s", d.View())
	}
}

func TestGroupChangesWithoutSelf(t *testing.T) {
	items := groupChanges([]gerrit.ChangeInfo{{Number: 1}, {Number: 2}}, 0)
	if len(items) != 2 || items[0].change == nil || items[1].change == nil {
		t.Fatalf("without a known account the changes should be listed flat, got %+v", items)
	}
}

func TestDashboardNarrowHasNoPreview(t *testing.T) {
	d := loadedDashboard(t, 100)
	if d.split() || d.previewFor != 0 {
		t.Fatal("narrow dashboard should not preview")
	}

	wide := loadedDashboard(t, 200)
	wide.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	if wide.split() {
		t.Fatal("P should hide the preview")
	}
}
