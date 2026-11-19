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
	// prefetchMargin is how close to the end the cursor gets before the
	// next page is fetched.
	prefetchMargin = 10
)

var previewBorder = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
	BorderForeground(lipgloss.Color("8")).PaddingLeft(1)

type changesLoadedMsg struct {
	// gen is the load generation the page belongs to, start its offset.
	gen     int
	start   int
	changes []gerrit.ChangeInfo
	more    bool
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

// dashboard lists the changes of a query. The root dashboard groups the
// user's own changes first; search results are listed as they come.
type dashboard struct {
	client  *gerrit.Client
	query   string
	root    bool
	recent  *[]string
	changes []gerrit.ChangeInfo
	items   []dashItem
	cursor  int
	offset  int
	self    *gerrit.AccountInfo
	loading bool
	err     error
	// showHidden lists the changes of HiddenProjects too.
	showHidden bool
	// more is set when further pages match; gen tells stale pages apart.
	more        bool
	loadingMore bool
	gen         int
	width       int
	height      int
	notice      notice

	showPreview bool
	preview     viewport.Model
	previews    map[int]*gerrit.ChangeInfo
	previewErr  error
	previewSeq  int
	previewFor  int
}

func newDashboard(client *gerrit.Client, query string) *dashboard {
	d := newResults(client, query, nil, &[]string{})
	d.root = true
	return d
}

// newResults lists the changes matching a search.
func newResults(client *gerrit.Client, query string, self *gerrit.AccountInfo, recent *[]string) *dashboard {
	return &dashboard{
		client:      client,
		query:       query,
		recent:      recent,
		self:        self,
		showPreview: true,
		preview:     viewport.New(0, 0),
		previews:    map[int]*gerrit.ChangeInfo{},
	}
}

// Init loads the first page again, as large as what is already listed.
func (d *dashboard) Init() tea.Cmd {
	d.loading, d.loadingMore = true, false
	d.gen++
	return d.fetch(0, max(PageSize, len(d.changes)))
}

// maybeLoadMore fetches the next page once the cursor nears the end.
func (d *dashboard) maybeLoadMore() tea.Cmd {
	if !d.more || d.loading || d.loadingMore || d.cursor < len(d.items)-prefetchMargin {
		return nil
	}
	d.loadingMore = true
	return d.fetch(len(d.changes), PageSize)
}

// filtering reports whether changes of hidden projects are left out.
func (d *dashboard) filtering() bool {
	return len(HiddenProjects) > 0 && !d.showHidden
}

func (d *dashboard) fetch(start, limit int) tea.Cmd {
	client, query, self, gen := d.client, d.query, d.self, d.gen
	if d.filtering() {
		query = gerrit.ExcludeProjects(query, HiddenProjects, VisibleProjects)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		var (
			wg                sync.WaitGroup
			changes           []gerrit.ChangeInfo
			more              bool
			queryErr, selfErr error
		)
		wg.Go(func() { changes, more, queryErr = client.QueryChanges(ctx, query, start, limit) })
		if self == nil {
			wg.Go(func() { self, selfErr = client.Self(ctx) })
		}
		wg.Wait()
		return changesLoadedMsg{gen: gen, start: start, changes: changes, more: more, self: self, err: errors.Join(queryErr, selfErr)}
	}
}

func (d *dashboard) Update(msg tea.Msg) (screen, tea.Cmd) {
	d.notice.update(msg)
	switch msg := msg.(type) {
	case copiedMsg:
		return d, nil

	case changesLoadedMsg:
		if msg.self != nil {
			d.self = msg.self
		}
		if msg.gen != d.gen {
			return d, nil
		}
		d.err = msg.err
		if msg.start == 0 {
			d.loading = false
		} else {
			d.loadingMore = false
		}
		if msg.err == nil || msg.changes != nil {
			prev, _ := d.selected()
			if msg.start == 0 {
				d.changes = msg.changes
				d.previewFor = 0
			} else {
				d.changes = append(d.changes, msg.changes...)
			}
			d.more = msg.more
			d.items = groupChanges(d.changes, d.groupBy())
			d.selectNumber(prev)
		}
		return d, tea.Batch(d.schedulePreview(), d.maybeLoadMore())

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
			if d.root {
				return d, tea.Quit
			}
			return d, pop
		case "esc", "backspace":
			if !d.root {
				return d, pop
			}
			return d, nil
		case "/":
			prefill := ""
			if !d.root {
				prefill = d.query
			}
			return d, push(newSearchScreen(d.client, prefill, d.self, d.recent, !d.root))
		case "r":
			clear(d.previews)
			return d, d.Init()
		case "H":
			if len(HiddenProjects) == 0 {
				d.notice = notice{text: "set hidden_projects in the config file to hide projects"}
				return d, nil
			}
			d.showHidden = !d.showHidden
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
	return tea.Batch(d.schedulePreview(), d.maybeLoadMore())
}

// groupBy is the account whose changes are grouped first, if any.
func (d *dashboard) groupBy() int {
	if !d.root || d.self == nil {
		return 0
	}
	return d.self.AccountID
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
		d.preview.SetContent(renderChange(c, d.preview.Width, changeView{}))
	default:
		d.preview.SetContent(dimStyle.Render("loading…"))
	}
	d.preview.GotoTop()
}

func (d *dashboard) View() string {
	body := d.listView(d.width)
	if d.split() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, d.listView(d.listWidth()), previewBorder.Height(d.height).Render(d.preview.View()))
	}
	return body
}

func (d *dashboard) Title() string {
	if d.root {
		return "Dashboard"
	}
	return "Search"
}

func (d *dashboard) Status() string {
	hints := []string{"enter", "open", "/", "search", "y", "copy number"}
	if d.width >= previewMinWidth {
		hints = append(hints, "J/K", "scroll preview", "P", "toggle preview")
	}
	if len(HiddenProjects) > 0 {
		if d.showHidden {
			hints = append(hints, "H", "hide projects")
		} else {
			hints = append(hints, "H", "show hidden")
		}
	}
	hints = append(hints, "r", "reload")
	if d.root {
		hints = append(hints, "q", "quit")
	} else {
		hints = append(hints, "esc", "back")
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
		d.preview.Width, d.preview.Height = d.width-d.listWidth()-2, d.height
		if d.previewFor != 0 {
			d.renderPreview()
		}
	}
	d.scroll()
}
