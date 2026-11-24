package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerardo/internal/gerrit"
)

type selfLoadedMsg struct {
	self   *gerrit.AccountInfo
	drafts int
	err    error
}

type reviewPostedMsg struct{ err error }

// changeUpdatedMsg asks the change screen to reload after a review was posted.
type changeUpdatedMsg struct{}

var errEmptyReview = errors.New("nothing to send: change a vote, write a message or add draft comments")

type reviewLabel struct {
	name     string
	info     gerrit.LabelInfo
	values   []int
	current  int
	selected int
}

func (l *reviewLabel) move(delta int) {
	i := slices.Index(l.values, l.selected) + delta
	if i >= 0 && i < len(l.values) {
		l.selected = l.values[i]
	}
}

type reviewScreen struct {
	client   *gerrit.Client
	change   *gerrit.ChangeInfo
	labels   []*reviewLabel
	cursor   int
	message  textarea.Model
	editing  bool
	ready    bool
	sending  bool
	err      error
	revision int
	drafts   int
}

func newReviewScreen(client *gerrit.Client, change *gerrit.ChangeInfo) *reviewScreen {
	ta := textarea.New()
	ta.Placeholder = "Optional message"
	ta.ShowLineNumbers = false
	ta.SetHeight(6)
	ta.Blur()

	rev, _ := change.Current()
	s := &reviewScreen{client: client, change: change, message: ta, revision: rev.Number}
	for _, name := range sortedKeys(change.PermittedLabels) {
		label := &reviewLabel{name: name, info: change.Labels[name]}
		for _, v := range change.PermittedLabels[name] {
			if n, err := gerrit.ParseVote(v); err == nil {
				label.values = append(label.values, n)
			}
		}
		slices.Sort(label.values)
		s.labels = append(s.labels, label)
	}
	return s
}

func (s *reviewScreen) Init() tea.Cmd {
	client, number := s.client, strconv.Itoa(s.change.Number)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		var (
			wg                sync.WaitGroup
			self              *gerrit.AccountInfo
			drafts            map[string][]gerrit.CommentInfo
			selfErr, draftErr error
		)
		wg.Go(func() { self, selfErr = client.Self(ctx) })
		wg.Go(func() { drafts, draftErr = client.ListDrafts(ctx, number) })
		wg.Wait()

		msg := selfLoadedMsg{self: self, err: errors.Join(selfErr, draftErr)}
		for _, list := range drafts {
			msg.drafts += len(list)
		}
		return msg
	}
}

func (s *reviewScreen) setSelf(self *gerrit.AccountInfo) {
	for _, l := range s.labels {
		l.current = l.info.VoteOf(self.AccountID)
		l.selected = l.current
	}
	s.ready = true
}

func (s *reviewScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case selfLoadedMsg:
		s.err = msg.err
		if msg.err == nil {
			s.setSelf(msg.self)
			s.drafts = msg.drafts
		}
		return s, nil

	case reviewPostedMsg:
		s.sending, s.err = false, msg.err
		if msg.err != nil {
			return s, nil
		}
		return s, tea.Sequence(pop, func() tea.Msg { return changeUpdatedMsg{} })

	case tea.KeyMsg:
		return s.handleKey(msg)
	}

	if s.editing {
		var cmd tea.Cmd
		s.message, cmd = s.message.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *reviewScreen) handleKey(msg tea.KeyMsg) (screen, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if s.editing {
			s.editing = false
			s.message.Blur()
			return s, nil
		}
		return s, pop
	case "ctrl+s":
		return s, s.send()
	case "tab":
		s.editing = !s.editing
		if s.editing {
			return s, s.message.Focus()
		}
		s.message.Blur()
		return s, nil
	}

	if s.editing {
		var cmd tea.Cmd
		s.message, cmd = s.message.Update(msg)
		return s, cmd
	}
	if !s.ready || len(s.labels) == 0 {
		if msg.String() == "q" {
			return s, pop
		}
		return s, nil
	}

	switch msg.String() {
	case "q":
		return s, pop
	case "up", "k":
		s.cursor = max(s.cursor-1, 0)
	case "down", "j":
		s.cursor = min(s.cursor+1, len(s.labels)-1)
	case "left", "h":
		s.labels[s.cursor].move(-1)
	case "right", "l":
		s.labels[s.cursor].move(1)
	case "0":
		s.labels[s.cursor].selected = 0
	}
	return s, nil
}

