// Command snapshot downloads the REST API documentation of Gerrit versions
// and writes what apidoc extracts from it, one gerrit-<version>.json file
// per version:
//
//	go run ./internal/apidoc/snapshot -out internal/apidoc/testdata 3.15.0
//
// The version "all" stands for the latest patch release of every 3.x minor,
// "latest" for the newest release.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/peano88/gerardo/internal/apidoc"
)

const docs = "https://gerrit-documentation.storage.googleapis.com/"

// pages are the documentation pages of the endpoints gerardo uses.
var pages = []string{"rest-api-changes.html", "rest-api-accounts.html"}

func main() {
	out := flag.String("out", ".", "directory to write the snapshots to")
	flag.Parse()
	if err := run(*out, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "snapshot:", err)
		os.Exit(1)
	}
}

func run(out string, versions []string) error {
	if slices.Contains(versions, "all") || slices.Contains(versions, "latest") {
		all, err := latestMinors()
		if err != nil {
			return err
		}
		if slices.Contains(versions, "all") {
			versions = all
		} else {
			versions = all[len(all)-1:]
		}
	}
	for _, v := range versions {
		var html []string
		for _, p := range pages {
			body, err := get(docs + "Documentation/" + v + "/" + p)
			if err != nil {
				return err
			}
			html = append(html, body)
		}
		spec := apidoc.Parse(v, html...)
		b, err := json.MarshalIndent(spec, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(out, "gerrit-"+v+".json")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Printf("%s: %d entities, %d endpoints, %d examples\n", path, len(spec.Entities), len(spec.Endpoints), len(spec.Examples))
	}
	return nil
}

var prefixRe = regexp.MustCompile(`<Prefix>Documentation/(3\.\d+\.\d+)/</Prefix>`)

// latestMinors lists the latest patch release of every 3.x minor version
// from the documentation bucket.
func latestMinors() ([]string, error) {
	latest := map[string][]int{}
	marker := ""
	for {
		body, err := get(docs + "?prefix=Documentation/&delimiter=/&marker=" + marker)
		if err != nil {
			return nil, err
		}
		for _, m := range prefixRe.FindAllStringSubmatch(body, -1) {
			v := parseVersion(m[1])
			minor := fmt.Sprintf("%d.%d", v[0], v[1])
			if slices.Compare(v, latest[minor]) > 0 {
				latest[minor] = v
			}
		}
		next := regexp.MustCompile(`<NextMarker>([^<]+)</NextMarker>`).FindStringSubmatch(body)
		if next == nil || !strings.Contains(body, "<IsTruncated>true") {
			break
		}
		marker = next[1]
	}
	var out [][]int
	for _, v := range latest {
		out = append(out, v)
	}
	slices.SortFunc(out, slices.Compare)
	versions := make([]string, len(out))
	for i, v := range out {
		versions[i] = fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
	}
	return versions, nil
}

func parseVersion(s string) []int {
	var v []int
	for p := range strings.SplitSeq(s, ".") {
		n, _ := strconv.Atoi(p)
		v = append(v, n)
	}
	return v
}

func get(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
