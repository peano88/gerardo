package gerrit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGetDiff(t *testing.T) {
	var gotPath, gotContext, gotBase string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotContext, gotBase = r.URL.EscapedPath(), r.URL.Query().Get("context"), r.URL.Query().Get("base")
		_, _ = w.Write([]byte(")]}'\n" + `{"change_type": "MODIFIED", "content": [{"ab": ["a"]}, {"a": ["b"], "b": ["c"]}, {"skip": 4}]}`))
	}))
	defer srv.Close()

	diff, err := NewClient(srv.URL, "u", "p").GetDiff(context.Background(), "42", "abc", 0, "pkg/sub/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/a/changes/42/revisions/abc/files/pkg%2Fsub%2Fmain.go/diff"; gotPath != want {
		t.Fatalf("path: got %q, want %q", gotPath, want)
	}
	if gotContext != "ALL" {
		t.Fatalf("context: got %q, want ALL", gotContext)
	}
	if len(diff.Content) != 3 || diff.Content[1].B[0] != "c" || diff.Content[2].Skip != 4 {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	if gotBase != "" {
		t.Fatalf("a diff against the parent should not set base, got %q", gotBase)
	}

	if _, err := NewClient(srv.URL, "u", "p").GetDiff(context.Background(), "42", "3", 1, "main.go"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/a/changes/42/revisions/3/files/main.go/diff" || gotBase != "1" || gotContext != "ALL" {
		t.Fatalf("diff against patch set 1: got %q base %q context %q", gotPath, gotBase, gotContext)
	}
}

func TestListFiles(t *testing.T) {
	var gotPath, gotBase string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotBase = r.URL.EscapedPath(), r.URL.Query().Get("base")
		_, _ = w.Write([]byte(")]}'\n" + `{"/COMMIT_MSG": {"status": "A"}, "main.go": {"lines_inserted": 2}}`))
	}))
	defer srv.Close()

	files, err := NewClient(srv.URL, "u", "p").ListFiles(context.Background(), "42", "3", 2)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/a/changes/42/revisions/3/files/" || gotBase != "2" {
		t.Fatalf("got %q base %q", gotPath, gotBase)
	}
	if len(files) != 2 || files["main.go"].LinesInserted != 2 {
		t.Fatalf("unexpected files: %+v", files)
	}
}

