package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
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

// dialog is a modal box over a screen, taking every key while open.
type dialog interface {
	update(key tea.KeyMsg) (done bool, cmd tea.Cmd)
	view(width int) string
	hints() string
}

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

func (c *confirmation) hints() string {
	return keyHints("y", c.verb, "n/esc", "cancel")
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

// choice is an option of a choiceDialog. An option with a placeholder asks
// for a value, passed to run.
type choice struct {
	label       string
	detail      string
	placeholder string
	run         func(value string) tea.Cmd
}

// choiceDialog asks which way to run an action that cannot be undone: the
// option is picked with ↑/↓ or its number and confirmed with y, or with
// enter once the value of an option asking for one is typed.
type choiceDialog struct {
	title    string
	details  []string
	verb     string
	choices  []choice
	selected int
	input    textinput.Model
}

func newChoiceDialog(title string, details []string, verb string, choices []choice, selected int) *choiceDialog {
	in := textinput.New()
	in.Prompt = "› "
	in.PromptStyle = hintKeyStyle
	d := &choiceDialog{title: title, details: details, verb: verb, choices: choices, input: in}
	d.selectChoice(selected)
	return d
}

func (d *choiceDialog) selectChoice(i int) tea.Cmd {
	d.selected = min(max(i, 0), len(d.choices)-1)
	if c := d.choices[d.selected]; c.placeholder != "" {
		d.input.Placeholder = c.placeholder
		return d.input.Focus()
	}
	d.input.Blur()
	return nil
}

func (d *choiceDialog) asksValue() bool {
	return d.choices[d.selected].placeholder != ""
}

func (d *choiceDialog) update(key tea.KeyMsg) (done bool, cmd tea.Cmd) {
	switch key.String() {
	case "esc":
		return true, nil
	case "up", "shift+tab":
		return false, d.selectChoice(d.selected - 1)
	case "down", "tab":
		return false, d.selectChoice(d.selected + 1)
	}
	if d.asksValue() {
		if key.String() == "enter" {
			if v := strings.TrimSpace(d.input.Value()); v != "" {
				return true, d.choices[d.selected].run(v)
			}
			return false, nil
		}
		d.input, cmd = d.input.Update(key)
		return false, cmd
	}
	switch k := key.String(); k {
	case "y", "Y":
		return true, d.choices[d.selected].run("")
	case "n", "N", "q":
		return true, nil
	case "k":
		return false, d.selectChoice(d.selected - 1)
	case "j":
		return false, d.selectChoice(d.selected + 1)
	default:
		if len(k) == 1 && k[0] >= '1' && int(k[0]-'0') <= len(d.choices) {
			return false, d.selectChoice(int(k[0] - '1'))
		}
	}
	return false, nil
}

func (d *choiceDialog) hints() string {
	if d.asksValue() {
		return keyHints("enter", d.verb, "↑/↓", "choose", "esc", "cancel")
	}
	return keyHints("y", d.verb, "↑/↓", "choose", "n/esc", "cancel")
}

func (d *choiceDialog) view(width int) string {
	textWidth := min(64, max(width-12, 20))
	lines := []string{confirmTitle.Render(d.title), ""}
	for _, l := range d.details {
		lines = append(lines, lipgloss.NewStyle().Width(textWidth).Render(l))
	}
	lines = append(lines, "")
	for i, c := range d.choices {
		mark, label := "  ", c.label
		if i == d.selected {
			mark, label = hintKeyStyle.Render("▶ "), titleStyle.Render(label)
		}
		lines = append(lines, mark+dimStyle.Render(fmt.Sprintf("%d ", i+1))+label)
		if c.detail != "" {
			lines = append(lines, "    "+dimStyle.Width(textWidth-4).Render(c.detail))
		}
		if i == d.selected && c.placeholder != "" {
			d.input.Width = textWidth - 8
			lines = append(lines, "    "+d.input.View())
		}
	}
	buttons := confirmButton.Render("y "+d.verb) + "   " + cancelButton.Render("n cancel")
	if d.asksValue() {
		buttons = confirmButton.Render("enter "+d.verb) + "   " + cancelButton.Render("esc cancel")
	}
	lines = append(lines, "", buttons)
	return confirmBox.Render(strings.Join(lines, "\n"))
}
