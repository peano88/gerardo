package ui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

const requestTimeout = 20 * time.Second

type screen int

const (
	screenList screen = iota
	screenDetail
)

type changesLoadedMsg struct {
	changes []gerrit.ChangeInfo
	err     error
}

type changeLoadedMsg struct {
	change *gerrit.ChangeInfo
	err    error
}

// Model is the root Bubble Tea model of the application.
type Model struct {
	client  *gerrit.Client
	query   string
	screen  screen
	loading bool
	err     error
	width   int
	height  int

	changes []gerrit.ChangeInfo
	table   table.Model

	change   *gerrit.ChangeInfo
	viewport viewport.Model
}

// New creates the root model showing the changes matching query.
func New(client *gerrit.Client, query string) Model {
	t := table.New(table.WithFocused(true))
	return Model{
		client:   client,
		query:    query,
		loading:  true,
		table:    t,
		viewport: viewport.New(0, 0),
	}
}

// Init starts loading the dashboard.
func (m Model) Init() tea.Cmd {
	return m.loadChanges()
}

func (m Model) loadChanges() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		changes, err := m.client.QueryChanges(ctx, m.query, 100)
		return changesLoadedMsg{changes: changes, err: err}
	}
}

func (m Model) loadChange(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		change, err := m.client.GetChange(ctx, id)
		return changeLoadedMsg{change: change, err: err}
	}
}

// Update handles messages and key presses.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case changesLoadedMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.changes = msg.changes
			m.table.SetRows(m.rows())
		}
		return m, nil

	case changeLoadedMsg:
		m.loading, m.err = false, msg.err
		if msg.err == nil {
			m.change = msg.change
			m.screen = screenDetail
			m.viewport.SetContent(renderChange(m.change, m.width))
			m.viewport.GotoTop()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.screen {
	case screenList:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "r":
			m.loading = true
			return m, m.loadChanges()
		case "enter":
			if len(m.changes) == 0 {
				return m, nil
			}
			c := m.changes[m.table.Cursor()]
			m.loading = true
			return m, m.loadChange(strconv.Itoa(c.Number))
		}
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd

	case screenDetail:
		switch msg.String() {
		case "q", "esc", "backspace":
			m.screen, m.err = screenList, nil
			return m, nil
		case "r":
			m.loading = true
			return m, m.loadChange(strconv.Itoa(m.change.Number))
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) resize() {
	bodyHeight := max(m.height-2, 1)

	fixed := 7 + 30 + 4 + 4 + 5
	subjectWidth := max(m.width-fixed-2*2-12, 20)
	m.table.SetColumns([]table.Column{
		{Title: "#", Width: 7},
		{Title: "Subject", Width: subjectWidth},
		{Title: "Owner", Width: 30},
		{Title: "CR", Width: 4},
		{Title: "V", Width: 4},
		{Title: "Upd", Width: 5},
	})
	m.table.SetHeight(bodyHeight)

	m.viewport.Width, m.viewport.Height = m.width, bodyHeight
	if m.change != nil {
		m.viewport.SetContent(renderChange(m.change, m.width))
	}
}

func (m Model) rows() []table.Row {
	rows := make([]table.Row, 0, len(m.changes))
	for _, c := range m.changes {
		subject := c.Subject
		if c.WorkInProgress {
			subject = "[WIP] " + subject
		}
		rows = append(rows, table.Row{
			strconv.Itoa(c.Number),
			subject,
			c.Owner.DisplayName(),
			c.Labels["Code-Review"].Summary(),
			c.Labels["Verified"].Summary(),
			relativeTime(c.Updated.Time),
		})
	}
	return rows
}

// View renders the current screen.
func (m Model) View() string {
	var body, help string
	switch m.screen {
	case screenList:
		body = m.table.View()
		help = fmt.Sprintf("%d changes · %s · enter open · r reload · q quit", len(m.changes), m.query)
	case screenDetail:
		body = m.viewport.View()
		help = fmt.Sprintf("%3.0f%% · ↑/↓ scroll · r reload · esc back", m.viewport.ScrollPercent()*100)
	}

	status := dimStyle.Render(help)
	switch {
	case m.loading:
		status = dimStyle.Render("loading…")
	case m.err != nil:
		status = errStyle.Render(m.err.Error())
	}
	return lipgloss.JoinVertical(lipgloss.Left, body, status)
}
