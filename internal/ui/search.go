package ui

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/peano88/gerardo/internal/gerrit"
)

const (
	// suggestDelay lets typing settle before accounts are suggested.
	suggestDelay = 200 * time.Millisecond
	maxRecent    = 5
	maxSuggested = 6
)

type searchPreset struct {
	name, query string
}

// searchPresets are common searches. A query ending with ":" is completed in
// the input before it runs.
var searchPresets = []searchPreset{
	{"Waiting on me", "attention:self"},
	{"My merged changes", "owner:self status:merged"},
	{"My abandoned changes", "owner:self status:abandoned"},
	{"Reviewed by me", "reviewedby:self -owner:self"},
	{"Merged in the last week", "status:merged -age:1w"},
	{"Open changes of…", "status:open owner:"},
	{"Merged changes of…", "status:merged owner:"},
}

// accountOperand matches a query ending in an account operator being typed.
var accountOperand = regexp.MustCompile(`(?:^|\s)-?(?:owner|reviewer|author|committer|cc|uploader|attention|reviewedby|from):("?)([^\s"]*)$`)

type suggestTickMsg struct {
	seq  int
	text string
}

type accountsSuggestedMsg struct {
	text     string
	accounts []gerrit.AccountInfo
	err      error
}

// searchItem is a row under the input: a suggested account, or a query.
type searchItem struct {
	account *gerrit.AccountInfo
	name    string
	query   string
}

type searchScreen struct {
	client *gerrit.Client
	self   *gerrit.AccountInfo
	recent *[]string
	// replace pops the results the search was opened from.
	replace bool

	input       textinput.Model
	cursor      int
	suggestions []gerrit.AccountInfo
	suggestSeq  int
	err         error
	width       int
	height      int
}

func newSearchScreen(client *gerrit.Client, query string, self *gerrit.AccountInfo, recent *[]string, replace bool) *searchScreen {
	in := textinput.New()
	in.Prompt = "› "
	in.PromptStyle = hintKeyStyle
	in.Placeholder = "owner:self status:merged"
	in.SetValue(query)
	in.CursorEnd()
	return &searchScreen{client: client, self: self, recent: recent, replace: replace, input: in, cursor: -1}
}

func (s *searchScreen) Init() tea.Cmd {
	return s.input.Focus()
}

func (s *searchScreen) items() []searchItem {
	var items []searchItem
	if len(s.suggestions) > 0 {
		for i := range s.suggestions {
			items = append(items, searchItem{account: &s.suggestions[i]})
		}
		return items
	}
	for _, q := range *s.recent {
		items = append(items, searchItem{query: q})
	}
	for _, p := range searchPresets {
		items = append(items, searchItem{name: p.name, query: p.query})
	}
	return items
}

func (s *searchScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case suggestTickMsg:
		if msg.seq != s.suggestSeq {
			return s, nil
		}
		client, text := s.client, msg.text
		return s, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
			defer cancel()
			accounts, err := client.SuggestAccounts(ctx, text, maxSuggested)
			return accountsSuggestedMsg{text: text, accounts: accounts, err: err}
		}

	case accountsSuggestedMsg:
		if partial, ok := s.accountPartial(); !ok || partial != msg.text {
			return s, nil
		}
		s.err, s.suggestions, s.cursor = msg.err, msg.accounts, -1
		return s, nil

	case tea.KeyMsg:
		items := s.items()
		switch msg.String() {
		case "esc":
			return s, pop
		case "up", "ctrl+p":
			s.cursor = max(s.cursor-1, -1)
			return s, nil
		case "down", "ctrl+n":
			s.cursor = min(s.cursor+1, len(items)-1)
			return s, nil
		case "tab":
			switch {
			case len(s.suggestions) > 0:
				s.complete(s.suggestions[max(s.cursor, 0)])
			case s.cursor >= 0:
				s.setQuery(items[s.cursor].query)
			}
			return s, s.scheduleSuggest()
		case "enter":
			if s.cursor < 0 {
				return s, s.run(s.input.Value())
			}
			item := items[s.cursor]
			switch {
			case item.account != nil:
				s.complete(*item.account)
			case strings.HasSuffix(item.query, ":"):
				s.setQuery(item.query)
			default:
				return s, s.run(item.query)
			}
			return s, s.scheduleSuggest()
		}
	}

	before := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if s.input.Value() != before {
		s.cursor, s.err = -1, nil
		return s, tea.Batch(cmd, s.scheduleSuggest())
	}
	return s, cmd
}

