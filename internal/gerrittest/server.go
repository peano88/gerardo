// Package gerrittest is a fake Gerrit server, holding changes in memory and
// serving the part of the REST API gerrit-tui uses. It backs integration
// tests and the demo mode.
//
// Patch sets hold the content of the files they change, so that file lists
// and diffs between any two patch sets, and comments ported across them, are
// computed as Gerrit would.
package gerrittest

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

// Deleted, as the content of a file in a patch set, deletes the file.
const Deleted = "\x00deleted"

// Credentials accepted by the server.
const (
	User     = "demo"
	Password = "secret"
)

// PatchSet is a revision of a change.
type PatchSet struct {
	// Files maps the paths changed from the base of the change to their
	// content, or Deleted.
	Files map[string]string
	// Message is the commit message, the subject of the change by default.
	Message string
	// parent is the commit the patch set is based on.
	parent string
}

// Change is a change held by the server. The zero values of its fields are
// filled in by AddChange.
type Change struct {
	Number         int
	Project        string
	Branch         string
	Topic          string
	Subject        string
	Status         string
	WorkInProgress bool
	Owner          int
	Reviewers      []int
	Attention      []int
	// Base holds the files of the commit the change is based on.
	Base      map[string]string
	PatchSets []PatchSet
	// ParentChange is the change this one is stacked on, if any.
	ParentChange int
	// Votes maps labels to the votes of accounts.
	Votes    map[string]map[int]int
	Messages []gerrit.ChangeMessageInfo
	Comments []gerrit.CommentInfo
	// Drafts are the unpublished comments of the authenticated user.
	Drafts  []gerrit.CommentInfo
	Updated time.Time
}

// labels are the labels of every change, with their values.
var labels = map[string]map[string]string{
	"Code-Review": {
		"-2": "This shall not be submitted",
		"-1": "I would prefer this is not submitted as is",
		" 0": "No score",
		"+1": "Looks good to me, but someone else must approve",
		"+2": "Looks good to me, approved",
	},
	"Verified": {
		"-1": "Fails",
		" 0": "No score",
		"+1": "Verified",
	},
}

// Server is a fake Gerrit server.
type Server struct {
	mu       sync.Mutex
	accounts []gerrit.AccountInfo
	self     int
	changes  []*Change
	now      time.Time
	nextID   int
	srv      *http.Server
}

// New creates a server where self is the authenticated account. Its clock
// starts at now and moves on by a second on every change.
func New(self gerrit.AccountInfo, now time.Time) *Server {
	return &Server{accounts: []gerrit.AccountInfo{self}, self: self.AccountID, now: now.UTC()}
}

// AddAccount adds an account the changes can refer to.
func (s *Server) AddAccount(a gerrit.AccountInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = append(s.accounts, a)
}

// AddChange adds a change, uploading its patch sets.
func (s *Server) AddChange(c Change) *Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Number == 0 {
		c.Number = 1
		for _, other := range s.changes {
			c.Number = max(c.Number, other.Number+1)
		}
	}
	c.Project = cmp.Or(c.Project, "demo")
	c.Branch = cmp.Or(c.Branch, "main")
	c.Status = cmp.Or(c.Status, "NEW")
	if c.Votes == nil {
		c.Votes = map[string]map[int]int{}
	}
	if c.Base == nil {
		c.Base = map[string]string{}
	}
	patchSets := c.PatchSets
	c.PatchSets = nil
	ch := &c
	s.changes = append(s.changes, ch)
	for _, ps := range patchSets {
		s.upload(ch, ps, "")
	}
	return ch
}

// upload adds a patch set to c, with a message telling why when not empty.
func (s *Server) upload(c *Change, ps PatchSet, why string) {
	ps.Message = cmp.Or(ps.Message, c.Subject)
	ps.parent = s.parentCommit(c)
	c.PatchSets = append(c.PatchSets, ps)
	msg := fmt.Sprintf("Uploaded patch set %d.", len(c.PatchSets))
	if why != "" {
		msg = fmt.Sprintf("Patch Set %d: %s", len(c.PatchSets), why)
	}
	s.addMessage(c, c.Owner, len(c.PatchSets), msg)
}

