package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerardo/internal/gerrit"
)

// hashtagDialog edits the hashtags of a change: the offered and current ones
// are toggled, new ones typed on the last row.
type hashtagDialog struct {
	number  int
	current []string
	options []string
	checked map[string]bool
	// cursor is the row of an option, or len(options) for the input row.
	cursor int
	input  textinput.Model
	apply  func(add, remove []string) tea.Cmd
}

func newHashtagDialog(c *gerrit.ChangeInfo, offered []string, apply func(add, remove []string) tea.Cmd) *hashtagDialog {
	in := textinput.New()
	in.Prompt = "› "
	in.PromptStyle = hintKeyStyle
	in.Placeholder = "new hashtag"
	d := &hashtagDialog{number: c.Number, current: c.Hashtags, checked: map[string]bool{}, input: in, apply: apply}
	for _, h := range slices.Concat(offered, c.Hashtags) {
		if !slices.Contains(d.options, h) {
			d.options = append(d.options, h)
		}
	}
	for _, h := range c.Hashtags {
		d.checked[h] = true
	}
	if len(d.options) == 0 {
		d.input.Focus()
	}
	return d
}

func (d *hashtagDialog) onInput() bool {
	return d.cursor == len(d.options)
}

func (d *hashtagDialog) moveTo(row int) tea.Cmd {
	d.cursor = min(max(row, 0), len(d.options))
	if d.onInput() {
		return d.input.Focus()
	}
	d.input.Blur()
	return nil
}

// changes returns the hashtags to add and to remove.
func (d *hashtagDialog) changes() (add, remove []string) {
	for _, h := range d.options {
		switch was := slices.Contains(d.current, h); {
		case d.checked[h] && !was:
			add = append(add, h)
		case !d.checked[h] && was:
			remove = append(remove, h)
		}
	}
	for _, h := range strings.FieldsFunc(d.input.Value(), func(r rune) bool { return r == ' ' || r == ',' }) {
		h = strings.TrimPrefix(h, "#")
		if h != "" && !slices.Contains(d.current, h) && !slices.Contains(add, h) {
			add = append(add, h)
		}
	}
	return add, remove
}

func (d *hashtagDialog) update(key tea.KeyMsg) (bool, tea.Cmd) {
	switch k := key.String(); k {
	case "esc":
		return true, nil
	case "enter":
		add, remove := d.changes()
		if len(add) == 0 && len(remove) == 0 {
			return true, nil
		}
		return true, d.apply(add, remove)
	case "up", "shift+tab":
		return false, d.moveTo(d.cursor - 1)
	case "down", "tab":
		return false, d.moveTo(d.cursor + 1)
	}
	if d.onInput() {
		var cmd tea.Cmd
		d.input, cmd = d.input.Update(key)
		return false, cmd
	}
	switch k := key.String(); {
	case k == " " || k == "x":
		h := d.options[d.cursor]
		d.checked[h] = !d.checked[h]
	case k == "k":
		return false, d.moveTo(d.cursor - 1)
	case k == "j":
		return false, d.moveTo(d.cursor + 1)
	case k == "q":
		return true, nil
	case len(k) == 1 && k[0] >= '1' && int(k[0]-'0') <= len(d.options):
		h := d.options[k[0]-'1']
		d.checked[h] = !d.checked[h]
	}
	return false, nil
}

func (d *hashtagDialog) hints() string {
	if d.onInput() {
		return keyHints("enter", "apply", "↑", "hashtags", "esc", "cancel")
	}
	return keyHints("space", "toggle", "↑/↓", "choose", "enter", "apply", "esc", "cancel")
}

func (d *hashtagDialog) view(width int) string {
	lines := []string{confirmTitle.Render(fmt.Sprintf("Hashtags of change %d", d.number)), ""}
	for i, h := range d.options {
		mark, box, label := "  ", "[ ]", "#"+h
		if d.checked[h] {
			box = addStyle.Render("[x]")
		}
		if i == d.cursor {
			mark, label = hintKeyStyle.Render("▶ "), titleStyle.Render(label)
		}
		lines = append(lines, mark+dimStyle.Render(fmt.Sprintf("%d ", i+1))+box+" "+label)
	}
	d.input.Width = min(40, max(width-20, 10))
	mark := "  "
	if d.onInput() {
		mark = hintKeyStyle.Render("▶ ")
	}
	lines = append(lines, mark+d.input.View())
	buttons := confirmButton.Render("enter apply") + "   " + cancelButton.Render("esc cancel")
	lines = append(lines, "", buttons)
	return confirmBox.Render(strings.Join(lines, "\n"))
}
