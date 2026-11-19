// Package config reads the gerrit-tui configuration file.
//
// The file holds `key = value` lines, a subset of TOML: strings are quoted,
// booleans and integers are bare, lists of strings are in brackets and may
// span lines, and # starts a comment.
//
//	url = "https://review.example.com"
//	query = "is:open (owner:self OR reviewer:self)"
//	style = "catppuccin-mocha"
//	side_by_side = true
//	page_size = 50
//	hidden_paths = ["services/accounts"]
//	visible_paths = [
//	  "services/accounts/pkg",
//	  "services/accounts/billing",
//	]
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds the settings of gerrit-tui.
type Config struct {
	URL        string
	Query      string
	Style      string
	SideBySide bool
	PageSize   int
	// HiddenPaths are patterns of files: the changes touching only files
	// matching them, and not VisiblePaths, are left out of the change lists.
	HiddenPaths  []string
	VisiblePaths []string
}

// Default returns the settings used when the file does not set them.
func Default() Config {
	return Config{
		URL:      "https://review.example.com",
		Query:    "is:open (owner:self OR reviewer:self)",
		Style:    "catppuccin-mocha",
		PageSize: 50,
	}
}

// Path returns the location of the configuration file.
func Path() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "gerrit-tui", "config.toml"), nil
}

// Load reads the file at path over the defaults; a missing file is no error.
func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	if err := parse(f, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func parse(r io.Reader, c *Config) error {
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("line %d: expected key = value", n)
		}
		start := n
		key, value = strings.TrimSpace(key), stripComment(strings.TrimSpace(value))
		// A list continues until its closing bracket.
		for strings.HasPrefix(value, "[") && !strings.HasSuffix(value, "]") {
			if !sc.Scan() {
				return fmt.Errorf("line %d: %s: unterminated list", start, key)
			}
			n++
			if next := stripComment(strings.TrimSpace(sc.Text())); next != "" {
				value += " " + next
			}
		}
		if err := c.set(key, value); err != nil {
			return fmt.Errorf("line %d: %s: %w", start, key, err)
		}
	}
	return sc.Err()
}

// stripComment removes a trailing comment, outside of a quoted string.
func stripComment(value string) string {
	if strings.HasPrefix(value, `"`) {
		if end := strings.LastIndex(value, `"`); end > 0 {
			rest, _, _ := strings.Cut(value[end+1:], "#")
			return value[:end+1] + strings.TrimSpace(rest)
		}
		return value
	}
	v, _, _ := strings.Cut(value, "#")
	return strings.TrimSpace(v)
}

func (c *Config) set(key, value string) error {
	var err error
	switch key {
	case "url":
		c.URL, err = unquote(value)
	case "query":
		c.Query, err = unquote(value)
	case "style":
		c.Style, err = unquote(value)
	case "side_by_side":
		c.SideBySide, err = strconv.ParseBool(value)
	case "page_size":
		c.PageSize, err = strconv.Atoi(value)
		if err == nil && (c.PageSize < 1 || c.PageSize > 500) {
			err = errors.New("must be between 1 and 500")
		}
	case "hidden_paths":
		c.HiddenPaths, err = stringList(value)
	case "visible_paths":
		c.VisiblePaths, err = stringList(value)
	default:
		err = errors.New("unknown setting")
	}
	return err
}

// stringList parses a list of quoted strings, such as ["a", "b",].
func stringList(value string) ([]string, error) {
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, errors.New("expected a list of quoted strings")
	}
	var list []string
	for _, item := range strings.Split(value[1:len(value)-1], ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		s, err := unquote(item)
		if err != nil || s == "" {
			return nil, errors.New("expected a list of quoted strings")
		}
		list = append(list, s)
	}
	return list, nil
}

func unquote(value string) (string, error) {
	s, err := strconv.Unquote(value)
	if err != nil {
		return "", errors.New("expected a quoted string")
	}
	return s, nil
}
