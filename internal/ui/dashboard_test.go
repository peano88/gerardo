package ui

import (
	"slices"
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
	if n, _ := d.selected(); n != 2 || d.cursor != 1 {
		t.Fatalf("moving up onto the top header should bounce back, got %d at row %d", n, d.cursor)
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

func TestDashboardCopyNumber(t *testing.T) {
	d := loadedDashboard(t, 80)
	if _, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}); cmd == nil {
		t.Fatal("y should copy the selected change number")
	}
	d.Update(copiedMsg{text: "1"})
	if !strings.Contains(d.Status(), "copied 1") {
		t.Fatalf("status should confirm the copy: %q", d.Status())
	}
	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(d.Status(), "copied") {
		t.Fatal("the next key press should clear the notice")
	}
}

func TestDashboardKeepsSelectionOnReload(t *testing.T) {
	d := loadedDashboard(t, 80)
	d.Update(keyRunes("G"))
	if n, _ := d.selected(); n != 2 {
		t.Fatalf("G should select the last change, got %d", n)
	}
	d.Update(changesLoadedMsg{changes: []gerrit.ChangeInfo{
		{Number: 3, Subject: "new"},
		{Number: 2, Subject: "second"},
	}})
	if n, _ := d.selected(); n != 2 {
		t.Fatalf("reloading should keep the selected change, got %d", n)
	}
	d.Update(keyRunes("g"))
	if n, _ := d.selected(); n != 3 {
		t.Fatalf("g should select the first change, got %d", n)
	}
}

func TestDashboardEmpty(t *testing.T) {
	d := newDashboard(nil, "is:open")
	d.SetSize(80, 10)
	d.Update(changesLoadedMsg{})
	if !strings.Contains(d.View(), "No changes match is:open") {
		t.Fatalf("empty dashboard should say so:\n%s", d.View())
	}
}

func TestDashboardAttention(t *testing.T) {
	me := gerrit.AccountInfo{AccountID: 7}
	d := newDashboard(nil, "is:open")
	d.SetSize(120, 10)
	d.Update(changesLoadedMsg{self: &me, changes: []gerrit.ChangeInfo{
		{Number: 1, Subject: "waiting", Owner: me, UnresolvedCommentCount: 4,
			AttentionSet: map[string]gerrit.AttentionSetInfo{"7": {Account: me}}},
		{Number: 2, Subject: "idle", Owner: me},
	}})
	lines := plainLines(d.View())
	if !strings.Contains(lines[0], "1 waiting on you") || !strings.Contains(lines[0], "2 changes") {
		t.Fatalf("title bar should count the changes waiting on the user: %q", lines[0])
	}
	for _, l := range lines {
		switch {
		case strings.Contains(l, "1  waiting"):
			if !strings.HasPrefix(l, "▌●") || !slices.Contains(strings.Fields(l), "4") {
				t.Fatalf("change in the attention set should be marked with its unresolved count: %q", l)
			}
		case strings.Contains(l, "idle") && strings.Contains(l, "●"):
			t.Fatalf("change not waiting on the user should not be marked: %q", l)
		}
	}
}
