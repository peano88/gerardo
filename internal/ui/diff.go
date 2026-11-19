package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

const (
	diffContext = 3
	scrollOff   = 3
	hScrollStep = 8
)

type diffLoadedMsg struct {
	path    string
	diff    *gerrit.DiffInfo
	syntax  *syntaxLines
	threads []gerrit.Thread
	err     error
}

type draftSavedMsg struct {
	notice string
	err    error
}

// anchor identifies the cursor position across re-renders.
type anchor struct {
	a, b      int
	threadID  string
	fileLevel bool
}

type diffScreen struct {
	client      *gerrit.Client
	change      *gerrit.ChangeInfo
	paths       []string
	index       int
	fullContext bool
	diff        *gerrit.DiffInfo
	syntax      *syntaxLines
	threads     []gerrit.Thread
	rendered    renderedDiff
	cursor      int
	count       int
	pending     string
	viewport    viewport.Model
	loading     bool
	err         error
	notice      string
	// focusThread is the root of the thread to put the cursor on once loaded.
	focusThread string

	searching    bool
	searchInput  textinput.Model
	query        string
	prevQuery    string
	searchOrigin int
}

func newDiffScreen(client *gerrit.Client, change *gerrit.ChangeInfo, paths []string, index int) *diffScreen {
	vp := viewport.New(0, 0)
	vp.SetHorizontalStep(hScrollStep)
	in := textinput.New()
	in.Prompt = "/"
	return &diffScreen{client: client, change: change, paths: paths, index: index, viewport: vp, searchInput: in}
}

func (s *diffScreen) path() string {
	return s.paths[s.index]
}

func (s *diffScreen) revisionNumber() int {
	rev, _ := s.change.Current()
	return rev.Number
}

func (s *diffScreen) Init() tea.Cmd {
	s.loading = true
	client, change, path, number := s.client, s.change, s.path(), s.revisionNumber()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()

		var (
			wg                  sync.WaitGroup
			diff                *gerrit.DiffInfo
			comments            changeComments
			diffErr, commentErr error
		)
		wg.Go(func() { diff, diffErr = client.GetDiff(ctx, strconv.Itoa(change.Number), change.CurrentRevision, path) })
		wg.Go(func() { comments, commentErr = fetchComments(ctx, client, change) })
		wg.Wait()

		if diffErr != nil {
			return diffLoadedMsg{path: path, err: diffErr}
		}
		return diffLoadedMsg{
			path:    path,
			diff:    diff,
			syntax:  highlightDiff(path, diff),
			threads: comments.threads(path, number),
			err:     commentErr,
		}
	}
}

func (s *diffScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case diffLoadedMsg:
		if msg.path != s.path() {
			return s, nil
		}
		s.loading, s.err = false, msg.err
		if msg.diff != nil {
			reload := s.diff != nil
			keep := s.anchor()
			s.diff, s.syntax, s.threads = msg.diff, msg.syntax, msg.threads
			s.rerender()
			if reload {
				s.cursor = s.findAnchor(keep)
			} else {
				s.cursor = 0
				s.viewport.GotoTop()
				s.viewport.SetXOffset(0)
			}
			if s.focusThread != "" {
				if row, ok := s.threadRow(s.focusThread); ok {
					s.cursor = row
				} else {
					s.notice = "the thread is not shown on the current patch set"
				}
				s.focusThread = ""
			}
			s.refresh()
		}
		return s, nil

	case editorClosedMsg:
		switch {
		case msg.err != nil:
			s.err = msg.err
			return s, nil
		case msg.text == "":
			s.notice = "empty comment, nothing saved"
			return s, nil
		}
		msg.req.input.Message = msg.text
		return s, s.saveDraft(msg.req, "draft saved")

	case draftSavedMsg:
		s.loading, s.err, s.notice = false, msg.err, msg.notice
		if msg.err != nil {
			return s, nil
		}
		return s, s.Init()

	case changeUpdatedMsg:
		return s, s.Init()

	case tea.KeyMsg:
		if s.searching {
			return s, s.handleSearchKey(msg)
		}
		return s.handleKey(msg.String())
	}
	return s, nil
}

func (s *diffScreen) startSearch() tea.Cmd {
	s.searching, s.prevQuery, s.searchOrigin = true, s.query, s.cursor
	s.searchInput.SetValue("")
	return s.searchInput.Focus()
}

