package ui

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerardo/internal/gerrit"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainLines strips the styling and the trailing background padding of rendered rows.
func plainLines(s string) []string {
	lines := strings.Split(ansiRe.ReplaceAllString(s, ""), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

func TestRenderDiffLineNumbers(t *testing.T) {
	d := &gerrit.DiffInfo{
		ChangeType: "MODIFIED",
		Content: []gerrit.DiffContent{
			{AB: []string{"package main"}},
			{Skip: 10},
			{A: []string{"old"}, B: []string{"new", "\tnewer"}},
			{AB: []string{"}"}},
		},
	}
	got := plainLines(renderDiff("main.go", d, nil, diffOptions{context: 3, width: 80}).content)
	want := []string{
		"main.go modified",
		"",
		"    1     1   package main",
		"            ⋯ 10 unchanged lines",
		"   12       - old",
		"         12 + new",
		"         13 +     newer",
		"   13    14   }",
		"",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRenderDiffCollapsesUnchangedLines(t *testing.T) {
	numbered := func(from, to int) []string {
		var lines []string
		for i := from; i <= to; i++ {
			lines = append(lines, "l"+strconv.Itoa(i))
		}
		return lines
	}
	d := &gerrit.DiffInfo{
		ChangeType: "MODIFIED",
		Content: []gerrit.DiffContent{
			{AB: numbered(1, 10)},
			{B: []string{"added"}},
			{AB: numbered(11, 20)},
			{A: []string{"removed"}},
			{AB: numbered(21, 30)},
		},
	}
	got := plainLines(renderDiff("f", d, nil, diffOptions{context: 2, width: 80}).content)
	want := []string{
		"f modified",
		"",
		"            ⋯ 8 unchanged lines",
		"    9     9   l9",
		"   10    10   l10",
		"         11 + added",
		"   11    12   l11",
		"   12    13   l12",
		"            ⋯ 6 unchanged lines",
		"   19    20   l19",
		"   20    21   l20",
		"   21       - removed",
		"   22    22   l21",
		"   23    23   l22",
		"            ⋯ 8 unchanged lines",
		"",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	full := ansiRe.ReplaceAllString(renderDiff("f", d, nil, diffOptions{context: -1, width: 80}).content, "")
	if strings.Contains(full, "unchanged") || !strings.Contains(full, "   31    31   l30") {
		t.Fatalf("full context should render every line:\n%s", full)
	}
}

func TestRenderDiffWithThreads(t *testing.T) {
	var common []string
	for i := 1; i <= 20; i++ {
		common = append(common, "l"+strconv.Itoa(i))
	}
	d := &gerrit.DiffInfo{
		ChangeType: "MODIFIED",
		Content: []gerrit.DiffContent{
			{AB: common},
			{A: []string{"old"}, B: []string{"new"}},
		},
	}
	threads := []gerrit.Thread{
		{Comments: []gerrit.CommentInfo{{ID: "f", Message: "file level", Author: &gerrit.AccountInfo{Name: "Ann"}}}},
		{Comments: []gerrit.CommentInfo{{ID: "c", Line: 5, Message: "why l5?", Unresolved: true, Author: &gerrit.AccountInfo{Name: "Bob"}}}},
		{Comments: []gerrit.CommentInfo{{ID: "p", Line: 21, Side: gerrit.SideParent, Message: "keep old", Author: &gerrit.AccountInfo{Name: "Cy"}}}},
	}

	r := renderDiff("f", d, threads, diffOptions{context: 1, width: 80})
	lines := plainLines(r.content)
	find := func(substr string) int {
		t.Helper()
		for i, l := range lines {
			if strings.Contains(l, substr) {
				return i
			}
		}
		t.Fatalf("%q not found in:\n%s", substr, strings.Join(lines, "\n"))
		return -1
	}

	l5 := find("    5     5   l5")
	const threadHeight = 4
	switch {
	case find("why l5?") <= l5:
		t.Fatal("thread should be rendered below its line")
	case find("    4     4   l4") != l5-1 || find("    6     6   l6") != l5+1+threadHeight:
		t.Fatalf("commented line should keep its context visible:\n%s", strings.Join(lines, "\n"))
	case find("keep old") <= find("- old") || find("keep old") >= find("+ new"):
		t.Fatal("parent-side thread should be rendered below the deleted line")
	case find("file level") >= l5:
		t.Fatal("file-level thread should be rendered before the diff")
	}

	want := []int{find("file comment"), find("╭─ ● unresolved"), find("- old") + 1}
	if len(r.threadOffsets) != len(want) {
		t.Fatalf("got offsets %v, want %v", r.threadOffsets, want)
	}
	for i := range want {
		if r.threadOffsets[i] != want[i] {
			t.Fatalf("got offsets %v, want %v", r.threadOffsets, want)
		}
	}
}

func TestTablesWithRowsBeforeSize(t *testing.T) {
	change := &gerrit.ChangeInfo{
		Number:          42,
		CurrentRevision: "abc",
		Revisions: map[string]gerrit.RevisionInfo{"abc": {Number: 1, Files: map[string]gerrit.FileInfo{
			"/COMMIT_MSG": {Status: "A"},
			"main.go":     {LinesInserted: 3},
		}}},
	}
	files := newFilesScreen(nil, change)
	_ = files.View()
	files.SetSize(80, 10)
	if v := files.View(); !strings.Contains(v, "main.go") || !strings.Contains(v, "Commit message") {
		t.Fatalf("files screen: got %q", v)
	}

	d := newDashboard(nil, "is:open")
	d.Update(changesLoadedMsg{changes: []gerrit.ChangeInfo{*change}})
	if v := d.View(); !strings.Contains(v, "42") {
		t.Fatalf("dashboard: got %q", v)
	}
}

func TestReviewScreen(t *testing.T) {
	change := &gerrit.ChangeInfo{
		Number:          42,
		CurrentRevision: "abc",
		Labels: map[string]gerrit.LabelInfo{
			"Code-Review": {All: []gerrit.ApprovalInfo{{AccountInfo: gerrit.AccountInfo{AccountID: 7}, Value: 1}}},
			"Verified":    {},
		},
		PermittedLabels: map[string][]string{
			"Code-Review": {"+2", "-2", " 0", "-1", "+1"},
			"Verified":    {"-1", " 0", "+1"},
		},
	}
	s := newReviewScreen(nil, change)
	s.setSelf(&gerrit.AccountInfo{AccountID: 7})

	cr, v := s.labels[0], s.labels[1]
	if cr.name != "Code-Review" || !slices.Equal(cr.values, []int{-2, -1, 0, 1, 2}) {
		t.Fatalf("unexpected label: %+v", cr)
	}
	if cr.current != 1 || cr.selected != 1 || v.current != 0 {
		t.Fatalf("current votes not prefilled: CR %d/%d, V %d", cr.current, cr.selected, v.current)
	}

	if _, err := s.reviewInput(); !errors.Is(err, errEmptyReview) {
		t.Fatalf("unchanged review: got %v, want errEmptyReview", err)
	}

	cr.move(1)
	cr.move(1)
	if cr.selected != 2 {
		t.Fatalf("move should stop at +2, got %d", cr.selected)
	}
	in, err := s.reviewInput()
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Labels) != 1 || in.Labels["Code-Review"] != 2 {
		t.Fatalf("only changed votes should be sent, got %+v", in.Labels)
	}
}

type stubScreen struct{ name string }

func (s *stubScreen) Init() tea.Cmd                    { return nil }
func (s *stubScreen) Update(tea.Msg) (screen, tea.Cmd) { return s, nil }
func (s *stubScreen) View() string                     { return s.name }
func (s *stubScreen) Status() string                   { return "" }
func (s *stubScreen) Title() string                    { return s.name }
func (s *stubScreen) SetSize(int, int)                 {}

func TestAppNavigation(t *testing.T) {
	var app tea.Model = App{stack: []screen{&stubScreen{name: "root"}}, width: 80, height: 10}
	body := func() string { return plainLines(app.View())[1] }
	title := func() string { return plainLines(app.View())[0] }

	app, _ = app.Update(pushMsg{screen: &stubScreen{name: "child"}})
	if body() != "child" || title() != " gerardo  root › child" {
		t.Fatalf("after push: got title %q, body %q", title(), body())
	}

	app, cmd := app.Update(popMsg{})
	if body() != "root" || title() != " gerardo  root" {
		t.Fatalf("after pop: got title %q, body %q", title(), body())
	}
	if _, ok := cmd().(focusMsg); !ok {
		t.Fatal("pop should focus the uncovered screen")
	}

	_, cmd = app.Update(popMsg{})
	if cmd == nil {
		t.Fatal("popping the last screen should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("popping the last screen should quit")
	}
}

func TestRenderChangeCollapsesOlderPatchSets(t *testing.T) {
	alice, bob := &gerrit.AccountInfo{Name: "Alice"}, &gerrit.AccountInfo{Name: "Bob"}
	c := &gerrit.ChangeInfo{Number: 1, Subject: "s", Messages: []gerrit.ChangeMessageInfo{
		{Author: alice, RevisionNumber: 1, Message: "Uploaded patch set 1."},
		{Author: bob, RevisionNumber: 1, Message: "Patch Set 1: Code-Review-1\n\nold remark"},
		{Author: alice, RevisionNumber: 2, Message: "Uploaded patch set 2."},
		{Author: bob, RevisionNumber: 1, Message: "late reply on the first patch set"},
		{Author: bob, RevisionNumber: 2, Message: "Patch Set 2: Code-Review+2"},
	}}

	got := strings.Join(plainLines(renderChange(c, 80, changeView{})), "\n")
	switch {
	case !strings.Contains(got, "▸ Patch Set 1 · 3 messages · Alice, Bob"):
		t.Fatal("older patch set should be summarized")
	case strings.Contains(got, "old remark") || strings.Contains(got, "late reply"):
		t.Fatal("older patch set messages should be hidden")
	case !strings.Contains(got, "▾ Patch Set 2 · 2 messages") || !strings.Contains(got, "Code-Review+2"):
		t.Fatal("latest patch set should be expanded")
	}

	all := strings.Join(plainLines(renderChange(c, 80, changeView{allPatchSets: true})), "\n")
	if !strings.Contains(all, "▾ Patch Set 1 · 3 messages") || !strings.Contains(all, "late reply") {
		t.Fatalf("every patch set should be expanded:\n%s", all)
	}
	if strings.Index(all, "late reply") > strings.Index(all, "Patch Set 2 ·") {
		t.Fatal("messages should be grouped under their patch set")
	}
}

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		msg, votes, body string
	}{
		{"Patch Set 2: Code-Review+2 Verified-1", "Code-Review+2 Verified-1", ""},
		{"Patch Set 2: Code-Review+1\n\nlooks good", "Code-Review+1", "looks good"},
		{"Patch Set 2: -Code-Review\n\n(1 comment)", "-Code-Review", "(1 comment)"},
		{"Patch Set 2:\n\n(3 comments)", "", "(3 comments)"},
		{"Patch Set 2: Commit message was updated.", "", "Commit message was updated."},
		{"Patch Set 2: Restored", "", "Restored"},
		{"Uploaded patch set 2.", "", "Uploaded patch set 2."},
		{"Patch Set 1: Code-Review+2", "", "Patch Set 1: Code-Review+2"},
	}
	for _, tt := range tests {
		votes, body := splitMessage(gerrit.ChangeMessageInfo{RevisionNumber: 2, Message: tt.msg})
		if strings.Join(votes, " ") != tt.votes || body != tt.body {
			t.Errorf("%q: got votes %q body %q", tt.msg, votes, body)
		}
	}
}

func TestRenderChangeShowsMessageComments(t *testing.T) {
	c := &gerrit.ChangeInfo{Number: 1, Subject: "s", Messages: []gerrit.ChangeMessageInfo{
		{ID: "m1", Author: &gerrit.AccountInfo{Name: "Me"}, RevisionNumber: 2, Message: "Patch Set 2:\n\n(2 comments)"},
	}}
	comments := map[string][]gerrit.CommentInfo{"m1": {
		{Path: gerrit.PatchSetLevel, Message: "overall fine"},
		{Path: "main.go", Line: 42, Message: "rename this", Unresolved: true},
		{Path: "main.go", Line: 50, InReplyTo: "p", Message: "done"},
	}}
	byID := map[string]gerrit.CommentInfo{"p": {Author: &gerrit.AccountInfo{Name: "Bob"}, Message: "why is this here?\nmore"}}

	got := strings.Join(plainLines(renderChange(c, 80, changeView{comments: comments, commentsByID: byID})), "\n")
	for _, want := range []string{"Patch set\n", "│ overall fine", "main.go:42 ● unresolved", "│ rename this", "│ ↳ Bob: why is this here?\n", "│ done"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "(2 comments)") {
		t.Errorf("the comment count is redundant once the comments are shown:\n%s", got)
	}
	if bare := strings.Join(plainLines(renderChange(c, 80, changeView{})), "\n"); !strings.Contains(bare, "(2 comments)") {
		t.Errorf("without the comments the count should stay:\n%s", bare)
	}
}

func TestRenderChangeHeader(t *testing.T) {
	alice := gerrit.AccountInfo{Name: "Alice"}
	c := &gerrit.ChangeInfo{Number: 7, Subject: "s", Status: "MERGED", Project: "p", Branch: "main",
		Owner:        gerrit.AccountInfo{Name: "Me"},
		AttentionSet: map[string]gerrit.AttentionSetInfo{"1": {Account: alice}},
		Labels: map[string]gerrit.LabelInfo{"Code-Review": {Approved: &alice,
			All: []gerrit.ApprovalInfo{{AccountInfo: alice, Value: 2}}}}}
	got := plainLines(renderChange(c, 80, changeView{}))
	want := []string{" MERGED", "7: s", "p · main", "Me · +0 -0", "● waiting on Alice", "", "Labels", "  ✓  Code-Review  +2 Alice"}
	if strings.Join(got[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got[:len(want)], "\n"), strings.Join(want, "\n"))
	}
}

func TestPairLines(t *testing.T) {
	lines := flattenDiff(&gerrit.DiffInfo{Content: []gerrit.DiffContent{
		{AB: []string{"a"}},
		{A: []string{"d1", "d2"}, B: []string{"b1"}},
		{AB: []string{"c"}},
		{B: []string{"b2"}},
	}})
	got := pairLines(lines)
	want := []diffRow{{0, 0}, {1, 3}, {2, -1}, {4, 4}, {-1, 5}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRenderDiffSideBySide(t *testing.T) {
	d := &gerrit.DiffInfo{Content: []gerrit.DiffContent{
		{AB: []string{"same"}},
		{A: []string{"old"}, B: []string{"new"}},
	}}
	threads := []gerrit.Thread{{Comments: []gerrit.CommentInfo{{ID: "t", Line: 2, Side: gerrit.SideParent, Message: "on old"}}}}
	r := renderDiff("f", d, threads, diffOptions{context: 3, width: 41, sideBySide: true})
	got := plainLines(r.content)
	want := []string{
		"f",
		"",
		"    1   same        │    1   same",
		"    2 - old         │    2 + new",
	}
	if strings.Join(got[:4], "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if find := strings.Index(strings.Join(got, "\n"), "on old"); find < len(strings.Join(got[:4], "\n")) || r.targets[3].line.b != 2 || !r.targets[3].hasAlt || r.targets[3].alt.a != 2 {
		t.Fatalf("parent side thread should follow its row, targeting both lines:\n%s", strings.Join(got, "\n"))
	}
}

func TestDiffToggleSideBySideKeepsCursor(t *testing.T) {
	s := newTestDiffScreen(t, nil)
	press(s, "2", "6", "G")
	s.handleKey("s")
	if tg := s.target(); !s.sideBySide || tg.line.b != 26 {
		t.Fatalf("toggling should keep the cursor on its line, got %+v", tg)
	}
	s.handleKey("s")
	if tg := s.target(); s.sideBySide || tg.line.b != 26 {
		t.Fatalf("toggling back should keep the cursor, got %+v", tg)
	}
}
