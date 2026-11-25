package ui

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerardo/internal/gerrit"
	"github.com/peano88/gerardo/internal/gerrittest"
)

// driver runs the app against a fake Gerrit server, running every command
// to completion and feeding its messages back, as the Bubble Tea runtime
// would, but one at a time.
type driver struct {
	t   *testing.T
	srv *gerrittest.Server
	app tea.Model
}

func newDriver(t *testing.T, query string) *driver {
	t.Helper()
	srv := gerrittest.Demo(time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC))
	url, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	d := &driver{t: t, srv: srv, app: New(gerrit.NewClient(url, gerrittest.User, gerrittest.Password), query)}
	d.send(tea.WindowSizeMsg{Width: 160, Height: 40})
	d.run(d.app.Init())
	return d
}

var cmdType = reflect.TypeFor[tea.Cmd]()

// messages runs cmd, expanding batches and sequences into their messages.
func (d *driver) messages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		var out []tea.Msg
		for i := range v.Len() {
			out = append(out, d.messages(v.Index(i).Interface().(tea.Cmd))...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func (d *driver) run(cmd tea.Cmd) {
	queue := d.messages(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		// A blinking cursor would schedule its next blink forever.
		if _, ok := msg.(tea.QuitMsg); ok || reflect.TypeOf(msg).PkgPath() == "github.com/charmbracelet/bubbles/cursor" {
			continue
		}
		var next tea.Cmd
		d.app, next = d.app.Update(msg)
		queue = append(queue, d.messages(next)...)
	}
}

func (d *driver) send(msg tea.Msg) {
	d.run(func() tea.Msg { return msg })
}

// press sends keys: names such as "enter" or "ctrl+s", or runes.
func (d *driver) press(keys ...string) {
	for _, k := range keys {
		switch k {
		case "enter", "esc", "tab", "down", "up", "left", "right":
			d.send(keyType(k))
		case "ctrl+s":
			d.send(tea.KeyMsg{Type: tea.KeyCtrlS})
		default:
			d.send(keyRunes(k))
		}
	}
}

func (d *driver) top() screen {
	return d.app.(App).top()
}

func (d *driver) view() string {
	return strings.Join(plainLines(d.app.View()), "\n")
}

func (d *driver) expect(texts ...string) {
	d.t.Helper()
	v := d.view()
	for _, text := range texts {
		if !strings.Contains(v, text) {
			d.t.Fatalf("screen should show %q:\n%s", text, v)
		}
	}
}

func TestIntegrationDashboard(t *testing.T) {
	d := newDriver(t, "is:open (owner:self OR reviewer:self)")
	d.expect("My changes (2)", "Retry unavailable servic", "Reviews (4)", "Cache user profiles", "acme/web")
	// The preview shows the first change, with its files.
	d.expect("Preview", "httpclient/client_test.go")

	d.press("/")
	if _, ok := d.top().(*searchScreen); !ok {
		t.Fatalf("/ should open the search, got %T", d.top())
	}
}

func TestIntegrationVoteAndSubmit(t *testing.T) {
	d := newDriver(t, "change:4219")
	d.press("enter")
	d.expect("Fix the first page of search results being skipped")

	d.press("v")
	if _, ok := d.top().(*reviewScreen); !ok {
		t.Fatalf("v should open the review, got %T", d.top())
	}
	d.press("right", "right", "ctrl+s")
	ch, _ := d.srv.Change(4219)
	if v := ch.Votes["Code-Review"][gerrittest.Alex.AccountID]; v != 2 {
		t.Fatalf("Code-Review+2 should be posted, got %d", v)
	}

	if _, ok := d.top().(*changeScreen); !ok {
		t.Fatalf("posting should go back to the change, got %T", d.top())
	}
	d.press("S")
	d.expect("Submit change 4219?")
	d.press("y")
	if ch, _ := d.srv.Change(4219); ch.Status != "MERGED" {
		t.Fatalf("the change should be merged, got %s", ch.Status)
	}
	d.expect("MERGED")
}

func TestIntegrationComparePatchSets(t *testing.T) {
	d := newDriver(t, "change:4210")
	d.press("enter", "f")
	d.expect("PS3 files", "httpclient/client_test.go")

	// Compare PS3 with PS2: client.go only gains a comment.
	d.press("p", "down", "down", "enter")
	d.expect("PS2 → PS3 files")
	files := d.top().(*filesScreen)
	if f := files.files["httpclient/client.go"]; f.LinesInserted != 1 || f.LinesDeleted != 0 {
		t.Fatalf("got %+v for client.go between PS2 and PS3", f)
	}

	// Against PS1, the threads started on it are on the left.
	d.press("p", "up", "enter", "j", "enter")
	diff, ok := d.top().(*diffScreen)
	if !ok || diff.path() != "httpclient/client.go" || diff.rng != (patchRange{base: 1, target: 3}) {
		t.Fatalf("got %T on %v", d.top(), diff.rng)
	}
	var onBase int
	for _, th := range diff.threads {
		if th.OnBase {
			onBase++
		}
	}
	if onBase != 2 {
		t.Fatalf("the two threads of PS1 should be on the left, got %d of %d", onBase, len(diff.threads))
	}

	// Replying to the thread of PS2 ported onto PS3, on the right, drafts on
	// PS3; replying to one of PS1, on the left, drafts on PS1.
	d.press("]")
	if th, _ := diff.cursorThread(); th.OnBase {
		t.Fatal("the first thread should be the one ported from PS2")
	}
	d.press("A", "]", "D")
	ch, _ := d.srv.Change(4210)
	if len(ch.Drafts) != 2 || ch.Drafts[0].PatchSet != 3 || ch.Drafts[1].PatchSet != 1 || ch.Drafts[1].Message != "Done" {
		t.Fatalf("got drafts %+v", ch.Drafts)
	}

	// Going back keeps the range in the files.
	d.press("q")
	d.expect("PS1 → PS3 files")
}

func TestIntegrationRebaseOntoParent(t *testing.T) {
	d := newDriver(t, "change:4216")
	d.press("enter", "R")
	d.expect("Rebase change 4216?", "Onto parent change 4215", "now based on patch set 1")
	d.press("y")
	if ch, _ := d.srv.Change(4216); len(ch.PatchSets) != 2 {
		t.Fatalf("the rebase should upload PS2, got %d patch sets", len(ch.PatchSets))
	}
}

func TestIntegrationPreviewShowsCommit(t *testing.T) {
	d := newDriver(t, "change:4210")
	ch, _ := d.srv.Change(4210)
	if len(ch.PatchSets) != 3 {
		t.Fatalf("got %d patch sets", len(ch.PatchSets))
	}
	info := d.top().(*dashboard).previews[4210]
	if info == nil || len(info.CurrentRevision) != 40 {
		t.Fatalf("preview should hold the change with its commit, got %+v", info)
	}
	d.expect("commit " + info.CurrentRevision)
}

func TestIntegrationToggleWorkInProgress(t *testing.T) {
	d := newDriver(t, "owner:self is:open")
	d.expect("[WIP] Read the settings")
	d.press("j", "enter")
	if s, ok := d.top().(*changeScreen); !ok || s.number != 4221 {
		t.Fatalf("should open the WIP change, got %T", d.top())
	}
	d.expect("W ready for review")

	d.press("W")
	if ch, _ := d.srv.Change(4221); ch.WorkInProgress {
		t.Fatal("W should mark the change ready for review")
	}
	d.expect("marked ready for review")
	if hint := d.top().(*changeScreen).wipHint(); hint != "set WIP" {
		t.Fatalf("the hint should offer to set WIP again, got %q", hint)
	}

	// Back on the dashboard, the list is loaded again.
	d.press("q")
	if strings.Contains(d.view(), "[WIP]") {
		t.Fatalf("the dashboard should show the change ready:\n%s", d.view())
	}

	d.press("enter", "W")
	if ch, _ := d.srv.Change(4221); !ch.WorkInProgress {
		t.Fatal("W again should mark it work in progress")
	}
}

func TestIntegrationHashtags(t *testing.T) {
	defer func(h []string) { Hashtags = h }(Hashtags)
	Hashtags = []string{"auto-submit"}
	d := newDriver(t, "change:4210")
	d.press("enter", "#")
	d.expect("Hashtags of change 4210", "1 [ ] #auto-submit")

	// Tick the offered hashtag and type another one.
	d.press("1", "down")
	for _, r := range "urgent" {
		d.press(string(r))
	}
	d.press("enter")
	ch, _ := d.srv.Change(4210)
	if strings.Join(ch.Hashtags, ",") != "auto-submit,urgent" {
		t.Fatalf("got hashtags %v", ch.Hashtags)
	}
	if last := ch.Messages[len(ch.Messages)-1].Message; last != "Hashtag added: auto-submit, urgent" {
		t.Fatalf("got message %q", last)
	}
	d.expect("added #auto-submit, added #urgent", "#auto-submit · #urgent")

	// Unticking removes it.
	d.press("#", "2", "enter")
	if ch, _ := d.srv.Change(4210); strings.Join(ch.Hashtags, ",") != "auto-submit" {
		t.Fatalf("urgent should be removed, got %v", ch.Hashtags)
	}
}

// pickAction opens the actions menu of the change shown and runs the
// action labelled label.
func (d *driver) pickAction(label string) {
	d.t.Helper()
	d.press("a")
	menu, ok := d.top().(*changeScreen).dialog.(*actionMenu)
	if !ok {
		d.t.Fatalf("a should open the actions menu:\n%s", d.view())
	}
	for i, a := range menu.actions {
		if a.info.Label == label {
			d.press(strconv.Itoa(i + 1))
			return
		}
	}
	d.t.Fatalf("no action %q in the menu:\n%s", label, d.view())
}

func TestIntegrationActions(t *testing.T) {
	d := newDriver(t, "change:4210")
	d.press("enter", "a")
	d.expect("Actions on change 4210", "Abandon", "Cherry Pick", "Edit Hashtags", "Nudge", "Rebase")
	if v := d.view(); strings.Contains(v, "Move Change") || strings.Contains(v, "Submit ") {
		t.Fatalf("actions needing input gerardo cannot give, and disabled ones, should be left out:\n%s", v)
	}
	d.press("esc")

	d.pickAction("Abandon")
	d.expect("Abandon change 4210?")
	d.press("y")
	if ch, _ := d.srv.Change(4210); ch.Status != "ABANDONED" {
		t.Fatalf("got status %s", ch.Status)
	}
	d.pickAction("Restore")
	d.press("y")
	if ch, _ := d.srv.Change(4210); ch.Status != "NEW" {
		t.Fatalf("got status %s", ch.Status)
	}

	// The action of a plugin runs after a confirmation.
	d.pickAction("Nudge")
	d.expect("Nudge on change 4210?", "Remind the reviewers")
	d.press("y")
	if ch, _ := d.srv.Change(4210); ch.Messages[len(ch.Messages)-1].Message != "Reminded the reviewers" {
		t.Fatalf("the plugin action should have run, got %q", ch.Messages[len(ch.Messages)-1].Message)
	}

	// A cherry-pick opens the change it creates.
	d.pickAction("Cherry Pick")
	for _, r := range "release" {
		d.press(string(r))
	}
	d.press("enter")
	pick, ok := d.top().(*changeScreen)
	if !ok || pick.number == 4210 || pick.change == nil || pick.change.Branch != "release" {
		t.Fatalf("the cherry-pick should be opened, got %T", d.top())
	}
}

func TestIntegrationRevert(t *testing.T) {
	d := newDriver(t, "change:4198")
	d.press("enter")
	d.pickAction("Revert")
	d.press("y")
	revert, ok := d.top().(*changeScreen)
	if !ok || revert.change == nil || revert.change.Subject != `Revert "Build with Go 1.25"` {
		t.Fatalf("the revert should be opened, got %T", d.top())
	}
	d.press("f", "j", "enter")
	d.expect("go 1.24")
}
