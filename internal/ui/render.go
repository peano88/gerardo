package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).MarginTop(1)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	addStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	delStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	draftStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	cursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
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

// renderChange renders a change, with the messages of the older patch sets
// collapsed unless allPatchSets.
func renderChange(c *gerrit.ChangeInfo, width int, allPatchSets bool) string {
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
	groups := groupMessages(c.Messages)
	wrap := lipgloss.NewStyle().Width(max(width-6, 20)).PaddingLeft(6)
	for i, g := range groups {
		count := dimStyle.Render(fmt.Sprintf(" · %s", plural(len(g.messages), "message")))
		if !allPatchSets && i < len(groups)-1 {
			last := g.messages[len(g.messages)-1].Date.Local().Format("2006-01-02")
			summary := fmt.Sprintf("  ▸ %s · %s · %s · %s", g.title(), plural(len(g.messages), "message"), g.authors(), last)
			b.WriteString(dimStyle.Render(ansi.Truncate(summary, max(width, 20), "…")) + "\n")
			continue
		}
		b.WriteString("  " + sectionStyle.Render("▾ "+g.title()) + count + "\n")
		for _, m := range g.messages {
			fmt.Fprintf(&b, "    %s %s\n", titleStyle.Render(m.Author.DisplayName()),
				dimStyle.Render(m.Date.Local().Format("2006-01-02 15:04")))
			b.WriteString(wrap.Render(strings.TrimSpace(m.Message)) + "\n")
		}
	}

	return b.String()
}

type messageGroup struct {
	patchSet int
	messages []gerrit.ChangeMessageInfo
}

func (g messageGroup) title() string {
	if g.patchSet == 0 {
		return "Change"
	}
	return fmt.Sprintf("Patch Set %d", g.patchSet)
}

func (g messageGroup) authors() string {
	var names []string
	for _, m := range g.messages {
		if n := m.Author.DisplayName(); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return strings.Join(names, ", ")
}

// groupMessages groups the messages by patch set, in patch set order, keeping
// them chronological within a group.
func groupMessages(messages []gerrit.ChangeMessageInfo) []messageGroup {
	var groups []messageGroup
	for _, m := range messages {
		i := slices.IndexFunc(groups, func(g messageGroup) bool { return g.patchSet == m.RevisionNumber })
		if i < 0 {
			groups = append(groups, messageGroup{patchSet: m.RevisionNumber})
			i = len(groups) - 1
		}
		groups[i].messages = append(groups[i].messages, m)
	}
	slices.SortStableFunc(groups, func(a, b messageGroup) int { return a.patchSet - b.patchSet })
	return groups
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
