// Command gerrit-tui is a terminal client for reviewing Gerrit changes.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerrit-tui/internal/gerrit"
	"github.com/peano88/gerrit-tui/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gerrit-tui:", err)
		os.Exit(1)
	}
}

func run() error {
	baseURL := flag.String("url", envOr("GERRIT_URL", "https://review.example.com"), "Gerrit server URL")
	query := flag.String("q", "is:open (owner:self OR reviewer:self)", "Gerrit search query for the dashboard")
	flag.Parse()

	user, password, err := credentials(*baseURL)
	if err != nil {
		return err
	}

	client := gerrit.NewClient(*baseURL, user, password)
	_, err = tea.NewProgram(ui.New(client, *query), tea.WithAltScreen()).Run()
	return err
}

func credentials(baseURL string) (string, string, error) {
	if u, p := os.Getenv("GERRIT_USER"), os.Getenv("GERRIT_PASSWORD"); u != "" && p != "" {
		return u, p, nil
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid Gerrit URL: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	user, password, err := gerrit.NetrcCredentials(filepath.Join(home, ".netrc"), parsed.Hostname())
	if errors.Is(err, gerrit.ErrNoCredentials) || errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("no credentials for %s: generate an HTTP password in Gerrit (Settings → HTTP Credentials) "+
			"and add `machine %s login <user> password <http-password>` to ~/.netrc, or set GERRIT_USER/GERRIT_PASSWORD",
			parsed.Hostname(), parsed.Hostname())
	}
	return user, password, err
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
