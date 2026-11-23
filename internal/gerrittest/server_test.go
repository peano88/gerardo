package gerrittest

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

var demoNow = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

func start(t *testing.T, s *Server) *gerrit.Client {
	t.Helper()
	url, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return gerrit.NewClient(url, User, Password)
}

func TestAuthentication(t *testing.T) {
	s := Demo(demoNow)
	url, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := gerrit.NewClient(url, User, "wrong").Self(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong password: got %v", err)
	}
}

func TestQuery(t *testing.T) {
	c := start(t, Demo(demoNow))
	ctx := context.Background()

	numbers := func(q string) []int {
		t.Helper()
		changes, _, err := c.QueryChanges(ctx, q, 0, 50, false)
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for _, ch := range changes {
			out = append(out, ch.Number)
		}
		slices.Sort(out)
		return out
	}
	for q, want := range map[string][]int{
		"is:open (owner:self OR reviewer:self)": {4210, 4215, 4216, 4219, 4221, 4224},
		"is:open owner:self":                    {4210, 4221},
		"is:open -is:wip owner:self":            {4210},
		"status:merged":                         {4198},
		"project:acme/web":                      {4224},
		"attention:self":                        {4210, 4215, 4216, 4224},
		"is:open owner:kim@example.com":         {4215, 4216},
		"backoff":                               {4210},
		"4219":                                  {4219},
	} {
		if got := numbers(q); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", q, got, want)
		}
	}

	first, more, err := c.QueryChanges(ctx, "is:open", 0, 4, false)
	if err != nil || len(first) != 4 || !more {
		t.Fatalf("first page: got %d changes, more %v, %v", len(first), more, err)
	}
	rest, more, err := c.QueryChanges(ctx, "is:open", 4, 4, false)
	if err != nil || len(rest) != 2 || more {
		t.Fatalf("second page: got %d changes, more %v, %v", len(rest), more, err)
	}
	if first[0].Number != 4210 {
		t.Fatalf("changes should be sorted by update, most recent first, got %d", first[0].Number)
	}

	if _, _, err := c.QueryChanges(ctx, "label:Code-Review+2", 0, 10, false); err == nil {
		t.Fatal("unsupported operators should be rejected")
	}
}

func TestChangeAndDiffs(t *testing.T) {
	c := start(t, Demo(demoNow))
	ctx := context.Background()

	ch, err := c.GetChange(ctx, "4210")
	if err != nil {
		t.Fatal(err)
	}
	rev, ok := ch.Current()
	if !ok || rev.Number != 3 || rev.Commit == nil || len(ch.Messages) == 0 {
		t.Fatalf("unexpected change: %+v", ch)
	}
	if _, ok := rev.Files["httpclient/client_test.go"]; !ok || rev.Files["httpclient/client_test.go"].Status != "A" {
		t.Fatalf("PS3 should add the test, got %+v", rev.Files)
	}
	if ch.Labels["Code-Review"].VoteOf(Sam.AccountID) != 1 || ch.Labels["Verified"].Approved == nil {
		t.Fatalf("unexpected labels: %+v", ch.Labels)
	}

	files, err := c.ListFiles(ctx, "4210", "3", 2)
	if err != nil {
		t.Fatal(err)
	}
	if f := files["httpclient/client.go"]; f.LinesInserted != 1 || f.LinesDeleted != 0 {
		t.Fatalf("PS2 → PS3 adds a comment line to client.go, got %+v", f)
	}

	diff, err := c.GetDiff(ctx, "4210", "3", 2, "httpclient/client.go")
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, chunk := range diff.Content {
		added = append(added, chunk.B...)
	}
	if len(added) != 1 || !strings.Contains(added[0], "maxAttempts bounds") {
		t.Fatalf("got added lines %q", added)
	}
	diff, err = c.GetDiff(ctx, "4210", "3", 0, "httpclient/client_test.go")
	if err != nil || diff.ChangeType != "ADDED" || diff.MetaA != nil {
		t.Fatalf("against Base the test is added, got %+v, %v", diff, err)
	}
}

