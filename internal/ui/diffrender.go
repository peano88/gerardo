package ui

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/peano88/gerardo/internal/gerrit"
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
	edits   []mark
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
		editsA := intralineMarks(c.A, c.EditA)
		for i, t := range c.A {
			lines = append(lines, diffLine{kind: lineDeleted, a: a, text: t, edits: editsA[i]})
			a++
		}
		editsB := intralineMarks(c.B, c.EditB)
		for i, t := range c.B {
			lines = append(lines, diffLine{kind: lineAdded, b: b, text: t, edits: editsB[i]})
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
	// alt is the parent side line paired with line in a side-by-side row.
	alt    diffLine
	hasAlt bool
}

var noTarget = rowTarget{thread: -1}

// renderedDiff is a rendered diff: its rows, what each row refers to, the row
// of each thread header and of each search match, in order, and the number of
// matching lines hidden in folds.
type renderedDiff struct {
	content       string
	lines         []string
	targets       []rowTarget
	threadOffsets []int
	matchRows     []int
	hiddenMatches int
}

// diffOptions controls the rendering of a diff. A negative context renders
// the whole file; width is the space available for a row.
type diffOptions struct {
	context    int
	width      int
	syntax     *syntaxLines
	search     *regexp.Regexp
	sideBySide bool
}

// diffRow is a row of a side-by-side diff: the parent side line on the left
// and the revision side line on the right, as indexes in the flattened diff,
// -1 when a side is empty. Unchanged lines are on both sides.
type diffRow struct {
	left, right int
}

// pairLines lays a flattened diff out side by side, pairing each run of
// deleted lines with the added lines that follow it.
func pairLines(lines []diffLine) []diffRow {
	var rows []diffRow
	for i := 0; i < len(lines); {
		switch lines[i].kind {
		case lineDeleted, lineAdded:
			var dels, adds []int
			for ; i < len(lines) && lines[i].kind == lineDeleted; i++ {
				dels = append(dels, i)
			}
			for ; i < len(lines) && lines[i].kind == lineAdded; i++ {
				adds = append(adds, i)
			}
			for j := range max(len(dels), len(adds)) {
				row := diffRow{left: -1, right: -1}
				if j < len(dels) {
					row.left = dels[j]
				}
				if j < len(adds) {
					row.right = adds[j]
				}
				rows = append(rows, row)
			}
		default:
			rows = append(rows, diffRow{left: i, right: i})
			i++
		}
	}
	return rows
}

func (r *renderedDiff) add(s string, t rowTarget) {
	r.lines = append(r.lines, s)
	r.targets = append(r.targets, t)
}

