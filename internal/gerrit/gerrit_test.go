package gerrit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetrcCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netrc")
	content := "machine github.com login gh password ghpw\n" +
		"machine review.example.com\n  login alice\n  password s3cret\n" +
		"default login anon password anonpw\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		host, login, password string
		err                   error
	}{
		{host: "review.example.com", login: "alice", password: "s3cret"},
		{host: "github.com", login: "gh", password: "ghpw"},
		{host: "unknown.example.com", login: "anon", password: "anonpw"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			login, password, err := NetrcCredentials(path, tt.host)
			if !errors.Is(err, tt.err) || login != tt.login || password != tt.password {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, %v)", login, password, err, tt.login, tt.password, tt.err)
			}
		})
	}
}

func TestNetrcCredentialsNoMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(path, []byte("machine github.com login gh password ghpw"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NetrcCredentials(path, "review.example.com"); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("got %v, want ErrNoCredentials", err)
	}
}

func TestGetDiff(t *testing.T) {
	var gotPath, gotContext string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotContext = r.URL.EscapedPath(), r.URL.Query().Get("context")
		_, _ = w.Write([]byte(")]}'\n" + `{"change_type": "MODIFIED", "content": [{"ab": ["a"]}, {"a": ["b"], "b": ["c"]}, {"skip": 4}]}`))
	}))
	defer srv.Close()

	diff, err := NewClient(srv.URL, "u", "p").GetDiff(context.Background(), "42", "abc", "pkg/sub/main.go")
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

	changes, more, err := NewClient(srv.URL, "u", "p").QueryChanges(context.Background(), "status:merged", 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("q") != "status:merged" || got.Get("S") != "50" || got.Get("n") != "2" {
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