func (s *searchScreen) setQuery(q string) {
	s.input.SetValue(q)
	s.input.CursorEnd()
	s.cursor = -1
}

// accountPartial returns the account being typed after an account operator.
func (s *searchScreen) accountPartial() (string, bool) {
	m := accountOperand.FindStringSubmatch(s.input.Value())
	if m == nil {
		return "", false
	}
	return m[2], true
}

// scheduleSuggest suggests accounts for the operand being typed, once the
// typing settles.
func (s *searchScreen) scheduleSuggest() tea.Cmd {
	s.suggestSeq++
	partial, ok := s.accountPartial()
	if !ok || len([]rune(partial)) < 2 || partial == "self" {
		s.suggestions = nil
		return nil
	}
	seq := s.suggestSeq
	return tea.Tick(suggestDelay, func(time.Time) tea.Msg { return suggestTickMsg{seq: seq, text: partial} })
}

// complete replaces the account being typed with a.
func (s *searchScreen) complete(a gerrit.AccountInfo) {
	v := s.input.Value()
	loc := accountOperand.FindStringSubmatchIndex(v)
	if loc == nil {
		return
	}
	s.setQuery(v[:loc[2]] + accountQuery(a) + " ")
	s.suggestions = nil
}

// accountQuery identifies an account in a query, preferring unambiguous ids.
func accountQuery(a gerrit.AccountInfo) string {
	switch {
	case a.Username != "":
		return a.Username
	case a.Email != "":
		return a.Email
	default:
		return `"` + a.Name + `"`
	}
}

// run shows the results of query in place of the search, remembering it.
func (s *searchScreen) run(query string) tea.Cmd {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	recent := slices.DeleteFunc(*s.recent, func(q string) bool { return q == query })
	*s.recent = append([]string{query}, recent[:min(len(recent), maxRecent-1)]...)

	cmds := []tea.Cmd{pop}
	if s.replace {
		cmds = append(cmds, pop)
	}
	cmds = append(cmds, push(newResults(s.client, query, s.self, s.recent)))
	return tea.Sequence(cmds...)
}

var (
	searchItemStyle = lipgloss.NewStyle().PaddingLeft(1)
	searchSelStyle  = lipgloss.NewStyle().Background(selectedBg).Bold(true)
)

func (s *searchScreen) View() string {
	var b strings.Builder
	b.WriteString("\n " + s.input.View() + "\n\n")

	items := s.items()
	heading := ""
	for i, item := range items {
		var h, line string
		switch {
		case item.account != nil:
			h = "Accounts"
			line = item.account.DisplayName()
			if item.account.Email != "" {
				line += "  " + dimStyle.Render(item.account.Email)
			}
		case item.name != "":
			h = "Presets"
			line = lipgloss.NewStyle().Width(26).Render(item.name) + dimStyle.Render(item.query)
		default:
			h = "Recent"
			line = item.query
		}
		if h != heading {
			if heading != "" {
				b.WriteString("\n")
			}
			b.WriteString(" " + sectionStyle.Render(h) + "\n")
			heading = h
		}
		bar := "  "
		if i == s.cursor {
			bar = lipgloss.NewStyle().Foreground(accent).Render("▌ ")
			line = searchSelStyle.Render(ansi.Strip(line))
		}
		b.WriteString(searchItemStyle.Render(bar+line) + "\n")
	}

	b.WriteString("\n " + dimStyle.Render("Gerrit query syntax: owner:NAME reviewer:NAME status:merged project:P -age:2w is:wip …"))
	return lipgloss.NewStyle().Height(s.height).MaxHeight(s.height).Render(b.String())
}

func (s *searchScreen) Title() string {
	return "Search"
}

func (s *searchScreen) Status() string {
	if s.err != nil {
		return status(false, s.err, "")
	}
	return keyHints("enter", "search", "↑/↓", "select", "tab", "complete", "esc", "cancel")
}

func (s *searchScreen) SetSize(width, height int) {
	s.width, s.height = width, height
	s.input.Width = max(width-4, 20)
}
