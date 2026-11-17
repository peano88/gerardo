package ui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

type changesLoadedMsg struct {
	changes []gerrit.ChangeInfo
	err     error
}

type dashboard struct {
	client  *gerrit.Client
	query   string
	table   table.Model
	changes []gerrit.ChangeInfo
	loading bool
	err     error
}

func newDashboard(client *gerrit.Client, query string) *dashboard {
	return &dashboard{
		client: client,
		query:  query,
		table:  table.New(table.WithFocused(true), table.WithColumns(dashboardColumns(0))),
	}
}

func (d *dashboard) Init() tea.Cmd {
	d.loading = true
	client, query := d.client, d.query
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		changes, err := client.QueryChanges(ctx, query, 100)
		return changesLoadedMsg{changes: changes, err: err}
	}
}

func (d *dashboard) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case changesLoadedMsg:
		d.loading, d.err = false, msg.err
		if msg.err == nil {
			d.changes = msg.changes
			d.table.SetRows(d.rows())
		}
		return d, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return d, tea.Quit
		case "r":
			return d, d.Init()
		case "enter":
			if len(d.changes) == 0 {
				return d, nil
			}
			return d, push(newChangeScreen(d.client, d.changes[d.table.Cursor()].Number))
		}
	}

	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

func (d *dashboard) View() string {
	return d.table.View()
}

func (d *dashboard) Status() string {
	return status(d.loading, d.err, fmt.Sprintf("%d changes · %s · enter open · r reload · q quit", len(d.changes), d.query))
}

func (d *dashboard) SetSize(width, height int) {
	d.table.SetColumns(dashboardColumns(width))
	d.table.SetHeight(height)
}

func dashboardColumns(width int) []table.Column {
	const number, owner, vote, updated, padding = 7, 30, 4, 5, 2 * 6
	return []table.Column{
		{Title: "#", Width: number},
		{Title: "Subject", Width: max(width-number-owner-2*vote-updated-padding, 20)},
		{Title: "Owner", Width: owner},
		{Title: "CR", Width: vote},
		{Title: "V", Width: vote},
		{Title: "Upd", Width: updated},
	}
}

func (d *dashboard) rows() []table.Row {
	rows := make([]table.Row, 0, len(d.changes))
	for _, c := range d.changes {
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
