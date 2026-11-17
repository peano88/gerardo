package gerrit

import (
	"slices"
	"strings"
	"time"
)

const timestampLayout = "2006-01-02 15:04:05.000000000"

// Timestamp is a Gerrit REST timestamp, always expressed in UTC.
type Timestamp struct {
	time.Time
}

// UnmarshalJSON parses the Gerrit timestamp format.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	parsed, err := time.ParseInLocation(timestampLayout, s, time.UTC)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}

// AccountInfo describes a Gerrit account.
type AccountInfo struct {
	AccountID int    `json:"_account_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Username  string `json:"username"`
}

// DisplayName returns the most human-friendly identifier available.
func (a *AccountInfo) DisplayName() string {
	switch {
	case a == nil:
		return ""
	case a.Name != "":
		return a.Name
	case a.Username != "":
		return a.Username
	default:
		return a.Email
	}
}

// ApprovalInfo is a single vote on a label.
type ApprovalInfo struct {
	AccountInfo
	Value int `json:"value"`
}

// LabelInfo summarizes the votes on a label.
type LabelInfo struct {
	Approved    *AccountInfo   `json:"approved"`
	Rejected    *AccountInfo   `json:"rejected"`
	Recommended *AccountInfo   `json:"recommended"`
	Disliked    *AccountInfo   `json:"disliked"`
	Optional    bool           `json:"optional"`
	All         []ApprovalInfo `json:"all"`
}

// Summary returns the decisive vote of the label, such as "+2" or "-1".
func (l LabelInfo) Summary() string {
	switch {
	case l.Rejected != nil:
		return "-2"
	case l.Approved != nil:
		return "+2"
	case l.Disliked != nil:
		return "-1"
	case l.Recommended != nil:
		return "+1"
	default:
		return ""
	}
}

// CommitInfo describes the commit of a revision.
type CommitInfo struct {
	Subject string `json:"subject"`
	Message string `json:"message"`
}

// FileInfo describes a file modified by a revision.
type FileInfo struct {
	Status        string `json:"status"`
	LinesInserted int    `json:"lines_inserted"`
	LinesDeleted  int    `json:"lines_deleted"`
	Binary        bool   `json:"binary"`
}

// RevisionInfo describes a patch set.
type RevisionInfo struct {
	Number int                 `json:"_number"`
	Commit *CommitInfo         `json:"commit"`
	Files  map[string]FileInfo `json:"files"`
}

// ChangeMessageInfo is a top-level message posted on a change.
type ChangeMessageInfo struct {
	Author         *AccountInfo `json:"author"`
	Date           Timestamp    `json:"date"`
	Message        string       `json:"message"`
	RevisionNumber int          `json:"_revision_number"`
}

// ChangeInfo describes a Gerrit change.
type ChangeInfo struct {
	ID                     string                  `json:"id"`
	Number                 int                     `json:"_number"`
	Project                string                  `json:"project"`
	Branch                 string                  `json:"branch"`
	Topic                  string                  `json:"topic"`
	Subject                string                  `json:"subject"`
	Status                 string                  `json:"status"`
	WorkInProgress         bool                    `json:"work_in_progress"`
	Submittable            bool                    `json:"submittable"`
	Owner                  AccountInfo             `json:"owner"`
	Updated                Timestamp               `json:"updated"`
	Insertions             int                     `json:"insertions"`
	Deletions              int                     `json:"deletions"`
	UnresolvedCommentCount int                     `json:"unresolved_comment_count"`
	Labels                 map[string]LabelInfo    `json:"labels"`
	CurrentRevision        string                  `json:"current_revision"`
	Revisions              map[string]RevisionInfo `json:"revisions"`
	Messages               []ChangeMessageInfo     `json:"messages"`
}

// SortedPaths returns the file paths of the revision in a stable order.
func (r RevisionInfo) SortedPaths() []string {
	paths := make([]string, 0, len(r.Files))
	for p := range r.Files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths
}

// DiffFileMeta describes one side of a file diff.
type DiffFileMeta struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Lines       int    `json:"lines"`
}

// DiffContent is a chunk of a diff: common lines (AB), lines only on the
// parent side (A), lines only on the revision side (B), or skipped common lines.
type DiffContent struct {
	AB   []string `json:"ab"`
	A    []string `json:"a"`
	B    []string `json:"b"`
	Skip int      `json:"skip"`
}

// DiffInfo is the diff of a file between a revision and its parent.
type DiffInfo struct {
	MetaA      *DiffFileMeta `json:"meta_a"`
	MetaB      *DiffFileMeta `json:"meta_b"`
	ChangeType string        `json:"change_type"`
	Binary     bool          `json:"binary"`
	Content    []DiffContent `json:"content"`
}

// Current returns the current revision, if it was requested.
func (c *ChangeInfo) Current() (RevisionInfo, bool) {
	r, ok := c.Revisions[c.CurrentRevision]
	return r, ok
}
