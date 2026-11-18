package ui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type helpScreen struct {
	title    string
	text     string
	viewport viewport.Model
}

func newHelpScreen(title, text string) *helpScreen {
	return &helpScreen{title: title, text: text, viewport: viewport.New(0, 0)}
}

func (s *helpScreen) Init() tea.Cmd {
	return nil
}

func (s *helpScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "q", "esc", "?":
			return s, pop
		}
	}
	var cmd tea.Cmd
	s.viewport, cmd = s.viewport.Update(msg)
	return s, cmd
}

func (s *helpScreen) View() string {
	return s.viewport.View()
}

func (s *helpScreen) Status() string {
	return status(false, nil, s.title+" · esc close")
}

func (s *helpScreen) SetSize(width, height int) {
	s.viewport.Width, s.viewport.Height = width, height
	s.viewport.SetContent(titleStyle.Render(s.title) + "\n\n" + s.text)
}
