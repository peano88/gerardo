package apidoc_test

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/peano88/gerardo/internal/apidoc"
	"github.com/peano88/gerardo/internal/gerrit"
	"github.com/peano88/gerardo/internal/gerrittest"
)

// entities maps the API types of the client to the documented entities.
var entities = map[reflect.Type]string{
	reflect.TypeFor[gerrit.AccountInfo]():       "AccountInfo",
	reflect.TypeFor[gerrit.ActionInfo]():        "ActionInfo",
	reflect.TypeFor[gerrit.ApprovalInfo]():      "ApprovalInfo",
	reflect.TypeFor[gerrit.AttentionSetInfo]():  "AttentionSetInfo",
	reflect.TypeFor[gerrit.ChangeInfo]():        "ChangeInfo",
	reflect.TypeFor[gerrit.ChangeMessageInfo](): "ChangeMessageInfo",
	reflect.TypeFor[gerrit.CommentInfo]():       "CommentInfo",
	reflect.TypeFor[gerrit.CommentInput]():      "CommentInput",
	reflect.TypeFor[gerrit.CommentRange]():      "CommentRange",
	reflect.TypeFor[gerrit.CommitInfo]():        "CommitInfo",
	reflect.TypeFor[gerrit.CherryPickInput]():   "CherryPickInput",
	reflect.TypeFor[gerrit.DiffContent]():       "DiffContent",
	reflect.TypeFor[gerrit.DiffFileMeta]():      "DiffFileMetaInfo",
	reflect.TypeFor[gerrit.DiffInfo]():          "DiffInfo",
	reflect.TypeFor[gerrit.FileInfo]():          "FileInfo",
	reflect.TypeFor[gerrit.HashtagsInput]():     "HashtagsInput",
	reflect.TypeFor[gerrit.LabelInfo]():         "LabelInfo",
	reflect.TypeFor[gerrit.RebaseInput]():       "RebaseInput",
	reflect.TypeFor[gerrit.RelatedChangeInfo](): "RelatedChangeAndCommitInfo",
	reflect.TypeFor[gerrit.RevisionInfo]():      "RevisionInfo",
	reflect.TypeFor[gerrit.ReviewInput]():       "ReviewInput",
}

type comments = map[string][]gerrit.CommentInfo

// responses are the types the client decodes the responses of endpoints into.
var responses = map[string]func() any{
	"GET /accounts/":                                                        func() any { return &[]gerrit.AccountInfo{} },
	"GET /accounts/{account-id}":                                            func() any { return &gerrit.AccountInfo{} },
	"GET /changes/":                                                         func() any { return &[]gerrit.ChangeInfo{} },
	"GET /changes/{change-id}":                                              func() any { return &gerrit.ChangeInfo{} },
	"GET /changes/{change-id}/comments":                                     func() any { return &comments{} },
	"POST /changes/{change-id}/hashtags":                                    func() any { return &[]string{} },
	"POST /changes/{change-id}/revert":                                      func() any { return &gerrit.ChangeInfo{} },
	"POST /changes/{change-id}/revisions/{revision-id}/cherrypick":          func() any { return &gerrit.ChangeInfo{} },
	"GET /changes/{change-id}/drafts":                                       func() any { return &comments{} },
	"GET /changes/{change-id}/revisions/{revision-id}/files/":               func() any { return &map[string]gerrit.FileInfo{} },
	"GET /changes/{change-id}/revisions/{revision-id}/files/{file-id}/diff": func() any { return &gerrit.DiffInfo{} },
	"GET /changes/{change-id}/revisions/{revision-id}/ported_comments":      func() any { return &comments{} },
	"GET /changes/{change-id}/revisions/{revision-id}/related": func() any {
		return &struct {
			Changes []gerrit.RelatedChangeInfo `json:"changes"`
		}{}
	},
}

func specs(t *testing.T) []apidoc.Spec {
	t.Helper()
	dir := os.Getenv("APIDOC_SPECS")
	if dir == "" {
		dir = "testdata"
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "gerrit-*.json"))
	if len(paths) == 0 {
		t.Fatalf("no snapshot in %s: run go generate ./internal/apidoc", dir)
	}
	var out []apidoc.Spec
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var s apidoc.Spec
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, s)
	}
	return out
}

