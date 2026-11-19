package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

// fileSelectedMsg tells the files screen which file the diff screen moved to,
// so that going back keeps the cursor on the last viewed file.
type fileSelectedMsg struct{ index int }

type commentsLoadedMsg struct {
	comments changeComments
	err      error
}

// changeComments holds the published comments of a change, the unresolved
// threads ported onto its current revision and the user's drafts.
type changeComments struct {
	comments, ported, drafts map[string][]gerrit.CommentInfo
}

func fetchComments(ctx context.Context, client *gerrit.Client, change *gerrit.ChangeInfo) (changeComments, error) {
	number := strconv.Itoa(change.Number)
	var (
		wg                      sync.WaitGroup
		c                       changeComments
		cErr, portErr, draftErr error
	)
	wg.Go(func() { c.comments, cErr = client.ListComments(ctx, number) })
	wg.Go(func() { c.ported, portErr = client.PortedComments(ctx, number, change.CurrentRevision) })
	wg.Go(func() { c.drafts, draftErr = client.ListDrafts(ctx, number) })
	wg.Wait()
	return c, errors.Join(cErr, portErr, draftErr)
}

func (c changeComments) threads(path string, revision int) []gerrit.Thread {
	return gerrit.ThreadsForRevision(path, revision, c.comments, c.ported, c.drafts)
}

type filesScreen struct {
	client   *gerrit.Client
	change   *gerrit.ChangeInfo
	rev      gerrit.RevisionInfo
	paths    []string
	comments changeComments
	cursor   int
	offset   int
	width    int
	height   int
	loading  bool
	err      error
}

func newFilesScreen(client *gerrit.Client, change *gerrit.ChangeInfo) *filesScreen {
	rev, _ := change.Current()
	return &filesScreen{client: client, change: change, rev: rev, paths: rev.SortedPaths()}
}

func (s *filesScreen) Init() tea.Cmd {
	s.loading = true
	client, change := s.client, s.change
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		c, err := fetchComments(ctx, client, change)
		return commentsLoadedMsg{comments: c, err: err}
	}
}

func (s *filesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case commentsLoadedMsg:
		s.loading, s.err = false, msg.err
		s.comments = msg.comments
		return s, nil

	case fileSelectedMsg:
		s.setCursor(msg.index)
		return s, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "backspace":
			return s, pop
		case "r":
			return s, s.Init()
		case "up", "k":
			s.setCursor(s.cursor - 1)
		case "down", "j":
			s.setCursor(s.cursor + 1)
		case "pgup", "ctrl+b":
			s.setCursor(s.cursor - s.listHeight())
		case "pgdown", "ctrl+f":
			s.setCursor(s.cursor + s.listHeight())
		case "ctrl+u":
			s.setCursor(s.cursor - s.listHeight()/2)
		case "ctrl+d":
			s.setCursor(s.cursor + s.listHeight()/2)
		case "home", "g":
			s.setCursor(0)
		case "end", "G":
			s.setCursor(len(s.paths) - 1)
		case "enter":
			if len(s.paths) == 0 {
				return s, nil
			}
			return s, push(newDiffScreen(s.client, s.change, s.paths, s.cursor))
		}
	}
	return s, nil
}

func (s *filesScreen) listHeight() int {
	return max(s.height-1, 1)
}

func (s *filesScreen) setCursor(i int) {
	s.cursor = min(max(i, 0), max(len(s.paths)-1, 0))
	h := s.listHeight()
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+h {
		s.offset = s.cursor - h + 1
	}
	s.offset = min(max(s.offset, 0), max(len(s.paths)-h, 0))
}

// fileThreads counts the threads shown on path, and how many are unresolved.
func (s *filesScreen) fileThreads(path string) (threads, open int) {
	for _, t := range s.comments.threads(path, s.rev.Number) {
		threads++
		if t.Unresolved() {
			open++
		}
	}
	return threads, open
}

type fileColumns struct {
	path, lines, churn, threads int
}

func newFileColumns(width int) fileColumns {
	c := fileColumns{lines: 6, churn: 10, threads: 8}
	c.path = max(width-listGutter-1-c.churn-2*c.lines-c.threads-5*len(cellGap), 20)
	return c
}

func (s *filesScreen) View() string {
	cols := newFileColumns(s.width)
	maxLines := 1
	for _, f := range s.rev.Files {
		maxLines = max(maxLines, f.LinesInserted+f.LinesDeleted)
	}

	head := func(t string, w int, right bool) string {
		return renderCell(columnStyle, lipgloss.NewStyle(), t, w, right)
	}
	lines := []string{strings.Repeat(" ", listGutter) + strings.Join([]string{
		head("", 1, false), head("Path", cols.path, false), head("+", cols.lines, true), head("-", cols.lines, true),
		head("", cols.churn, false), head("Threads", cols.threads, true),
	}, cellGap)}
	for i := s.offset; i < min(len(s.paths), s.offset+s.listHeight()); i++ {
		lines = append(lines, s.fileLine(s.paths[i], cols, maxLines, i == s.cursor))
	}
	return lipgloss.NewStyle().Width(s.width).Height(s.height).Render(strings.Join(lines, "\n"))
}