// handleSearchKey edits the search query, moving to the first match after the
// cursor as the query is typed (vim's incsearch).
func (s *diffScreen) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		s.searching = false
		s.searchInput.Blur()
		s.setQuery(s.prevQuery)
		s.setCursor(s.searchOrigin)
		return nil
	case "enter":
		s.searching = false
		s.searchInput.Blur()
		if s.query == "" {
			s.setQuery(s.prevQuery)
			return nil
		}
		if len(s.rendered.matchRows) == 0 {
			s.notice = s.notFound()
		}
		return nil
	}

	var cmd tea.Cmd
	s.searchInput, cmd = s.searchInput.Update(msg)
	if v := s.searchInput.Value(); v != s.query {
		s.setQuery(v)
		s.cursor = s.searchOrigin
		if !s.jumpMatch(1, true) {
			s.setCursor(s.searchOrigin)
		}
		s.notice = ""
	}
	return cmd
}

func (s *diffScreen) setQuery(q string) {
	if q == s.query {
		return
	}
	keep := s.anchor()
	s.query = q
	s.rerender()
	s.cursor = s.findAnchor(keep)
	s.refresh()
}

func (s *diffScreen) notFound() string {
	if s.rendered.hiddenMatches > 0 {
		return fmt.Sprintf("%q only matches folded lines, zR to unfold", s.query)
	}
	return fmt.Sprintf("pattern not found: %s", s.query)
}

// jumpMatch moves the cursor to the next (direction 1) or previous (-1)
// search match, wrapping around. inclusive accepts a match on the cursor row.
func (s *diffScreen) jumpMatch(direction int, inclusive bool) bool {
	rows := s.rendered.matchRows
	if len(rows) == 0 {
		return false
	}
	if direction > 0 {
		for _, r := range rows {
			if r > s.cursor || (inclusive && r == s.cursor) {
				s.setCursor(r)
				return true
			}
		}
		s.setCursor(rows[0])
		s.notice = "search hit BOTTOM, continuing at TOP"
		return true
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i] < s.cursor {
			s.setCursor(rows[i])
			return true
		}
	}
	s.setCursor(rows[len(rows)-1])
	s.notice = "search hit TOP, continuing at BOTTOM"
	return true
}

func (s *diffScreen) searchNext(direction int) {
	switch {
	case s.query == "":
		s.notice = "no previous search, use / to search"
	case !s.jumpMatch(direction, false):
		s.notice = s.notFound()
	}
}

func (s *diffScreen) handleKey(key string) (screen, tea.Cmd) {
	s.notice, s.err = "", nil
	if p := s.pending; p != "" {
		s.pending = ""
		return s, s.handlePending(p, key)
	}
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && (key != "0" || s.count > 0) {
		s.count = s.count*10 + int(key[0]-'0')
		return s, nil
	}
	count := s.count
	s.count = 0
	n := max(count, 1)
	half := max(s.viewport.Height/2, 1)

	switch key {
	case "esc":
		if s.query != "" {
			s.setQuery("")
			return s, nil
		}
		return s, s.back()
	case "q", "backspace":
		return s, s.back()
	case "/":
		return s, s.startSearch()
	case "n":
		s.searchNext(1)
	case "N":
		s.searchNext(-1)
	case "?":
		return s, push(newHelpScreen("Diff keys", diffHelp))
	case "j", "down":
		s.move(n)
	case "k", "up":
		s.move(-n)
	case "ctrl+d":
		s.move(n * half)
	case "ctrl+u":
		s.move(-n * half)
	case "ctrl+f", "pgdown", " ":
		s.move(n * s.viewport.Height)
	case "ctrl+b", "pgup":
		s.move(-n * s.viewport.Height)
	case "G":
		if count > 0 {
			s.gotoFileLine(count)
		} else {
			s.setCursor(len(s.rendered.lines) - 1)
		}
	case "g", "z", "x":
		if key == "x" {
			if _, ok := s.cursorDraft(); !ok {
				s.notice = "no draft in this thread"
				return s, nil
			}
			s.notice = "press x again to delete the draft"
		}
		s.pending = key
	case "h", "left":
		s.viewport.ScrollLeft(hScrollStep)
	case "l", "right":
		s.viewport.ScrollRight(hScrollStep)
	case "0":
		s.viewport.SetXOffset(0)
	case "]":
		s.jumpThread(1)
	case "[":
		s.jumpThread(-1)
	case "tab":
		return s, s.moveTo(s.index + 1)
	case "shift+tab":
		return s, s.moveTo(s.index - 1)
	case "c", "i", "enter":
		return s, s.comment()
	case "r":
		return s, s.reply()
	case "A":
		return s, s.quickReply("Ack")
	case "D":
		return s, s.quickReply("Done")
	case "e":
		return s, s.editDraft()
	case "u":
		return s, s.toggleResolved()
	case "v":
		return s, push(newReviewScreen(s.client, s.change))
	case "ctrl+l":
		return s, s.Init()
	}
	return s, nil
}

