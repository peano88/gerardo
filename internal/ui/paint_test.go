package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

func TestIntralineMarks(t *testing.T) {
	lines := []string{"foo := bar", "café = 1"}
	// "foo := " skipped, "bar" marked, then "\ncaf" skipped and "é" marked:
	// é is one UTF-16 unit but two bytes.
	got := intralineMarks(lines, [][2]int{{7, 3}, {4, 1}})
	want := [][]mark{
		{{start: 7, end: 10, kind: markEdit}},
		{{start: 3, end: 5, kind: markEdit}},
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Fatalf("line %d: got %+v, want %+v", i, got[i], want[i])
		}
	}

	spanning := intralineMarks([]string{"ab", "cd"}, [][2]int{{1, 3}})
	if !slices.Equal(spanning[0], []mark{{start: 1, end: 2, kind: markEdit}}) ||
		!slices.Equal(spanning[1], []mark{{start: 0, end: 1, kind: markEdit}}) {
		t.Fatalf("edit across lines: got %+v", spanning)
	}
}

func TestUTF16Offset(t *testing.T) {
	s := "a😀b" // the emoji is two UTF-16 units and four bytes
	for units, want := range map[int]int{0: 0, 1: 1, 3: 5, 4: 6, 10: 6} {
		if got := utf16Offset(s, units); got != want {
			t.Fatalf("utf16Offset(%q, %d) = %d, want %d", s, units, got, want)
		}
	}
}

func TestPaintKeepsText(t *testing.T) {
	spans := []span{{start: 0, end: 4, style: lipgloss.NewStyle().Bold(true)}, {start: 5, end: 9, style: lipgloss.NewStyle()}}
	marks := []mark{{start: 2, end: 7, kind: markEdit}, {start: 6, end: 8, kind: markSearch}}
	got := ansiRe.ReplaceAllString(paint("func\tmain()", spans, marks, lipgloss.NewStyle(), addedBg, addedEditBg), "")
	if got != "func    main()" {
		t.Fatalf("got %q", got)
	}
}

func TestSearchRegexpSmartcase(t *testing.T) {
	if searchRegexp("") != nil {
		t.Fatal("empty query should not search")
	}
	if re := searchRegexp("foo.bar"); !re.MatchString("FOO.BAR") || re.MatchString("fooxbar") {
		t.Fatal("lower-case query should be literal and case-insensitive")
	}
	if re := searchRegexp("Foo"); re.MatchString("foo") || !re.MatchString("Foo") {
		t.Fatal("query with an upper-case letter should be case-sensitive")
	}
}

func TestHighlightDiff(t *testing.T) {
	d := &gerrit.DiffInfo{Content: []gerrit.DiffContent{
		{AB: []string{"package main", "/* a"}},
		{A: []string{"old */"}, B: []string{"new */", "func main() {}"}},
	}}
	syn := highlightDiff("cmd/main.go", d)
	if syn == nil || len(syn.a) != 3 || len(syn.b) != 4 {
		t.Fatalf("unexpected highlighting: %+v", syn)
	}
	for side, lines := range map[string][]string{
		"a": {"package main", "/* a", "old */"},
		"b": {"package main", "/* a", "new */", "func main() {}"},
	} {
		spans := syn.a
		if side == "b" {
			spans = syn.b
		}
		for i, l := range lines {
			covered := 0
			for _, s := range spans[i] {
				covered += s.end - s.start
			}
			if covered != len(l) {
				t.Fatalf("side %s line %d: spans cover %d bytes of %q", side, i+1, covered, l)
			}
		}
	}
	if highlightDiff("/COMMIT_MSG", d) != nil {
		t.Fatal("files without a lexer should not be highlighted")
	}
}

func TestDiffSearch(t *testing.T) {
	s := newTestDiffScreen(t, nil)
	typeSearch := func(q string) {
		s.handleKey("/")
		for _, r := range q {
			s.handleSearchKey(keyRunes(string(r)))
		}
	}

	s.handleKey("z")
	s.handleKey("R")
	typeSearch("l4")
	if !s.searching || s.target().line.b != 4 {
		t.Fatalf("incremental search should jump to l4, got %+v", s.target())
	}
	s.handleSearchKey(keyType("enter"))
	if s.searching || s.query != "l4" {
		t.Fatalf("enter should keep the query, got %q", s.query)
	}
	if got := len(s.rendered.matchRows); got != 11 {
		t.Fatalf("l4 and l40..l49 should match, got %d", got)
	}
	s.handleKey("n")
	if s.target().line.text != "l40" {
		t.Fatalf("n: got %q", s.target().line.text)
	}
	s.handleKey("N")
	s.handleKey("N")
	if s.target().line.text != "l49" || !strings.Contains(s.Status(), "TOP") {
		t.Fatalf("N should wrap to the last match, got %q, status %q", s.target().line.text, s.Status())
	}

	s.handleKey("z")
	s.handleKey("M")
	s.handleKey("esc")
	if s.query != "" {
		t.Fatal("esc should clear the search before leaving")
	}

	typeSearch("l1")
	s.handleSearchKey(keyType("enter"))
	if !strings.Contains(s.Status(), "only matches folded lines") {
		t.Fatalf("expected a hint about folded matches, status %q", s.Status())
	}

	origin := s.cursor
	typeSearch("added")
	s.handleSearchKey(keyType("esc"))
	if s.query != "l1" || s.cursor != origin {
		t.Fatalf("esc while typing should restore the previous search and cursor, got %q at %d", s.query, s.cursor)
	}
}
