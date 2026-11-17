// Package ui implements the terminal screens of gerrit-tui.
package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

const requestTimeout = 20 * time.Second

// screen is one level of the navigation stack. Screens own their loading and
// error state and receive every message while they are on top of the stack.
type screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (screen, tea.Cmd)
	View() string
	Status() string
	SetSize(width, height int)
}

type pushMsg struct{ screen screen }

type popMsg struct{}

func push(s screen) tea.Cmd {
	return func() tea.Msg { return pushMsg{screen: s} }
}

func pop() tea.Msg { return popMsg{} }

// App is the root Bubble Tea model holding the navigation stack.
type App struct {
	stack  []screen
	width  int
	height int
}

// New creates the application starting on the dashboard for query.
func New(client *gerrit.Client, query string) App {
	return App{stack: []screen{newDashboard(client, query)}}
}

// Init initializes the first screen.
func (a App) Init() tea.Cmd {
	return a.top().Init()
}

// Update handles navigation and forwards everything else to the top screen.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		for _, s := range a.stack {
			s.SetSize(a.bodySize())
		}
		return a, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}

	case pushMsg:
		msg.screen.SetSize(a.bodySize())
		a.stack = append(a.stack, msg.screen)
		return a, msg.screen.Init()

	case popMsg:
		if len(a.stack) == 1 {
			return a, tea.Quit
		}
		a.stack = a.stack[:len(a.stack)-1]
		return a, nil
	}

	s, cmd := a.top().Update(msg)
	a.stack[len(a.stack)-1] = s
	return a, cmd
}

// View renders the top screen and its status line.
func (a App) View() string {
	return lipgloss.JoinVertical(lipgloss.Left, a.top().View(), a.top().Status())
}

func (a App) top() screen {
	return a.stack[len(a.stack)-1]
}

func (a App) bodySize() (int, int) {
	return a.width, max(a.height-1, 1)
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
