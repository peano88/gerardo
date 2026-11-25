// Package config reads the gerardo configuration file, in TOML.
//
//	url = "https://review.example.com"
//	query = "is:open (owner:self OR reviewer:self)"
//	style = "catppuccin-mocha"
//	side_by_side = true
//	page_size = 50
//	group_by_project = true
//	hidden_paths = ["services/accounts"]
//	visible_paths = [
//	  "services/accounts/pkg",
//	  "services/accounts/billing",
//	]
//	hashtags = ["auto-submit"]
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds the settings of gerardo.
type Config struct {
	URL        string `toml:"url"`
	Query      string `toml:"query"`
	Style      string `toml:"style"`
	SideBySide bool   `toml:"side_by_side"`
	PageSize   int    `toml:"page_size"`
	// GroupByProject groups the change lists by project.
	GroupByProject bool `toml:"group_by_project"`
	// HiddenPaths are patterns of files: the changes touching only files
	// matching them, and not VisiblePaths, are left out of the change lists.
	HiddenPaths  []string `toml:"hidden_paths"`
	VisiblePaths []string `toml:"visible_paths"`
	// Hashtags are offered when editing the hashtags of a change.
	Hashtags []string `toml:"hashtags"`
}

// Default returns the settings used when the file does not set them.
func Default() Config {
	return Config{
		Query:          "is:open (owner:self OR reviewer:self)",
		Style:          "catppuccin-mocha",
		PageSize:       50,
		GroupByProject: true,
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
	return filepath.Join(dir, "gerardo", "config.toml"), nil
}

// Load reads the file at path over the defaults; a missing file is no error.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := parse(string(b), &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// parse reads the settings of a file over c, rejecting unknown ones.
func parse(content string, c *Config) error {
	md, err := toml.Decode(content, c)
	if err != nil {
		return err
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return fmt.Errorf("unknown setting %s", strings.Join(keys, ", "))
	}
	if c.PageSize < 1 || c.PageSize > 500 {
		return errors.New("page_size: must be between 1 and 500")
	}
	return nil
}
