package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

type changeLoadedMsg struct {
	number   int
	change   *gerrit.ChangeInfo
	comments map[string][]gerrit.CommentInfo
	err      error
}

type changeScreen struct {
	client   *gerrit.Client
	number   int
	change   *gerrit.ChangeInfo
	viewport viewport.Model
	loading  bool
	err      error
	notice   notice

	// comments maps message IDs to the inline comments published with them.
	comments     map[string][]gerrit.CommentInfo
	allPatchSets bool
}

func newChangeScreen(client *gerrit.Client, number int) *changeScreen {
	return &changeScreen{client: client, number: number, viewport: viewport.New(0, 0)}
}

func (s *changeScreen) Init() tea.Cmd {
	s.loading = true
	client, number := s.client, s.number
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		var (
			wg                    sync.WaitGroup
			change                *gerrit.ChangeInfo
			comments              map[string][]gerrit.CommentInfo
			changeErr, commentErr error
		)
		wg.Go(func() { change, changeErr = client.GetChange(ctx, strconv.Itoa(number)) })
		wg.Go(func() { comments, commentErr = client.ListComments(ctx, strconv.Itoa(number)) })
		wg.Wait()
		return changeLoadedMsg{number: number, change: change, comments: comments, err: errors.Join(changeErr, commentErr)}
	}
}

func (s *changeScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	s.notice.update(msg)
	switch msg := msg.(type) {
	case copiedMsg:
		return s, nil

	case changeLoadedMsg:
		if msg.number != s.number {
			return s, nil
		}
		s.loading, s.err = false, msg.err
		if msg.change != nil {
			s.change = msg.change
			s.comments = gerrit.MessageComments(msg.change.Messages, msg.comments)
			s.render()
			s.viewport.GotoTop()
		}
		return s, nil

	case changeUpdatedMsg:
		return s, s.Init()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "backspace":
			return s, pop
		case "r":
			return s, s.Init()
		case "y":
			return s, copyNumber(s.number)
		case "m":
			s.allPatchSets = !s.allPatchSets
			s.render()
			return s, nil
		case "v":
			if s.change == nil {
				return s, nil
			}
			return s, push(newReviewScreen(s.client, s.change))
		case "f", "enter":
			if s.change == nil {
				return s, nil
			}
			if _, ok := s.change.Current(); !ok {
				return s, nil
			}
			return s, push(newFilesScreen(s.client, s.change))
		}
	}

	var cmd tea.Cmd
	s.viewport, cmd = s.viewport.Update(msg)
	return s, cmd
}

func (s *changeScreen) View() string {
	return s.viewport.View()
}

func (s *changeScreen) Title() string {
	return strconv.Itoa(s.number)
}

func (s *changeScreen) Status() string {
	return s.notice.status(s.loading, s.err, dimStyle.Render(fmt.Sprintf("%3.0f%%", s.viewport.ScrollPercent()*100))+"   "+
		keyHints("↑/↓", "scroll", "f", "files", "v", "vote", "m", s.messagesHint(), "y", "copy number", "r", "reload", "esc", "back"))
}

func (s *changeScreen) messagesHint() string {
	if s.allPatchSets {
		return "latest messages"
	}
	return "all messages"
}

func (s *changeScreen) SetSize(width, height int) {
	s.viewport.Width, s.viewport.Height = width, height
	s.render()
}

func (s *changeScreen) render() {
	if s.change != nil {
		s.viewport.SetContent(renderChange(s.change, s.viewport.Width, changeView{allPatchSets: s.allPatchSets, comments: s.comments}))
	}
}
