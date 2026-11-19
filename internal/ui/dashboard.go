package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

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
	changes []gerrit.ChangeInfo
	items   []dashItem
	cursor  int
	offset  int
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
			prev, _ := d.selected()
			d.changes = msg.changes
			d.items = groupChanges(d.changes, selfID)
			d.selectNumber(prev)
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
		case "up", "k":
			return d, d.moveBy(-1)
		case "down", "j":
			return d, d.moveBy(1)
		case "pgup", "ctrl+b":
			return d, d.moveBy(-d.listHeight())
		case "pgdown", "ctrl+f":
			return d, d.moveBy(d.listHeight())
		case "ctrl+u":
			return d, d.moveBy(-d.listHeight() / 2)
		case "ctrl+d":
			return d, d.moveBy(d.listHeight() / 2)
		case "home", "g":
			return d, d.moveBy(-len(d.items))
		case "end", "G":
			return d, d.moveBy(len(d.items))
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

	if d.previewFor == 0 {
		return d, d.schedulePreview()
	}
	return d, nil
}

func (d *dashboard) moveBy(delta int) tea.Cmd {
	d.move(delta)
	return d.schedulePreview()
}

func (d *dashboard) selected() (int, bool) {
	if d.cursor < 0 || d.cursor >= len(d.items) || d.items[d.cursor].change == nil {
		return 0, false
	}
	return d.items[d.cursor].change.Number, true
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
	body := d.listView(d.width)
	if d.split() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, d.listView(d.listWidth()), previewBorder.Height(d.bodyHeight()).Render(d.preview.View()))
	}
	return d.titleBar() + "\n" + body
}

func (d *dashboard) Status() string {
	hints := []string{"enter", "open", "y", "copy number", "r", "reload", "q", "quit"}
	if d.width >= previewMinWidth {
		hints = []string{"enter", "open", "y", "copy number", "J/K", "scroll preview", "P", "toggle preview", "r", "reload", "q", "quit"}
	}
	return d.notice.status(d.loading, d.err, keyHints(hints...))
}

func (d *dashboard) SetSize(width, height int) {
	d.width, d.height = width, height
	d.layout()
}

func (d *dashboard) listWidth() int {
	if d.split() {
		return d.width * 45 / 100
	}
	return d.width
}

func (d *dashboard) layout() {
	if d.split() {
		d.preview.Width, d.preview.Height = d.width-d.listWidth()-2, d.bodyHeight()
		if d.previewFor != 0 {
			d.renderPreview()
		}
	}
	d.scroll()
}
