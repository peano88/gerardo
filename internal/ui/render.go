package ui

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
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

var (
	newBadge         = badge("12")
	mergedBadge      = badge("5")
	abandonedBadge   = badge("8")
	wipBadge         = badge("3")
	submittableBadge = badge("2")
)

func badge(bg string) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color(bg)).Padding(0, 1)
}

func changeBadges(c *gerrit.ChangeInfo) string {
	var badges []string
	switch c.Status {
	case "MERGED":
		badges = append(badges, mergedBadge.Render("MERGED"))
	case "ABANDONED":
		badges = append(badges, abandonedBadge.Render("ABANDONED"))
	default:
		badges = append(badges, newBadge.Render(cmp.Or(c.Status, "NEW")))
	}
	if c.WorkInProgress {
		badges = append(badges, wipBadge.Render("WIP"))
	}
	if c.Submittable {
		badges = append(badges, submittableBadge.Render("SUBMITTABLE"))
	}
	return strings.Join(badges, " ")
}

// attentionNames lists the accounts in the attention set, sorted.
func attentionNames(c *gerrit.ChangeInfo) []string {
	var names []string
	for _, a := range c.AttentionSet {
		names = append(names, a.Account.DisplayName())
	}
	slices.Sort(names)
	return names
}

func fileStatusStyle(status string) lipgloss.Style {
	switch status {
	case "A":
		return addStyle
	case "D":
		return delStyle
	case "R", "C":
		return numberStyle
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

type changeView struct {
	// allPatchSets expands the messages of the older patch sets.
	allPatchSets bool
	// comments maps message IDs to the inline comments published with them.
	comments map[string][]gerrit.CommentInfo
	// commentsByID finds the comments replies answer.
	commentsByID map[string]gerrit.CommentInfo
}

func renderChange(c *gerrit.ChangeInfo, width int, view changeView) string {
	var b strings.Builder

	b.WriteString(changeBadges(c) + "\n")
	b.WriteString(numberStyle.Bold(true).Render(strconv.Itoa(c.Number)+":") + " " + titleStyle.Render(c.Subject) + "\n")

	rev, hasRev := c.Current()
	where := []string{c.Project, c.Branch}
	if hasRev {
		where = append(where, fmt.Sprintf("PS%d", rev.Number))
	}
	if c.Topic != "" {
		where = append(where, "topic:"+c.Topic)
	}
	b.WriteString(dimStyle.Render(strings.Join(where, " · ")) + "\n")

	facts := []string{c.Owner.DisplayName()}
	if !c.Updated.IsZero() {
		facts = append(facts, dimStyle.Render("updated "+relativeTime(c.Updated.Time)+" ago"))
	}
	facts = append(facts, addStyle.Render(fmt.Sprintf("+%d", c.Insertions))+" "+delStyle.Render(fmt.Sprintf("-%d", c.Deletions)))
	if c.UnresolvedCommentCount > 0 {
		facts = append(facts, unresolvedSt.Render(fmt.Sprintf("%d unresolved", c.UnresolvedCommentCount)))
	}
	b.WriteString(strings.Join(facts, dimStyle.Render(" · ")) + "\n")
	if names := attentionNames(c); len(names) > 0 {
		b.WriteString(attentionDot + " " + dimStyle.Render("waiting on ") + strings.Join(names, ", ") + "\n")
	}

	b.WriteString(headingStyle.Render("Labels") + "\n")
	nameWidth := 0
	for name := range c.Labels {
		nameWidth = max(nameWidth, ansi.StringWidth(name))
	}
	for _, name := range sortedKeys(c.Labels) {
		label := c.Labels[name]
		decisive, decisiveStyle := voteCell(label)
		var votes []string
		for _, a := range label.All {
			if a.Value == 0 {
				continue
			}
			votes = append(votes, voteStyle(a.Value).Render(fmt.Sprintf("%+d", a.Value))+" "+a.DisplayName())
		}
		if len(votes) == 0 {
			votes = []string{dimStyle.Render("no votes")}
		}
		fmt.Fprintf(&b, "  %s %s  %s\n", decisiveStyle.Width(2).Render(decisive),
			lipgloss.NewStyle().Width(nameWidth).Render(name), strings.Join(votes, dimStyle.Render(", ")))
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
			fmt.Fprintf(&b, "  %s %s %s %s\n", fileStatusStyle(st).Render(st),
				addStyle.Render(fmt.Sprintf("%+5d", f.LinesInserted)),
				delStyle.Render(fmt.Sprintf("-%-4d", f.LinesDeleted)), path)
		}
	}

	b.WriteString(headingStyle.Render("Messages") + "\n")
	groups := groupMessages(c.Messages)
	wrap := lipgloss.NewStyle().Width(max(width-6, 20)).PaddingLeft(6)
	for i, g := range groups {
		count := dimStyle.Render(fmt.Sprintf(" · %s", plural(len(g.messages), "message")))
		if !view.allPatchSets && i < len(groups)-1 {
			last := g.messages[len(g.messages)-1].Date.Local().Format("2006-01-02")
			summary := fmt.Sprintf("  ▸ %s · %s · %s · %s", g.title(), plural(len(g.messages), "message"), g.authors(), last)
			b.WriteString(dimStyle.Render(ansi.Truncate(summary, max(width, 20), "…")) + "\n")
			continue
		}
		b.WriteString("  " + sectionStyle.Render("▾ "+g.title()) + count + "\n")
		for _, m := range g.messages {
			votes, body := splitMessage(m)
			header := titleStyle.Render(m.Author.DisplayName()) + " " + dimStyle.Render(m.Date.Local().Format("2006-01-02 15:04"))
			if len(votes) > 0 {
				header += "  " + renderVotes(votes)
			}
			b.WriteString("    " + header + "\n")
			comments := view.comments[m.ID]
			if len(comments) > 0 {
				body = strings.TrimSpace(commentCount.ReplaceAllString(body, ""))
			}
			if body != "" {
				b.WriteString(wrap.Render(body) + "\n")
			}
			for _, cm := range comments {
				b.WriteString(renderMessageComment(cm, view.commentsByID, width))
			}
		}
	}

	return b.String()
}

// commentCount matches the "(N comments)" line Gerrit adds to a message.
var commentCount = regexp.MustCompile(`(?m)^\(\d+ comments?\)$`)

var commentBar = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).
	BorderForeground(lipgloss.Color("8")).PaddingLeft(1)

