package gerrit

import (
	"cmp"
	"slices"
)

// Comment sides.
const (
	SideRevision = "REVISION"
	SideParent   = "PARENT"
)

// CommentRange is the character range a comment applies to.
type CommentRange struct {
	StartLine      int `json:"start_line"`
	StartCharacter int `json:"start_character"`
	EndLine        int `json:"end_line"`
	EndCharacter   int `json:"end_character"`
}

// CommentInfo is an inline comment. A zero Line means a file-level comment.
type CommentInfo struct {
	ID         string        `json:"id"`
	Path       string        `json:"path"`
	Side       string        `json:"side"`
	Line       int           `json:"line"`
	Range      *CommentRange `json:"range"`
	InReplyTo  string        `json:"in_reply_to"`
	Message    string        `json:"message"`
	Updated    Timestamp     `json:"updated"`
	Author     *AccountInfo  `json:"author"`
	PatchSet   int           `json:"patch_set"`
	Unresolved bool          `json:"unresolved"`
	Draft      bool          `json:"-"`
}

// CommentInput is the payload to create or update a draft comment. A zero
// Line makes a file-level comment.
type CommentInput struct {
	ID         string `json:"id,omitempty"`
	Path       string `json:"path"`
	Side       string `json:"side,omitempty"`
	Line       int    `json:"line,omitempty"`
	InReplyTo  string `json:"in_reply_to,omitempty"`
	Message    string `json:"message"`
	Unresolved *bool  `json:"unresolved,omitempty"`
}

// OnParent reports whether the comment is on the parent (left) side of the diff.
func (c CommentInfo) OnParent() bool {
	return c.Side == SideParent
}

// Thread is a root comment and its replies, oldest first.
type Thread struct {
	Comments []CommentInfo
}

// Root returns the comment that started the thread.
func (t Thread) Root() CommentInfo {
	return t.Comments[0]
}

// Unresolved reports whether the latest comment left the thread unresolved.
func (t Thread) Unresolved() bool {
	return t.Comments[len(t.Comments)-1].Unresolved
}

// BuildThreads groups comments into threads ordered by line, then by date.
func BuildThreads(comments []CommentInfo) []Thread {
	byID := make(map[string]CommentInfo, len(comments))
	for _, c := range comments {
		byID[c.ID] = c
	}
	rootOf := func(c CommentInfo) string {
		for c.InReplyTo != "" {
			parent, ok := byID[c.InReplyTo]
			if !ok {
				break
			}
			c = parent
		}
		return c.ID
	}

	groups := map[string][]CommentInfo{}
	for _, c := range comments {
		root := rootOf(c)
		groups[root] = append(groups[root], c)
	}

	threads := make([]Thread, 0, len(groups))
	for _, group := range groups {
		slices.SortStableFunc(group, func(a, b CommentInfo) int { return a.Updated.Compare(b.Updated.Time) })
		threads = append(threads, Thread{Comments: group})
	}
	slices.SortFunc(threads, func(a, b Thread) int {
		return cmp.Or(cmp.Compare(a.Root().Line, b.Root().Line), a.Root().Updated.Compare(b.Root().Updated.Time))
	})
	return threads
}

// ThreadsForRevision returns the threads to show on path when viewing patch
// set number: threads started on that patch set, plus unresolved threads of
// earlier patch sets ported onto it, with the user's drafts attached.
// Ported comments take precedence so that their lines match the revision.
func ThreadsForRevision(path string, number int, comments, ported, drafts map[string][]CommentInfo) []Thread {
	seen := map[string]bool{}
	portedIDs := map[string]bool{}
	var all []CommentInfo
	add := func(list []CommentInfo, isPorted bool) {
		for _, c := range list {
			if isPorted {
				portedIDs[c.ID] = true
			}
			if !seen[c.ID] {
				seen[c.ID] = true
				all = append(all, c)
			}
		}
	}
	add(ported[path], true)
	add(comments[path], false)
	add(drafts[path], false)

	var threads []Thread
	for _, t := range BuildThreads(all) {
		if root := t.Root(); root.PatchSet == number || portedIDs[root.ID] {
			threads = append(threads, t)
		}
	}
	return threads
}