// clientRequests calls every method of the client against the fake server,
// returning the requests it sent.
func clientRequests(t *testing.T) []string {
	t.Helper()
	srv := gerrittest.Demo(time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC))
	url, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c, ctx := gerrit.NewClient(url, gerrittest.User, gerrittest.Password), context.Background()

	called := map[string]error{}
	call := func(method string, err error) { called[method] = err }
	_, _, err = c.QueryChanges(ctx, "is:open", 0, 10, true)
	call("QueryChanges", err)
	_, err = c.SuggestAccounts(ctx, "sam", 5)
	call("SuggestAccounts", err)
	_, err = c.Self(ctx)
	call("Self", err)
	_, err = c.GetChange(ctx, "4210")
	call("GetChange", err)
	_, err = c.ListFiles(ctx, "4210", "3", 1)
	call("ListFiles", err)
	_, err = c.GetDiff(ctx, "4210", "3", 1, "httpclient/client.go")
	call("GetDiff", err)
	_, err = c.ListComments(ctx, "4210")
	call("ListComments", err)
	_, err = c.PortedComments(ctx, "4210", "3")
	call("PortedComments", err)
	_, err = c.RelatedChanges(ctx, "4216")
	call("RelatedChanges", err)
	in := gerrit.CommentInput{Path: "search/page.go", Line: 5, Message: "Nice", Unresolved: new(false)}
	call("CreateDraft", c.CreateDraft(ctx, "4219", "1", in))
	drafts, err := c.ListDrafts(ctx, "4219")
	call("ListDrafts", err)
	if d := drafts["search/page.go"]; len(d) == 1 {
		call("UpdateDraft", c.UpdateDraft(ctx, "4219", "1", d[0].ID, in))
		call("DeleteDraft", c.DeleteDraft(ctx, "4219", "1", d[0].ID))
	}
	call("SetReview", c.SetReview(ctx, "4219", "1", gerrit.ReviewInput{Labels: map[string]int{"Code-Review": 2}}))
	call("Submit", c.Submit(ctx, "4219"))
	call("Rebase", c.Rebase(ctx, "4216", "4215"))
	err = c.SetWorkInProgress(ctx, "4221", false)
	if err == nil {
		err = c.SetWorkInProgress(ctx, "4221", true)
	}
	call("SetWorkInProgress", err)
	_, err = c.SetHashtags(ctx, "4210", []string{"auto-submit"}, []string{"none"})
	call("SetHashtags", err)
	_, err = c.Revert(ctx, "4219")
	call("Revert", err)
	_, err = c.CherryPick(ctx, "4210", "3", "release")
	call("CherryPick", err)
	call("Abandon", c.Abandon(ctx, "4210"))
	call("Restore", c.Restore(ctx, "4210"))
	call("RunAction", c.RunAction(ctx, "4215", "", "demo~nudge", gerrit.ActionInfo{Method: "POST"}))

	for i := range reflect.TypeFor[*gerrit.Client]().NumMethod() {
		name := reflect.TypeFor[*gerrit.Client]().Method(i).Name
		err, ok := called[name]
		switch {
		case name == "BaseURL":
		case !ok:
			t.Errorf("Client.%s is not called: add it to clientRequests", name)
		case err != nil:
			t.Errorf("Client.%s: %v", name, err)
		}
	}
	return srv.Requests()
}

// TestEntitiesCovered checks that every API type of the client is checked
// against an entity.
func TestEntitiesCovered(t *testing.T) {
	pkgs, err := parser.ParseDir(token.NewFileSet(), "../gerrit", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	mapped := map[string]bool{}
	for typ := range entities {
		mapped[typ.Name()] = true
	}
	for _, f := range pkgs["gerrit"].Files {
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				if field.Tag != nil && strings.Contains(field.Tag.Value, `json:"`) && !mapped[spec.Name.Name] {
					t.Errorf("gerrit.%s has JSON fields but no documented entity: add it to entities", spec.Name.Name)
					break
				}
			}
			return true
		})
	}
}

// TestContract checks the client, and the fake server answering it, against
// the documentation of every Gerrit version snapshotted in testdata.
func TestContract(t *testing.T) {
	requests := clientRequests(t)
	for _, spec := range specs(t) {
		t.Run(spec.Version, func(t *testing.T) {
			for typ, entity := range entities {
				checkFields(t, spec, typ, entity)
			}

			used := map[string]bool{}
			for _, r := range requests {
				// The views of plugins, named plugin~view, are documented
				// by the plugins.
				if strings.Contains(r[strings.LastIndex(r, "/")+1:], "~") {
					continue
				}
				e, ok := spec.Endpoint(r)
				if !ok {
					t.Errorf("%s is not a documented endpoint", r)
					continue
				}
				used[e] = true
			}

			for _, ex := range spec.Examples {
				e, ok := spec.Endpoint(ex.Request)
				newValue := responses[e]
				// Several queries at once answer a list per query: the
				// client sends one.
				if !ok || !used[e] || newValue == nil || strings.Count(ex.Request, "q=") > 1 {
					continue
				}
				if err := json.Unmarshal(ex.Response, newValue()); err != nil {
					t.Errorf("the documented response of %s does not decode: %v", ex.Request, err)
				}
			}
		})
	}
}

// checkFields checks that the JSON fields of typ are documented in entity,
// those of an embedded struct in the entity of the embedded type.
func checkFields(t *testing.T, spec apidoc.Spec, typ reflect.Type, entity string) {
	t.Helper()
	fields, ok := spec.Entities[entity]
	if !ok {
		t.Errorf("entity %s is not documented", entity)
		return
	}
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Anonymous {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if _, ok := fields[name]; !ok {
			t.Errorf("%s.%s: field %q is not documented in %s", typ.Name(), f.Name, name, entity)
		}
	}
}

// TestEndpoint checks the matching of requests to endpoint templates.
func TestEndpoint(t *testing.T) {
	spec := apidoc.Spec{Endpoints: []string{
		"GET /accounts/{account-id}",
		"GET /changes/{change-id}/revisions/{revision-id}/files/",
		"GET /changes/{change-id}/revisions/{revision-id}/files/{file-id}/diff",
		"GET /changes/{change-id}/revisions/{revision-id}/{x}",
	}}
	for request, want := range map[string]string{
		"GET /accounts/self":                                    "GET /accounts/{account-id}",
		"GET /changes/42/revisions/3/files/?base=1":             "GET /changes/{change-id}/revisions/{revision-id}/files/",
		"GET /changes/42/revisions/3/files/a%2Fb.go/diff":       "GET /changes/{change-id}/revisions/{revision-id}/files/{file-id}/diff",
		"GET /changes/42/revisions/3/ported_comments":           "GET /changes/{change-id}/revisions/{revision-id}/{x}",
		"PUT /accounts/self":                                    "",
		"GET /changes/42/revisions/3/files/a%2Fb.go/diff/extra": "",
	} {
		if got, _ := spec.Endpoint(request); got != want {
			t.Errorf("%s: got %q, want %q", request, got, want)
		}
	}
	if !slices.IsSorted(spec.Endpoints) {
		t.Fatal("test endpoints should be sorted")
	}
}
