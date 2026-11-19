package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

func loadedChangeScreen(t *testing.T, client *gerrit.Client) *changeScreen {
	t.Helper()
	bob := &gerrit.AccountInfo{Name: "Bob"}
	s := newChangeScreen(client, 42)
	s.SetSize(100, 40)
	s.Update(changeLoadedMsg{number: 42,
		change: &gerrit.ChangeInfo{Number: 42, Subject: "s", CurrentRevision: "abc",
			Revisions: map[string]gerrit.RevisionInfo{"abc": {Number: 3, Files: map[string]gerrit.FileInfo{"main.go": {}}}},
			Messages: []gerrit.ChangeMessageInfo{
				{ID: "m1", Author: bob, RevisionNumber: 3, Message: "Patch Set 3:\n\n(2 comments)"},
			}},
		comments: map[string][]gerrit.CommentInfo{
			gerrit.PatchSetLevel: {{ID: "p1", Path: gerrit.PatchSetLevel, PatchSet: 3, Author: bob, Message: "overall", ChangeMessageID: "m1"}},
			"main.go":            {{ID: "c1", Path: "main.go", Line: 7, PatchSet: 2, Author: bob, Message: "rename", Unresolved: true, ChangeMessageID: "m1"}},
		},
	})
	return s
}

func TestChangeCommentCursor(t *testing.T) {
	s := loadedChangeScreen(t, nil)
	if !strings.Contains(s.Status(), "comments") {
		t.Fatalf("footer should tell how to select comments: %q", s.Status())
	}

	s.Update(keyRunes("]"))
	if s.selected != "p1" || !strings.Contains(ansiRe.ReplaceAllString(s.View(), ""), "▶ Patch set") {
		t.Fatalf("] should select the first comment, got %q", s.selected)
	}
	if !strings.Contains(s.Status(), "reply") {
		t.Fatalf("footer should offer replying: %q", s.Status())
	}
	s.Update(keyRunes("]"))
	s.Update(keyRunes("]"))
	if s.selected != "c1" {
		t.Fatalf("] should stop on the last comment, got %q", s.selected)
	}
	s.Update(keyRunes("["))
	if s.selected != "p1" {
		t.Fatalf("[ should go back, got %q", s.selected)
	}

	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.selected != "" {
		t.Fatal("esc should unselect before going back")
	}
	if _, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil || cmd() != pop() {
		t.Fatal("esc without a selection should go back")
	}
}

func TestChangeOpensThreadInDiff(t *testing.T) {
	s := loadedChangeScreen(t, nil)
	s.selected = "c1"
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	push, ok := cmd().(pushMsg)
	if !ok {
		t.Fatal("enter on a file comment should open the diff")
	}
	d := push.screen.(*diffScreen)
	if d.path() != "main.go" || d.focusThread != "c1" {
		t.Fatalf("diff should open on the thread, got %s focusing %q", d.path(), d.focusThread)
	}
}

func TestDiffFocusesThread(t *testing.T) {
	threads := []gerrit.Thread{{Comments: []gerrit.CommentInfo{{ID: "t1", Path: "main.go", Line: 40, Message: "here"}}}}
	change := &gerrit.ChangeInfo{Number: 42, CurrentRevision: "abc", Revisions: map[string]gerrit.RevisionInfo{"abc": {Number: 2}}}
	d := newDiffScreen(nil, change, []string{"main.go"}, 0)
	d.focusThread = "t1"
	d.SetSize(100, 20)
	var lines []string
	for range 50 {
		lines = append(lines, "x")
	}
	d.Update(diffLoadedMsg{path: "main.go", diff: &gerrit.DiffInfo{Content: []gerrit.DiffContent{{AB: lines}}}, threads: threads})
	if tg := d.target(); tg.thread != 0 {
		t.Fatalf("cursor should be on the focused thread, got %+v", tg)
	}
}

func TestChangeQuickReply(t *testing.T) {
	var method, path string
	var got gerrit.CommentInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(")]}'\n{}"))
	}))
	defer srv.Close()

	s := loadedChangeScreen(t, gerrit.NewClient(srv.URL, "u", "p"))
	s.selected = "c1"
	_, cmd := s.Update(keyRunes("D"))
	msg, ok := cmd().(draftSavedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("D should save a draft, got %+v", msg)
	}
	// The reply goes on the patch set of the comment it answers.
	if method != http.MethodPut || path != "/a/changes/42/revisions/2/drafts" {
		t.Fatalf("unexpected request %s %s", method, path)
	}
	if got.InReplyTo != "c1" || got.Line != 7 || got.Message != "Done" || got.Unresolved == nil || *got.Unresolved {
		t.Fatalf("unexpected draft: %+v", got)
	}
}

func TestChangeShowsDraftReplies(t *testing.T) {
	s := loadedChangeScreen(t, nil)
	s.setComments(map[string][]gerrit.CommentInfo{
		"main.go": {{ID: "c1", Path: "main.go", Line: 7, PatchSet: 2, Message: "rename", ChangeMessageID: "m1"}},
	}, map[string][]gerrit.CommentInfo{
		"main.go": {{ID: "d1", Path: "main.go", Line: 7, PatchSet: 2, InReplyTo: "c1", Message: "will do", Draft: true}},
	})
	s.render()
	if !strings.Contains(ansiRe.ReplaceAllString(s.View(), ""), "✎ your draft: will do") {
		t.Fatalf("drafts should show under the comment they answer:\n%s", s.View())
	}
	// Replying again edits the draft instead of adding another one.
	s.selected = "c1"
	if _, draft := lastPublished(s.threads["c1"]); draft == nil || draft.ID != "d1" {
		t.Fatal("the thread should end with the user's draft")
	}
}
