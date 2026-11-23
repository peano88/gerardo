package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

// patchRange is what a diff compares: patch set target against base, a
// patch set number or 0 for the parent of target.
type patchRange struct {
	base, target int
}

// latestRange compares the current patch set of change with its parent.
func latestRange(change *gerrit.ChangeInfo) patchRange {
	rev, _ := change.Current()
	return patchRange{target: rev.Number}
}

func (r patchRange) String() string {
	return fmt.Sprintf("%s → PS%d", baseLabel(r.base), r.target)
}

func baseLabel(base int) string {
	if base == 0 {
		return "Base"
	}
	return fmt.Sprintf("PS%d", base)
}

// revision is the patch set to put a comment on, on the left side when left.
func (r patchRange) revision(left bool) string {
	if left && r.base > 0 {
		return strconv.Itoa(r.base)
	}
	return strconv.Itoa(r.target)
}

// rangeFiles returns the files modified in r, from change itself when it
// compares its current patch set with its parent.
func rangeFiles(ctx context.Context, client *gerrit.Client, change *gerrit.ChangeInfo, r patchRange) (map[string]gerrit.FileInfo, error) {
	if r == latestRange(change) {
		rev, _ := change.Current()
		return rev.Files, nil
	}
	return client.ListFiles(ctx, strconv.Itoa(change.Number), strconv.Itoa(r.target), r.base)
}

// rangeDialog chooses the patch sets to compare: a column lists the bases,
// Base and every patch set but the latest, the other the patch sets to
// compare with them, always later than the base.
type rangeDialog struct {
	latest int
	rng    patchRange
	// onTarget is set when the target column is focused.
	onTarget bool
	apply    func(patchRange) tea.Cmd
}

// openRangeDialog returns the dialog choosing the patch sets of change to
// compare, or a notice when it has a single one.
func openRangeDialog(change *gerrit.ChangeInfo, rng patchRange, apply func(patchRange) tea.Cmd) (dialog, notice) {
	latest := latestRange(change).target
	if latest < 2 {
		return nil, notice{text: "the change has a single patch set"}
	}
	return &rangeDialog{latest: latest, rng: rng, apply: apply}, notice{}
}

func (d *rangeDialog) move(delta int) {
	if d.onTarget {
		d.rng.target = min(max(d.rng.target+delta, 1), d.latest)
		d.rng.base = min(d.rng.base, d.rng.target-1)
	} else {
		d.rng.base = min(max(d.rng.base+delta, 0), d.latest-1)
		d.rng.target = max(d.rng.target, d.rng.base+1)
	}
}

func (d *rangeDialog) update(key tea.KeyMsg) (done bool, cmd tea.Cmd) {
	switch key.String() {
	case "esc", "q":
		return true, nil
	case "enter", "y":
		return true, d.apply(d.rng)
	case "left", "h":
		d.onTarget = false
	case "right", "l":
		d.onTarget = true
	case "tab", "shift+tab":
		d.onTarget = !d.onTarget
	case "up", "k":
		d.move(-1)
	case "down", "j":
		d.move(1)
	case "home", "g":
		d.move(-d.latest)
	case "end", "G":
		d.move(d.latest)
	}
	return false, nil
}

func (d *rangeDialog) hints() string {
	return keyHints("←/→", "from/to", "↑/↓", "choose", "enter", "compare", "esc", "cancel")
}

const rangeColumnWidth = 16

// view lists the patch sets in two columns, one row per patch set, so that
// a base is always above the patch sets it can be compared with.
func (d *rangeDialog) view(int) string {
	cell := func(label string, selected, focused, latest bool) string {
		mark, style := "  ", lipgloss.NewStyle()
		switch {
		case selected && focused:
			mark, style = hintKeyStyle.Render("▶ "), titleStyle
		case selected:
			mark, style = dimStyle.Render("▸ "), lipgloss.NewStyle().Bold(true)
		}
		text := mark + style.Render(label)
		if latest {
			text += dimStyle.Render(" latest")
		}
		return lipgloss.NewStyle().Width(rangeColumnWidth).Render(text)
	}
	heading := func(title string, focused bool) string {
		style := columnStyle
		if focused {
			style = sectionStyle
		}
		return lipgloss.NewStyle().Width(rangeColumnWidth).Render("  " + style.Render(title))
	}

	lines := []string{confirmTitle.Render("Compare patch sets"), "",
		heading("From", !d.onTarget) + heading("To", d.onTarget)}
	for ps := 0; ps <= d.latest; ps++ {
		from := strings.Repeat(" ", rangeColumnWidth)
		if ps < d.latest {
			from = cell(baseLabel(ps), ps == d.rng.base, !d.onTarget, false)
		}
		to := ""
		if ps > 0 {
			to = cell(fmt.Sprintf("PS%d", ps), ps == d.rng.target, d.onTarget, ps == d.latest)
		}
		lines = append(lines, from+to)
	}
	buttons := confirmButton.Render("enter compare") + "   " + cancelButton.Render("esc cancel")
	lines = append(lines, "", dimStyle.Render(d.rng.String()), "", buttons)
	return confirmBox.Render(strings.Join(lines, "\n"))
}
