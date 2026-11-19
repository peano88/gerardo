package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

func typeText(s screen, text string) tea.Cmd {
	var cmd tea.Cmd
	for _, r := range text {
		_, cmd = s.Update(keyRunes(string(r)))
	}
	return cmd
}

func TestSearchSuggestsAndCompletesAccounts(t *testing.T) {
	s := newSearchScreen(nil, "", nil, &[]string{}, false)
	s.Init()
	if cmd := typeText(s, "status:open owner:bo"); cmd == nil {
		t.Fatal("typing an account should schedule suggestions")
	}
	// A late answer for an older prefix is ignored.
	s.Update(accountsSuggestedMsg{text: "b", accounts: []gerrit.AccountInfo{{Name: "Ben"}}})
	if len(s.suggestions) != 0 {
		t.Fatal("suggestions for an older prefix should be ignored")
	}
	s.Update(accountsSuggestedMsg{text: "bo", accounts: []gerrit.AccountInfo{
		{Name: "Bob Builder", Username: "bob", Email: "bob@example.com"},
		{Name: "Bobby Tables", Email: "bobby@example.com"},
	}})
	if !strings.Contains(s.View(), "Bobby Tables") {
		t.Fatalf("suggestions should be listed:\n%s", s.View())
	}

	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := s.input.Value(); got != "status:open owner:bobby@example.com " {
		t.Fatalf("tab should complete the selected account, got %q", got)
	}
	if len(s.suggestions) != 0 {
		t.Fatal("completing should close the suggestions")
	}
}

func TestSearchPresetsAndRecent(t *testing.T) {
	recent := []string{}
	s := newSearchScreen(nil, "", nil, &recent, false)

	// Presets needing an account fill the input instead of running.
	idx := 0
	for i, p := range searchPresets {
		if strings.HasSuffix(p.query, ":") {
			idx = i
			break
		}
	}
	for range idx + 1 {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.input.Value() != searchPresets[idx].query || s.cursor != -1 {
		t.Fatalf("preset should fill the input, got %q", s.input.Value())
	}

	s.setQuery("status:merged")
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || len(recent) != 1 || recent[0] != "status:merged" {
		t.Fatalf("running a search should remember it, recent %q", recent)
	}
	msgs := fmt.Sprintf("%T", cmd())
	if !strings.Contains(msgs, "sequenceMsg") {
		t.Fatalf("running should pop the search and push results, got %s", msgs)
	}

	s = newSearchScreen(nil, "", nil, &recent, false)
	if items := s.items(); items[0].query != "status:merged" || items[0].name != "" {
		t.Fatalf("recent searches should come first, got %+v", items[0])
	}
}
