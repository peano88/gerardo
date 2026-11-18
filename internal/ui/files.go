package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

// fileSelectedMsg tells the files screen which file the diff screen moved to,
// so that going back keeps the cursor on the last viewed file.
type fileSelectedMsg struct{ index int }

type commentsLoadedMsg struct {
	comments, ported map[string][]gerrit.CommentInfo
	err              error
}

// changeComments holds the published comments of a change and the unresolved
// threads ported onto its current revision.
type changeComments struct {
	comments, ported map[string][]gerrit.CommentInfo
}

func fetchComments(ctx context.Context, client *gerrit.Client, change *gerrit.ChangeInfo) (changeComments, error) {
	number := strconv.Itoa(change.Number)
	var (
		wg           sync.WaitGroup
		c            changeComments
		cErr, portEr error
	)
	wg.Go(func() { c.comments, cErr = client.ListComments(ctx, number) })
	wg.Go(func() { c.ported, portEr = client.PortedComments(ctx, number, change.CurrentRevision) })
	wg.Wait()
	return c, errors.Join(cErr, portEr)
}

func (c changeComments) threads(path string, revision int) []gerrit.Thread {
	return gerrit.ThreadsForRevision(path, revision, c.comments, c.ported)
}

type filesScreen struct {
	client   *gerrit.Client
	change   *gerrit.ChangeInfo
	rev      gerrit.RevisionInfo
	paths    []string
	comments changeComments
	table    table.Model
	loading  bool
	err      error
}

func newFilesScreen(client *gerrit.Client, change *gerrit.ChangeInfo) *filesScreen {
	rev, _ := change.Current()
	s := &filesScreen{
		client: client,
		change: change,
		rev:    rev,
		paths:  rev.SortedPaths(),
		table:  table.New(table.WithFocused(true), table.WithColumns(fileColumns(0))),
	}
	s.table.SetRows(s.rows())
	return s
}

func (s *filesScreen) rows() []table.Row {
	rows := make([]table.Row, 0, len(s.paths))
	for _, p := range s.paths {
		f := s.rev.Files[p]
		st := f.Status
		if st == "" {
			st = "M"
		}
		threads, open := 0, 0
		for _, t := range s.comments.threads(p, s.rev.Number) {
			threads++
			if t.Unresolved() {
				open++
			}
		}
		var count string
		switch {
		case open > 0:
			count = fmt.Sprintf("%d (%d open)", threads, open)
		case threads > 0:
			count = strconv.Itoa(threads)
		}
		rows = append(rows, table.Row{st, fmt.Sprintf("+%d", f.LinesInserted), fmt.Sprintf("-%d", f.LinesDeleted), count, p})
	}
	return rows
}

func (s *filesScreen) Init() tea.Cmd {
	s.loading = true
	client, change := s.client, s.change
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		c, err := fetchComments(ctx, client, change)
		return commentsLoadedMsg{comments: c.comments, ported: c.ported, err: err}
	}
}

func (s *filesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case commentsLoadedMsg:
		s.loading, s.err = false, msg.err
		s.comments = changeComments{comments: msg.comments, ported: msg.ported}
		s.table.SetRows(s.rows())
		return s, nil

	case fileSelectedMsg:
		s.table.SetCursor(msg.index)
		return s, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "backspace":
			return s, pop
		case "r":
			return s, s.Init()
		case "enter":
			if len(s.paths) == 0 {
				return s, nil
			}
			return s, push(newDiffScreen(s.client, s.change, s.paths, s.table.Cursor()))
		}
	}

	var cmd tea.Cmd
	s.table, cmd = s.table.Update(msg)
	return s, cmd
}

func (s *filesScreen) View() string {
	return s.table.View()
}

func (s *filesScreen) Status() string {
	return status(s.loading, s.err, fmt.Sprintf("%d: PS%d · %d files · enter diff · r reload · esc back", s.change.Number, s.rev.Number, len(s.paths)))
}

func (s *filesScreen) SetSize(width, height int) {
	s.table.SetColumns(fileColumns(width))
	s.table.SetHeight(height)
}

func fileColumns(width int) []table.Column {
	const st, lines, threads = 3, 7, 12
	return []table.Column{
		{Title: "St", Width: st},
		{Title: "+", Width: lines},
		{Title: "-", Width: lines},
		{Title: "Threads", Width: threads},
		{Title: "Path", Width: max(width-st-2*lines-threads-2*5, 20)},
	}
}
