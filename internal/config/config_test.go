package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `# gerardo
url = "https://review.example.com"  # the server
query = "status:open #tag"
side_by_side = true
page_size = 25
hidden_paths = ["accounts/*", "legacy/*"]  # not mine
visible_paths = [
  "accounts/pkg",   # still mine
  # "accounts/old",
  "accounts/billing",
]
hashtags = ["auto-submit"]
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
	want.HiddenPaths = []string{"accounts/*", "legacy/*"}
	want.VisiblePaths = []string{"accounts/pkg", "accounts/billing"}
	want.Hashtags = []string{"auto-submit"}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || !reflect.DeepEqual(c, Default()) {
		t.Fatalf("a missing file should give the defaults, got %+v, %v", c, err)
	}
}

func TestLoadErrors(t *testing.T) {
	for content, want := range map[string]string{
		"url = https://unquoted":      `line 1 (last key "url")`,
		"colour = \"blue\"":           "unknown setting colour",
		"side_by_side = maybe":        `line 1 (last key "side_by_side")`,
		"page_size = 0":               "page_size: must be between 1 and 500",
		"just words":                  "line 1",
		`hidden_paths = "accounts/*"`: "incompatible types",
		`hidden_paths = [accounts/*]`: `line 1 (last key "hidden_paths")`,
		"hidden_paths = [\n\"a\",":    "line 2",
	} {
		_, err := parseString(content)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want an error with %q, got %v", content, want, err)
		}
	}
}

func parseString(s string) (Config, error) {
	c := Default()
	return c, parse(s, &c)
}

func TestPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, _ := Path(); p != "/xdg/gerardo/config.toml" {
		t.Fatalf("got %q", p)
	}
}