func (s *Server) parentCommit(c *Change) string {
	if p := s.change(c.ParentChange); p != nil && len(p.PatchSets) > 0 {
		return commitID(p.Number, len(p.PatchSets))
	}
	return fmt.Sprintf("%040x", 0xba5e)
}

func (s *Server) tick() time.Time {
	s.now = s.now.Add(time.Second)
	return s.now
}

func (s *Server) newID() string {
	s.nextID++
	return fmt.Sprintf("c%04d", s.nextID)
}

func (s *Server) addMessage(c *Change, author, patchSet int, text string) string {
	at := s.tick()
	m := gerrit.ChangeMessageInfo{ID: s.newID(), Author: s.account(author), Date: gerrit.Timestamp{Time: at}, Message: text, RevisionNumber: patchSet}
	c.Messages = append(c.Messages, m)
	c.Updated = at
	return m.ID
}

// Comment is an inline comment to publish with a review.
type Comment struct {
	Path       string
	Line       int
	OnParent   bool
	InReplyTo  string
	Message    string
	Unresolved bool
}

// Review publishes a review by author on patch set ps of change number:
// votes, a message and comments, returning the IDs of the comments.
func (s *Server) Review(number, author, ps int, votes map[string]int, message string, comments ...Comment) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.change(number)
	var drafts []gerrit.CommentInfo
	for _, in := range comments {
		d := gerrit.CommentInfo{ID: s.newID(), Path: in.Path, Line: in.Line, InReplyTo: in.InReplyTo,
			Message: in.Message, Unresolved: in.Unresolved, PatchSet: ps, Author: s.account(author)}
		if in.OnParent {
			d.Side = gerrit.SideParent
		}
		drafts = append(drafts, d)
	}
	s.review(c, author, ps, votes, message, drafts)
	ids := make([]string, len(drafts))
	for i, d := range drafts {
		ids[i] = d.ID
	}
	return ids
}

func (s *Server) review(c *Change, author, ps int, votes map[string]int, message string, comments []gerrit.CommentInfo) {
	text := fmt.Sprintf("Patch Set %d:", ps)
	for _, label := range slices.Sorted(maps.Keys(votes)) {
		if c.Votes[label] == nil {
			c.Votes[label] = map[int]int{}
		}
		c.Votes[label][author] = votes[label]
		text += fmt.Sprintf(" %s%+d", label, votes[label])
	}
	if n := len(comments); n > 0 {
		text += fmt.Sprintf("\n\n(%d comment%s)", n, map[bool]string{true: "s"}[n > 1])
	}
	if message != "" {
		text += "\n\n" + message
	}
	id := s.addMessage(c, author, ps, text)
	at := c.Messages[len(c.Messages)-1].Date
	for _, cm := range comments {
		cm.ChangeMessageID, cm.Updated, cm.Draft = id, at, false
		c.Comments = append(c.Comments, cm)
	}
	if author != c.Owner && !slices.Contains(c.Reviewers, author) {
		c.Reviewers = append(c.Reviewers, author)
	}
	c.Attention = slices.DeleteFunc(c.Attention, func(id int) bool { return id == author })
}

// Change returns a copy of the change number, as it is now.
func (s *Server) Change(number int) (Change, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.change(number)
	if c == nil {
		return Change{}, false
	}
	return *c, true
}

func (s *Server) change(number int) *Change {
	for _, c := range s.changes {
		if c.Number == number {
			return c
		}
	}
	return nil
}

func (s *Server) account(id int) *gerrit.AccountInfo {
	for i := range s.accounts {
		if s.accounts[i].AccountID == id {
			a := s.accounts[i]
			return &a
		}
	}
	return &gerrit.AccountInfo{AccountID: id}
}

