package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

const gutterWidth = 12

type diffLineKind int

const (
	lineCommon diffLineKind = iota
	lineDeleted
	lineAdded
	lineSkipped
)

// diffLine is one line of a unified diff. a and b are the line numbers on the
// parent and revision sides, 0 when the line does not exist on that side.
type diffLine struct {
	kind    diffLineKind
	a, b    int
	text    string
	skipped int
}

func flattenDiff(d *gerrit.DiffInfo) []diffLine {
	var lines []diffLine
	a, b := 1, 1
	for _, c := range d.Content {
		for _, t := range c.AB {
			lines = append(lines, diffLine{kind: lineCommon, a: a, b: b, text: t})
			a++
			b++
		}
		for _, t := range c.A {
			lines = append(lines, diffLine{kind: lineDeleted, a: a, text: t})
			a++
		}
		for _, t := range c.B {
			lines = append(lines, diffLine{kind: lineAdded, b: b, text: t})
			b++
		}
		if c.Skip > 0 {
			lines = append(lines, diffLine{kind: lineSkipped, skipped: c.Skip})
			a += c.Skip
			b += c.Skip
		}
	}
	return lines
}

type threadKey struct {
	parent bool
	line   int
}

// threadKeys returns the keys under which threads attached to l are indexed.
func (l diffLine) threadKeys() []threadKey {
	switch l.kind {
	case lineCommon:
		return []threadKey{{parent: true, line: l.a}, {line: l.b}}
	case lineDeleted:
		return []threadKey{{parent: true, line: l.a}}
	case lineAdded:
		return []threadKey{{line: l.b}}
	default:
		return nil
	}
}

// visibleLines marks the lines within context of a change or a comment
// thread; a negative context shows every line.
func visibleLines(lines []diffLine, context int, hasThreads func(diffLine) bool) []bool {
	visible := make([]bool, len(lines))
	if context < 0 {
		for i, l := range lines {
			visible[i] = l.kind != lineSkipped
		}
		return visible
	}

	important := func(l diffLine) bool {
		return l.kind == lineDeleted || l.kind == lineAdded || hasThreads(l)
	}
	far := len(lines) + context + 1
	dist := make([]int, len(lines))
	last := -far
	for i, l := range lines {
		if important(l) {
			last = i
		}
		dist[i] = i - last
	}
	last = far + len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if important(lines[i]) {
			last = i
		}
		visible[i] = lines[i].kind != lineSkipped && min(dist[i], last-i) <= context
	}
	return visible
}

// renderedDiff is a rendered diff and the content line of each thread, in order.
type renderedDiff struct {
	content       string
	threadOffsets []int
}

type diffWriter struct {
	b     strings.Builder
	lines int
}

func (w *diffWriter) line(s string) {
	w.b.WriteString(s)
	w.b.WriteByte('\n')
	w.lines++
}

// renderDiff renders a unified diff with its comment threads, keeping context
// unchanged lines around each change and thread; a negative context renders
// the whole file.
func renderDiff(path string, d *gerrit.DiffInfo, context int, threads []gerrit.Thread, width int) renderedDiff {
	var w diffWriter
	var offsets []int
	writeThread := func(t gerrit.Thread) {
		offsets = append(offsets, w.lines)
		for _, l := range renderThread(t, width) {
			w.line(l)
		}
	}

	header := path
	if d.MetaA != nil && d.MetaB != nil && d.MetaA.Name != d.MetaB.Name {
		header = d.MetaA.Name + " → " + d.MetaB.Name
	}
	w.line(titleStyle.Render(header) + " " + dimStyle.Render(strings.ToLower(d.ChangeType)))
	w.line("")

	byLine := map[threadKey][]gerrit.Thread{}
	var fileLevel []gerrit.Thread
	for _, t := range threads {
		root := t.Root()
		if root.Line == 0 {
			fileLevel = append(fileLevel, t)
			continue
		}
		key := threadKey{parent: root.OnParent(), line: root.Line}
		byLine[key] = append(byLine[key], t)
	}
	for _, t := range fileLevel {
		writeThread(t)
	}

	if d.Binary {
		w.line(dimStyle.Render("binary file"))
		return renderedDiff{content: w.b.String(), threadOffsets: offsets}
	}

	lines := flattenDiff(d)
	hasThreads := func(l diffLine) bool {
		for _, k := range l.threadKeys() {
			if len(byLine[k]) > 0 {
				return true
			}
		}
		return false
	}
	visible := visibleLines(lines, context, hasThreads)

	hidden := 0
	flush := func() {
		if hidden > 0 {
			w.line(dimStyle.Render(fmt.Sprintf("%*s⋯ %d unchanged lines", gutterWidth, "", hidden)))
			hidden = 0
		}
	}
	for i, l := range lines {
		if l.kind == lineSkipped {
			hidden += l.skipped
			continue
		}
		if !visible[i] {
			hidden++
			continue
		}
		flush()
		w.line(renderDiffLine(l))
		for _, k := range l.threadKeys() {
			for _, t := range byLine[k] {
				writeThread(t)
			}
			delete(byLine, k)
		}
	}
	flush()

	// Threads whose line is outside the diff, e.g. stale ports.
	for _, ts := range byLine {
		for _, t := range ts {
			writeThread(t)
		}
	}
	return renderedDiff{content: w.b.String(), threadOffsets: offsets}
}

func renderDiffLine(l diffLine) string {
	num := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	gutter := dimStyle.Render(fmt.Sprintf("%5s %5s ", num(l.a), num(l.b)))
	text := strings.ReplaceAll(l.text, "\t", "    ")
	switch l.kind {
	case lineDeleted:
		return gutter + delStyle.Render("- "+text)
	case lineAdded:
		return gutter + addStyle.Render("+ "+text)
	default:
		return gutter + "  " + text
	}
}

func renderThread(t gerrit.Thread, width int) []string {
	barStyle, state := dimStyle, "resolved"
	if t.Unresolved() {
		barStyle, state = errStyle, "unresolved"
	}
	indent := strings.Repeat(" ", gutterWidth)
	bar := indent + barStyle.Render("│") + " "
	wrap := lipgloss.NewStyle().Width(max(width-gutterWidth-4, 20))

	root := t.Root()
	header := barStyle.Render(state)
	if root.Line == 0 {
		header += dimStyle.Render(" · file comment")
	}
	if root.PatchSet > 0 {
		header += dimStyle.Render(fmt.Sprintf(" · PS%d", root.PatchSet))
	}

	out := []string{indent + barStyle.Render("╭ ") + header}
	for _, c := range t.Comments {
		out = append(out, bar+titleStyle.Render(c.Author.DisplayName())+" "+
			dimStyle.Render(c.Updated.Local().Format("2006-01-02 15:04")))
		for l := range strings.SplitSeq(wrap.Render(strings.TrimSpace(c.Message)), "\n") {
			out = append(out, bar+"  "+l)
		}
	}
	return append(out, indent+barStyle.Render("╰"))
}
