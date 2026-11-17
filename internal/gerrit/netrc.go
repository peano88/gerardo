package gerrit

import (
	"errors"
	"os"
	"strings"
)

// ErrNoCredentials is returned when no netrc entry matches the host.
var ErrNoCredentials = errors.New("no credentials found")

// NetrcCredentials returns the login and password of the netrc entry for host,
// falling back to the "default" entry.
func NetrcCredentials(path, host string) (string, string, error) {
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
	return "", "", ErrNoCredentials
}