// renderDiff renders a unified diff with its comment threads, keeping context
// unchanged lines around each change and thread.
func renderDiff(path string, d *gerrit.DiffInfo, threads []gerrit.Thread, opts diffOptions) (r renderedDiff) {
	writeThread := func(i int) {
		r.threadOffsets = append(r.threadOffsets, len(r.lines))
		for _, l := range renderThread(threads[i], opts.width) {
			if opts.search != nil && opts.search.MatchString(ansi.Strip(l)) {
				r.matchRows = append(r.matchRows, len(r.lines))
			}
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
		key := threadKey{parent: t.Left(), line: root.Line}
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
	visible := visibleLines(lines, opts.context, hasThreads)

	hidden := 0
	flush := func() {
		if hidden > 0 {
			r.add(dimStyle.Render(fmt.Sprintf("%*s⋯ %d unchanged lines", gutterWidth, "", hidden)), noTarget)
			hidden = 0
		}
	}
	writeThreads := func(keys []threadKey) {
		for _, k := range keys {
			for _, t := range byLine[k] {
				writeThread(t)
			}
			delete(byLine, k)
		}
	}

	if opts.sideBySide {
		for _, row := range pairLines(lines) {
			primary := row.right
			if primary < 0 {
				primary = row.left
			}
			if l := lines[primary]; l.kind == lineSkipped {
				hidden += l.skipped
				continue
			}
			var left, right *diffLine
			var leftMatches, rightMatches []mark
			if row.left >= 0 {
				left, leftMatches = &lines[row.left], searchMarks(opts.search, lines[row.left].text)
			}
			if row.right >= 0 {
				right, rightMatches = &lines[row.right], searchMarks(opts.search, lines[row.right].text)
			}
			matched := len(leftMatches) > 0 || (row.right != row.left && len(rightMatches) > 0)
			if !(row.left >= 0 && visible[row.left]) && !(row.right >= 0 && visible[row.right]) {
				hidden++
				if matched {
					r.hiddenMatches++
				}
				continue
			}
			flush()
			if matched {
				r.matchRows = append(r.matchRows, len(r.lines))
			}
			leftWidth := (opts.width - 1) / 2
			line := renderSideCell(left, true, opts.syntax, leftMatches, leftWidth) + dimStyle.Render("│") +
				renderSideCell(right, false, opts.syntax, rightMatches, opts.width-leftWidth-1)
			target := rowTarget{line: lines[primary], hasLine: true, thread: -1}
			if row.left >= 0 && row.left != primary {
				target.alt, target.hasAlt = lines[row.left], true
			}
			r.add(line, target)
			if left != nil {
				writeThreads(left.threadKeys())
			}
			if right != nil && row.right != row.left {
				writeThreads(right.threadKeys())
			}
		}
		flush()
		for _, k := range keys {
			for _, t := range byLine[k] {
				writeThread(t)
			}
		}
		return r
	}

	for i, l := range lines {
		if l.kind == lineSkipped {
			hidden += l.skipped
			continue
		}
		matches := searchMarks(opts.search, l.text)
		if !visible[i] {
			hidden++
			if len(matches) > 0 {
				r.hiddenMatches++
			}
			continue
		}
		flush()
		if len(matches) > 0 {
			r.matchRows = append(r.matchRows, len(r.lines))
		}
		r.add(renderDiffLine(l, opts.syntax.spans(l), matches, opts.width), rowTarget{line: l, hasLine: true, thread: -1})
		writeThreads(l.threadKeys())
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

// renderDiffLine renders a diff line with its syntax highlighting, intraline
// edits and search matches. Changed lines get a background padded to width.
func renderDiffLine(l diffLine, spans []span, matches []mark, width int) string {
	num := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	gutter := dimStyle.Render(fmt.Sprintf("%5s %5s ", num(l.a), num(l.b)))

	var bg, editBg lipgloss.TerminalColor
	marker, base := " ", lipgloss.NewStyle()
	switch l.kind {
	case lineDeleted:
		bg, editBg, marker, base = deletedBg, deletedEditBg, "-", delStyle
	case lineAdded:
		bg, editBg, marker, base = addedBg, addedEditBg, "+", addStyle
	}
	markerStyle := base
	if len(spans) > 0 {
		base = lipgloss.NewStyle()
	}
	if bg != nil {
		markerStyle = markerStyle.Background(bg)
	}
	text := paint(l.text, spans, append(slices.Clone(l.edits), matches...), base, bg, editBg)
	out := gutter + markerStyle.Render(marker+" ") + text
	if bg != nil {
		if pad := width - gutterWidth - 2 - lipgloss.Width(expandTabs(l.text)); pad > 0 {
			out += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", pad))
		}
	}
	return out
}

// sideGutter is the width of the line number and marker of a side-by-side cell.
const sideGutter = 8

// renderSideCell renders one side of a side-by-side row, its parent side
// (left) or revision side, cut or padded to width. An empty side is blank.
func renderSideCell(l *diffLine, left bool, syntax *syntaxLines, matches []mark, width int) string {
	if l == nil {
		return lipgloss.NewStyle().Background(emptySideBg).Render(strings.Repeat(" ", width))
	}
	n := l.b
	if left {
		n = l.a
	}
	var bg, editBg lipgloss.TerminalColor
	marker, base := " ", lipgloss.NewStyle()
	switch l.kind {
	case lineDeleted:
		bg, editBg, marker, base = deletedBg, deletedEditBg, "-", delStyle
	case lineAdded:
		bg, editBg, marker, base = addedBg, addedEditBg, "+", addStyle
	}
	markerStyle := base
	spans := syntax.spans(*l)
	if len(spans) > 0 {
		base = lipgloss.NewStyle()
	}
	if bg != nil {
		markerStyle = markerStyle.Background(bg)
	}
	textWidth := max(width-sideGutter, 1)
	text := ansi.Truncate(paint(l.text, spans, append(slices.Clone(l.edits), matches...), base, bg, editBg), textWidth, "…")
	out := dimStyle.Render(fmt.Sprintf("%5d ", n)) + markerStyle.Render(marker+" ") + text
	if pad := textWidth - lipgloss.Width(text); pad > 0 {
		padStyle := lipgloss.NewStyle()
		if bg != nil {
			padStyle = padStyle.Background(bg)
		}
		out += padStyle.Render(strings.Repeat(" ", pad))
	}
	return out
}

func renderThread(t gerrit.Thread, width int) []string {
	barStyle, label := dimStyle, dimStyle.Render("✓ resolved")
	if t.Unresolved() {
		barStyle, label = unresolvedSt, unresolvedSt.Bold(true).Render("● unresolved")
	}
	root := t.Root()
	if root.Line == 0 {
		label += dimStyle.Render(" · file comment")
	}
	if root.PatchSet > 0 {
		label += dimStyle.Render(fmt.Sprintf(" · PS%d", root.PatchSet))
	}
	label += dimStyle.Render(" · " + plural(len(t.Comments), "comment"))

	indent := strings.Repeat(" ", gutterWidth)
	ruleWidth := max(width-gutterWidth-2, 20)
	bar := indent + barStyle.Render("│") + " "
	wrap := lipgloss.NewStyle().Width(max(width-gutterWidth-6, 20))

	out := []string{indent + barStyle.Render("╭─ ") + label + " " + barStyle.Render(strings.Repeat("─", max(ruleWidth-4-lipgloss.Width(label), 0)))}
	for i, c := range t.Comments {
		if i > 0 {
			out = append(out, strings.TrimRight(bar, " "))
		}
		author := bar + titleStyle.Render(c.Author.DisplayName()) + " " + dimStyle.Render(c.Updated.Local().Format("2006-01-02 15:04"))
		if c.Draft {
			author = bar + draftStyle.Render("✎ DRAFT") + " " + dimStyle.Render("not published yet")
		}
		out = append(out, author)
		for l := range strings.SplitSeq(wrap.Render(strings.TrimSpace(c.Message)), "\n") {
			out = append(out, bar+"  "+l)
		}
	}
	return append(out, indent+barStyle.Render("╰"+strings.Repeat("─", ruleWidth-1)))
}
