package netrc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLookup(t *testing.T) {
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
			login, password, err := Lookup(path, tt.host)
			if !errors.Is(err, tt.err) || login != tt.login || password != tt.password {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, %v)", login, password, err, tt.login, tt.password, tt.err)
			}
		})
	}
}

func TestLookupNoMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(path, []byte("machine github.com login gh password ghpw"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Lookup(path, "review.example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