// reviewInput returns the votes that differ from the current ones and the
// message, publishing the user's drafts.
func (s *reviewScreen) reviewInput() (gerrit.ReviewInput, error) {
	in := gerrit.ReviewInput{Message: strings.TrimSpace(s.message.Value()), Drafts: gerrit.PublishAllDrafts}
	for _, l := range s.labels {
		if l.selected != l.current {
			if in.Labels == nil {
				in.Labels = map[string]int{}
			}
			in.Labels[l.name] = l.selected
		}
	}
	if in.Message == "" && len(in.Labels) == 0 && s.drafts == 0 {
		return in, errEmptyReview
	}
	return in, nil
}

func (s *reviewScreen) send() tea.Cmd {
	if !s.ready || s.sending {
		return nil
	}
	in, err := s.reviewInput()
	if err != nil {
		s.err = err
		return nil
	}
	s.sending, s.err = true, nil
	client, number, revision := s.client, strconv.Itoa(s.change.Number), s.change.CurrentRevision
	return write(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		return reviewPostedMsg{err: client.SetReview(ctx, number, revision, in)}
	})
}

func (s *reviewScreen) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", titleStyle.Render(fmt.Sprintf("Review %d: %s (PS%d)", s.change.Number, s.change.Subject, s.revision)))

	b.WriteString(headingStyle.Render("Votes") + "\n")
	if len(s.labels) == 0 {
		b.WriteString(dimStyle.Render("  you cannot vote on this change") + "\n")
	}
	nameWidth := 0
	for _, l := range s.labels {
		nameWidth = max(nameWidth, len(l.name))
	}
	selectedStyle := lipgloss.NewStyle().Reverse(true)
	for i, l := range s.labels {
		pointer := "  "
		if i == s.cursor && !s.editing {
			pointer = "> "
		}
		var values []string
		for _, v := range l.values {
			text := fmt.Sprintf(" %+d ", v)
			if v == 0 {
				text = "  0 "
			}
			if v == l.selected {
				text = selectedStyle.Inherit(voteStyle(v)).Render(text)
			} else {
				text = dimStyle.Render(text)
			}
			values = append(values, text)
		}
		line := fmt.Sprintf("%s%-*s  %s  %s", pointer, nameWidth, l.name, strings.Join(values, ""), l.info.Description(l.selected))
		if l.selected != l.current {
			line += dimStyle.Render(fmt.Sprintf("  (was %+d)", l.current))
		}
		b.WriteString(line + "\n")
	}

	if s.drafts > 0 {
		b.WriteString(headingStyle.Render("Drafts") + "\n")
		b.WriteString(draftStyle.Render(fmt.Sprintf("  %d draft comments will be published", s.drafts)) + "\n")
	}

	b.WriteString(headingStyle.Render("Message") + "\n")
	b.WriteString(s.message.View() + "\n")
	return b.String()
}

func (s *reviewScreen) Title() string {
	return "Vote"
}

func (s *reviewScreen) Status() string {
	help := keyHints("↑/↓", "label", "←/→", "vote", "tab", "message", "ctrl+s", "send", "esc", "cancel")
	if s.editing {
		help = keyHints("tab/esc", "back to votes", "ctrl+s", "send")
	}
	return status(s.sending || (!s.ready && s.err == nil), s.err, help)
}

func (s *reviewScreen) SetSize(width, _ int) {
	s.message.SetWidth(max(width-2, 20))
}
