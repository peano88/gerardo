package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

// fileSelectedMsg tells the files screen which file the diff screen moved to,
// so that going back keeps the cursor on the last viewed file.
type fileSelectedMsg struct{ index int }

type filesScreen struct {
	client *gerrit.Client
	change *gerrit.ChangeInfo
	rev    gerrit.RevisionInfo
	paths  []string
	table  table.Model
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
	rows := make([]table.Row, 0, len(s.paths))
	for _, p := range s.paths {
		f := rev.Files[p]
		st := f.Status
		if st == "" {
			st = "M"
		}
		rows = append(rows, table.Row{st, fmt.Sprintf("+%d", f.LinesInserted), fmt.Sprintf("-%d", f.LinesDeleted), p})
	}
	s.table.SetRows(rows)
	return s
}

func (s *filesScreen) Init() tea.Cmd {
	return nil
}

func (s *filesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case fileSelectedMsg:
		s.table.SetCursor(msg.index)
		return s, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "backspace":
			return s, pop
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
	return status(false, nil, fmt.Sprintf("%d: PS%d · %d files · enter diff · esc back", s.change.Number, s.rev.Number, len(s.paths)))
}

func (s *filesScreen) SetSize(width, height int) {
	s.table.SetColumns(fileColumns(width))
	s.table.SetHeight(height)
}

func fileColumns(width int) []table.Column {
	const st, lines = 3, 7
	return []table.Column{
		{Title: "St", Width: st},
		{Title: "+", Width: lines},
		{Title: "-", Width: lines},
		{Title: "Path", Width: max(width-st-2*lines-2*4, 20)},
	}
}
