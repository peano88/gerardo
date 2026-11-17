package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).MarginTop(1)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	addStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	delStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

func voteStyle(v int) lipgloss.Style {
	switch {
	case v > 0:
		return addStyle
	case v < 0:
		return delStyle
	default:
		return dimStyle
	}
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func renderChange(c *gerrit.ChangeInfo, width int) string {
	var b strings.Builder

	status := c.Status
	if c.WorkInProgress {
		status += " (WIP)"
	}
	if c.Submittable {
		status += " · submittable"
	}
	fmt.Fprintf(&b, "%s\n", titleStyle.Render(fmt.Sprintf("%d: %s", c.Number, c.Subject)))
	rev, hasRev := c.Current()
	fmt.Fprintf(&b, "%s  %s/%s", status, c.Project, c.Branch)
	if hasRev {
		fmt.Fprintf(&b, "  PS%d", rev.Number)
	}
	if c.Topic != "" {
		fmt.Fprintf(&b, "  topic:%s", c.Topic)
	}
	fmt.Fprintf(&b, "\nOwner: %s  ·  updated %s ago  ·  %s %s",
		c.Owner.DisplayName(), relativeTime(c.Updated.Time),
		addStyle.Render(fmt.Sprintf("+%d", c.Insertions)), delStyle.Render(fmt.Sprintf("-%d", c.Deletions)))
	if c.UnresolvedCommentCount > 0 {
		fmt.Fprintf(&b, "  ·  %s", errStyle.Render(fmt.Sprintf("%d unresolved", c.UnresolvedCommentCount)))
	}
	b.WriteString("\n")

	b.WriteString(headingStyle.Render("Labels") + "\n")
	for _, name := range sortedKeys(c.Labels) {
		label := c.Labels[name]
		var votes []string
		for _, a := range label.All {
			if a.Value == 0 {
				continue
			}
			votes = append(votes, voteStyle(a.Value).Render(fmt.Sprintf("%+d %s", a.Value, a.DisplayName())))
		}
		if len(votes) == 0 {
			votes = []string{dimStyle.Render("no votes")}
		}
		fmt.Fprintf(&b, "  %-14s %s\n", name, strings.Join(votes, ", "))
	}

	if hasRev && rev.Commit != nil {
		b.WriteString(headingStyle.Render("Commit message") + "\n")
		for line := range strings.SplitSeq(strings.TrimRight(rev.Commit.Message, "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}

	if hasRev {
		b.WriteString(headingStyle.Render(fmt.Sprintf("Files (%d)", len(rev.Files))) + "\n")
		for _, path := range rev.SortedPaths() {
			f := rev.Files[path]
			st := f.Status
			if st == "" {
				st = "M"
			}
			fmt.Fprintf(&b, "  %s %s %s %s\n", st,
				addStyle.Render(fmt.Sprintf("%+5d", f.LinesInserted)),
				delStyle.Render(fmt.Sprintf("-%-4d", f.LinesDeleted)), path)
		}
	}

	b.WriteString(headingStyle.Render("Messages") + "\n")
	wrap := lipgloss.NewStyle().Width(max(width-4, 20)).PaddingLeft(4)
	for _, m := range c.Messages {
		fmt.Fprintf(&b, "  %s %s\n", titleStyle.Render(m.Author.DisplayName()),
			dimStyle.Render(fmt.Sprintf("PS%d · %s", m.RevisionNumber, m.Date.Local().Format("2006-01-02 15:04"))))
		b.WriteString(wrap.Render(strings.TrimSpace(m.Message)) + "\n")
	}

	return b.String()
}

// renderDiff renders a unified diff, keeping context unchanged lines around
// each change; a negative context renders the whole file.
func renderDiff(path string, d *gerrit.DiffInfo, context int) string {
	var b strings.Builder

	header := path
	if d.MetaA != nil && d.MetaB != nil && d.MetaA.Name != d.MetaB.Name {
		header = d.MetaA.Name + " → " + d.MetaB.Name
	}
	fmt.Fprintf(&b, "%s %s\n\n", titleStyle.Render(header), dimStyle.Render(strings.ToLower(d.ChangeType)))
	if d.Binary {
		b.WriteString(dimStyle.Render("binary file") + "\n")
		return b.String()
	}

	gutter := func(a, b int) string {
		num := func(n int) string {
			if n == 0 {
				return ""
			}
			return strconv.Itoa(n)
		}
		return dimStyle.Render(fmt.Sprintf("%5s %5s ", num(a), num(b)))
	}
	line := func(s string) string {
		return strings.ReplaceAll(s, "\t", "    ")
	}

	aLine, bLine := 1, 1
	skip := func(n int) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("%11s ⋯ %d unchanged lines", "", n)) + "\n")
		aLine += n
		bLine += n
	}
	common := func(lines []string) {
		for _, l := range lines {
			b.WriteString(gutter(aLine, bLine) + "  " + line(l) + "\n")
			aLine++
			bLine++
		}
	}

	for i, c := range d.Content {
		leading, trailing := context, context
		if i == 0 {
			leading = 0
		}
		if i == len(d.Content)-1 {
			trailing = 0
		}
		if context < 0 || len(c.AB) <= leading+trailing {
			common(c.AB)
		} else {
			common(c.AB[:leading])
			skip(len(c.AB) - leading - trailing)
			common(c.AB[len(c.AB)-trailing:])
		}
		for _, l := range c.A {
			b.WriteString(gutter(aLine, 0) + delStyle.Render("- "+line(l)) + "\n")
			aLine++
		}
		for _, l := range c.B {
			b.WriteString(gutter(0, bLine) + addStyle.Render("+ "+line(l)) + "\n")
			bLine++
		}
		if c.Skip > 0 {
			skip(c.Skip)
		}
	}
	return b.String()
}
