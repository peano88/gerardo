package gerrit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func at(minute int) Timestamp {
	return Timestamp{Time: time.Date(2026, 11, 17, 10, minute, 0, 0, time.UTC)}
}

func ids(t Thread) []string {
	var out []string
	for _, c := range t.Comments {
		out = append(out, c.ID)
	}
	return out
}

func TestBuildThreads(t *testing.T) {
	comments := []CommentInfo{
		{ID: "reply2", InReplyTo: "reply1", Line: 10, Updated: at(3), Unresolved: false},
		{ID: "root", Line: 10, Updated: at(1), Unresolved: true},
		{ID: "other", Line: 2, Updated: at(5), Unresolved: true},
		{ID: "reply1", InReplyTo: "root", Line: 10, Updated: at(2), Unresolved: true},
		{ID: "orphan", InReplyTo: "missing", Line: 2, Updated: at(4)},
	}

	threads := BuildThreads(comments)
	if len(threads) != 3 {
		t.Fatalf("got %d threads, want 3", len(threads))
	}
	got := [][]string{ids(threads[0]), ids(threads[1]), ids(threads[2])}
	want := [][]string{{"orphan"}, {"other"}, {"root", "reply1", "reply2"}}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("thread %d: got %v, want %v", i, got[i], want[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("thread %d: got %v, want %v", i, got[i], want[i])
			}
		}
	}
	if threads[2].Unresolved() {
		t.Fatal("thread whose last reply resolved it should be resolved")
	}
	if !threads[1].Unresolved() {
		t.Fatal("thread should be unresolved")
	}
}

func TestThreadsForRevision(t *testing.T) {
	comments := map[string][]CommentInfo{
		"main.go": {
			{ID: "ps2", PatchSet: 2, Line: 5, Updated: at(1)},
			{ID: "ps1", PatchSet: 1, Line: 3, Updated: at(1), Unresolved: true},
		},
	}
	ported := map[string][]CommentInfo{
		"main.go": {{ID: "ps1", PatchSet: 1, Line: 4, Updated: at(1), Unresolved: true}},
	}

	threads := ThreadsForRevision("main.go", 2, comments, ported, nil)
	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}
	if threads[0].Root().ID != "ps1" || threads[0].Root().Line != 4 {
		t.Fatalf("earlier patch set thread should use its ported line, got %+v", threads[0].Root())
	}
	if threads[1].Root().ID != "ps2" {
		t.Fatalf("got %+v, want ps2", threads[1].Root())
	}
}

func TestThreadsForRevisionWithDrafts(t *testing.T) {
	comments := map[string][]CommentInfo{
		"main.go": {{ID: "old", PatchSet: 1, Line: 3, Updated: at(1), Unresolved: true}},
	}
	ported := map[string][]CommentInfo{
		"main.go": {{ID: "old", PatchSet: 1, Line: 8, Updated: at(1), Unresolved: true}},
	}
	drafts := map[string][]CommentInfo{
		"main.go": {
			{ID: "reply", InReplyTo: "old", PatchSet: 2, Line: 8, Updated: at(2), Draft: true},
			{ID: "new", PatchSet: 2, Line: 1, Updated: at(3), Draft: true, Unresolved: true},
			{ID: "stale", PatchSet: 1, Line: 1, Updated: at(3), Draft: true},
		},
	}

	threads := ThreadsForRevision("main.go", 2, comments, ported, drafts)
	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2 (drafts on older patch sets are hidden)", len(threads))
	}
	if threads[0].Root().ID != "new" || !threads[0].Comments[0].Draft {
		t.Fatalf("new draft should start its own thread, got %v", ids(threads[0]))
	}
	got := ids(threads[1])
	if len(got) != 2 || got[0] != "old" || got[1] != "reply" || threads[1].Root().Line != 8 {
		t.Fatalf("draft reply should join the ported thread, got %v on line %d", got, threads[1].Root().Line)
	}
	if threads[1].Unresolved() {
		t.Fatal("a resolving draft reply should make the thread show as resolved")
	}
}

func TestDraftEndpoints(t *testing.T) {
	type request struct{ method, path, body string }
	var requests []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, request{r.Method, r.URL.EscapedPath(), string(body)})
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(")]}'\n{}"))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(")]}'\n" + `{"a/b.go": [{"id": "d1", "line": 2}]}`))
		}
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "u", "p")
	ctx := context.Background()

	drafts, err := client.ListDrafts(ctx, "42")
	if err != nil {
		t.Fatal(err)
	}
	if d := drafts["a/b.go"][0]; !d.Draft || d.Path != "a/b.go" {
		t.Fatalf("unexpected draft: %+v", d)
	}
	in := CommentInput{Path: "a/b.go", Line: 2, Message: "nit", Unresolved: new(true)}
	if err := client.CreateDraft(ctx, "42", "abc", in); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateDraft(ctx, "42", "3", "d1", in); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteDraft(ctx, "42", "3", "d1"); err != nil {
		t.Fatal(err)
	}

	want := []request{
		{http.MethodGet, "/a/changes/42/drafts", ""},
		{http.MethodPut, "/a/changes/42/revisions/abc/drafts", `{"path":"a/b.go","line":2,"message":"nit","unresolved":true}`},
		{http.MethodPut, "/a/changes/42/revisions/3/drafts/d1", `{"path":"a/b.go","line":2,"message":"nit","unresolved":true}`},
		{http.MethodDelete, "/a/changes/42/revisions/3/drafts/d1", ""},
	}
	if len(requests) != len(want) {
		t.Fatalf("got %d requests, want %d", len(requests), len(want))
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("request %d: got %+v, want %+v", i, requests[i], want[i])
		}
	}
}

func TestListComments(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(")]}'\n" + `{"pkg/main.go": [{"id": "c1", "line": 3, "message": "nit", "unresolved": true}]}`))
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "u", "p")

	comments, err := client.ListComments(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if c := comments["pkg/main.go"][0]; c.Path != "pkg/main.go" || c.Line != 3 || !c.Unresolved {
		t.Fatalf("unexpected comment: %+v", c)
	}
	if _, err := client.PortedComments(context.Background(), "42", "abc"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/a/changes/42/comments", "/a/changes/42/revisions/abc/ported_comments"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("got paths %v, want %v", paths, want)
	}
}
