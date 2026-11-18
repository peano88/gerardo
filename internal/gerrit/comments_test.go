package gerrit

import (
	"context"
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

	threads := ThreadsForRevision("main.go", 2, comments, ported)
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