func (s *diffScreen) back() tea.Cmd {
	index := s.index
	return tea.Sequence(pop, func() tea.Msg { return fileSelectedMsg{index: index} })
}

func (s *diffScreen) handlePending(pending, key string) tea.Cmd {
	switch pending + key {
	case "gg":
		s.setCursor(0)
	case "zz":
		s.viewport.SetYOffset(max(s.cursor-s.viewport.Height/2, 0))
	case "zR":
		s.setFolding(true)
	case "zM":
		s.setFolding(false)
	case "za":
		s.setFolding(!s.fullContext)
	case "xx":
		return s.deleteDraft()
	}
	return nil
}

func (s *diffScreen) setFolding(full bool) {
	if s.diff == nil || s.fullContext == full {
		return
	}
	keep := s.anchor()
	s.fullContext = full
	s.rerender()
	s.cursor = s.findAnchor(keep)
	s.refresh()
}

func (s *diffScreen) moveTo(index int) tea.Cmd {
	if index < 0 || index >= len(s.paths) {
		return nil
	}
	s.index = index
	s.diff, s.syntax, s.threads, s.rendered = nil, nil, nil, renderedDiff{}
	s.cursor = 0
	s.viewport.SetContent("")
	return s.Init()
}

func (s *diffScreen) target() rowTarget {
	if s.cursor < 0 || s.cursor >= len(s.rendered.targets) {
		return noTarget
	}
	return s.rendered.targets[s.cursor]
}

func (s *diffScreen) anchor() anchor {
	t := s.target()
	switch {
	case t.thread >= 0 && t.thread < len(s.threads):
		return anchor{threadID: s.threads[t.thread].Root().ID}
	case t.hasLine:
		return anchor{a: t.line.a, b: t.line.b}
	default:
		return anchor{fileLevel: t.fileLevel}
	}
}

// findAnchor returns the row of an anchor in the current rendering, or the
// closest following diff line when the exact row no longer exists.
func (s *diffScreen) findAnchor(an anchor) int {
	for i, t := range s.rendered.targets {
		switch {
		case an.threadID != "" && t.thread >= 0 && s.threads[t.thread].Root().ID == an.threadID:
			return i
		case an.threadID == "" && t.hasLine && t.line.a == an.a && t.line.b == an.b && (an.a != 0 || an.b != 0):
			return i
		}
	}
	for i, t := range s.rendered.targets {
		if t.hasLine && ((an.b > 0 && t.line.b >= an.b) || (an.b == 0 && an.a > 0 && t.line.a >= an.a)) {
			return i
		}
	}
	return min(s.cursor, max(len(s.rendered.lines)-1, 0))
}

// threadRow returns the row of the thread started by rootID.
func (s *diffScreen) threadRow(rootID string) (int, bool) {
	for i, t := range s.rendered.targets {
		if t.thread >= 0 && s.threads[t.thread].Root().ID == rootID {
			return i, true
		}
	}
	return 0, false
}

func (s *diffScreen) move(delta int) {
	s.setCursor(s.cursor + delta)
}

func (s *diffScreen) setCursor(row int) {
	s.cursor = max(min(row, len(s.rendered.lines)-1), 0)
	s.refresh()
}

func (s *diffScreen) gotoFileLine(line int) {
	for i, t := range s.rendered.targets {
		if t.hasLine && t.line.b >= line {
			s.setCursor(i)
			return
		}
	}
	s.setCursor(len(s.rendered.lines) - 1)
}

// jumpThread moves the cursor to the next (direction 1) or previous (-1) thread.
func (s *diffScreen) jumpThread(direction int) {
	offsets := s.rendered.threadOffsets
	for i := range offsets {
		off := offsets[i]
		if direction < 0 {
			off = offsets[len(offsets)-1-i]
		}
		if (direction > 0 && off > s.cursor) || (direction < 0 && off < s.cursor && s.target().thread != s.rendered.targets[off].thread) {
			s.setCursor(off)
			return
		}
	}
}

func (s *diffScreen) rerender() {
	if s.diff == nil {
		return
	}
	context := diffContext
	if s.fullContext {
		context = -1
	}
	// One column is reserved for the cursor marker.
	s.rendered = renderDiff(s.path(), s.diff, s.threads, diffOptions{
		context: context,
		width:   s.viewport.Width - 1,
		syntax:  s.syntax,
		search:  searchRegexp(s.query),
	})
}

