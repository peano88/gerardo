package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	confirmBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("3")).Padding(1, 3)
	confirmTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	confirmButton = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3")).Padding(0, 1)
	cancelButton  = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("8")).Padding(0, 1)
)

// confirmation is a dialog asking before an action that cannot be undone.
type confirmation struct {
	title   string
	details []string
	verb    string
	run     tea.Cmd
}

// update handles a key while the dialog is open: y runs the action, n, esc
// or q cancel, and anything else is ignored so that a stray key does nothing.
func (c *confirmation) update(key tea.KeyMsg) (done bool, cmd tea.Cmd) {
	switch key.String() {
	case "y", "Y":
		return true, c.run
	case "n", "N", "esc", "q":
		return true, nil
	}
	return false, nil
}

func (c *confirmation) view(width int) string {
	textWidth := lipgloss.Width(c.title)
	for _, d := range c.details {
		textWidth = max(textWidth, lipgloss.Width(d))
	}
	textWidth = min(textWidth, 64, max(width-12, 20))
	lines := []string{confirmTitle.Render(c.title), ""}
	for _, d := range c.details {
		lines = append(lines, lipgloss.NewStyle().Width(textWidth).Render(d))
	}
	buttons := confirmButton.Render("y "+c.verb) + "   " + cancelButton.Render("n cancel")
	lines = append(lines, "", buttons)
	return confirmBox.Render(strings.Join(lines, "\n"))
}

// overlay draws box centered over bg, a width × height screen.
func overlay(bg, box string, width, height int) string {
	lines := strings.Split(bg, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	boxLines := strings.Split(box, "\n")
	boxWidth := lipgloss.Width(box)
	x, y := max((width-boxWidth)/2, 0), max((height-len(boxLines))/2, 0)
	for i, l := range boxLines {
		row := y + i
		if row >= len(lines) {
			break
		}
		left := ansi.Truncate(lines[row], x, "")
		left += strings.Repeat(" ", max(x-ansi.StringWidth(left), 0))
		right := ansi.TruncateLeft(lines[row], x+boxWidth, "")
		lines[row] = left + "\x1b[0m" + l + "\x1b[0m" + right
	}
	return strings.Join(lines, "\n")
}
