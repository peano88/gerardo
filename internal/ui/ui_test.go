package ui

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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
	got := strings.Split(ansi.ReplaceAllString(renderDiff("main.go", d, 3), ""), "\n")
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
	got := strings.Split(ansi.ReplaceAllString(renderDiff("f", d, 2), ""), "\n")
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

	full := ansi.ReplaceAllString(renderDiff("f", d, -1), "")
	if strings.Contains(full, "unchanged") || !strings.Contains(full, "   31    31   l30") {
		t.Fatalf("full context should render every line:\n%s", full)
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
	if v := newFilesScreen(nil, change).View(); !strings.Contains(v, "main.go") {
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
func (s *stubScreen) SetSize(int, int)                 {}

func TestAppNavigation(t *testing.T) {
	var app tea.Model = App{stack: []screen{&stubScreen{name: "root"}}}

	app, _ = app.Update(pushMsg{screen: &stubScreen{name: "child"}})
	if v := app.View(); !strings.HasPrefix(v, "child") {
		t.Fatalf("after push: got %q", v)
	}

	app, cmd := app.Update(popMsg{})
	if v := app.View(); !strings.HasPrefix(v, "root") || cmd != nil {
		t.Fatalf("after pop: got %q", v)
	}

	_, cmd = app.Update(popMsg{})
	if cmd == nil {
		t.Fatal("popping the last screen should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("popping the last screen should quit")
	}
}