// isAccount tells whether account id is the one a query refers to.
func (s *Server) isAccount(id int, ref string) bool {
	if ref == "self" {
		return id == s.self
	}
	a := s.account(id)
	return strconv.Itoa(id) == ref || a.Username == ref || a.Email == ref || strings.EqualFold(a.Name, ref)
}

// Start serves the API on a free port of the loopback interface, returning
// its URL.
func (s *Server) Start() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(l) }()
	return "http://" + l.Addr().String(), nil
}

// Close stops the server started by Start.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Close()
}

// httpError is an error with the status to answer it with.
type httpError struct {
	status int
	msg    string
}

func (e httpError) Error() string { return e.msg }

func errorf(status int, format string, args ...any) error {
	return httpError{status: status, msg: fmt.Sprintf(format, args...)}
}

// ServeHTTP answers the authenticated REST API, under /a/.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if user, password, ok := r.BasicAuth(); !ok || user != User || password != Password {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	path, ok := strings.CutPrefix(r.URL.EscapedPath(), "/a/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	var segments []string
	for seg := range strings.SplitSeq(strings.TrimSuffix(path, "/"), "/") {
		unescaped, err := url.PathUnescape(seg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		segments = append(segments, unescaped)
	}

	s.mu.Lock()
	out, err := s.route(r, segments)
	s.mu.Unlock()

	var he httpError
	switch {
	case errors.As(err, &he):
		http.Error(w, he.msg, he.status)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	case out == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		b, err := json.Marshal(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		_, _ = w.Write(append([]byte(")]}'\n"), b...))
	}
}

func (s *Server) route(r *http.Request, seg []string) (any, error) {
	q := r.URL.Query()
	method := r.Method
	switch {
	case len(seg) == 2 && seg[0] == "accounts" && seg[1] == "self" && method == http.MethodGet:
		return s.account(s.self), nil
	case len(seg) == 1 && seg[0] == "accounts" && method == http.MethodGet:
		return s.suggest(q.Get("q"), q.Get("n")), nil
	case len(seg) == 1 && seg[0] == "changes" && method == http.MethodGet:
		return s.query(q.Get("q"), q.Get("S"), q.Get("n"), q["o"])
	case len(seg) < 2 || seg[0] != "changes":
		return nil, errorf(http.StatusNotFound, "Not found")
	}

	c := s.lookup(seg[1])
	if c == nil {
		return nil, errorf(http.StatusNotFound, "Not found: %s", seg[1])
	}
	rest := seg[2:]
	switch {
	case len(rest) == 0 && method == http.MethodGet:
		return s.info(c, q["o"]), nil
	case len(rest) == 1 && rest[0] == "comments" && method == http.MethodGet:
		return byPath(c.Comments), nil
	case len(rest) == 1 && rest[0] == "drafts" && method == http.MethodGet:
		return byPath(c.Drafts), nil
	case len(rest) == 1 && rest[0] == "submit" && method == http.MethodPost:
		return s.submit(c)
	case len(rest) == 1 && rest[0] == "rebase" && method == http.MethodPost:
		var in gerrit.RebaseInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return s.rebase(c, in.Base)
	case len(rest) < 2 || rest[0] != "revisions":
		return nil, errorf(http.StatusNotFound, "Not found")
	}

	ps, err := revisionNumber(c, rest[1])
	if err != nil {
		return nil, err
	}
	rest = rest[2:]
	switch {
	case len(rest) == 1 && rest[0] == "files" && method == http.MethodGet:
		base, err := baseNumber(c, ps, q.Get("base"))
		if err != nil {
			return nil, err
		}
		return s.files(c, base, ps), nil
	case len(rest) == 3 && rest[0] == "files" && rest[2] == "diff" && method == http.MethodGet:
		base, err := baseNumber(c, ps, q.Get("base"))
		if err != nil {
			return nil, err
		}
		return s.diff(c, base, ps, rest[1])
	case len(rest) == 1 && rest[0] == "ported_comments" && method == http.MethodGet:
		return s.ported(c, ps), nil
	case len(rest) == 1 && rest[0] == "related" && method == http.MethodGet:
		return s.related(c), nil
	case len(rest) == 1 && rest[0] == "review" && method == http.MethodPost:
		var in gerrit.ReviewInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return s.postReview(c, ps, in)
	case len(rest) == 1 && rest[0] == "drafts" && method == http.MethodPut:
		var in gerrit.CommentInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return s.saveDraft(c, ps, "", in)
	case len(rest) == 2 && rest[0] == "drafts" && method == http.MethodPut:
		var in gerrit.CommentInput
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return s.saveDraft(c, ps, rest[1], in)
	case len(rest) == 2 && rest[0] == "drafts" && method == http.MethodDelete:
		return nil, s.deleteDraft(c, rest[1])
	}
	return nil, errorf(http.StatusNotFound, "Not found")
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errorf(http.StatusBadRequest, "invalid input: %v", err)
	}
	return nil
}

