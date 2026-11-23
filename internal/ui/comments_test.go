package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

func TestEditorTemplateRoundTrip(t *testing.T) {
	content := editorTemplate("existing draft", []string{"Replying on main.go, line 3", "", "Bob:", "  # not a heading"})
	if !strings.HasPrefix(content, "existing draft\n\n"+scissors+"\n") {
		t.Fatalf("unexpected template:\n%s", content)
	}
	if !strings.Contains(content, "\n#\n") || !strings.Contains(content, "# Bob:\n") {
		t.Fatalf("context should be commented out:\n%s", content)
	}

	edited := "# Title kept\n\nLooks good,\nbut see below.\n\n" + content[len("existing draft"):]
	if got, want := parseEditorText(edited), "# Title kept\n\nLooks good,\nbut see below."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := parseEditorText("\n\n" + scissors + "\nanything"); got != "" {
		t.Fatalf("empty comment: got %q", got)
	}
	if got := parseEditorText("  no scissors  \n"); got != "no scissors" {
		t.Fatalf("without scissors: got %q", got)
	}
}

func TestCommentInput(t *testing.T) {
	tests := []struct {
		name   string
		target rowTarget
		rng    patchRange
		side   string
		line   int
		rev    string
		ok     bool
	}{
		{name: "file header", target: rowTarget{fileLevel: true, thread: -1}, rev: "3", ok: true},
		{name: "added line", target: rowTarget{hasLine: true, line: diffLine{kind: lineAdded, b: 7}, thread: -1}, line: 7, rev: "3", ok: true},
		{name: "common line", target: rowTarget{hasLine: true, line: diffLine{kind: lineCommon, a: 4, b: 5}, thread: -1}, line: 5, rev: "3", ok: true},
		{name: "deleted line", target: rowTarget{hasLine: true, line: diffLine{kind: lineDeleted, a: 9}, thread: -1}, side: gerrit.SideParent, line: 9, rev: "3", ok: true},
		{name: "deleted line against a patch set", target: rowTarget{hasLine: true, line: diffLine{kind: lineDeleted, a: 9}, thread: -1}, rng: patchRange{base: 1}, line: 9, rev: "1", ok: true},
		{name: "added line against a patch set", target: rowTarget{hasLine: true, line: diffLine{kind: lineAdded, b: 7}, thread: -1}, rng: patchRange{base: 1}, line: 7, rev: "3", ok: true},
		{name: "fold marker", target: noTarget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.rng.target = 3
			in, rev, ok := commentInput("main.go", tt.target, tt.rng)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if in.Path != "main.go" || in.Side != tt.side || in.Line != tt.line || rev != tt.rev || in.Unresolved == nil || !*in.Unresolved {
				t.Fatalf("unexpected input: %+v", in)
			}
		})
	}
}

func TestReplyInput(t *testing.T) {
	thread := gerrit.Thread{Comments: []gerrit.CommentInfo{
		{ID: "root", Path: "main.go", Side: gerrit.SideParent, Line: 3, Unresolved: true},
		{ID: "last", Path: "main.go", Side: gerrit.SideParent, Line: 3, Unresolved: true},
	}}
	in := replyInput(thread, "Done", false)
	if in.InReplyTo != "last" || in.Side != gerrit.SideParent || in.Line != 3 || in.Message != "Done" || *in.Unresolved {
		t.Fatalf("unexpected reply: %+v", in)
	}
}

func newTestDiffScreen(t *testing.T, threads []gerrit.Thread) *diffScreen {
	t.Helper()
	var common []string
	for i := 1; i <= 50; i++ {
		common = append(common, "l"+strconv.Itoa(i))
	}
	change := &gerrit.ChangeInfo{
		Number:          42,
		CurrentRevision: "abc",
		Revisions:       map[string]gerrit.RevisionInfo{"abc": {Number: 2}},
	}
	s := newDiffScreen(nil, change, latestRange(change), []string{"main.go"}, 0)
	s.SetSize(100, 20)
	s.Update(diffLoadedMsg{
		rng:  s.rng,
		path: "main.go",
		diff: &gerrit.DiffInfo{Content: []gerrit.DiffContent{
			{AB: common[:25]},
			{B: []string{"added"}},
			{AB: common[25:]},
		}},
		threads: threads,
	})
	return s
}

func press(s *diffScreen, keys ...string) {
	for _, k := range keys {
		s.handleKey(k)
	}
}

