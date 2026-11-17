package ui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

const diffContext = 3

type diffLoadedMsg struct {
	path string
	diff *gerrit.DiffInfo
	err  error
}

type diffScreen struct {
	client      *gerrit.Client
	change      *gerrit.ChangeInfo
	paths       []string
	index       int
	fullContext bool
	diff        *gerrit.DiffInfo
	viewport    viewport.Model
	loading     bool
	err         error
}

func newDiffScreen(client *gerrit.Client, change *gerrit.ChangeInfo, paths []string, index int) *diffScreen {
	vp := viewport.New(0, 0)
	vp.SetHorizontalStep(8)
	return &diffScreen{client: client, change: change, paths: paths, index: index, viewport: vp}
}

func (s *diffScreen) path() string {
	return s.paths[s.index]
}

func (s *diffScreen) Init() tea.Cmd {
	s.loading = true
	client, number, revision := s.client, strconv.Itoa(s.change.Number), s.change.CurrentRevision
	path := s.path()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		diff, err := client.GetDiff(ctx, number, revision, path)
		return diffLoadedMsg{path: path, diff: diff, err: err}
	}
}

func (s *diffScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case diffLoadedMsg:
		if msg.path != s.path() {
			return s, nil
		}
		s.loading, s.err = false, msg.err
		if msg.err == nil {
			s.diff = msg.diff
			s.render()
			s.viewport.GotoTop()
			s.viewport.SetXOffset(0)
		}
		return s, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "backspace":
			index := s.index
			return s, tea.Sequence(pop, func() tea.Msg { return fileSelectedMsg{index: index} })
		case "n", "]":
			return s, s.moveTo(s.index + 1)
		case "p", "[":
			return s, s.moveTo(s.index - 1)
		case "c":
			s.fullContext = !s.fullContext
			s.render()
			return s, nil
		case "r":
			return s, s.Init()
		}
	}

	var cmd tea.Cmd
	s.viewport, cmd = s.viewport.Update(msg)
	return s, cmd
}

func (s *diffScreen) moveTo(index int) tea.Cmd {
	if index < 0 || index >= len(s.paths) {
		return nil
	}
	s.index = index
	return s.Init()
}

func (s *diffScreen) View() string {
	return s.viewport.View()
}

func (s *diffScreen) Status() string {
	context := "full file"
	if s.fullContext {
		context = "changes only"
	}
	return status(s.loading, s.err, fmt.Sprintf("[%d/%d] %s · %3.0f%% · n/p file · c %s · ←/→ scroll · esc back",
		s.index+1, len(s.paths), s.path(), s.viewport.ScrollPercent()*100, context))
}

func (s *diffScreen) SetSize(width, height int) {
	s.viewport.Width, s.viewport.Height = width, height
}

func (s *diffScreen) render() {
	if s.diff == nil {
		return
	}
	context := diffContext
	if s.fullContext {
		context = -1
	}
	s.viewport.SetContent(renderDiff(s.path(), s.diff, context))
}
