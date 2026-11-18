package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

const scissors = "# ------------------------ >8 ------------------------"

// draftRequest describes the draft to save once the comment text is known.
// An empty draftID creates a new draft on revision.
type draftRequest struct {
	revision string
	draftID  string
	input    gerrit.CommentInput
}

type editorClosedMsg struct {
	text string
	req  draftRequest
	err  error
}

func editorCommand() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if fields := strings.Fields(os.Getenv(env)); len(fields) > 0 {
			return fields
		}
	}
	return []string{"vi"}
}

// editorTemplate returns the file content shown in the editor: the initial
// text, then the context below a scissors line, git-commit style.
func editorTemplate(initial string, context []string) string {
	var b strings.Builder
	b.WriteString(initial)
	b.WriteString("\n\n")
	b.WriteString(scissors + "\n")
	b.WriteString("# Do not modify or remove the line above.\n")
	b.WriteString("# Everything below it is ignored; an empty comment is discarded.\n")
	if len(context) > 0 {
		b.WriteString("#\n")
	}
	for _, l := range context {
		b.WriteString(strings.TrimRight("# "+l, " ") + "\n")
	}
	return b.String()
}

// parseEditorText returns the comment written above the scissors line.
func parseEditorText(content string) string {
	if i := strings.Index(content, scissors); i >= 0 {
		content = content[:i]
	}
	return strings.TrimSpace(content)
}

// openEditor suspends the UI and edits the comment in the user's editor.
func openEditor(initial string, context []string, req draftRequest) tea.Cmd {
	f, err := os.CreateTemp("", "gerrit-comment-*.md")
	if err != nil {
		return func() tea.Msg { return editorClosedMsg{err: fmt.Errorf("create comment file: %w", err)} }
	}
	name := f.Name()
	_, err = f.WriteString(editorTemplate(initial, context))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return func() tea.Msg { return editorClosedMsg{err: fmt.Errorf("write comment file: %w", err)} }
	}

	editor := editorCommand()
	cmd := exec.Command(editor[0], append(editor[1:], name)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer func() { _ = os.Remove(name) }()
		if err != nil {
			return editorClosedMsg{err: fmt.Errorf("editor: %w", err)}
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return editorClosedMsg{err: fmt.Errorf("read comment file: %w", err)}
		}
		return editorClosedMsg{text: parseEditorText(string(data)), req: req}
	})
}