// renderMessageComment shows an inline comment under its message: where it
// is, then its text.
func renderMessageComment(c gerrit.CommentInfo, byID map[string]gerrit.CommentInfo, width int) string {
	var where string
	switch c.Path {
	case gerrit.PatchSetLevel:
		where = "Patch set"
	case gerrit.CommitMessage:
		where = "Commit message"
	default:
		where = c.Path
	}
	if c.Line > 0 {
		where += ":" + strconv.Itoa(c.Line)
	}
	location := numberStyle.Render(where)
	if c.Unresolved {
		location += " " + unresolvedSt.Render("● unresolved")
	}
	textWidth := max(width-8, 20)
	text := strings.TrimSpace(c.Message)
	if parent, ok := byID[c.InReplyTo]; ok {
		first, _, _ := strings.Cut(strings.TrimSpace(parent.Message), "\n")
		quote := ansi.Truncate("↳ "+parent.Author.DisplayName()+": "+first, textWidth-2, "…")
		text = dimStyle.Italic(true).Render(quote) + "\n" + text
	}
	text = commentBar.Width(textWidth).Render(text)
	return lipgloss.NewStyle().PaddingLeft(6).Render(location+"\n"+text) + "\n"
}

// voteToken matches a vote such as "Code-Review+2", or a removed one such as "-Verified".
var voteToken = regexp.MustCompile(`^(?:-[A-Za-z][\w-]*|[A-Za-z][\w-]*([+-])\d+)$`)

// splitMessage drops the "Patch Set N:" prefix Gerrit puts on messages,
// returning the votes that follow it on the same line separately.
func splitMessage(m gerrit.ChangeMessageInfo) (votes []string, body string) {
	msg := strings.TrimSpace(m.Message)
	rest, ok := strings.CutPrefix(msg, fmt.Sprintf("Patch Set %d:", m.RevisionNumber))
	if !ok {
		return nil, msg
	}
	first, after, _ := strings.Cut(rest, "\n")
	tokens := strings.Fields(first)
	if len(tokens) == 0 || !slices.ContainsFunc(tokens, func(t string) bool { return !voteToken.MatchString(t) }) {
		return tokens, strings.TrimSpace(after)
	}
	return nil, strings.TrimSpace(rest)
}

// renderVotes colours votes by their sign and strikes out removed ones.
func renderVotes(votes []string) string {
	out := make([]string, len(votes))
	for i, v := range votes {
		switch voteToken.FindStringSubmatch(v)[1] {
		case "+":
			out[i] = addStyle.Render(v)
		case "-":
			out[i] = delStyle.Render(v)
		default:
			out[i] = dimStyle.Strikethrough(true).Render(strings.TrimPrefix(v, "-"))
		}
	}
	return strings.Join(out, " ")
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
