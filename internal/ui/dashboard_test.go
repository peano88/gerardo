package ui

import (
	"net/http"
	"net/http/httptest"
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
	if info := ansiRe.ReplaceAllString(d.TitleInfo(), ""); !strings.Contains(info, "1 waiting on you") || !strings.Contains(info, "2 changes") {
		t.Fatalf("title bar should count the changes waiting on the user: %q", info)
	}
	lines := plainLines(d.View())
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

func TestDashboardLoadsMorePages(t *testing.T) {
	page := func(from, n int) []gerrit.ChangeInfo {
		var changes []gerrit.ChangeInfo
		for i := range n {
			changes = append(changes, gerrit.ChangeInfo{Number: from + i, Subject: "c"})
		}
		return changes
	}
	d := newResults(nil, "status:merged", nil, &[]string{})
	d.SetSize(100, 10)
	d.Init()
	d.Update(changesLoadedMsg{gen: d.gen, changes: page(1, PageSize), more: true})
	if d.loadingMore || !strings.Contains(d.TitleInfo(), "50+ changes") {
		t.Fatal("first page should not fetch more until the cursor nears the end")
	}

	d.Update(keyRunes("G"))
	if !d.loadingMore {
		t.Fatal("reaching the end should fetch the next page")
	}
	if !strings.Contains(d.View(), "loading more…") {
		t.Fatalf("the list should show the next page loading:\n%s", d.View())
	}

	// A page from before a reload is dropped.
	d.Update(changesLoadedMsg{gen: d.gen - 1, start: PageSize, changes: page(999, 1)})
	if len(d.changes) != PageSize {
		t.Fatal("stale page should be ignored")
	}
	d.Update(changesLoadedMsg{gen: d.gen, start: PageSize, changes: page(PageSize+1, 3)})
	if len(d.changes) != PageSize+3 || d.more || d.loadingMore {
		t.Fatalf("next page should be appended, got %d changes", len(d.changes))
	}
	if n, _ := d.selected(); n != PageSize {
		t.Fatalf("appending should keep the selection, got %d", n)
	}
	if d.items[0].change == nil {
		t.Fatal("search results should not be grouped")
	}
}

func TestResultsGoBack(t *testing.T) {
	d := newResults(nil, "status:merged", nil, &[]string{})
	if _, cmd := d.Update(keyRunes("q")); cmd == nil || cmd() != pop() {
		t.Fatal("q on search results should go back, not quit")
	}
	_, cmd := d.Update(keyRunes("/"))
	push, ok := cmd().(pushMsg)
	if !ok || push.screen.(*searchScreen).input.Value() != "status:merged" || !push.screen.(*searchScreen).replace {
		t.Fatal("/ on results should edit the current query in place")
	}
}

func TestPathFilter(t *testing.T) {
	SetPathFilter([]string{"services/accounts", "legacy/*.go"}, []string{"services/accounts/pkg", "services/accounts/bill?ng/"})
	t.Cleanup(func() { SetPathFilter(nil, nil) })
	for path, hidden := range map[string]bool{
		"services/accounts/api/main.go":    true,
		"services/accounts":                true,
		"services/accountsx/main.go":       false,
		"services/accounts/pkg/x/y.go":     false,
		"services/accounts/billing/x.go": false,
		"services/payments/main.go":         false,
		"legacy/a/b.go":                   true,
		"legacy/README.md":                false,
	} {
		if got := paths.hidesPath(path); got != hidden {
			t.Errorf("%s: hidden %v, want %v", path, got, hidden)
		}
	}
}

func TestDashboardHidesChangesByPath(t *testing.T) {
	SetPathFilter([]string{"services/accounts"}, []string{"services/accounts/pkg"})
	t.Cleanup(func() { SetPathFilter(nil, nil) })
	files := func(paths ...string) map[string]gerrit.RevisionInfo {
		f := map[string]gerrit.FileInfo{"/COMMIT_MSG": {}}
		for _, p := range paths {
			f[p] = gerrit.FileInfo{}
		}
		return map[string]gerrit.RevisionInfo{"r": {Number: 1, Files: f}}
	}

	var options []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/a/changes/") {
			options = r.URL.Query()["o"]
		}
		_, _ = w.Write([]byte(")]}'\n[]"))
	}))
	defer srv.Close()

	d := newResults(gerrit.NewClient(srv.URL, "u", "p"), "is:open", &gerrit.AccountInfo{AccountID: 7}, &[]string{})
	d.SetSize(120, 10)
	d.Init()()
	if !slices.Contains(options, "CURRENT_FILES") {
		t.Fatalf("the files are needed to filter, got options %v", options)
	}

	d.Update(changesLoadedMsg{gen: d.gen, changes: []gerrit.ChangeInfo{
		{Number: 1, Subject: "accounts only", CurrentRevision: "r", Revisions: files("services/accounts/api/a.go")},
		{Number: 2, Subject: "accounts and payment", CurrentRevision: "r", Revisions: files("services/accounts/api/a.go", "services/payments/b.go")},
		{Number: 3, Subject: "accounts pkg", CurrentRevision: "r", Revisions: files("services/accounts/pkg/c.go")},
	}})
	numbers := func() []int {
		var n []int
		for _, it := range d.items {
			n = append(n, it.change.Number)
		}
		return n
	}
	if got := numbers(); !slices.Equal(got, []int{2, 3}) {
		t.Fatalf("only changes touching hidden paths only should be hidden, listed %v", got)
	}
	if info := ansiRe.ReplaceAllString(d.TitleInfo(), ""); !strings.Contains(info, "⊘ 1 in services/accounts hidden") || !strings.Contains(info, "2 changes") {
		t.Fatalf("title bar should count the hidden changes: %q", info)
	}

	d.Update(keyRunes("H"))
	if got := numbers(); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("H should show the hidden changes, listed %v", got)
	}
	if info := ansiRe.ReplaceAllString(d.TitleInfo(), ""); !strings.Contains(info, "showing filtered changes") {
		t.Fatalf("title bar should tell filtered changes are shown: %q", info)
	}
}
