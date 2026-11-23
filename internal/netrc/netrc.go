// Package netrc reads credentials from a .netrc file.
package netrc

import (
	"errors"
	"os"
	"strings"
)

// ErrNotFound is returned when no entry matches the host.
var ErrNotFound = errors.New("no credentials found")

// Lookup returns the login and password of the entry for host in the file at
// path, falling back to the "default" entry.
func Lookup(path, host string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}

	type entry struct{ login, password string }
	var matched, fallback *entry
	var current *entry

	tokens := strings.Fields(string(data))
	for i := 0; i < len(tokens); i++ {
		next := func() string {
			if i+1 < len(tokens) {
				i++
				return tokens[i]
			}
			return ""
		}
		switch tokens[i] {
		case "machine":
			current = nil
			if next() == host && matched == nil {
				matched = &entry{}
				current = matched
			}
		case "default":
			current = nil
			if fallback == nil {
				fallback = &entry{}
				current = fallback
			}
		case "login":
			if v := next(); current != nil {
				current.login = v
			}
		case "password":
			if v := next(); current != nil {
				current.password = v
			}
		}
	}

	for _, e := range []*entry{matched, fallback} {
		if e != nil && e.login != "" && e.password != "" {
			return e.login, e.password, nil
		}
	}
	return "", "", ErrNotFound
}
