package ui

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

const (
	diffContext = 3
	// threadMargin keeps a few diff lines visible above a thread jumped to.
	threadMargin = 3
)

type diffLoadedMsg struct {
	path    string
	diff    *gerrit.DiffInfo
	threads []gerrit.Thread
	err     error
}

type diffScreen struct {
	client      *gerrit.Client
	change      *gerrit.ChangeInfo
	paths       []string
	index       int
	fullContext bool
	diff        *gerrit.DiffInfo
	threads     []gerrit.Thread
	offsets     []int
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
	client, change, path := s.client, s.change, s.path()
	rev, _ := change.Current()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		var (
			wg                  sync.WaitGroup
			diff                *gerrit.DiffInfo
			comments            changeComments
			diffErr, commentErr error
		)
		wg.Go(func() { diff, diffErr = client.GetDiff(ctx, strconv.Itoa(change.Number), change.CurrentRevision, path) })
		wg.Go(func() { comments, commentErr = fetchComments(ctx, client, change) })
		wg.Wait()

		if diffErr != nil {
			return diffLoadedMsg{path: path, err: diffErr}
		}
		return diffLoadedMsg{path: path, diff: diff, threads: comments.threads(path, rev.Number), err: commentErr}
	}
}

func (s *diffScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case diffLoadedMsg:
		if msg.path != s.path() {
			return s, nil
		}
		s.loading, s.err = false, msg.err
		if msg.diff != nil {
			s.diff, s.threads = msg.diff, msg.threads
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
		case "n":
			return s, s.moveTo(s.index + 1)
		case "p":
			return s, s.moveTo(s.index - 1)
		case "]":
			s.jumpThread(1)
			return s, nil
		case "[":
			s.jumpThread(-1)
			return s, nil
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
	s.diff, s.threads, s.offsets = nil, nil, nil
	return s.Init()
}

// jumpThread scrolls to the next (direction 1) or previous (-1) thread.
func (s *diffScreen) jumpThread(direction int) {
	anchor := s.viewport.YOffset + threadMargin
	target := -1
	for i := range s.offsets {
		off := s.offsets[i]
		if direction < 0 {
			off = s.offsets[len(s.offsets)-1-i]
		}
		if (direction > 0 && off > anchor) || (direction < 0 && off < anchor) {
			target = off
			break
		}
	}
	if target >= 0 {
		s.viewport.SetYOffset(max(target-threadMargin, 0))
	}
}

func (s *diffScreen) View() string {
	return s.viewport.View()
}

func (s *diffScreen) Status() string {
	context := "full file"
	if s.fullContext {
		context = "changes only"
	}
	unresolved := 0
	for _, t := range s.threads {
		if t.Unresolved() {
			unresolved++
		}
	}
	return status(s.loading, s.err, fmt.Sprintf("[%d/%d] %s · %d threads (%d open) · %3.0f%% · n/p file · ]/[ thread · c %s · esc back",
		s.index+1, len(s.paths), s.path(), len(s.threads), unresolved, s.viewport.ScrollPercent()*100, context))
}

func (s *diffScreen) SetSize(width, height int) {
	s.viewport.Width, s.viewport.Height = width, height
	s.render()
}

func (s *diffScreen) render() {
	if s.diff == nil {
		return
	}
	context := diffContext
	if s.fullContext {
		context = -1
	}
	r := renderDiff(s.path(), s.diff, context, s.threads, s.viewport.Width)
	s.offsets = r.threadOffsets
	s.viewport.SetContent(r.content)
}