// lookup finds a change by number or by its project~branch~number ID.
func (s *Server) lookup(id string) *Change {
	if i := strings.LastIndex(id, "~"); i >= 0 {
		id = id[i+1:]
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return nil
	}
	return s.change(n)
}

func revisionNumber(c *Change, id string) (int, error) {
	if id == "current" {
		return len(c.PatchSets), nil
	}
	for ps := 1; ps <= len(c.PatchSets); ps++ {
		if id == strconv.Itoa(ps) || id == commitID(c.Number, ps) {
			return ps, nil
		}
	}
	return 0, errorf(http.StatusNotFound, "Not found: %s", id)
}

func baseNumber(c *Change, ps int, base string) (int, error) {
	if base == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(base)
	if err != nil || n < 1 || n > len(c.PatchSets) {
		return 0, errorf(http.StatusBadRequest, "invalid base %q", base)
	}
	if n == ps {
		return 0, errorf(http.StatusBadRequest, "base must differ from the revision")
	}
	return n, nil
}

func commitID(number, ps int) string {
	return fmt.Sprintf("%020x%020x", number, ps)
}

// tree returns the files of patch set ps of c, or of its base when ps is 0,
// with the commit message.
func (c *Change) tree(ps int) map[string]string {
	t := map[string]string{}
	for p, content := range c.Base {
		t[p] = content
	}
	if ps == 0 {
		return t
	}
	for p, content := range c.PatchSets[ps-1].Files {
		if content == Deleted {
			delete(t, p)
		} else {
			t[p] = content
		}
	}
	t[gerrit.CommitMessage] = fmt.Sprintf("Parent:     %s\nAuthor:     Change %d\n\n%s\n", c.PatchSets[ps-1].parent[:8], c.Number, c.PatchSets[ps-1].Message)
	return t
}

func (s *Server) files(c *Change, base, ps int) map[string]gerrit.FileInfo {
	a, b := c.tree(base), c.tree(ps)
	out := map[string]gerrit.FileInfo{}
	for p := range keysOf(a, b) {
		oldContent, inA := a[p]
		newContent, inB := b[p]
		if inA && inB && oldContent == newContent && p != gerrit.CommitMessage {
			continue
		}
		var f gerrit.FileInfo
		switch {
		case !inA:
			f.Status = "A"
		case !inB:
			f.Status = "D"
		}
		f.LinesInserted, f.LinesDeleted = countChanges(splitLines(oldContent), splitLines(newContent))
		out[p] = f
	}
	return out
}

func (s *Server) diff(c *Change, base, ps int, path string) (*gerrit.DiffInfo, error) {
	a, b := c.tree(base), c.tree(ps)
	oldContent, inA := a[path]
	newContent, inB := b[path]
	if !inA && !inB {
		return nil, errorf(http.StatusNotFound, "Not found: %s", path)
	}
	d := &gerrit.DiffInfo{ChangeType: "MODIFIED", Content: diffContent(splitLines(oldContent), splitLines(newContent))}
	if inA {
		d.MetaA = &gerrit.DiffFileMeta{Name: path, ContentType: "text/plain", Lines: len(splitLines(oldContent))}
	} else {
		d.ChangeType = "ADDED"
	}
	if inB {
		d.MetaB = &gerrit.DiffFileMeta{Name: path, ContentType: "text/plain", Lines: len(splitLines(newContent))}
	} else {
		d.ChangeType = "DELETED"
	}
	return d, nil
}

