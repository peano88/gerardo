package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

const (
	// previewMinWidth is the terminal width from which the preview is shown.
	previewMinWidth = 140
	// previewDelay lets the cursor settle before a preview is fetched.
	previewDelay = 150 * time.Millisecond
)

var previewBorder = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
	BorderForeground(lipgloss.Color("8")).PaddingLeft(1)

type changesLoadedMsg struct {
	changes []gerrit.ChangeInfo
	self    *gerrit.AccountInfo
	err     error
}

// dashItem is a dashboard row: a change, or a section header when change is nil.
type dashItem struct {
	change *gerrit.ChangeInfo
	header string
}

// groupChanges puts the changes owned by selfID first, each group under a
// header; without a known selfID the changes are listed as they come.
func groupChanges(changes []gerrit.ChangeInfo, selfID int) []dashItem {
	var mine, others []dashItem
	for i := range changes {
		item := dashItem{change: &changes[i]}
		if selfID != 0 && changes[i].Owner.AccountID == selfID {
			mine = append(mine, item)
		} else {
			others = append(others, item)
		}
	}
	if selfID == 0 {
		return others
	}

	var items []dashItem
	if len(mine) > 0 {
		items = append(items, dashItem{header: fmt.Sprintf("▾ My changes (%d)", len(mine))})
		items = append(items, mine...)
	}
	if len(others) > 0 {
		items = append(items, dashItem{header: fmt.Sprintf("▾ Reviews (%d)", len(others))})
		items = append(items, others...)
	}
	return items
}

type previewTickMsg struct {
	seq    int
	number int
}

type previewLoadedMsg struct {
	number int
	change *gerrit.ChangeInfo
	err    error
}

type dashboard struct {
	client  *gerrit.Client
	query   string
	table   table.Model
	changes []gerrit.ChangeInfo
	items   []dashItem
	self    *gerrit.AccountInfo
	loading bool
	err     error
	width   int
	height  int
	notice  notice

	showPreview bool
	preview     viewport.Model
	previews    map[int]*gerrit.ChangeInfo
	previewErr  error
	previewSeq  int
	previewFor  int
}

func newDashboard(client *gerrit.Client, query string) *dashboard {
	return &dashboard{
		client:      client,
		query:       query,
		table:       table.New(table.WithFocused(true), table.WithColumns(dashboardColumns(0, false))),
		showPreview: true,
		preview:     viewport.New(0, 0),
		previews:    map[int]*gerrit.ChangeInfo{},
	}
}

func (d *dashboard) Init() tea.Cmd {
	d.loading = true
	client, query, self := d.client, d.query, d.self
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		var (
			wg                sync.WaitGroup
			changes           []gerrit.ChangeInfo
			queryErr, selfErr error
		)
		wg.Go(func() { changes, queryErr = client.QueryChanges(ctx, query, 100) })
		if self == nil {
			wg.Go(func() { self, selfErr = client.Self(ctx) })
		}
		wg.Wait()
		return changesLoadedMsg{changes: changes, self: self, err: errors.Join(queryErr, selfErr)}
	}
}

func (d *dashboard) Update(msg tea.Msg) (screen, tea.Cmd) {
	d.notice.update(msg)
	switch msg := msg.(type) {
	case copiedMsg:
		return d, nil

	case changesLoadedMsg:
		d.loading, d.err = false, msg.err
		if msg.self != nil {
			d.self = msg.self
		}
		if msg.changes != nil || msg.err == nil {
			selfID := 0
			if d.self != nil {
				selfID = d.self.AccountID
			}
			d.changes = msg.changes
			d.items = groupChanges(d.changes, selfID)
			d.table.SetRows(d.rows())
			d.skipHeader(1)
			d.previewFor = 0
		}
		return d, d.schedulePreview()

	case previewTickMsg:
		if msg.seq != d.previewSeq {
			return d, nil
		}
		client, number := d.client, msg.number
		return d, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
			defer cancel()
			change, err := client.GetChange(ctx, strconv.Itoa(number))
			return previewLoadedMsg{number: number, change: change, err: err}
		}

	case focusMsg:
		return d, d.schedulePreview()

	case previewLoadedMsg:
		if msg.err == nil {
			d.previews[msg.number] = msg.change
		}
		if n, ok := d.selected(); ok && n == msg.number {
			d.previewErr = msg.err
			d.renderPreview()
		}
		return d, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return d, tea.Quit
		case "r":
			clear(d.previews)
			return d, d.Init()
		case "P":
			d.showPreview = !d.showPreview
			d.layout()
			return d, d.schedulePreview()
		case "y":
			if n, ok := d.selected(); ok {
				return d, copyNumber(n)
			}
			return d, nil
		case "J":
			d.preview.ScrollDown(3)
			return d, nil
		case "K":
			d.preview.ScrollUp(3)
			return d, nil
		case "enter":
			n, ok := d.selected()
			if !ok {
				return d, nil
			}
			// The change may be voted on or commented, refresh it on return.
			delete(d.previews, n)
			d.previewFor = 0
			return d, push(newChangeScreen(d.client, n))
		}
	}

	before := d.table.Cursor()
	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	if after := d.table.Cursor(); after != before {
		direction := 1
		if after < before {
			direction = -1
		}
		d.skipHeader(direction)
		return d, tea.Batch(cmd, d.schedulePreview())
	}
	if d.previewFor == 0 {
		return d, tea.Batch(cmd, d.schedulePreview())
	}
	return d, cmd
}

