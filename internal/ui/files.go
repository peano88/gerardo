package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

// fileSelectedMsg tells the files screen which file the diff screen moved to,
// and the patch sets it compared, so that going back keeps both.
type fileSelectedMsg struct {
	path string
	rng  patchRange
}

type filesLoadedMsg struct {
	rng      patchRange
	files    map[string]gerrit.FileInfo
	comments changeComments
	err      error
}

// changeComments holds the published comments of a change, the unresolved
// threads ported onto a patch set and the user's drafts.
type changeComments struct {
	comments, ported, drafts map[string][]gerrit.CommentInfo
}

func fetchComments(ctx context.Context, client *gerrit.Client, change *gerrit.ChangeInfo, target int) (changeComments, error) {
	number := strconv.Itoa(change.Number)
	var (
		wg                      sync.WaitGroup
		c                       changeComments
		cErr, portErr, draftErr error
	)
	wg.Go(func() { c.comments, cErr = client.ListComments(ctx, number) })
	wg.Go(func() { c.ported, portErr = client.PortedComments(ctx, number, strconv.Itoa(target)) })
	wg.Go(func() { c.drafts, draftErr = client.ListDrafts(ctx, number) })
	wg.Wait()
	return c, errors.Join(cErr, portErr, draftErr)
}

func (c changeComments) threads(path string, r patchRange) []gerrit.Thread {
	return gerrit.ThreadsForRange(path, r.base, r.target, c.comments, c.ported, c.drafts)
}

type filesScreen struct {
	client   *gerrit.Client
	change   *gerrit.ChangeInfo
	rng      patchRange
	files    map[string]gerrit.FileInfo
	paths    []string
	comments changeComments
	cursor   int
	offset   int
	width    int
	height   int
	loading  bool
	err      error
	notice   notice
	dialog   dialog
	// selectPath is the file to put the cursor on once the files are loaded.
	selectPath string
}

func newFilesScreen(client *gerrit.Client, change *gerrit.ChangeInfo) *filesScreen {
	rev, _ := change.Current()
	return &filesScreen{client: client, change: change, rng: latestRange(change), files: rev.Files, paths: gerrit.SortedPaths(rev.Files)}
}

func (s *filesScreen) Init() tea.Cmd {
	s.loading = true
	client, change, rng := s.client, s.change, s.rng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		var (
			wg               sync.WaitGroup
			files            map[string]gerrit.FileInfo
			c                changeComments
			filesErr, comErr error
		)
		wg.Go(func() { files, filesErr = rangeFiles(ctx, client, change, rng) })
		wg.Go(func() { c, comErr = fetchComments(ctx, client, change, rng.target) })
		wg.Wait()
		return filesLoadedMsg{rng: rng, files: files, comments: c, err: errors.Join(filesErr, comErr)}
	}
}

func (s *filesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && s.dialog != nil {
		done, cmd := s.dialog.update(key)
		if done {
			s.dialog = nil
		}
		return s, cmd
	}
	s.notice.update(msg)
	switch msg := msg.(type) {
	case filesLoadedMsg:
		if msg.rng != s.rng {
			return s, nil
		}
		s.loading, s.err = false, msg.err
		if msg.files != nil {
			s.files, s.paths = msg.files, gerrit.SortedPaths(msg.files)
			s.selectFile(s.selectPath)
			s.selectPath = ""
		}
		s.comments = msg.comments
		return s, nil

	case fileSelectedMsg:
		if msg.rng != s.rng {
			s.rng, s.selectPath = msg.rng, msg.path
			return s, s.Init()
		}
		s.selectFile(msg.path)
		return s, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "backspace":
			return s, pop
		case "r":
			return s, s.Init()
		case "p":
			s.dialog, s.notice = openRangeDialog(s.change, s.rng, s.applyRange)
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
			return s, push(newDiffScreen(s.client, s.change, s.rng, s.paths, s.cursor))
		}
	}
	return s, nil
}

// applyRange lists the files of r, keeping the cursor on the same file.
func (s *filesScreen) applyRange(r patchRange) tea.Cmd {
	if r == s.rng {
		return nil
	}
	s.rng = r
	if s.cursor < len(s.paths) {
		s.selectPath = s.paths[s.cursor]
	}
	return s.Init()
}

func (s *filesScreen) selectFile(path string) {
	if i := slices.Index(s.paths, path); i >= 0 {
		s.setCursor(i)
	} else {
		s.setCursor(s.cursor)
	}
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
	for _, t := range s.comments.threads(path, s.rng) {
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
	for _, f := range s.files {
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
	if len(s.paths) == 0 && !s.loading {
		lines = append(lines, strings.Repeat(" ", listGutter)+dimStyle.Render("No files differ between "+s.rng.String()))
	}
	body := lipgloss.NewStyle().Width(s.width).Height(s.height).Render(strings.Join(lines, "\n"))
	if s.dialog != nil {
		return overlay(body, s.dialog.view(s.width), s.width, s.height)
	}
	return body
}

func (s *filesScreen) fileLine(path string, cols fileColumns, maxLines int, selected bool) string {
	f := s.files[path]
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
	if s.rng.base == 0 {
		return fmt.Sprintf("PS%d files", s.rng.target)
	}
	return fmt.Sprintf("PS%d → PS%d files", s.rng.base, s.rng.target)
}

// TitleInfo sums up the files of the revision.
func (s *filesScreen) TitleInfo() string {
	inserted, deleted, threads, open := 0, 0, 0, 0
	for _, p := range s.paths {
		f := s.files[p]
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
	if s.dialog != nil {
		return s.dialog.hints()
	}
	return s.notice.status(s.loading, s.err, keyHints("enter", "diff", "↑/↓", "select", "p", "patch sets", "r", "reload", "esc", "back"))
}

func (s *filesScreen) SetSize(width, height int) {
	s.width, s.height = width, height
	s.setCursor(s.cursor)
}
