package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

type changeLoadedMsg struct {
	number   int
	change   *gerrit.ChangeInfo
	comments map[string][]gerrit.CommentInfo
	drafts   map[string][]gerrit.CommentInfo
	err      error
}

type changeScreen struct {
	client   *gerrit.Client
	number   int
	change   *gerrit.ChangeInfo
	viewport viewport.Model
	loading  bool
	err      error
	notice   notice

	// comments maps message IDs to the inline comments published with them.
	comments     map[string][]gerrit.CommentInfo
	commentsByID map[string]gerrit.CommentInfo
	draftReplies map[string][]gerrit.CommentInfo
	// threads holds the thread of every comment and draft, by comment ID.
	threads      map[string]gerrit.Thread
	allPatchSets bool

	// selected is the ID of the comment under the cursor; positions lists
	// the shown comments, in order.
	selected  string
	positions []commentPos

	// confirm is the open confirmation dialog, if any.
	confirm *confirmation
}

type changeActionMsg struct {
	notice string
	err    error
}

func newChangeScreen(client *gerrit.Client, number int) *changeScreen {
	return &changeScreen{client: client, number: number, viewport: viewport.New(0, 0)}
}

func (s *changeScreen) Init() tea.Cmd {
	s.loading = true
	client, number := s.client, strconv.Itoa(s.number)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		var (
			wg                              sync.WaitGroup
			change                          *gerrit.ChangeInfo
			comments, drafts                map[string][]gerrit.CommentInfo
			changeErr, commentErr, draftErr error
		)
		wg.Go(func() { change, changeErr = client.GetChange(ctx, number) })
		wg.Go(func() { comments, commentErr = client.ListComments(ctx, number) })
		wg.Go(func() { drafts, draftErr = client.ListDrafts(ctx, number) })
		wg.Wait()
		return changeLoadedMsg{number: s.number, change: change, comments: comments, drafts: drafts,
			err: errors.Join(changeErr, commentErr, draftErr)}
	}
}

func (s *changeScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && s.confirm != nil {
		done, cmd := s.confirm.update(key)
		if done {
			s.confirm = nil
			if cmd != nil {
				s.loading = true
			}
		}
		return s, cmd
	}
	s.notice.update(msg)
	switch msg := msg.(type) {
	case changeActionMsg:
		s.loading = false
		s.notice = notice{text: msg.notice, err: msg.err}
		if msg.err != nil {
			return s, nil
		}
		return s, s.Init()

	case copiedMsg:
		return s, nil

	case changeLoadedMsg:
		if msg.number != s.number {
			return s, nil
		}
		s.loading, s.err = false, msg.err
		if msg.change != nil {
			first := s.change == nil
			s.change = msg.change
			s.setComments(msg.comments, msg.drafts)
			s.render()
			if first {
				s.viewport.GotoTop()
			}
		}
		return s, nil

	case changeUpdatedMsg, focusMsg:
		return s, s.Init()

	case editorClosedMsg:
		switch {
		case msg.err != nil:
			s.notice = notice{err: msg.err}
			return s, nil
		case msg.text == "":
			s.notice = notice{text: "empty comment, nothing saved"}
			return s, nil
		}
		msg.req.input.Message = msg.text
		return s, saveDraft(s.client, s.number, msg.req, "draft saved · v to publish")

	case draftSavedMsg:
		s.notice = notice{text: msg.notice, err: msg.err}
		if msg.err != nil {
			return s, nil
		}
		return s, s.Init()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "backspace":
			return s, pop
		case "esc":
			if s.selected != "" {
				s.selected = ""
				s.render()
				return s, nil
			}
			return s, pop
		case "]":
			s.jumpComment(1)
			return s, nil
		case "[":
			s.jumpComment(-1)
			return s, nil
		case "r":
			if s.selected != "" {
				return s, s.reply()
			}
			return s, s.Init()
		case "S":
			s.askSubmit()
			return s, nil
		case "R":
			s.askRebase()
			return s, nil
		case "A":
			return s, s.quickReply("Ack")
		case "D":
			return s, s.quickReply("Done")
		case "y":
			return s, copyNumber(s.number)
		case "m":
			s.allPatchSets = !s.allPatchSets
			s.render()
			return s, nil
		case "v":
			if s.change == nil {
				return s, nil
			}
			return s, push(newReviewScreen(s.client, s.change))
		case "enter":
			if s.selected != "" {
				return s, s.openThread()
			}
			return s, s.openFiles()
		case "f":
			return s, s.openFiles()
		}
	}

	var cmd tea.Cmd
	s.viewport, cmd = s.viewport.Update(msg)
	return s, cmd
}

