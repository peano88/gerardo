package gerrit

import (
	"strings"
)

// ExcludeProjects narrows query to leave out the projects matching hidden,
// except those matching visible. Patterns are globs where * matches any
// characters, including /, and ? a single one.
func ExcludeProjects(query string, hidden, visible []string) string {
	if len(hidden) == 0 {
		return query
	}
	terms := make([]string, len(hidden))
	for i, p := range hidden {
		terms[i] = projectTerm(p)
	}
	excluded := terms[0]
	if len(terms) > 1 {
		excluded = "(" + strings.Join(terms, " OR ") + ")"
	}
	for _, p := range visible {
		excluded += " -" + projectTerm(p)
	}
	return "(" + query + ") -(" + excluded + ")"
}

// projectTerm matches the projects of a glob pattern.
func projectTerm(pattern string) string {
	if !strings.ContainsAny(pattern, "*?") {
		return `project:"` + pattern + `"`
	}
	var b strings.Builder
	b.WriteString(`project:"^`)
	for _, r := range pattern {
		switch {
		case r == '*':
			b.WriteString(".*")
		case r == '?':
			b.WriteByte('.')
		case strings.ContainsRune(`.+|{}[]()\#@&<>~^$`, r):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
