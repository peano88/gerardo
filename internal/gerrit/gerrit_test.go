package gerrit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	case change.Labels["Code-Review"].Summary() != "+2":
		t.Fatalf("unexpected label summary: %q", change.Labels["Code-Review"].Summary())
	}

	if _, err := NewClient(srv.URL, "alice", "wrong").GetChange(context.Background(), "42"); err == nil {
		t.Fatal("expected an error with wrong credentials")
	}
}