// ported returns the comments of the unresolved threads started before
// patch set ps, with their lines mapped onto it.
func (s *Server) ported(c *Change, ps int) map[string][]gerrit.CommentInfo {
	var out []gerrit.CommentInfo
	for _, t := range gerrit.BuildThreads(slices.Clone(c.Comments)) {
		if root := t.Root(); root.PatchSet >= ps || !t.Unresolved() {
			continue
		}
		for _, cm := range t.Comments {
			if cm.Line > 0 && !cm.OnParent() {
				from, to := c.tree(cm.PatchSet)[cm.Path], c.tree(ps)[cm.Path]
				cm.Line = mapLine(splitLines(from), splitLines(to), cm.Line)
			}
			out = append(out, cm)
		}
	}
	return byPath(out)
}

func (s *Server) related(c *Change) map[string][]gerrit.RelatedChangeInfo {
	var chain []gerrit.RelatedChangeInfo
	p := s.change(c.ParentChange)
	if p == nil {
		return map[string][]gerrit.RelatedChangeInfo{"changes": {}}
	}
	for _, ch := range []*Change{c, p} {
		rev := len(ch.PatchSets)
		if ch == p {
			for i := range p.PatchSets {
				if commitID(p.Number, i+1) == c.PatchSets[len(c.PatchSets)-1].parent {
					rev = i + 1
				}
			}
		}
		chain = append(chain, gerrit.RelatedChangeInfo{
			Number: ch.Number, RevisionNumber: rev, CurrentRevisionNumber: len(ch.PatchSets), Status: ch.Status,
			Commit: gerrit.CommitInfo{Commit: commitID(ch.Number, rev), Subject: ch.Subject},
		})
	}
	return map[string][]gerrit.RelatedChangeInfo{"changes": chain}
}

func (s *Server) postReview(c *Change, ps int, in gerrit.ReviewInput) (any, error) {
	for label, v := range in.Labels {
		values, ok := labels[label]
		if !ok {
			return nil, errorf(http.StatusBadRequest, "label %s not found", label)
		}
		if _, ok := values[fmt.Sprintf("%+d", v)]; !ok && v != 0 {
			return nil, errorf(http.StatusBadRequest, "label %s: %d is not a valid value", label, v)
		}
	}
	var drafts []gerrit.CommentInfo
	if in.Drafts == gerrit.PublishAllDrafts {
		drafts, c.Drafts = c.Drafts, nil
	}
	s.review(c, s.self, ps, in.Labels, in.Message, drafts)
	return map[string]any{"labels": in.Labels}, nil
}

func (s *Server) saveDraft(c *Change, ps int, id string, in gerrit.CommentInput) (any, error) {
	if in.Path == "" {
		return nil, errorf(http.StatusBadRequest, "path must be set")
	}
	d := gerrit.CommentInfo{ID: id, Path: in.Path, Line: in.Line, InReplyTo: in.InReplyTo, Message: in.Message,
		PatchSet: ps, Author: s.account(s.self), Updated: gerrit.Timestamp{Time: s.tick()}, Draft: true}
	if in.Side == gerrit.SideParent {
		d.Side = gerrit.SideParent
	}
	if in.Unresolved != nil {
		d.Unresolved = *in.Unresolved
	}
	if id == "" {
		d.ID = s.newID()
		c.Drafts = append(c.Drafts, d)
		return d, nil
	}
	i := slices.IndexFunc(c.Drafts, func(x gerrit.CommentInfo) bool { return x.ID == id })
	if i < 0 {
		return nil, errorf(http.StatusNotFound, "Not found: %s", id)
	}
	c.Drafts[i] = d
	return d, nil
}