// refresh redraws the cursor marker and keeps the cursor visible.
func (s *diffScreen) refresh() {
	var b strings.Builder
	for i, l := range s.rendered.lines {
		if i == s.cursor {
			b.WriteString(cursorStyle.Render("▌"))
		} else {
			b.WriteByte(' ')
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	s.viewport.SetContent(b.String())

	h := s.viewport.Height
	off := min(scrollOff, max((h-1)/2, 0))
	y := s.viewport.YOffset
	if s.cursor < y+off {
		y = s.cursor - off
	}
	if s.cursor > y+h-1-off {
		y = s.cursor - h + 1 + off
	}
	s.viewport.SetYOffset(max(y, 0))
}

// commentInput returns the draft to create for a new comment at target.
func commentInput(path string, t rowTarget) (gerrit.CommentInput, bool) {
	in := gerrit.CommentInput{Path: path, Unresolved: new(true)}
	switch {
	case t.fileLevel:
	case t.hasLine && t.line.kind == lineDeleted:
		in.Side, in.Line = gerrit.SideParent, t.line.a
	case t.hasLine:
		in.Line = t.line.b
	default:
		return in, false
	}
	return in, true
}

// replyInput returns the draft replying to the last comment of a thread.
func replyInput(t gerrit.Thread, message string, unresolved bool) gerrit.CommentInput {
	root, last := t.Root(), t.Comments[len(t.Comments)-1]
	return gerrit.CommentInput{
		Path:       root.Path,
		Side:       root.Side,
		Line:       root.Line,
		InReplyTo:  last.ID,
		Message:    message,
		Unresolved: new(unresolved),
	}
}

func (s *diffScreen) cursorThread() (gerrit.Thread, bool) {
	t := s.target()
	if t.thread < 0 || t.thread >= len(s.threads) {
		return gerrit.Thread{}, false
	}
	return s.threads[t.thread], true
}

// cursorDraft returns the user's draft ending the thread under the cursor.
func (s *diffScreen) cursorDraft() (gerrit.CommentInfo, bool) {
	t, ok := s.cursorThread()
	if !ok {
		return gerrit.CommentInfo{}, false
	}
	last := t.Comments[len(t.Comments)-1]
	return last, last.Draft
}

func (s *diffScreen) comment() tea.Cmd {
	if _, ok := s.cursorThread(); ok {
		return s.reply()
	}
	t := s.target()
	in, ok := commentInput(s.path(), t)
	if !ok {
		s.notice = "move the cursor to a diff line or the file header to comment"
		return nil
	}
	where := "file comment"
	if in.Line > 0 {
		where = fmt.Sprintf("line %d", in.Line)
		if in.Side == gerrit.SideParent {
			where += " of the parent"
		}
	}
	context := []string{fmt.Sprintf("New comment on %s, %s (PS%d)", s.path(), where, s.revisionNumber())}
	if t.hasLine {
		context = append(context, "", "    "+t.line.text)
	}
	return openEditor("", context, draftRequest{revision: s.change.CurrentRevision, input: in})
}

func (s *diffScreen) reply() tea.Cmd {
	t, ok := s.cursorThread()
	if !ok {
		s.notice = "move the cursor to a thread to reply"
		return nil
	}
	if _, ok := s.cursorDraft(); ok {
		return s.editDraft()
	}
	in := replyInput(t, "", t.Unresolved())
	return openEditor("", threadContext(t), draftRequest{revision: s.change.CurrentRevision, input: in})
}

func (s *diffScreen) quickReply(message string) tea.Cmd {
	t, ok := s.cursorThread()
	if !ok {
		s.notice = "move the cursor to a thread to reply"
		return nil
	}
	req := draftRequest{revision: s.change.CurrentRevision, input: replyInput(t, message, false)}
	if d, ok := s.cursorDraft(); ok {
		req = draftRequest{revision: strconv.Itoa(d.PatchSet), draftID: d.ID, input: draftInput(d)}
		req.input.Message, req.input.Unresolved = message, new(false)
	}
	return s.saveDraft(req, fmt.Sprintf("%q draft saved", message))
}

func draftInput(d gerrit.CommentInfo) gerrit.CommentInput {
	return gerrit.CommentInput{
		ID:         d.ID,
		Path:       d.Path,
		Side:       d.Side,
		Line:       d.Line,
		InReplyTo:  d.InReplyTo,
		Message:    d.Message,
		Unresolved: new(d.Unresolved),
	}
}

func (s *diffScreen) editDraft() tea.Cmd {
	d, ok := s.cursorDraft()
	if !ok {
		s.notice = "no draft in this thread"
		return nil
	}
	t, _ := s.cursorThread()
	req := draftRequest{revision: strconv.Itoa(d.PatchSet), draftID: d.ID, input: draftInput(d)}
	return openEditor(d.Message, threadContext(gerrit.Thread{Comments: t.Comments[:len(t.Comments)-1]}), req)
}

func (s *diffScreen) toggleResolved() tea.Cmd {
	d, ok := s.cursorDraft()
	if !ok {
		s.notice = "only your drafts can be marked resolved or unresolved"
		return nil
	}
	in := draftInput(d)
	in.Unresolved = new(!d.Unresolved)
	notice := "draft marks the thread resolved"
	if !d.Unresolved {
		notice = "draft marks the thread unresolved"
	}
	return s.saveDraft(draftRequest{revision: strconv.Itoa(d.PatchSet), draftID: d.ID, input: in}, notice)
}

func (s *diffScreen) deleteDraft() tea.Cmd {
	d, ok := s.cursorDraft()
	if !ok {
		return nil
	}
	s.loading = true
	client, number := s.client, strconv.Itoa(s.change.Number)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		return draftSavedMsg{notice: "draft deleted", err: client.DeleteDraft(ctx, number, strconv.Itoa(d.PatchSet), d.ID)}
	}
}

func (s *diffScreen) saveDraft(req draftRequest, notice string) tea.Cmd {
	s.loading = true
	return saveDraft(s.client, s.change.Number, req, notice)
}

func threadContext(t gerrit.Thread) []string {
	if len(t.Comments) == 0 {
		return nil
	}
	root := t.Root()
	where := "file comment"
	if root.Line > 0 {
		where = fmt.Sprintf("line %d", root.Line)
	}
	out := []string{fmt.Sprintf("Replying on %s, %s", root.Path, where)}
	for _, c := range t.Comments {
		out = append(out, "", c.Author.DisplayName()+":")
		for l := range strings.SplitSeq(strings.TrimSpace(c.Message), "\n") {
			out = append(out, "  "+l)
		}
	}
	return out
}

func (s *diffScreen) View() string {
	return s.viewport.View()
}

func (s *diffScreen) Title() string {
	return s.path()
}

func (s *diffScreen) Status() string {
	unresolved, drafts := 0, 0
	for _, t := range s.threads {
		if t.Unresolved() {
			unresolved++
		}
		for _, c := range t.Comments {
			if c.Draft {
				drafts++
			}
		}
	}
	if s.searching {
		return s.searchInput.View()
	}
	help := dimStyle.Render(fmt.Sprintf("[%d/%d] %d threads (%d open, %d drafts)", s.index+1, len(s.paths), len(s.threads), unresolved, drafts)) + "   " +
		keyHints("]/[", "thread", "c", "comment", "r", "reply", "A/D", "ack/done", "v", "publish", "?", "help")
	if s.query != "" {
		help = fmt.Sprintf("/%s: %d matches · %s", s.query, len(s.rendered.matchRows), help)
	}
	switch {
	case s.pending != "" && s.notice != "":
		help = s.notice
	case s.count > 0 || s.pending != "":
		help = fmt.Sprintf("%s%s", countPrefix(s.count), s.pending)
	case s.notice != "":
		help = s.notice
	}
	return status(s.loading, s.err, help)
}

func countPrefix(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func (s *diffScreen) SetSize(width, height int) {
	s.viewport.Width, s.viewport.Height = width, height
	if s.diff != nil {
		keep := s.anchor()
		s.rerender()
		s.cursor = s.findAnchor(keep)
		s.refresh()
	}
}

const diffHelp = `Motions
  j / k, ↓ / ↑        down / up (accepts a count, e.g. 5j)
  ctrl-d / ctrl-u     half page down / up
  ctrl-f / ctrl-b     page down / up
  gg / G              first / last line
  42G                 line 42 of the new file
  zz                  center the cursor
  h / l, ← / →, 0     scroll left / right / back to the start
  ] / [               next / previous thread
  tab / shift-tab     next / previous file

Search
  /                   search as you type, case-insensitive unless the
                      query has an upper-case letter; enter to confirm
  n / N               next / previous match
  esc                 clear the search highlight

Folding
  zR / zM / za        show the whole file / changes only / toggle

Comments
  c, i, enter         comment on the cursor line (file comment on the header);
                      on a thread, reply to it
  r                   reply to the thread (edits your draft if there is one)
  A / D               reply "Ack" / "Done", resolving the thread
  e                   edit your draft
  u                   toggle whether your draft resolves the thread
  xx                  delete your draft

  Comments are written in $VISUAL / $EDITOR and saved as drafts.
  Drafts are published with your next review (v).

Other
  v                   vote and publish drafts
  ctrl-l              reload
  q, esc              back`
