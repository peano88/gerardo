package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

// previewGap separates the list from the preview.
const previewGap = 2

var (
	previewBg          = lipgloss.AdaptiveColor{Light: "255", Dark: "235"}
	previewBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "240"})
	// previewFrame has no top border: previewTop draws it, titled, in line
	// with the column header of the list.
	previewFrame = lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), false, true, true).
			BorderForeground(previewBorderStyle.GetForeground()).Background(previewBg).Padding(0, 1)
)

// previewTop is the top border of a preview frame as wide as width.
func previewTop(title string, width int) string {
	rule := max(width-ansi.StringWidth(title)-5, 0)
	return previewBorderStyle.Render("╭─ ") + columnStyle.Render(title) +
		previewBorderStyle.Render(" "+strings.Repeat("─", rule)+"╮")
}

// fillBackground paints bg behind every line of s, padded to width, also
// after the resets of its styled spans.
func fillBackground(s string, width int, bg lipgloss.TerminalColor) string {
	on, _, _ := strings.Cut(lipgloss.NewStyle().Background(bg).Render(" "), " ")
	if on == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		pad := strings.Repeat(" ", max(width-ansi.StringWidth(l), 0))
		lines[i] = on + strings.ReplaceAll(l, "\x1b[0m", "\x1b[0m"+on) + pad + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

type changesLoadedMsg struct {
	// gen is the load generation the page belongs to, start its offset.
	gen     int
	start   int
	changes []gerrit.ChangeInfo
	more    bool
	self    *gerrit.AccountInfo
	err     error
}

// dashItem is a dashboard row: a change, or a header when change is nil,
// of a section or, when project is set, of the changes of a project.
type dashItem struct {
	change  *gerrit.ChangeInfo
	header  string
	project bool
}

// groupChanges puts the changes owned by selfID first, each group under a
// header; without a known selfID the changes are listed as they come. With
// byProject, the changes of each group are listed by project, in the order
// of their first change, when there are several projects.
func groupChanges(changes []gerrit.ChangeInfo, selfID int, byProject bool) []dashItem {
	var mine, others []*gerrit.ChangeInfo
	projects := map[string]bool{}
	for i := range changes {
		projects[changes[i].Project] = true
		if selfID != 0 && changes[i].Owner.AccountID == selfID {
			mine = append(mine, &changes[i])
		} else {
			others = append(others, &changes[i])
		}
	}
	byProject = byProject && len(projects) > 1

	var items []dashItem
	add := func(title string, list []*gerrit.ChangeInfo) {
		if len(list) == 0 {
			return
		}
		if title != "" {
			items = append(items, dashItem{header: fmt.Sprintf("▾ %s (%d)", title, len(list))})
		}
		if !byProject {
			for _, c := range list {
				items = append(items, dashItem{change: c})
			}
			return
		}
		var order []string
		perProject := map[string][]*gerrit.ChangeInfo{}
		for _, c := range list {
			if _, ok := perProject[c.Project]; !ok {
				order = append(order, c.Project)
			}
			perProject[c.Project] = append(perProject[c.Project], c)
		}
		for _, p := range order {
			items = append(items, dashItem{header: fmt.Sprintf("%s · %d", p, len(perProject[p])), project: true})
			for _, c := range perProject[p] {
				items = append(items, dashItem{change: c})
			}
		}
	}
	if selfID == 0 {
		add("", others)
		return items
	}
	add("My changes", mine)
	add("Reviews", others)
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
	// showHidden lists the changes hidden by the path filter too.
	showHidden bool
	// byProject groups the changes by project.
	byProject bool
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
		byProject:   GroupByProject,
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

// listed returns the changes to list, and how many the path filter hides.
func (d *dashboard) listed() ([]gerrit.ChangeInfo, int) {
	if d.showHidden || !paths.active() {
		return d.changes, 0
	}
	var out []gerrit.ChangeInfo
	for i := range d.changes {
		if !paths.hides(&d.changes[i]) {
			out = append(out, d.changes[i])
		}
	}
	return out, len(d.changes) - len(out)
}

// relist lists the changes again, keeping the selection.
func (d *dashboard) relist() {
	prev, _ := d.selected()
	listed, _ := d.listed()
	d.items = groupChanges(listed, d.groupBy(), d.byProject)
	d.selectNumber(prev)
}

func (d *dashboard) fetch(start, limit int) tea.Cmd {
	client, query, self, gen, withFiles := d.client, d.query, d.self, d.gen, paths.active()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		var (
			wg                sync.WaitGroup
			changes           []gerrit.ChangeInfo
			more              bool
			queryErr, selfErr error
		)
		wg.Go(func() { changes, more, queryErr = client.QueryChanges(ctx, query, start, limit, withFiles) })
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
			if msg.start == 0 {
				d.changes = msg.changes
				d.previewFor = 0
			} else {
				d.changes = append(d.changes, msg.changes...)
			}
			d.more = msg.more
			d.relist()
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
			if !paths.active() {
				d.notice = notice{text: "set hidden_paths in the config file to hide changes"}
				return d, nil
			}
			d.showHidden = !d.showHidden
			d.relist()
			return d, tea.Batch(d.schedulePreview(), d.maybeLoadMore())
		case "p":
			d.byProject = !d.byProject
			d.relist()
			return d, d.schedulePreview()
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
		frame := previewFrame.Render(fillBackground(d.preview.View(), d.preview.Width, previewBg))
		preview := lipgloss.NewStyle().MarginLeft(previewGap).
			Render(previewTop("Preview", lipgloss.Width(frame)) + "\n" + frame)
		body = lipgloss.JoinHorizontal(lipgloss.Top, d.listView(d.listWidth()), preview)
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
	group := "group by project"
	if d.byProject {
		group = "ungroup"
	}
	hints := []string{"enter", "open", "/", "search", "p", group, "y", "copy number"}
	if d.width >= previewMinWidth {
		hints = append(hints, "J/K", "scroll preview", "P", "toggle preview")
	}
	if paths.active() {
		if d.showHidden {
			hints = append(hints, "H", "hide filtered")
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
		d.preview.Width, d.preview.Height = d.width-d.listWidth()-previewGap-previewFrame.GetHorizontalFrameSize(), d.height-previewFrame.GetVerticalFrameSize()-1
		if d.previewFor != 0 {
			d.renderPreview()
		}
	}
	d.scroll()
}