func (s *changeScreen) setComments(comments, drafts map[string][]gerrit.CommentInfo) {
	s.comments = gerrit.MessageComments(s.change.Messages, comments)
	s.commentsByID = map[string]gerrit.CommentInfo{}
	s.draftReplies = map[string][]gerrit.CommentInfo{}
	var all []gerrit.CommentInfo
	for _, list := range comments {
		for _, c := range list {
			s.commentsByID[c.ID] = c
			all = append(all, c)
		}
	}
	for _, list := range drafts {
		for _, d := range list {
			if d.InReplyTo != "" {
				s.draftReplies[d.InReplyTo] = append(s.draftReplies[d.InReplyTo], d)
			}
			all = append(all, d)
		}
	}
	s.threads = map[string]gerrit.Thread{}
	for _, t := range gerrit.BuildThreads(all) {
		for _, c := range t.Comments {
			s.threads[c.ID] = t
		}
	}
}

func (s *changeScreen) openFiles() tea.Cmd {
	if s.change == nil {
		return nil
	}
	if _, ok := s.change.Current(); !ok {
		return nil
	}
	return push(newFilesScreen(s.client, s.change))
}

// jumpComment selects the next (direction 1) or previous (-1) shown comment,
// starting from the top of the screen when none is selected.
func (s *changeScreen) jumpComment(direction int) {
	if len(s.positions) == 0 {
		s.notice = notice{text: "no comments shown"}
		return
	}
	i := slices.IndexFunc(s.positions, func(p commentPos) bool { return p.id == s.selected })
	switch {
	case i >= 0:
		i = min(max(i+direction, 0), len(s.positions)-1)
	case direction > 0:
		i = slices.IndexFunc(s.positions, func(p commentPos) bool { return p.line >= s.viewport.YOffset })
		if i < 0 {
			i = len(s.positions) - 1
		}
	default:
		i = 0
		for j, p := range s.positions {
			if p.line < s.viewport.YOffset+s.viewport.Height {
				i = j
			}
		}
	}
	s.selected = s.positions[i].id
	s.render()

	line := s.positions[i].line
	if line < s.viewport.YOffset || line > s.viewport.YOffset+s.viewport.Height-6 {
		s.viewport.SetYOffset(max(line-3, 0))
	}
}

func (s *changeScreen) selectedThread() (gerrit.Thread, bool) {
	t, ok := s.threads[s.selected]
	if !ok {
		s.notice = notice{text: "select a comment with ] first"}
	}
	return t, ok
}

// lastPublished returns the latest published comment of a thread, and the
// user's draft ending it, if any.
func lastPublished(t gerrit.Thread) (gerrit.CommentInfo, *gerrit.CommentInfo) {
	var last gerrit.CommentInfo
	var draft *gerrit.CommentInfo
	for _, c := range t.Comments {
		if c.Draft {
			draft = &c
		} else {
			last = c
		}
	}
	return last, draft
}

// replyRequest answers the latest comment of t, on its patch set so that its
// line still applies.
func replyRequest(t gerrit.Thread, message string, unresolved bool) draftRequest {
	last, _ := lastPublished(t)
	return draftRequest{revision: strconv.Itoa(last.PatchSet), input: gerrit.CommentInput{
		Path:       last.Path,
		Side:       last.Side,
		Line:       last.Line,
		InReplyTo:  last.ID,
		Message:    message,
		Unresolved: new(unresolved),
	}}
}

func (s *changeScreen) reply() tea.Cmd {
	t, ok := s.selectedThread()
	if !ok {
		return nil
	}
	published := gerrit.Thread{Comments: slices.DeleteFunc(slices.Clone(t.Comments), func(c gerrit.CommentInfo) bool { return c.Draft })}
	if _, draft := lastPublished(t); draft != nil {
		req := draftRequest{revision: strconv.Itoa(draft.PatchSet), draftID: draft.ID, input: draftInput(*draft)}
		return openEditor(draft.Message, threadContext(published), req)
	}
	return openEditor("", threadContext(published), replyRequest(t, "", t.Unresolved()))
}

func (s *changeScreen) quickReply(message string) tea.Cmd {
	t, ok := s.selectedThread()
	if !ok {
		return nil
	}
	req := replyRequest(t, message, false)
	if _, draft := lastPublished(t); draft != nil {
		req = draftRequest{revision: strconv.Itoa(draft.PatchSet), draftID: draft.ID, input: draftInput(*draft)}
		req.input.Message, req.input.Unresolved = message, new(false)
	}
	return saveDraft(s.client, s.number, req, fmt.Sprintf("%q draft saved · v to publish", message))
}