// skipHeader moves the cursor off a section header, in direction when
// possible, otherwise the other way.
func (d *dashboard) skipHeader(direction int) {
	isHeader := func(i int) bool { return i >= 0 && i < len(d.items) && d.items[i].change == nil }
	c := d.table.Cursor()
	if !isHeader(c) {
		return
	}
	for _, dir := range []int{direction, -direction} {
		for i := c + dir; i >= 0 && i < len(d.items); i += dir {
			if !isHeader(i) {
				d.table.SetCursor(i)
				return
			}
		}
	}
}

func (d *dashboard) selected() (int, bool) {
	c := d.table.Cursor()
	if c < 0 || c >= len(d.items) || d.items[c].change == nil {
		return 0, false
	}
	return d.items[c].change.Number, true
}

func (d *dashboard) split() bool {
	return d.showPreview && d.width >= previewMinWidth
}

// schedulePreview shows the selected change from the cache, or fetches it
// once the cursor has stayed on it for previewDelay.
func (d *dashboard) schedulePreview() tea.Cmd {
	n, ok := d.selected()
	if !d.split() || !ok || n == d.previewFor {
		return nil
	}
	d.previewFor, d.previewErr = n, nil
	d.renderPreview()
	if _, cached := d.previews[n]; cached {
		return nil
	}
	d.previewSeq++
	seq := d.previewSeq
	return tea.Tick(previewDelay, func(time.Time) tea.Msg { return previewTickMsg{seq: seq, number: n} })
}

func (d *dashboard) renderPreview() {
	switch c, ok := d.previews[d.previewFor]; {
	case d.previewErr != nil:
		d.preview.SetContent(errStyle.Render(d.previewErr.Error()))
	case ok:
		d.preview.SetContent(renderChange(c, d.preview.Width))
	default:
		d.preview.SetContent(dimStyle.Render("loading…"))
	}
	d.preview.GotoTop()
}

func (d *dashboard) View() string {
	if !d.split() {
		return d.table.View()
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, d.table.View(), previewBorder.Height(d.height).Render(d.preview.View()))
}

func (d *dashboard) Status() string {
	help := "enter open · y copy number · r reload · q quit"
	if d.width >= previewMinWidth {
		help = "enter open · y copy number · J/K scroll preview · P toggle preview · r reload · q quit"
	}
	return d.notice.status(d.loading, d.err, fmt.Sprintf("%d changes · %s · %s", len(d.changes), d.query, help))
}

func (d *dashboard) SetSize(width, height int) {
	d.width, d.height = width, height
	d.layout()
}

func (d *dashboard) layout() {
	tableWidth := d.width
	if d.split() {
		tableWidth = d.width * 45 / 100
		d.preview.Width, d.preview.Height = d.width-tableWidth-2, d.height
		if d.previewFor != 0 {
			d.renderPreview()
		}
	}
	d.table.SetColumns(dashboardColumns(tableWidth, d.split()))
	d.table.SetHeight(d.height)
}

func dashboardColumns(width int, compact bool) []table.Column {
	const number, vote, updated, padding = 7, 4, 5, 2 * 6
	owner := 30
	if compact {
		owner = 16
	}
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
	rows := make([]table.Row, 0, len(d.items))
	for _, item := range d.items {
		c := item.change
		if c == nil {
			rows = append(rows, table.Row{"", item.header, "", "", "", ""})
			continue
		}
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
