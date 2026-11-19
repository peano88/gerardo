package ui

import (
	"os"
	"strconv"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

type copiedMsg struct {
	text string
	err  error
}

// copyToClipboard uses the system clipboard, falling back to an OSC 52
// sequence so copying also works over SSH or without a clipboard tool.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if clipboard.WriteAll(text) == nil {
			return copiedMsg{text: text}
		}
		seq := osc52.New(text)
		if os.Getenv("TMUX") != "" {
			seq = seq.Tmux()
		}
		_, err := seq.WriteTo(os.Stderr)
		return copiedMsg{text: text, err: err}
	}
}

func copyNumber(number int) tea.Cmd {
	return copyToClipboard(strconv.Itoa(number))
}

// notice is a one-off status message, shown until the next key press.
type notice struct {
	text string
	err  error
}

func (n *notice) update(msg tea.Msg) {
	switch msg := msg.(type) {
	case copiedMsg:
		*n = notice{text: "copied " + msg.text, err: msg.err}
	case tea.KeyMsg:
		*n = notice{}
	}
}

func (n notice) status(loading bool, err error, help string) string {
	switch {
	case n.err != nil:
		return status(false, n.err, "")
	case n.text != "":
		return status(false, nil, n.text)
	default:
		return status(loading, err, help)
	}
}
