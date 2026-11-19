package ui

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

// SyntaxStyle is the chroma style used to highlight diffs.
var SyntaxStyle = "catppuccin-mocha"

// span styles the bytes [start, end) of a line.
type span struct {
	start, end int
	style      lipgloss.Style
}

// syntaxLines holds the highlighting of each line of both sides of a diff,
// indexed by line number minus one.
type syntaxLines struct {
	a, b [][]span
}

func (s *syntaxLines) spans(l diffLine) []span {
	if s == nil {
		return nil
	}
	pick := func(lines [][]span, n int) []span {
		if n <= 0 || n > len(lines) {
			return nil
		}
		return lines[n-1]
	}
	if l.kind == lineDeleted {
		return pick(s.a, l.a)
	}
	return pick(s.b, l.b)
}

// highlightDiff tokenizes the full parent and revision files of a diff. It
// returns nil when no lexer matches the file.
func highlightDiff(path string, d *gerrit.DiffInfo) *syntaxLines {
	lexer := lexers.Match(filepath.Base(path))
	if lexer == nil || d.Binary {
		return nil
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get(SyntaxStyle)

	var a, b []string
	for _, c := range d.Content {
		a = append(append(a, c.AB...), c.A...)
		b = append(append(b, c.AB...), c.B...)
	}
	return &syntaxLines{a: highlightLines(lexer, style, a), b: highlightLines(lexer, style, b)}
}

func highlightLines(lexer chroma.Lexer, style *chroma.Style, lines []string) [][]span {
	out := make([][]span, len(lines))
	if len(lines) == 0 {
		return out
	}
	it, err := lexer.Tokenise(nil, strings.Join(lines, "\n")+"\n")
	if err != nil {
		return out
	}

	line, offset := 0, 0
	for _, tok := range it.Tokens() {
		st := tokenStyle(style, tok.Type)
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				line++
				offset = 0
			}
			if line >= len(lines) {
				return out
			}
			if part != "" {
				out[line] = append(out[line], span{start: offset, end: offset + len(part), style: st})
				offset += len(part)
			}
		}
	}
	return out
}

func tokenStyle(style *chroma.Style, t chroma.TokenType) lipgloss.Style {
	entry := style.Get(t)
	st := lipgloss.NewStyle()
	if entry.Colour.IsSet() {
		st = st.Foreground(lipgloss.Color(entry.Colour.String()))
	}
	if entry.Bold == chroma.Yes {
		st = st.Bold(true)
	}
	if entry.Italic == chroma.Yes {
		st = st.Italic(true)
	}
	return st
}
