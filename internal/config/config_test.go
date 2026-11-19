package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `# gerrit-tui
url = "https://review.example.com"  # the server
query = "status:open #tag"
side_by_side = true
page_size = 25
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.URL, want.Query, want.SideBySide, want.PageSize = "https://review.example.com", "status:open #tag", true, 25
	if c != want {
		t.Fatalf("got %+v, want %+v", c, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || c != Default() {
		t.Fatalf("a missing file should give the defaults, got %+v, %v", c, err)
	}
}

func TestLoadErrors(t *testing.T) {
	for _, content := range []string{
		"url = https://unquoted",
		"colour = \"blue\"",
		"side_by_side = maybe",
		"page_size = 0",
		"just words",
	} {
		_, err := parseString(content)
		if err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("%q: want an error on line 1, got %v", content, err)
		}
	}
}

func parseString(s string) (Config, error) {
	c := Default()
	return c, parse(strings.NewReader(s), &c)
}

func TestPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, _ := Path(); p != "/xdg/gerrit-tui/config.toml" {
		t.Fatalf("got %q", p)
	}
}
