// Command gerardo is a terminal client for reviewing Gerrit changes.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peano88/gerardo/internal/config"
	"github.com/peano88/gerardo/internal/gerrit"
	"github.com/peano88/gerardo/internal/gerrittest"
	"github.com/peano88/gerardo/internal/netrc"
	"github.com/peano88/gerardo/internal/ui"
)

// version is set at build time, by -ldflags "-X main.version=...".
var version string

// appVersion returns the version set at build time, else the module version
// of a go install build.
func appVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gerardo:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	baseURL := flag.String("url", envOr("GERRIT_URL", cfg.URL), "Gerrit server URL")
	query := flag.String("q", cfg.Query, "Gerrit search query for the dashboard")
	flag.StringVar(&ui.SyntaxStyle, "style", envOr("GERARDO_STYLE", cfg.Style), "chroma style used to highlight diffs")
	flag.BoolVar(&ui.SideBySide, "side-by-side", cfg.SideBySide, "show diffs side by side")
	demo := flag.Bool("demo", false, "explore made-up changes served by a built-in fake Gerrit server")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: gerardo [flags]\n\nFlags override the environment, which overrides %s.\n\n", configPath)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("gerardo", appVersion())
		return nil
	}
	ui.PageSize, ui.GroupByProject = cfg.PageSize, cfg.GroupByProject
	if *demo {
		// The settings of a real server could hide the demo changes.
		demoQuery := config.Default().Query
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "q" {
				demoQuery = *query
			}
		})
		return runDemo(demoQuery)
	}
	ui.SetPathFilter(cfg.HiddenPaths, cfg.VisiblePaths)
	if *baseURL == "" {
		return fmt.Errorf("no Gerrit server: set url = \"https://review.example.com\" in %s, GERRIT_URL or -url", configPath)
	}

	user, password, err := credentials(*baseURL)
	if err != nil {
		return err
	}

	client := gerrit.NewClient(*baseURL, user, password)
	_, err = tea.NewProgram(ui.New(client, *query), tea.WithAltScreen()).Run()
	return err
}

// runDemo runs the app on the demo changes of a fake server; what is done
// in it is lost on exit.
func runDemo(query string) error {
	srv := gerrittest.Demo(time.Now())
	url, err := srv.Start()
	if err != nil {
		return err
	}
	defer srv.Close()
	client := gerrit.NewClient(url, gerrittest.User, gerrittest.Password)
	_, err = tea.NewProgram(ui.New(client, query), tea.WithAltScreen()).Run()
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
	user, password, err := netrc.Lookup(filepath.Join(home, ".netrc"), parsed.Hostname())
	if errors.Is(err, netrc.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
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