func (s *filesScreen) fileLine(path string, cols fileColumns, maxLines int, selected bool) string {
	f := s.rev.Files[path]
	base := lipgloss.NewStyle()
	gutter := "   "
	if selected {
		base = base.Background(selectedBg)
		gutter = lipgloss.NewStyle().Foreground(accent).Inherit(base).Render("▌") + base.Render("  ")
	}
	st := cmp.Or(f.Status, "M")

	threads, open := s.fileThreads(path)
	var count string
	countStyle := dimStyle
	switch {
	case open > 0:
		count, countStyle = fmt.Sprintf("● %d/%d", open, threads), unresolvedSt
	case threads > 0:
		count = strconv.Itoa(threads)
	}

	gap := base.Render(cellGap)
	return gutter + strings.Join([]string{
		renderCell(fileStatusStyle(st).Bold(true), base, st, 1, false),
		renderPath(path, cols.path, base, selected),
		renderCell(addStyle, base, plusMinus("+", f.LinesInserted), cols.lines, true),
		renderCell(delStyle, base, plusMinus("-", f.LinesDeleted), cols.lines, true),
		churnBar(f.LinesInserted, f.LinesDeleted, maxLines, cols.churn, base),
		renderCell(countStyle, base, count, cols.threads, true),
	}, gap)
}

func plusMinus(sign string, n int) string {
	if n == 0 {
		return ""
	}
	return sign + strconv.Itoa(n)
}

// renderPath shows the directory of a file dimmed before its name, cutting
// the directory first when the path does not fit.
func renderPath(path string, width int, base lipgloss.Style, selected bool) string {
	var dir, name string
	switch path {
	case gerrit.CommitMessage:
		name = "Commit message"
	case "/MERGE_LIST":
		name = "Merge list"
	default:
		if i := strings.LastIndex(path, "/"); i >= 0 {
			dir, name = path[:i+1], path[i+1:]
		} else {
			name = path
		}
	}
	nameStyle := lipgloss.NewStyle().Inherit(base)
	if selected {
		nameStyle = nameStyle.Bold(true)
	}
	nameWidth, dirWidth := ansi.StringWidth(name), ansi.StringWidth(dir)
	switch room := width - nameWidth; {
	case room <= 1:
		dir, name = "", ansi.Truncate(name, width, "…")
	case dirWidth > room:
		dir = "…" + ansi.TruncateLeft(dir, dirWidth-room+1, "")
	}
	return base.Width(width).Render(dimStyle.Inherit(base).Render(dir) + nameStyle.Render(name))
}

// churnBar draws the inserted and deleted lines of a file relative to the
// largest file of the change.
func churnBar(inserted, deleted, maxLines, width int, base lipgloss.Style) string {
	total := inserted + deleted
	cells := 0
	if total > 0 {
		cells = max(1, total*width/maxLines)
	}
	plus := 0
	if total > 0 {
		plus = (inserted*cells + total/2) / total
	}
	return addStyle.Inherit(base).Render(strings.Repeat("■", plus)) +
		delStyle.Inherit(base).Render(strings.Repeat("■", cells-plus)) +
		base.Render(strings.Repeat(" ", width-cells))
}

func (s *filesScreen) Title() string {
	return fmt.Sprintf("PS%d files", s.rev.Number)
}

// TitleInfo sums up the files of the revision.
func (s *filesScreen) TitleInfo() string {
	inserted, deleted, threads, open := 0, 0, 0, 0
	for _, p := range s.paths {
		f := s.rev.Files[p]
		inserted, deleted = inserted+f.LinesInserted, deleted+f.LinesDeleted
		t, o := s.fileThreads(p)
		threads, open = threads+t, open+o
	}
	info := []string{dimStyle.Render(plural(len(s.paths), "file")), addStyle.Render(fmt.Sprintf("+%d", inserted)) + " " + delStyle.Render(fmt.Sprintf("-%d", deleted))}
	if threads > 0 {
		info = append(info, dimStyle.Render(plural(threads, "thread")))
	}
	if open > 0 {
		info = append(info, unresolvedSt.Render(fmt.Sprintf("● %d open", open)))
	}
	return strings.Join(info, dimStyle.Render("  ·  "))
}

func (s *filesScreen) Status() string {
	return status(s.loading, s.err, keyHints("enter", "diff", "↑/↓", "select", "r", "reload", "esc", "back"))
}

func (s *filesScreen) SetSize(width, height int) {
	s.width, s.height = width, height
	s.setCursor(s.cursor)
}
