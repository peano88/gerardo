package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerardo/internal/gerrit"
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
	d := newDiffScreen(nil, change, latestRange(change), []string{"main.go"}, 0)
	d.focusThread = "t1"
	d.SetSize(100, 20)
	var lines []string
	for range 50 {
		lines = append(lines, "x")
	}
	d.Update(diffLoadedMsg{rng: d.rng, path: "main.go", diff: &gerrit.DiffInfo{Content: []gerrit.DiffContent{{AB: lines}}}, threads: threads})
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

func TestChangeSubmitAsksFirst(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(")]}'\n{}"))
	}))
	defer srv.Close()

	s := loadedChangeScreen(t, gerrit.NewClient(srv.URL, "u", "p"))
	s.change.Status = "NEW"
	s.Update(keyRunes("S"))
	if s.dialog != nil || !strings.Contains(s.Status(), "not submittable") {
		t.Fatal("a change that is not submittable should not offer to submit")
	}

	s.change.Submittable = true
	s.Update(keyRunes("S"))
	if s.dialog == nil || !strings.Contains(ansiRe.ReplaceAllString(s.View(), ""), "Submit change 42?") {
		t.Fatalf("S should ask for confirmation:\n%s", s.View())
	}
	for _, k := range []tea.KeyMsg{keyRunes("j"), keyRunes("S"), {Type: tea.KeyEnter}} {
		if _, cmd := s.Update(k); cmd != nil || s.dialog == nil {
			t.Fatalf("%q should neither submit nor close the dialog", k.String())
		}
	}
	s.Update(keyRunes("n"))
	if s.dialog != nil || len(calls) != 0 {
		t.Fatal("n should cancel without submitting")
	}

	s.Update(keyRunes("S"))
	_, cmd := s.Update(keyRunes("y"))
	if s.dialog != nil || cmd == nil {
		t.Fatal("y should close the dialog and submit")
	}
	if msg := cmd().(changeActionMsg); msg.err != nil || msg.notice != "submitted" {
		t.Fatalf("unexpected result: %+v", msg)
	}
	if len(calls) != 1 || calls[0] != "POST /a/changes/42/submit" {
		t.Fatalf("unexpected calls: %v", calls)
	}
}

func TestChangeRebaseChoices(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, r.URL.Path+" "+string(b))
		_, _ = w.Write([]byte(")]}'\n{}"))
	}))
	defer srv.Close()

	s := loadedChangeScreen(t, gerrit.NewClient(srv.URL, "u", "p"))
	s.change.Status = "MERGED"
	if _, cmd := s.Update(keyRunes("R")); cmd != nil || s.dialog != nil {
		t.Fatal("a merged change should not be rebased")
	}
	s.change.Status, s.change.Branch = "NEW", "main"
	s.change.Revisions["abc"] = gerrit.RevisionInfo{Number: 3, Commit: &gerrit.CommitInfo{Parents: []gerrit.CommitInfo{{Commit: "p1"}}}}
	if _, cmd := s.Update(keyRunes("R")); cmd == nil {
		t.Fatal("R should load the relation chain")
	}
	s.Update(relatedLoadedMsg{related: []gerrit.RelatedChangeInfo{
		{Number: 41, Status: "NEW", RevisionNumber: 1, CurrentRevisionNumber: 2, Commit: gerrit.CommitInfo{Commit: "p1"}},
	}})
	view := ansiRe.ReplaceAllString(s.View(), "")
	for _, want := range []string{"Rebase change 42?", "▶ 1 Onto parent change 41", "onto its latest patch set 2; now based on patch set 1", "2 Onto the tip of main", "drops the dependency on change 41", "3 Onto another change or commit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in:\n%s", want, view)
		}
	}

	// Picking the tip of the branch needs y; enter does nothing.
	s.Update(keyRunes("2"))
	if _, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || s.dialog == nil {
		t.Fatal("enter should not confirm a rebase")
	}
	_, cmd := s.Update(keyRunes("y"))
	if msg := cmd().(changeActionMsg); msg.err != nil || msg.notice != "rebased onto main" {
		t.Fatalf("unexpected result: %+v", msg)
	}

	// Another change is typed, y included, and confirmed with enter.
	s.Update(keyRunes("R"))
	s.Update(relatedLoadedMsg{})
	s.Update(keyRunes("2"))
	typeText(s, "y1234")
	if _, cmd = s.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatal("enter should rebase onto the typed base")
	}
	cmd()
	want := []string{`/a/changes/42/rebase {"base":""}`, `/a/changes/42/rebase {"base":"y1234"}`}
	if strings.Join(bodies, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got requests:\n%s", strings.Join(bodies, "\n"))
	}
}

func TestOverlayCentersBox(t *testing.T) {
	bg := strings.Repeat("..........\n", 5) + ".........."
	got := strings.Split(overlay(bg, "ab\ncd", 10, 6), "\n")
	if ansiRe.ReplaceAllString(got[2], "") != "....ab...." || ansiRe.ReplaceAllString(got[3], "") != "....cd...." {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
}
