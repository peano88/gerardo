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

// rowTarget is what a rendered row refers to: a diff line, the file header
// (for file-level comments) or a thread, given as an index in the threads.
type rowTarget struct {
	line      diffLine
	hasLine   bool
	fileLevel bool
	thread    int
}

var noTarget = rowTarget{thread: -1}

// renderedDiff is a rendered diff: its rows, what each row refers to, and the
// row of each thread header, in order.
type renderedDiff struct {
	content       string
	lines         []string
	targets       []rowTarget
	threadOffsets []int
}

func (r *renderedDiff) add(s string, t rowTarget) {
	r.lines = append(r.lines, s)
	r.targets = append(r.targets, t)
}

// renderDiff renders a unified diff with its comment threads, keeping context
// unchanged lines around each change and thread; a negative context renders
// the whole file.
func renderDiff(path string, d *gerrit.DiffInfo, context int, threads []gerrit.Thread, width int) (r renderedDiff) {
	writeThread := func(i int) {
		r.threadOffsets = append(r.threadOffsets, len(r.lines))
		for _, l := range renderThread(threads[i], width) {
			r.add(l, rowTarget{thread: i})
		}
	}
	defer func() { r.content = strings.Join(r.lines, "\n") + "\n" }()

	header := path
	if d.MetaA != nil && d.MetaB != nil && d.MetaA.Name != d.MetaB.Name {
		header = d.MetaA.Name + " → " + d.MetaB.Name
	}
	r.add(titleStyle.Render(header)+" "+dimStyle.Render(strings.ToLower(d.ChangeType)), rowTarget{fileLevel: true, thread: -1})
	r.add("", noTarget)

	byLine := map[threadKey][]int{}
	var keys []threadKey
	for i, t := range threads {
		root := t.Root()
		if root.Line == 0 {
			writeThread(i)
			continue
		}
		key := threadKey{parent: root.OnParent(), line: root.Line}
		if _, ok := byLine[key]; !ok {
			keys = append(keys, key)
		}
		byLine[key] = append(byLine[key], i)
	}

	if d.Binary {
		r.add(dimStyle.Render("binary file"), noTarget)
		return r
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
			r.add(dimStyle.Render(fmt.Sprintf("%*s⋯ %d unchanged lines", gutterWidth, "", hidden)), noTarget)
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
		r.add(renderDiffLine(l), rowTarget{line: l, hasLine: true, thread: -1})
		for _, k := range l.threadKeys() {
			for _, t := range byLine[k] {
				writeThread(t)
			}
			delete(byLine, k)
		}
	}
	flush()

	// Threads whose line is outside the diff, e.g. stale ports.
	for _, k := range keys {
		for _, t := range byLine[k] {
			writeThread(t)
		}
	}
	return r
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
		author := bar + titleStyle.Render(c.Author.DisplayName()) + " " + dimStyle.Render(c.Updated.Local().Format("2006-01-02 15:04"))
		if c.Draft {
			author = bar + draftStyle.Render("DRAFT") + " " + dimStyle.Render("not published yet")
		}
		out = append(out, author)
		for l := range strings.SplitSeq(wrap.Render(strings.TrimSpace(c.Message)), "\n") {
			out = append(out, bar+"  "+l)
		}
	}
	return append(out, indent+barStyle.Render("╰"))
}