func TestSetReview(t *testing.T) {
	var gotMethod, gotPath, gotContentType string
	var got ReviewInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotContentType = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(")]}'\n{}"))
	}))
	defer srv.Close()

	in := ReviewInput{Message: "LGTM", Labels: map[string]int{"Code-Review": 2}}
	if err := NewClient(srv.URL, "u", "p").SetReview(context.Background(), "42", "abc", in); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/a/changes/42/revisions/abc/review" || gotContentType != "application/json" {
		t.Fatalf("unexpected request: %s %s (%s)", gotMethod, gotPath, gotContentType)
	}
	if got.Message != "LGTM" || got.Labels["Code-Review"] != 2 {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestLabelHelpers(t *testing.T) {
	l := LabelInfo{
		All:    []ApprovalInfo{{AccountInfo: AccountInfo{AccountID: 7}, Value: -1}},
		Values: map[string]string{"-1": "I would prefer this is not merged", " 0": "No score", "+2": "Looks good to me, approved"},
	}
	if v := l.VoteOf(7); v != -1 {
		t.Fatalf("VoteOf(7) = %d", v)
	}
	if v := l.VoteOf(8); v != 0 {
		t.Fatalf("VoteOf(8) = %d", v)
	}
	if d := l.Description(2); d != "Looks good to me, approved" {
		t.Fatalf("Description(2) = %q", d)
	}
	if d := l.Description(0); d != "No score" {
		t.Fatalf("Description(0) = %q", d)
	}
}

func TestGetChange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "alice" || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/a/changes/42" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(")]}'\n" + `{
			"_number": 42,
			"subject": "feat: add things",
			"updated": "2026-11-17 10:20:30.000000000",
			"current_revision": "abc",
			"revisions": {"abc": {"_number": 3, "files": {"main.go": {"lines_inserted": 5}}}},
			"labels": {"Code-Review": {"approved": {"name": "Bob"}}}
		}`))
	}))
	defer srv.Close()

	change, err := NewClient(srv.URL, "alice", "pw").GetChange(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	rev, ok := change.Current()
	switch {
	case change.Number != 42 || change.Subject != "feat: add things":
		t.Fatalf("unexpected change: %+v", change)
	case change.Updated.Hour() != 10:
		t.Fatalf("unexpected updated: %v", change.Updated)
	case !ok || rev.Number != 3 || rev.Files["main.go"].LinesInserted != 5:
		t.Fatalf("unexpected revision: %+v", rev)
	case change.Labels["Code-Review"].Approved == nil:
		t.Fatalf("Code-Review should be approved: %+v", change.Labels["Code-Review"])
	}

	if _, err := NewClient(srv.URL, "alice", "wrong").GetChange(context.Background(), "42"); err == nil {
		t.Fatal("expected an error with wrong credentials")
	}
}

func TestQueryChangesPages(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(")]}'\n" + `[{"_number": 1}, {"_number": 2, "_more_changes": true}]`))
	}))
	defer srv.Close()

	changes, more, err := NewClient(srv.URL, "u", "p").QueryChanges(context.Background(), "status:merged", 50, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("q") != "status:merged" || got.Get("S") != "50" || got.Get("n") != "2" ||
		strings.Join(got["o"], ",") != "LABELS,DETAILED_ACCOUNTS,CURRENT_REVISION,CURRENT_FILES" {
		t.Fatalf("unexpected query: %v", got)
	}
	if len(changes) != 2 || !more {
		t.Fatalf("got %d changes, more %v", len(changes), more)
	}
}

func TestSuggestAccounts(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(")]}'\n" + `[{"_account_id": 8, "name": "Bob", "username": "bob"}]`))
	}))
	defer srv.Close()

	accounts, err := NewClient(srv.URL, "u", "p").SuggestAccounts(context.Background(), "bo", 6)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["suggest"]; !ok || got.Get("q") != "bo" || got.Get("n") != "6" {
		t.Fatalf("unexpected query: %v", got)
	}
	if len(accounts) != 1 || accounts[0].Username != "bob" {
		t.Fatalf("unexpected accounts: %+v", accounts)
	}
}

func TestSubmitAndRebase(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/rebase") {
			http.Error(w, "change is already up to date", http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(")]}'\n{}"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	if err := c.Submit(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	err := c.Rebase(context.Background(), "42", "")
	if err == nil || !strings.Contains(err.Error(), "already up to date") {
		t.Fatalf("rebase should report the server's reason, got %v", err)
	}
	if strings.Join(calls, ", ") != "POST /a/changes/42/submit, POST /a/changes/42/rebase" {
		t.Fatalf("unexpected calls: %v", calls)
	}
}

func TestRebaseSendsBase(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(")]}'\n{}"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	for base, want := range map[string]string{"": `{"base":""}`, "12340": `{"base":"12340"}`} {
		if err := c.Rebase(context.Background(), "42", base); err != nil {
			t.Fatal(err)
		}
		if body != want {
			t.Errorf("base %q: sent %s, want %s", base, body, want)
		}
	}
}

func TestParentChange(t *testing.T) {
	change := &ChangeInfo{Number: 42, CurrentRevision: "c42", Revisions: map[string]RevisionInfo{
		"c42": {Number: 2, Commit: &CommitInfo{Parents: []CommitInfo{{Commit: "c41"}}}},
	}}
	related := []RelatedChangeInfo{
		{Number: 43, Status: "NEW", Commit: CommitInfo{Commit: "c43"}},
		{Number: 42, Status: "NEW", Commit: CommitInfo{Commit: "c42"}},
		{Number: 41, Status: "NEW", RevisionNumber: 1, CurrentRevisionNumber: 3, Commit: CommitInfo{Commit: "c41"}},
	}
	if p, ok := ParentChange(change, related); !ok || p.Number != 41 || p.CurrentRevisionNumber != 3 {
		t.Fatalf("got %+v, %v", p, ok)
	}
	related[2].Status = "MERGED"
	if _, ok := ParentChange(change, related); ok {
		t.Fatal("a merged parent is part of the branch, not a parent change")
	}
}