func TestDiffMotions(t *testing.T) {
	s := newTestDiffScreen(t, nil)

	if s.cursor != 0 || !s.target().fileLevel {
		t.Fatalf("cursor should start on the file header, got row %d", s.cursor)
	}

	press(s, "z", "R", "4", "2", "G")
	if tg := s.target(); !tg.hasLine || tg.line.b != 42 {
		t.Fatalf("42G: got %+v", tg)
	}
	press(s, "3", "k")
	if tg := s.target(); tg.line.b != 39 {
		t.Fatalf("3k: got b=%d", tg.line.b)
	}
	press(s, "g", "g")
	if s.cursor != 0 {
		t.Fatalf("gg: got row %d", s.cursor)
	}
	press(s, "G")
	if s.cursor != len(s.rendered.lines)-1 {
		t.Fatalf("G: got row %d of %d", s.cursor, len(s.rendered.lines))
	}

	press(s, "2", "6", "G")
	if tg := s.target(); tg.line.kind != lineAdded {
		t.Fatalf("26G should land on the added line, got %+v", tg)
	}
	press(s, "z", "M")
	if !s.target().hasLine || s.target().line.b != 26 {
		t.Fatalf("folding should keep the cursor on its line, got %+v", s.target())
	}
	if len(s.rendered.lines) > 15 {
		t.Fatalf("zM should fold unchanged lines, got %d rows", len(s.rendered.lines))
	}

	if y := s.viewport.YOffset; s.cursor < y || s.cursor >= y+s.viewport.Height {
		t.Fatalf("cursor row %d is outside the viewport [%d, %d)", s.cursor, y, y+s.viewport.Height)
	}
}

func TestDiffThreadActions(t *testing.T) {
	threads := []gerrit.Thread{
		{Comments: []gerrit.CommentInfo{{ID: "t1", Path: "main.go", Line: 10, PatchSet: 2, Unresolved: true, Author: &gerrit.AccountInfo{Name: "Bob"}}}},
		{Comments: []gerrit.CommentInfo{
			{ID: "t2", Path: "main.go", Line: 40, PatchSet: 2, Unresolved: true, Author: &gerrit.AccountInfo{Name: "Bob"}},
			{ID: "d", InReplyTo: "t2", Path: "main.go", Line: 40, PatchSet: 2, Draft: true, Unresolved: true, Message: "will do"},
		}},
	}
	s := newTestDiffScreen(t, threads)

	press(s, "]")
	if th, ok := s.cursorThread(); !ok || th.Root().ID != "t1" {
		t.Fatalf("]: should jump to the first thread, got row %d", s.cursor)
	}
	press(s, "x")
	if s.pending != "" || !strings.Contains(s.Status(), "no draft") {
		t.Fatalf("x on a thread without draft should not arm the deletion, status %q", s.Status())
	}

	press(s, "]")
	if d, ok := s.cursorDraft(); !ok || d.ID != "d" {
		t.Fatalf("]: should jump to the thread with the draft, got row %d", s.cursor)
	}
	press(s, "[")
	if th, _ := s.cursorThread(); th.Root().ID != "t1" {
		t.Fatalf("[: should jump back to the first thread")
	}

	press(s, "]", "x")
	if s.pending != "x" {
		t.Fatal("x on a draft should ask for confirmation")
	}
	s.handleKey("j")
	if s.pending != "" {
		t.Fatal("any other key should cancel the deletion")
	}

	press(s, "g", "g")
	if _, cmd := s.handleKey("r"); cmd != nil || !strings.Contains(s.Status(), "move the cursor to a thread") {
		t.Fatalf("r outside a thread should explain itself, status %q", s.Status())
	}
	s.handleKey("u")
	if !strings.Contains(s.Status(), "only your drafts") {
		t.Fatalf("u outside a draft should explain itself, status %q", s.Status())
	}
}

func TestReviewPublishesDrafts(t *testing.T) {
	s := newReviewScreen(nil, &gerrit.ChangeInfo{Number: 42})
	s.Update(selfLoadedMsg{self: &gerrit.AccountInfo{AccountID: 7}, drafts: 2})

	in, err := s.reviewInput()
	if err != nil {
		t.Fatalf("drafts alone should be publishable: %v", err)
	}
	if in.Drafts != gerrit.PublishAllDrafts {
		t.Fatalf("drafts: got %q", in.Drafts)
	}
	if v := s.View(); !strings.Contains(v, "2 draft comments will be published") {
		t.Fatalf("review screen should mention the drafts:\n%s", v)
	}
}

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keyType(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	panic("unknown key " + name)
}
