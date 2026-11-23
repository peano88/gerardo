package gerrittest

import (
	"strings"
	"time"

	"github.com/peano88/gerardo/internal/gerrit"
)

// Accounts of the demo.
var (
	Alex = gerrit.AccountInfo{AccountID: 1000, Name: "Alex Doe", Email: "alex@example.com", Username: "alex"}
	Sam  = gerrit.AccountInfo{AccountID: 1001, Name: "Sam Rivera", Email: "sam@example.com", Username: "sam"}
	Kim  = gerrit.AccountInfo{AccountID: 1002, Name: "Kim Lee", Email: "kim@example.com", Username: "kim"}
	Jo   = gerrit.AccountInfo{AccountID: 1003, Name: "Jo Park", Email: "jo@example.com", Username: "jo"}
	Bot  = gerrit.AccountInfo{AccountID: 1004, Name: "CI Bot", Email: "ci@example.com", Username: "ci"}
)

// Upload adds a patch set to change number.
func (s *Server) Upload(number int, ps PatchSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upload(s.change(number), ps, "")
}

// Advance moves the clock of the server on by d.
func (s *Server) Advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
}

func lines(l ...string) string {
	return strings.Join(l, "\n") + "\n"
}

// Demo returns a server holding a few changes of a made-up company, seen by
// Alex: changes of their own in review, changes to review, one ready to be
// submitted, one stacked on an outdated patch set and a merged one. Its
// clock ends at now.
func Demo(now time.Time) *Server {
	s := New(Alex, now.Add(-30*time.Hour))
	for _, a := range []gerrit.AccountInfo{Sam, Kim, Jo, Bot} {
		s.AddAccount(a)
	}
	verified := map[string]int{"Verified": 1}

	clientGo := lines(
		"package httpclient",
		"",
		"import (",
		"\t\"context\"",
		"\t\"net/http\"",
		")",
		"",
		"// Client sends requests to the backend services.",
		"type Client struct {",
		"\thttp *http.Client",
		"}",
		"",
		"// Do sends a request.",
		"func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {",
		"\treturn c.http.Do(req.WithContext(ctx))",
		"}",
	)
	retryPS1 := lines(
		"package httpclient",
		"",
		"import (",
		"\t\"context\"",
		"\t\"net/http\"",
		")",
		"",
		"// Client sends requests to the backend services.",
		"type Client struct {",
		"\thttp *http.Client",
		"}",
		"",
		"// Do sends a request, retrying it when the service is unavailable.",
		"func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {",
		"\tfor {",
		"\t\tresp, err := c.http.Do(req.WithContext(ctx))",
		"\t\tif err != nil || resp.StatusCode != http.StatusServiceUnavailable {",
		"\t\t\treturn resp, err",
		"\t\t}",
		"\t\tresp.Body.Close()",
		"\t}",
		"}",
	)
	retryPS2 := lines(
		"package httpclient",
		"",
		"import (",
		"\t\"context\"",
		"\t\"math/rand/v2\"",
		"\t\"net/http\"",
		"\t\"time\"",
		")",
		"",
		"const maxAttempts = 5",
		"",
		"// Client sends requests to the backend services.",
		"type Client struct {",
		"\thttp *http.Client",
		"}",
		"",
		"// Do sends a request, retrying it with an exponential backoff when the",
		"// service is unavailable.",
		"func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {",
		"\tdelay := 100 * time.Millisecond",
		"\tfor attempt := 1; ; attempt++ {",
		"\t\tresp, err := c.http.Do(req.WithContext(ctx))",
		"\t\tif err != nil || resp.StatusCode != http.StatusServiceUnavailable || attempt == maxAttempts {",
		"\t\t\treturn resp, err",
		"\t\t}",
		"\t\tresp.Body.Close()",
		"\t\tjitter := time.Duration(rand.Int64N(int64(delay / 2)))",
		"\t\tselect {",
		"\t\tcase <-ctx.Done():",
		"\t\t\treturn nil, ctx.Err()",
		"\t\tcase <-time.After(delay + jitter):",
		"\t\t}",
		"\t\tdelay *= 2",
		"\t}",
		"}",
	)
	retryPS3 := strings.Replace(retryPS2, "const maxAttempts = 5\n", "// maxAttempts bounds the requests sent for a call, retries included.\nconst maxAttempts = 5\n", 1)
	retryTest := lines(
		"package httpclient",
		"",
		"import (",
		"\t\"net/http\"",
		"\t\"net/http/httptest\"",
		"\t\"testing\"",
		")",
		"",
		"func TestDoRetriesUnavailable(t *testing.T) {",
		"\tcalls := 0",
		"\tsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {",
		"\t\tif calls++; calls < 3 {",
		"\t\t\tw.WriteHeader(http.StatusServiceUnavailable)",
		"\t\t}",
		"\t}))",
		"\tdefer srv.Close()",
		"",
		"\treq, _ := http.NewRequest(http.MethodGet, srv.URL, nil)",
		"\tresp, err := (&Client{http: srv.Client()}).Do(t.Context(), req)",
		"\tif err != nil || resp.StatusCode != http.StatusOK || calls != 3 {",
		"\t\tt.Fatalf(\"got %v, %v after %d calls\", resp, err, calls)",
		"\t}",
		"}",
	)

	s.AddChange(Change{
		Number: 4210, Project: "acme/backend", Branch: "main", Subject: "Retry unavailable services with a backoff",
		Owner: Alex.AccountID, Reviewers: []int{Sam.AccountID, Kim.AccountID},
		Base:      map[string]string{"httpclient/client.go": clientGo},
		PatchSets: []PatchSet{{Files: map[string]string{"httpclient/client.go": retryPS1}}},
	})
	s.Advance(3 * time.Hour)
	ids := s.Review(4210, Sam.AccountID, 1, nil, "Nice, a few things.",
		Comment{Path: "httpclient/client.go", Line: 15, Message: "This retries forever: can we cap the attempts?", Unresolved: true},
		Comment{Path: "httpclient/client.go", Line: 13, Message: "Retrying right away will hammer a service that is already down.", Unresolved: true},
	)
	s.Advance(2 * time.Hour)
	s.Upload(4210, PatchSet{Files: map[string]string{"httpclient/client.go": retryPS2}})
	s.Review(4210, Alex.AccountID, 1, nil, "",
		Comment{Path: "httpclient/client.go", Line: 15, InReplyTo: ids[0], Message: "Done, at most 5 attempts."},
		Comment{Path: "httpclient/client.go", Line: 13, InReplyTo: ids[1], Message: "Done: exponential backoff with jitter."},
	)
	s.Advance(4 * time.Hour)
	s.Review(4210, Sam.AccountID, 2, map[string]int{"Code-Review": 1}, "Looks good, one nit.",
		Comment{Path: "httpclient/client.go", Line: 10, Message: "Could you document why 5?", Unresolved: true},
	)
	s.Advance(20 * time.Hour)
	s.Upload(4210, PatchSet{Files: map[string]string{"httpclient/client.go": retryPS3, "httpclient/client_test.go": retryTest}})
	s.Review(4210, Bot.AccountID, 3, verified, "Build succeeded.")
	s.Review(4210, Kim.AccountID, 3, nil, "",
		Comment{Path: "httpclient/client_test.go", Line: 19, Message: "t.Context() needs Go 1.24, is the CI image up to date?", Unresolved: true},
	)
	s.setAttention(4210, Alex.AccountID)

	serviceGo := lines(
		"package profiles",
		"",
		"import \"context\"",
		"",
		"// Service serves the profiles of the users.",
		"type Service struct {",
		"\tstore Store",
		"}",
		"",
		"// Get returns the profile of a user.",
		"func (s *Service) Get(ctx context.Context, id string) (Profile, error) {",
		"\treturn s.store.Load(ctx, id)",
		"}",
	)
	cachePS1 := lines(
		"package profiles",
		"",
		"import \"sync\"",
		"",
		"// cache holds the profiles loaded recently.",
		"type cache struct {",
		"\tmu       sync.Mutex",
		"\tprofiles map[string]Profile",
		"}",
		"",
		"func (c *cache) get(id string) (Profile, bool) {",
		"\tc.mu.Lock()",
		"\tdefer c.mu.Unlock()",
		"\tp, ok := c.profiles[id]",
		"\treturn p, ok",
		"}",
		"",
		"func (c *cache) put(id string, p Profile) {",
		"\tc.mu.Lock()",
		"\tdefer c.mu.Unlock()",
		"\tc.profiles[id] = p",
		"}",
	)
	cachePS2 := strings.Replace(strings.Replace(cachePS1,
		"\tmu       sync.Mutex\n", "\tmu       sync.RWMutex\n", 1),
		"\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n\tp, ok", "\tc.mu.RLock()\n\tdefer c.mu.RUnlock()\n\tp, ok", 1)
	cachedService := strings.Replace(strings.Replace(serviceGo,
		"\tstore Store\n", "\tstore Store\n\tcache cache\n", 1),
		"\treturn s.store.Load(ctx, id)\n", lines(
			"\tif p, ok := s.cache.get(id); ok {",
			"\t\treturn p, nil",
			"\t}",
			"\tp, err := s.store.Load(ctx, id)",
			"\tif err == nil {",
			"\t\ts.cache.put(id, p)",
			"\t}",
			"\treturn p, err",
		), 1)

	s.Advance(-30 * time.Hour)
	s.AddChange(Change{
		Number: 4215, Project: "acme/backend", Branch: "main", Subject: "Cache user profiles in memory", Topic: "profile-cache",
		Owner: Kim.AccountID, Reviewers: []int{Alex.AccountID, Sam.AccountID},
		Base: map[string]string{"profiles/service.go": serviceGo},
		PatchSets: []PatchSet{{Files: map[string]string{"profiles/cache.go": cachePS1, "profiles/service.go": cachedService},
			Message: "Cache user profiles in memory\n\nProfiles are read on every request but rarely change."}},
	})
	s.Advance(time.Hour)
	metrics := lines(
		"package profiles",
		"",
		"import \"expvar\"",
		"",
		"var cacheHits = expvar.NewInt(\"profiles_cache_hits\")",
	)
	s.AddChange(Change{
		Number: 4216, Project: "acme/backend", Branch: "main", Subject: "Export the hit count of the profile cache", Topic: "profile-cache",
		Owner: Kim.AccountID, Reviewers: []int{Alex.AccountID}, ParentChange: 4215,
		Base:      map[string]string{"profiles/service.go": cachedService, "profiles/cache.go": cachePS1},
		PatchSets: []PatchSet{{Files: map[string]string{"profiles/metrics.go": metrics}}},
	})
	s.Advance(5 * time.Hour)
	s.Review(4215, Sam.AccountID, 1, map[string]int{"Code-Review": -1}, "",
		Comment{Path: "profiles/cache.go", Line: 7, Message: "Reads dominate, an RWMutex would let them run in parallel.", Unresolved: true},
		Comment{Path: "profiles/cache.go", Line: 0, Message: "Nothing ever evicts the profiles: is the memory bounded?", Unresolved: true},
	)
	s.Advance(6 * time.Hour)
	s.Upload(4215, PatchSet{Files: map[string]string{"profiles/cache.go": cachePS2, "profiles/service.go": cachedService},
		Message: "Cache user profiles in memory\n\nProfiles are read on every request but rarely change."})
	s.Review(4215, Bot.AccountID, 2, verified, "Build succeeded.")
	s.setAttention(4215, Alex.AccountID)
	s.setAttention(4216, Alex.AccountID)

	searchGo := lines(
		"package search",
		"",
		"// page returns the results of page n, counted from 1.",
		"func page(results []Result, n, size int) []Result {",
		"\tstart := n * size",
		"\tif start >= len(results) {",
		"\t\treturn nil",
		"\t}",
		"\treturn results[start:min(start+size, len(results))]",
		"}",
	)
	s.Advance(2 * time.Hour)
	s.AddChange(Change{
		Number: 4219, Project: "acme/backend", Branch: "main", Subject: "Fix the first page of search results being skipped",
		Owner: Jo.AccountID, Reviewers: []int{Alex.AccountID, Sam.AccountID},
		Base:      map[string]string{"search/page.go": searchGo},
		PatchSets: []PatchSet{{Files: map[string]string{"search/page.go": strings.Replace(searchGo, "start := n * size", "start := (n - 1) * size", 1)}}},
	})
	s.Review(4219, Bot.AccountID, 1, verified, "Build succeeded.")
	s.Review(4219, Sam.AccountID, 1, map[string]int{"Code-Review": 2}, "Good catch!")

	s.Advance(time.Hour)
	s.AddChange(Change{
		Number: 4221, Project: "acme/backend", Branch: "main", Subject: "Read the settings from a TOML file",
		Owner: Alex.AccountID, WorkInProgress: true,
		Base:      map[string]string{"config/config.go": lines("package config", "", "// Port is the port to listen on.", "var Port = 8080")},
		PatchSets: []PatchSet{{Files: map[string]string{"config/config.go": lines("package config", "", "// Settings are read from settings.toml.", "type Settings struct {", "\tPort int `toml:\"port\"`", "}")}}},
	})

	headerTsx := lines(
		"export function Header({ user }: { user: User }) {",
		"  return (",
		"    <header>",
		"      <Logo />",
		"      <span>{user.name}</span>",
		"    </header>",
		"  );",
		"}",
	)
	withAvatar := strings.Replace(headerTsx, "      <span>{user.name}</span>\n", "      <img src={user.avatarUrl} />\n      <span>{user.name}</span>\n", 1)
	withAlt := strings.Replace(withAvatar, "<img src={user.avatarUrl} />", "<img src={user.avatarUrl} alt=\"\" width={24} height={24} />", 1)
	s.Advance(4 * time.Hour)
	s.AddChange(Change{
		Number: 4224, Project: "acme/web", Branch: "main", Subject: "Show the avatar of the user in the header",
		Owner: Sam.AccountID, Reviewers: []int{Alex.AccountID},
		Base:      map[string]string{"src/Header.tsx": headerTsx},
		PatchSets: []PatchSet{{Files: map[string]string{"src/Header.tsx": withAvatar}}},
	})
	s.Advance(2 * time.Hour)
	s.Review(4224, Alex.AccountID, 1, nil, "",
		Comment{Path: "src/Header.tsx", Line: 5, Message: "The image needs an alt text, and a size so that the header does not jump.", Unresolved: true},
	)
	s.Advance(3 * time.Hour)
	s.Upload(4224, PatchSet{Files: map[string]string{"src/Header.tsx": withAlt}})
	s.saveDemoDraft(4224, gerrit.CommentInfo{Path: "src/Header.tsx", Line: 5, PatchSet: 2,
		Message: "Thanks! Could the size come from the theme?", Unresolved: true})
	s.setAttention(4224, Alex.AccountID)

	s.Advance(-50 * time.Hour)
	s.AddChange(Change{
		Number: 4198, Project: "acme/backend", Branch: "main", Subject: "Build with Go 1.25", Status: "MERGED",
		Owner: Jo.AccountID, Reviewers: []int{Alex.AccountID},
		Base:      map[string]string{"go.mod": lines("module example.com/acme/backend", "", "go 1.24")},
		PatchSets: []PatchSet{{Files: map[string]string{"go.mod": lines("module example.com/acme/backend", "", "go 1.25")}}},
	})
	s.Review(4198, Alex.AccountID, 1, map[string]int{"Code-Review": 2}, "")

	s.mu.Lock()
	s.now = now.UTC()
	s.mu.Unlock()
	return s
}

func (s *Server) setAttention(number, account int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.change(number)
	c.Attention = append(c.Attention, account)
}

func (s *Server) saveDemoDraft(number int, d gerrit.CommentInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.change(number)
	d.ID, d.Author, d.Updated, d.Draft = s.newID(), s.account(s.self), gerrit.Timestamp{Time: s.tick()}, true
	c.Drafts = append(c.Drafts, d)
}
