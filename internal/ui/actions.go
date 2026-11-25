package ui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerardo/internal/gerrit"
)

// changeAction is an action Gerrit offers on a change or, when revision is
// set, on its current patch set.
type changeAction struct {
	id       string
	info     gerrit.ActionInfo
	revision bool
}

// builtinActions are the actions of Gerrit itself gerardo runs; the other
// ones it offers are those of plugins, run without input.
var builtinActions = []string{"abandon", "cherrypick", "hashtags", "ready", "rebase", "restore", "revert", "submit", "wip"}

func isPluginAction(id string) bool {
	return strings.Contains(id, "~")
}

// menuActions returns the enabled actions of c gerardo can run, by label.
func menuActions(c *gerrit.ChangeInfo) []changeAction {
	var out []changeAction
	add := func(actions map[string]gerrit.ActionInfo, revision bool) {
		for id, a := range actions {
			if a.Enabled && (slices.Contains(builtinActions, id) || isPluginAction(id)) {
				out = append(out, changeAction{id: id, info: a, revision: revision})
			}
		}
	}
	add(c.Actions, false)
	if rev, ok := c.Current(); ok {
		add(rev.Actions, true)
	}
	slices.SortFunc(out, func(a, b changeAction) int {
		return cmp.Compare(strings.ToLower(cmp.Or(a.info.Label, a.id)), strings.ToLower(cmp.Or(b.info.Label, b.id)))
	})
	return out
}

type actionPickedMsg struct{ action changeAction }

type changeCreatedMsg struct {
	number int
	err    error
}

// actionMenu lists the actions of a change to pick one.
type actionMenu struct {
	number  int
	actions []changeAction
	cursor  int
}

func (m *actionMenu) update(key tea.KeyMsg) (bool, tea.Cmd) {
	pick := func(i int) (bool, tea.Cmd) {
		a := m.actions[i]
		return true, func() tea.Msg { return actionPickedMsg{action: a} }
	}
	switch k := key.String(); {
	case k == "esc" || k == "q":
		return true, nil
	case k == "enter":
		return pick(m.cursor)
	case k == "up" || k == "k":
		m.cursor = max(m.cursor-1, 0)
	case k == "down" || k == "j":
		m.cursor = min(m.cursor+1, len(m.actions)-1)
	case len(k) == 1 && k[0] >= '1' && int(k[0]-'0') <= len(m.actions):
		return pick(int(k[0] - '1'))
	}
	return false, nil
}

func (m *actionMenu) hints() string {
	return keyHints("enter", "run", "↑/↓", "choose", "esc", "cancel")
}

func (m *actionMenu) view(width int) string {
	labelWidth := 0
	for _, a := range m.actions {
		labelWidth = max(labelWidth, lipgloss.Width(cmp.Or(a.info.Label, a.id)))
	}
	titleWidth := max(min(56, width-labelWidth-20), 10)
	lines := []string{confirmTitle.Render(fmt.Sprintf("Actions on change %d", m.number)), ""}
	for i, a := range m.actions {
		mark, label := "  ", lipgloss.NewStyle().Width(labelWidth).Render(cmp.Or(a.info.Label, a.id))
		if i == m.cursor {
			mark, label = hintKeyStyle.Render("▶ "), titleStyle.Width(labelWidth).Render(cmp.Or(a.info.Label, a.id))
		}
		title := truncate(a.info.Title, titleWidth)
		lines = append(lines, mark+dimStyle.Render(fmt.Sprintf("%d ", i+1))+label+"  "+dimStyle.Render(title))
	}
	return confirmBox.Render(strings.Join(lines, "\n"))
}

func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	return string(r[:max(width-1, 0)]) + "…"
}

// openActions opens the menu of the actions of the change.
func (s *changeScreen) openActions() {
	actions := menuActions(s.change)
	if len(actions) == 0 {
		s.notice = notice{text: "no action available on this change"}
		return
	}
	s.dialog = &actionMenu{number: s.number, actions: actions}
}

// runAction runs a, through the flow of gerardo for the actions it has
// keys for, after asking for what the others need.
func (s *changeScreen) runAction(a changeAction) tea.Cmd {
	c := s.change
	details := []string{c.Subject, dimStyle.Render(c.Project + " · " + c.Branch)}
	switch a.id {
	case "submit":
		s.askSubmit()
	case "rebase":
		return s.askRebase()
	case "wip", "ready":
		return s.toggleWorkInProgress()
	case "hashtags":
		s.dialog = newHashtagDialog(c, Hashtags, s.setHashtags)
	case "abandon":
		s.dialog = &confirmation{
			title: fmt.Sprintf("Abandon change %d?", c.Number), details: details, verb: "abandon",
			run: s.action("abandoned", func(ctx context.Context, n string) error { return s.client.Abandon(ctx, n) }),
		}
	case "restore":
		s.dialog = &confirmation{
			title: fmt.Sprintf("Restore change %d?", c.Number), details: details, verb: "restore",
			run: s.action("restored", func(ctx context.Context, n string) error { return s.client.Restore(ctx, n) }),
		}
	case "revert":
		s.dialog = &confirmation{
			title: fmt.Sprintf("Revert change %d?", c.Number), verb: "revert",
			details: append(details, "", "This creates a change undoing it, opened once created."),
			run:     s.create(func(ctx context.Context, n string) (*gerrit.ChangeInfo, error) { return s.client.Revert(ctx, n) }),
		}
	case "cherrypick":
		revision := c.CurrentRevision
		s.dialog = newChoiceDialog(fmt.Sprintf("Cherry-pick change %d?", c.Number),
			append(details, "", "This creates a change applying its current patch set onto another branch."), "cherry-pick",
			[]choice{{label: "Onto the branch", placeholder: "branch name", run: func(branch string) tea.Cmd {
				return s.create(func(ctx context.Context, n string) (*gerrit.ChangeInfo, error) {
					return s.client.CherryPick(ctx, n, revision, branch)
				})
			}}}, 0)
	default:
		label := cmp.Or(a.info.Label, a.id)
		revision := ""
		if a.revision {
			revision = c.CurrentRevision
		}
		s.dialog = &confirmation{
			title: fmt.Sprintf("%s on change %d?", label, c.Number), verb: strings.ToLower(label),
			details: append(details, "", cmp.Or(a.info.Title, "Runs "+a.id+".")),
			run: s.action(strings.ToLower(label)+" done", func(ctx context.Context, n string) error {
				return s.client.RunAction(ctx, n, revision, a.id, a.info)
			}),
		}
	}
	return nil
}

// create runs an action creating a change, opened once created.
func (s *changeScreen) create(do func(ctx context.Context, number string) (*gerrit.ChangeInfo, error)) tea.Cmd {
	number := strconv.Itoa(s.number)
	return write(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		c, err := do(ctx, number)
		if err != nil {
			return changeCreatedMsg{err: err}
		}
		return changeCreatedMsg{number: c.Number}
	})
}