func (s *Server) deleteDraft(c *Change, id string) error {
	i := slices.IndexFunc(c.Drafts, func(x gerrit.CommentInfo) bool { return x.ID == id })
	if i < 0 {
		return errorf(http.StatusNotFound, "Not found: %s", id)
	}
	c.Drafts = slices.Delete(c.Drafts, i, i+1)
	return nil
}

// submittable tells whether c has an approval and passed verification,
// without blocking votes.
func submittable(c *Change) bool {
	if c.Status != "NEW" || c.WorkInProgress {
		return false
	}
	maxOf := func(label string) (lo, hi int) {
		for _, v := range c.Votes[label] {
			lo, hi = min(lo, v), max(hi, v)
		}
		return lo, hi
	}
	crLo, crHi := maxOf("Code-Review")
	vLo, vHi := maxOf("Verified")
	return crHi == 2 && crLo > -2 && vHi == 1 && vLo == 0
}

func (s *Server) submit(c *Change) (any, error) {
	if !submittable(c) {
		return nil, errorf(http.StatusConflict, "change %d is not submittable", c.Number)
	}
	c.Status = "MERGED"
	c.Attention = nil
	s.addMessage(c, s.self, len(c.PatchSets), "Change has been successfully merged")
	return map[string]string{"status": c.Status}, nil
}

func (s *Server) rebase(c *Change, base string) (any, error) {
	if c.Status != "NEW" {
		return nil, errorf(http.StatusConflict, "change %d is %s", c.Number, strings.ToLower(c.Status))
	}
	switch {
	case base == "":
		c.ParentChange = 0
	default:
		n, err := strconv.Atoi(strings.Split(base, "/")[0])
		if err != nil || s.change(n) == nil {
			return nil, errorf(http.StatusBadRequest, "base revision %q not found", base)
		}
		c.ParentChange = n
	}
	cur := c.PatchSets[len(c.PatchSets)-1]
	if cur.parent == s.parentCommit(c) {
		return nil, errorf(http.StatusConflict, "Change is already up to date.")
	}
	s.upload(c, PatchSet{Files: cur.Files, Message: cur.Message}, fmt.Sprintf("Patch Set %d was rebased", len(c.PatchSets)))
	return map[string]int{"_number": c.Number}, nil
}

func (s *Server) suggest(q, n string) []gerrit.AccountInfo {
	limit, err := strconv.Atoi(n)
	if err != nil || limit <= 0 {
		limit = 10
	}
	q = strings.ToLower(q)
	var out []gerrit.AccountInfo
	for _, a := range s.accounts {
		if len(out) < limit && strings.Contains(strings.ToLower(a.Name+" "+a.Email+" "+a.Username), q) {
			out = append(out, a)
		}
	}
	return out
}

func (s *Server) query(q, start, limit string, opts []string) ([]gerrit.ChangeInfo, error) {
	pred, err := parseQuery(q)
	if err != nil {
		return nil, errorf(http.StatusBadRequest, "%v", err)
	}
	from, _ := strconv.Atoi(start)
	n, err := strconv.Atoi(limit)
	if err != nil || n <= 0 {
		n = 25
	}
	var matched []*Change
	for _, c := range s.changes {
		if pred(s, c) {
			matched = append(matched, c)
		}
	}
	slices.SortStableFunc(matched, func(a, b *Change) int { return b.Updated.Compare(a.Updated) })
	out := []gerrit.ChangeInfo{}
	for i := from; i < len(matched) && i < from+n; i++ {
		out = append(out, *s.info(matched[i], opts))
	}
	if len(out) > 0 && from+n < len(matched) {
		out[len(out)-1].MoreChanges = true
	}
	return out, nil
}

