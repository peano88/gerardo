package ui

import (
	"regexp"
	"strings"

	"github.com/peano88/gerardo/internal/gerrit"
)

// pathFilter hides the changes whose files all match a hidden pattern and no
// visible one, e.g. the changes of a part of a monorepo.
type pathFilter struct {
	patterns        []string
	hidden, visible []*regexp.Regexp
}

// paths is the filter of the change lists.
var paths pathFilter

// SetPathFilter hides from the change lists the changes touching only files
// matching hidden and not visible. A pattern matches a path and everything
// below it; * matches any characters, including /, and ? a single one.
func SetPathFilter(hidden, visible []string) {
	paths = pathFilter{patterns: hidden, hidden: globs(hidden), visible: globs(visible)}
}

func globs(patterns []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range patterns {
		var b strings.Builder
		b.WriteString("^")
		for _, part := range strings.Split(strings.TrimSuffix(p, "/"), "*") {
			if b.Len() > 1 {
				b.WriteString(".*")
			}
			b.WriteString(strings.ReplaceAll(regexp.QuoteMeta(part), `\?`, "."))
		}
		b.WriteString("(/.*)?$")
		out = append(out, regexp.MustCompile(b.String()))
	}
	return out
}

func (f pathFilter) active() bool {
	return len(f.hidden) > 0
}

func (f pathFilter) hidesPath(path string) bool {
	match := func(res []*regexp.Regexp) bool {
		for _, re := range res {
			if re.MatchString(path) {
				return true
			}
		}
		return false
	}
	return match(f.hidden) && !match(f.visible)
}

// hides reports whether every file of the current revision of c is hidden.
// Changes without known files are kept.
func (f pathFilter) hides(c *gerrit.ChangeInfo) bool {
	if !f.active() {
		return false
	}
	rev, ok := c.Current()
	if !ok {
		return false
	}
	files := 0
	for p := range rev.Files {
		if strings.HasPrefix(p, "/") { // /COMMIT_MSG, /MERGE_LIST
			continue
		}
		files++
		if !f.hidesPath(p) {
			return false
		}
	}
	return files > 0
}
