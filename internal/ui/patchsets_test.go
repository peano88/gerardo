package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

func changeWithPatchSets(n int) *gerrit.ChangeInfo {
	return &gerrit.ChangeInfo{
		Number:          42,
		CurrentRevision: "abc",
		Revisions: map[string]gerrit.RevisionInfo{"abc": {Number: n, Files: map[string]gerrit.FileInfo{
			"a.go": {}, "b.go": {},
		}}},
	}
}

func TestRangeDialogKeepsBaseBeforeTarget(t *testing.T) {
	var applied patchRange
	d, n := openRangeDialog(changeWithPatchSets(4), patchRange{target: 4}, func(r patchRange) tea.Cmd {
		applied = r
		return nil
	})
	if d == nil || n.text != "" {
		t.Fatalf("a change with patch sets to compare should open the dialog, got %q", n.text)
	}
	keys := func(ks ...tea.KeyMsg) {
		for _, k := range ks {
			d.update(k)
		}
	}
	down, up := tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyUp}
	right := tea.KeyMsg{Type: tea.KeyRight}

	// The base goes down to PS3 at most, the latest patch set before PS4.
	keys(down, down, down, down, down)
	if got := d.(*rangeDialog).rng; got != (patchRange{base: 3, target: 4}) {
		t.Fatalf("base should stop before the latest patch set, got %v", got)
	}
	// Moving the target up past the base pushes the base up too.
	keys(right, up, up)
	if got := d.(*rangeDialog).rng; got != (patchRange{base: 1, target: 2}) {
		t.Fatalf("got %v, want PS1 → PS2", got)
	}
	if !strings.Contains(d.view(80), "PS1 → PS2") {
		t.Fatalf("dialog should show the range:\n%s", d.view(80))
	}
	if done, _ := d.update(tea.KeyMsg{Type: tea.KeyEnter}); !done || applied != (patchRange{base: 1, target: 2}) {
		t.Fatalf("enter should apply the range, got %v", applied)
	}

	if d, n := openRangeDialog(changeWithPatchSets(1), patchRange{target: 1}, nil); d != nil || n.text == "" {
		t.Fatal("a single patch set has nothing to compare")
	}
}

func TestFilesScreenRange(t *testing.T) {
	change := changeWithPatchSets(3)
	s := newFilesScreen(nil, change)
	s.SetSize(100, 20)
	s.setCursor(1)

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if s.dialog == nil {
		t.Fatal("p should open the patch set dialog")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	if _, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil || s.dialog != nil {
		t.Fatal("enter should close the dialog and list the files of the range")
	}
	want := patchRange{base: 1, target: 3}
	if s.rng != want || s.Title() != "PS1 → PS3 files" {
		t.Fatalf("got range %v titled %q", s.rng, s.Title())
	}

	// A late answer for the previous range is ignored.
	s.Update(filesLoadedMsg{rng: patchRange{target: 3}, files: map[string]gerrit.FileInfo{"c.go": {}}})
	s.Update(filesLoadedMsg{rng: want, files: map[string]gerrit.FileInfo{"0.go": {}, "b.go": {}, "z.go": {}}})
	if strings.Join(s.paths, ",") != "0.go,b.go,z.go" || s.paths[s.cursor] != "b.go" {
		t.Fatalf("cursor should stay on b.go, got %v at %d", s.paths, s.cursor)
	}

	// Going back from a diff that compared other patch sets lists them.
	if _, cmd := s.Update(fileSelectedMsg{path: "z.go", rng: patchRange{base: 2, target: 3}}); cmd == nil || s.rng.base != 2 {
		t.Fatalf("files should follow the range of the diff, got %v", s.rng)
	}
}

func TestDiffRangeMovesToChangedFile(t *testing.T) {
	change := changeWithPatchSets(3)
	s := newDiffScreen(nil, change, latestRange(change), []string{"a.go", "b.go"}, 0)
	s.SetSize(100, 20)
	s.Update(diffLoadedMsg{rng: s.rng, path: "a.go", diff: &gerrit.DiffInfo{Content: []gerrit.DiffContent{{AB: []string{"x"}}}}})

	r := patchRange{base: 2, target: 3}
	s.applyRange(r)
	s.Update(diffLoadedMsg{rng: r, path: "b.go", paths: []string{"b.go"}, diff: &gerrit.DiffInfo{Content: []gerrit.DiffContent{{B: []string{"y"}}}}})
	if s.path() != "b.go" || !strings.Contains(s.notice, "a.go does not differ") || s.diff == nil {
		t.Fatalf("diff should move to b.go with a notice, got %s, %q", s.path(), s.notice)
	}

	s.applyRange(patchRange{base: 1, target: 3})
	s.Update(diffLoadedMsg{rng: patchRange{base: 1, target: 3}, paths: []string{}})
	if s.rng != r || !strings.Contains(s.notice, "no file differs") {
		t.Fatalf("a range without changes should be undone, got %v, %q", s.rng, s.notice)
	}
}