func TestPortedComments(t *testing.T) {
	c := start(t, Demo(demoNow))
	ported, err := c.PortedComments(context.Background(), "4210", "3")
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, cm := range ported["httpclient/client.go"] {
		lines = append(lines, cm.Line)
	}
	// The resolved threads of PS1 are not ported; the PS2 thread on
	// "const maxAttempts" moves down by the comment added above it.
	if !slices.Equal(lines, []int{11}) {
		t.Fatalf("got ported lines %v, want [11]", lines)
	}
}

func TestDraftsAndReview(t *testing.T) {
	s := Demo(demoNow)
	c := start(t, s)
	ctx := context.Background()

	in := gerrit.CommentInput{Path: "search/page.go", Line: 5, Message: "Nice", Unresolved: new(false)}
	if err := c.CreateDraft(ctx, "4219", "1", in); err != nil {
		t.Fatal(err)
	}
	drafts, err := c.ListDrafts(ctx, "4219")
	if err != nil || len(drafts["search/page.go"]) != 1 {
		t.Fatalf("got drafts %v, %v", drafts, err)
	}
	d := drafts["search/page.go"][0]
	in.Message = "Nice catch"
	if err := c.UpdateDraft(ctx, "4219", "1", d.ID, in); err != nil {
		t.Fatal(err)
	}

	review := gerrit.ReviewInput{Labels: map[string]int{"Code-Review": 2}, Drafts: gerrit.PublishAllDrafts}
	if err := c.SetReview(ctx, "4219", "1", review); err != nil {
		t.Fatal(err)
	}
	ch, _ := s.Change(4219)
	if len(ch.Drafts) != 0 || len(ch.Comments) != 1 || ch.Comments[0].Message != "Nice catch" || ch.Comments[0].ChangeMessageID == "" {
		t.Fatalf("drafts should be published with the review, got %+v", ch.Comments)
	}
	if !strings.Contains(ch.Messages[len(ch.Messages)-1].Message, "Code-Review+2") {
		t.Fatalf("got message %q", ch.Messages[len(ch.Messages)-1].Message)
	}

	if err := c.SetReview(ctx, "4219", "1", gerrit.ReviewInput{Labels: map[string]int{"Code-Review": 3}}); err == nil {
		t.Fatal("invalid votes should be rejected")
	}
	if err := c.DeleteDraft(ctx, "4219", "1", d.ID); err == nil {
		t.Fatal("a published draft is no longer a draft")
	}
}

func TestSubmitAndRebase(t *testing.T) {
	s := Demo(demoNow)
	c := start(t, s)
	ctx := context.Background()

	if err := c.Submit(ctx, "4210"); err == nil {
		t.Fatal("a change without approval cannot be submitted")
	}
	if err := c.Submit(ctx, "4219"); err != nil {
		t.Fatal(err)
	}
	if ch, _ := s.Change(4219); ch.Status != "MERGED" {
		t.Fatalf("got status %s", ch.Status)
	}

	ch, err := c.GetChange(ctx, "4216")
	if err != nil {
		t.Fatal(err)
	}
	related, err := c.RelatedChanges(ctx, "4216")
	if err != nil {
		t.Fatal(err)
	}
	parent, ok := gerrit.ParentChange(ch, related)
	if !ok || parent.Number != 4215 || parent.RevisionNumber != 1 || parent.CurrentRevisionNumber != 2 {
		t.Fatalf("4216 should be on the outdated PS1 of 4215, got %+v, %v", parent, ok)
	}
	if err := c.Rebase(ctx, "4216", "4215"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rebase(ctx, "4216", "4215"); err == nil || !strings.Contains(err.Error(), "up to date") {
		t.Fatalf("rebasing again: got %v", err)
	}
	if ch, _ := s.Change(4216); len(ch.PatchSets) != 2 {
		t.Fatalf("rebase should upload PS2, got %d patch sets", len(ch.PatchSets))
	}
}
