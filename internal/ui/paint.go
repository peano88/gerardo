package ui

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/charmbracelet/lipgloss"
)

var (
	addedBg       = lipgloss.Color("#233a2c")
	addedEditBg   = lipgloss.Color("#2f6140")
	deletedBg     = lipgloss.Color("#3d2529")
	deletedEditBg = lipgloss.Color("#743540")
	searchStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#f9e2af"))
)

type markKind int

const (
	markEdit markKind = iota + 1
	markSearch
)

// mark highlights the bytes [start, end) of a line on top of its syntax.
type mark struct {
	start, end int
	kind       markKind
}

func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "    ")
}

// paint renders text with its syntax spans and marks. Bytes outside any span
// use base; bg and editBg are the line and changed-characters backgrounds.
func paint(text string, spans []span, marks []mark, base lipgloss.Style, bg, editBg lipgloss.TerminalColor) string {
	cuts := []int{0, len(text)}
	for _, s := range spans {
		cuts = append(cuts, s.start, s.end)
	}
	for _, m := range marks {
		cuts = append(cuts, m.start, m.end)
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)

	var b strings.Builder
	si := 0
	for i := 0; i+1 < len(cuts); i++ {
		start, end := cuts[i], cuts[i+1]
		if start < 0 || end > len(text) || start >= end {
			continue
		}
		for si < len(spans) && spans[si].end <= start {
			si++
		}
		st := base
		if si < len(spans) && spans[si].start <= start {
			st = spans[si].style
		}

		var kind markKind
		for _, m := range marks {
			if m.start <= start && end <= m.end {
				kind = max(kind, m.kind)
			}
		}
		switch {
		case kind == markSearch:
			st = searchStyle
		case kind == markEdit && editBg != nil:
			st = st.Background(editBg)
		case bg != nil:
			st = st.Background(bg)
		}
		b.WriteString(st.Render(expandTabs(text[start:end])))
	}
	return b.String()
}

// searchRegexp compiles a literal search, case-insensitive unless the query
// contains an upper-case letter (vim's smartcase). An empty query returns nil.
func searchRegexp(query string) *regexp.Regexp {
	if query == "" {
		return nil
	}
	pattern := regexp.QuoteMeta(query)
	if !strings.ContainsFunc(query, unicode.IsUpper) {
		pattern = "(?i)" + pattern
	}
	return regexp.MustCompile(pattern)
}

func searchMarks(re *regexp.Regexp, text string) []mark {
	if re == nil {
		return nil
	}
	var marks []mark
	for _, m := range re.FindAllStringIndex(text, -1) {
		if m[0] < m[1] {
			marks = append(marks, mark{start: m[0], end: m[1], kind: markSearch})
		}
	}
	return marks
}

// utf16Offset returns the byte offset in s of its n-th UTF-16 code unit, the
// unit Gerrit uses for intraline edits.
func utf16Offset(s string, n int) int {
	units := 0
	for i, r := range s {
		if units >= n {
			return i
		}
		if l := utf16.RuneLen(r); l > 0 {
			units += l
		} else {
			units++
		}
	}
	return len(s)
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if l := utf16.RuneLen(r); l > 0 {
			n += l
		} else {
			n++
		}
	}
	return n
}

// intralineMarks splits Gerrit intraline edits, given as [skip, mark] pairs of
// UTF-16 units over the chunk lines joined by newlines, into per-line marks.
func intralineMarks(lines []string, edits [][2]int) [][]mark {
	out := make([][]mark, len(lines))
	if len(edits) == 0 {
		return out
	}
	starts := make([]int, len(lines))
	pos := 0
	for i, l := range lines {
		starts[i] = pos
		pos += utf16Len(l) + 1
	}

	pos = 0
	for _, e := range edits {
		pos += e[0]
		start, end := pos, pos+e[1]
		pos = end
		for i, l := range lines {
			ls, le := starts[i], starts[i]+utf16Len(l)
			s, en := max(start, ls), min(end, le)
			if s < en {
				out[i] = append(out[i], mark{start: utf16Offset(l, s-ls), end: utf16Offset(l, en-ls), kind: markEdit})
			}
		}
	}
	return out
}