// info describes c with the details asked for by opts, the "o" options of
// the REST API.
func (s *Server) info(c *Change, opts []string) *gerrit.ChangeInfo {
	has := func(o string) bool { return slices.Contains(opts, o) }
	cur := len(c.PatchSets)
	ins, del := 0, 0
	for p, f := range s.files(c, 0, cur) {
		if p != gerrit.CommitMessage {
			ins, del = ins+f.LinesInserted, del+f.LinesDeleted
		}
	}
	info := &gerrit.ChangeInfo{
		ID:             fmt.Sprintf("%s~%s~%d", c.Project, c.Branch, c.Number),
		Number:         c.Number,
		Project:        c.Project,
		Branch:         c.Branch,
		Topic:          c.Topic,
		Subject:        c.Subject,
		Status:         c.Status,
		WorkInProgress: c.WorkInProgress,
		Owner:          *s.account(c.Owner),
		Updated:        gerrit.Timestamp{Time: c.Updated},
		Insertions:     ins,
		Deletions:      del,
		Labels:         map[string]gerrit.LabelInfo{},
		AttentionSet:   map[string]gerrit.AttentionSetInfo{},
	}
	for _, t := range gerrit.BuildThreads(slices.Clone(c.Comments)) {
		if t.Unresolved() {
			info.UnresolvedCommentCount++
		}
	}
	for _, id := range c.Attention {
		info.AttentionSet[strconv.Itoa(id)] = gerrit.AttentionSetInfo{Account: *s.account(id), Reason: "reviewer or owner"}
	}
	for name, values := range labels {
		l := gerrit.LabelInfo{Values: values}
		for _, id := range slices.Sorted(maps.Keys(c.Votes[name])) {
			v := c.Votes[name][id]
			l.All = append(l.All, gerrit.ApprovalInfo{AccountInfo: *s.account(id), Value: v})
			lo, hi := minMaxValue(values)
			switch {
			case v == hi:
				l.Approved = s.account(id)
			case v == lo:
				l.Rejected = s.account(id)
			case v > 0:
				l.Recommended = s.account(id)
			case v < 0:
				l.Disliked = s.account(id)
			}
		}
		info.Labels[name] = l
	}
	if has("DETAILED_LABELS") && c.Status == "NEW" {
		info.PermittedLabels = map[string][]string{}
		for name, values := range labels {
			info.PermittedLabels[name] = slices.Sorted(maps.Keys(values))
		}
	}
	if has("SUBMITTABLE") {
		info.Submittable = submittable(c)
	}
	if has("MESSAGES") {
		info.Messages = c.Messages
	}
	if cur > 0 && (has("CURRENT_REVISION") || has("ALL_REVISIONS")) {
		info.CurrentRevision = commitID(c.Number, cur)
		info.Revisions = map[string]gerrit.RevisionInfo{}
		for ps := 1; ps <= cur; ps++ {
			if ps != cur && !has("ALL_REVISIONS") {
				continue
			}
			rev := gerrit.RevisionInfo{Number: ps}
			if has("CURRENT_COMMIT") && ps == cur {
				p := c.PatchSets[ps-1]
				subject, _, _ := strings.Cut(p.Message, "\n")
				rev.Commit = &gerrit.CommitInfo{Parents: []gerrit.CommitInfo{{Commit: p.parent}}, Subject: subject, Message: p.Message + "\n"}
			}
			if has("CURRENT_FILES") && ps == cur {
				rev.Files = s.files(c, 0, ps)
			}
			info.Revisions[commitID(c.Number, ps)] = rev
		}
	}
	return info
}

func minMaxValue(values map[string]string) (lo, hi int) {
	for k := range values {
		v, _ := gerrit.ParseVote(k)
		lo, hi = min(lo, v), max(hi, v)
	}
	return lo, hi
}

// byPath groups comments by file, without their path as Gerrit does.
func byPath(comments []gerrit.CommentInfo) map[string][]gerrit.CommentInfo {
	out := map[string][]gerrit.CommentInfo{}
	for _, c := range comments {
		path := c.Path
		c.Path = ""
		out[path] = append(out[path], c)
	}
	return out
}

func keysOf(a, b map[string]string) map[string]bool {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	return keys
}