// openThread shows the selected thread in the diff of its file; patch set
// level threads have no file and are replied to directly.
func (s *changeScreen) openThread() tea.Cmd {
	t, ok := s.selectedThread()
	if !ok {
		return nil
	}
	root := t.Root()
	if root.Path == gerrit.PatchSetLevel {
		return s.reply()
	}
	rev, ok := s.change.Current()
	paths := rev.SortedPaths()
	i := slices.Index(paths, root.Path)
	if !ok || i < 0 {
		s.notice = notice{text: root.Path + " is not in the current patch set"}
		return nil
	}
	d := newDiffScreen(s.client, s.change, paths, i)
	d.focusThread = root.ID
	return push(d)
}

func (s *changeScreen) askSubmit() {
	c := s.change
	switch {
	case c == nil:
	case c.Status != "NEW":
		s.notice = notice{text: "only open changes can be submitted"}
	case !c.Submittable:
		s.notice = notice{text: "the change is not submittable yet: check its votes and requirements"}
	default:
		s.confirm = &confirmation{
			title:   fmt.Sprintf("Submit change %d?", c.Number),
			details: []string{c.Subject, dimStyle.Render(c.Project + " · " + c.Branch), "", "This merges the change into " + c.Branch + "."},
			verb:    "submit",
			run:     s.action("submitted", func(ctx context.Context, n string) error { return s.client.Submit(ctx, n) }),
		}
	}
}

func (s *changeScreen) askRebase() {
	c := s.change
	switch {
	case c == nil:
	case c.Status != "NEW":
		s.notice = notice{text: "only open changes can be rebased"}
	default:
		rev, _ := c.Current()
		s.confirm = &confirmation{
			title:   fmt.Sprintf("Rebase change %d?", c.Number),
			details: []string{c.Subject, dimStyle.Render(c.Project + " · " + c.Branch), "", fmt.Sprintf("This rebases patch set %d onto the tip of %s, uploading patch set %d.", rev.Number, c.Branch, rev.Number+1)},
			verb:    "rebase",
			run:     s.action("rebased", func(ctx context.Context, n string) error { return s.client.Rebase(ctx, n) }),
		}
	}
}

// action runs a change operation, reporting it with notice once done.
func (s *changeScreen) action(notice string, do func(ctx context.Context, number string) error) tea.Cmd {
	number := strconv.Itoa(s.number)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		return changeActionMsg{notice: notice, err: do(ctx, number)}
	}
}

func (s *changeScreen) View() string {
	if s.confirm != nil {
		return overlay(s.viewport.View(), s.confirm.view(s.viewport.Width), s.viewport.Width, s.viewport.Height)
	}
	return s.viewport.View()
}

func (s *changeScreen) Title() string {
	return strconv.Itoa(s.number)
}

func (s *changeScreen) Status() string {
	if s.confirm != nil {
		return keyHints("y", s.confirm.verb, "n/esc", "cancel")
	}
	var hints string
	if t, ok := s.threads[s.selected]; ok {
		open := "open in diff"
		if t.Root().Path == gerrit.PatchSetLevel {
			open = "reply"
		}
		hints = keyHints("]/[", "comment", "enter", open, "r", "reply", "A/D", "ack/done", "v", "publish", "esc", "unselect")
	} else {
		hints = dimStyle.Render(fmt.Sprintf("%3.0f%%", s.viewport.ScrollPercent()*100)) + "   " +
			keyHints(s.actionHints()...)
	}
	return s.notice.status(s.loading, s.err, hints)
}

func (s *changeScreen) actionHints() []string {
	hints := []string{"]/[", "comments", "f", "files", "v", "vote"}
	if s.change != nil && s.change.Status == "NEW" {
		if s.change.Submittable {
			hints = append(hints, "S", "submit")
		}
		hints = append(hints, "R", "rebase")
	}
	return append(hints, "m", s.messagesHint(), "y", "copy number", "r", "reload", "esc", "back")
}

func (s *changeScreen) messagesHint() string {
	if s.allPatchSets {
		return "latest messages"
	}
	return "all messages"
}

func (s *changeScreen) SetSize(width, height int) {
	s.viewport.Width, s.viewport.Height = width, height
	s.render()
}

func (s *changeScreen) render() {
	if s.change == nil {
		return
	}
	content, positions := renderChangeLayout(s.change, s.viewport.Width, changeView{
		allPatchSets: s.allPatchSets,
		comments:     s.comments,
		commentsByID: s.commentsByID,
		draftReplies: s.draftReplies,
		selected:     s.selected,
	})
	s.positions = positions
	if !slices.ContainsFunc(positions, func(p commentPos) bool { return p.id == s.selected }) {
		s.selected = ""
	}
	s.viewport.SetContent(content)
}
