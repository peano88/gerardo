// Package ui implements the terminal screens of gerardo.
package ui

import (
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/peano88/gerardo/internal/gerrit"
)

const requestTimeout = 20 * time.Second

// screen is one level of the navigation stack. Screens own their loading and
// error state and receive every message while they are on top of the stack.
type screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (screen, tea.Cmd)
	View() string
	Status() string
	// Title is the breadcrumb of the screen in the title bar.
	Title() string
	SetSize(width, height int)
}

// titleInfoer is a screen showing information on the right of the title bar.
type titleInfoer interface {
	TitleInfo() string
}

type pushMsg struct{ screen screen }

type popMsg struct{}

// quitMsg asks the app to quit once the writes running are done.
type quitMsg struct{}

func quit() tea.Msg { return quitMsg{} }

// writes counts the requests changing something on the server that are
// running, so that quitting does not cut them off.
var writes atomic.Int32

// write runs cmd as a request changing something on the server.
func write(cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		writes.Add(1)
		defer writes.Add(-1)
		return cmd()
	}
}

// focusMsg tells the screen uncovered by a pop that it is on top again.
type focusMsg struct{}

func push(s screen) tea.Cmd {
	return func() tea.Msg { return pushMsg{screen: s} }
}

func pop() tea.Msg { return popMsg{} }

// App is the root Bubble Tea model holding the navigation stack.
type App struct {
	stack  []screen
	host   string
	width  int
	height int
	// quitting is set while waiting for writes to end before quitting.
	quitting bool
}

// New creates the application starting on the dashboard for query.
func New(client *gerrit.Client, query string) App {
	return App{stack: []screen{newDashboard(client, query)}, host: hostOf(client)}
}

// Init initializes the first screen.
func (a App) Init() tea.Cmd {
	return a.top().Init()
}

// Update handles navigation and forwards everything else to the top screen.
// While quitting, it waits for the writes running to end.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	a, cmd := a.update(msg)
	if a.quitting && writes.Load() == 0 {
		return a, tea.Quit
	}
	return a, cmd
}

func (a App) update(msg tea.Msg) (App, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		for _, s := range a.stack {
			s.SetSize(a.bodySize())
		}
		return a, nil

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c" && a.quitting:
			return a, tea.Quit
		case msg.String() == "ctrl+c":
			a.quitting = true
			return a, nil
		case a.quitting:
			return a, nil
		}

	case quitMsg:
		a.quitting = true
		return a, nil

	case pushMsg:
		msg.screen.SetSize(a.bodySize())
		a.stack = append(a.stack, msg.screen)
		return a, msg.screen.Init()

	case popMsg:
		if len(a.stack) == 1 {
			a.quitting = true
			return a, nil
		}
		a.stack = a.stack[:len(a.stack)-1]
		return a, func() tea.Msg { return focusMsg{} }
	}

	s, cmd := a.top().Update(msg)
	a.stack[len(a.stack)-1] = s
	return a, cmd
}

// View renders the top screen and its status line.
func (a App) View() string {
	status := a.top().Status()
	if a.quitting {
		status = dimStyle.Render("saving… quitting once done   ") + keyHints("ctrl+c", "quit now")
	}
	return lipgloss.JoinVertical(lipgloss.Left, a.titleBar(), a.top().View(), status)
}

var (
	badgeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("12")).Padding(0, 1)
	crumbSep    = dimStyle.Render(" › ")
	activeCrumb = lipgloss.NewStyle().Bold(true)
)

// titleBar shows the server and the breadcrumbs of the navigation stack,
// with the information of the top screen on the right.
func (a App) titleBar() string {
	var crumbs []string
	for i, s := range a.stack {
		if i == len(a.stack)-1 {
			crumbs = append(crumbs, activeCrumb.Render(s.Title()))
		} else {
			crumbs = append(crumbs, dimStyle.Render(s.Title()))
		}
	}
	left := badgeStyle.Render("gerardo") + " "
	if a.host != "" {
		left += dimStyle.Render(a.host) + crumbSep
	}
	left += strings.Join(crumbs, crumbSep)

	var right string
	if t, ok := a.top().(titleInfoer); ok {
		right = t.TitleInfo()
	}
	gap := a.width - ansi.StringWidth(left) - ansi.StringWidth(right) - 1
	if right == "" || gap < 2 {
		return ansi.Truncate(left, a.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

func hostOf(c *gerrit.Client) string {
	if c == nil {
		return ""
	}
	u, err := url.Parse(c.BaseURL())
	if err != nil || u.Host == "" {
		return c.BaseURL()
	}
	return u.Host
}

func (a App) top() screen {
	return a.stack[len(a.stack)-1]
}

func (a App) bodySize() (int, int) {
	return a.width, max(a.height-2, 1)
}

var hintKeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)

// keyHints renders key, description pairs for the status line.
func keyHints(pairs ...string) string {
	var hints []string
	for i := 0; i+1 < len(pairs); i += 2 {
		hints = append(hints, hintKeyStyle.Render(pairs[i])+" "+dimStyle.Render(pairs[i+1]))
	}
	return strings.Join(hints, "   ")
}

func status(loading bool, err error, help string) string {
	switch {
	case loading:
		return dimStyle.Render("loading…")
	case err != nil:
		return errStyle.Render(err.Error())
	default:
		return dimStyle.Render(help)
	}
}
