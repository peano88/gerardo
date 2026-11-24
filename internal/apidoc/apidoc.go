// Package apidoc extracts the Gerrit REST API from its documentation pages:
// the JSON entities and their fields, the endpoints and the examples of
// requests and responses. Gerrit publishes no machine-readable schema, so
// the documentation is the reference the client and the fake server are
// checked against.
//
// testdata holds the snapshots of the oldest supported Gerrit version and
// of the latest one.
package apidoc

//go:generate go run ./snapshot -out testdata 3.3.11 3.15.0

import (
	"bytes"
	"encoding/json"
	"html"
	"regexp"
	"slices"
	"strings"
)

// Spec is the REST API of a Gerrit version.
type Spec struct {
	Version string `json:"version"`
	// Entities maps entity names, such as ChangeInfo, to their fields.
	Entities map[string]map[string]Field `json:"entities"`
	// Endpoints are templates such as "GET /changes/{change-id}".
	Endpoints []string  `json:"endpoints"`
	Examples  []Example `json:"examples"`
}

// Field is a field of an entity.
type Field struct {
	Optional bool `json:"optional,omitempty"`
}

// Example is a documented request, such as "GET /changes/x/comments", with
// its response. Only the examples answering valid JSON are kept: the
// documentation has hand-written ones that are not.
type Example struct {
	Request  string          `json:"request"`
	Response json.RawMessage `json:"response"`
}

var (
	headingRe  = regexp.MustCompile(`<h3 id="[^"]*">([^<]+)</h3>`)
	tableRe    = regexp.MustCompile(`(?s)<table class="tableblock.*?</table>`)
	rowRe      = regexp.MustCompile(`(?s)<tr>(.*?)</tr>`)
	cellRe     = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)
	codeRe     = regexp.MustCompile(`<code>([^<]+)</code>`)
	endpointRe = regexp.MustCompile(`<p>'((?:GET|PUT|POST|DELETE) /[^']*)'</p>`)
	listingRe  = regexp.MustCompile(`(?s)<div class="title">(Request|Response)</div>\s*<div class="content">\s*<pre>(.*?)</pre>`)
	tagRe      = regexp.MustCompile(`<[^>]+>`)
)

// Parse extracts the API from the HTML documentation pages of a version.
func Parse(version string, pages ...string) Spec {
	s := Spec{Version: version, Entities: map[string]map[string]Field{}}
	for _, page := range pages {
		parseEntities(page, s.Entities)
		for _, m := range endpointRe.FindAllStringSubmatch(page, -1) {
			s.Endpoints = append(s.Endpoints, text(m[1]))
		}
		s.Examples = append(s.Examples, parseExamples(page)...)
	}
	slices.Sort(s.Endpoints)
	s.Endpoints = slices.Compact(s.Endpoints)
	return s
}

// parseEntities reads the field tables following each heading of an entity:
// older versions split the fields depending on options in several tables.
func parseEntities(page string, entities map[string]map[string]Field) {
	headings := headingRe.FindAllStringSubmatchIndex(page, -1)
	for i, h := range headings {
		end := len(page)
		if i+1 < len(headings) {
			end = headings[i+1][0]
		}
		fields := map[string]Field{}
		for _, table := range tableRe.FindAllString(page[h[1]:end], -1) {
			if strings.Contains(table, "Field Name") {
				parseFields(table, fields)
			}
		}
		if len(fields) > 0 {
			entities[text(page[h[2]:h[3]])] = fields
		}
	}
}

func parseFields(table string, fields map[string]Field) {
	for _, row := range rowRe.FindAllStringSubmatch(table, -1) {
		cells := cellRe.FindAllStringSubmatch(row[1], -1)
		if len(cells) < 2 {
			continue
		}
		name := codeRe.FindStringSubmatch(cells[0][1])
		if name == nil {
			continue
		}
		fields[html.UnescapeString(name[1])] = Field{Optional: strings.Contains(text(cells[1][1]), "optional")}
	}
}

// parseExamples pairs each request with the response following it.
func parseExamples(page string) []Example {
	var out []Example
	var request string
	for _, m := range listingRe.FindAllStringSubmatch(page, -1) {
		body := html.UnescapeString(m[2])
		if m[1] == "Request" {
			request = requestLine(body)
			continue
		}
		if request == "" {
			continue
		}
		if _, payload, ok := strings.Cut(body, ")]}'"); ok {
			var b bytes.Buffer
			if json.Compact(&b, []byte(payload)) == nil {
				out = append(out, Example{Request: request, Response: b.Bytes()})
			}
		}
		request = ""
	}
	return out
}

// requestLine returns the method and path of an example request.
func requestLine(body string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[0] + " " + fields[1]
}

func text(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
}

var paramRe = regexp.MustCompile(`\{[^}]+\}`)

// Endpoint returns the endpoint template matching request, such as
// "GET /changes/42/comments", ignoring query strings. When several match,
// the one with the most literal text wins.
func (s Spec) Endpoint(request string) (string, bool) {
	method, path, _ := strings.Cut(request, " ")
	path, _, _ = strings.Cut(path, "?")
	best, bestLiteral := "", -1
	for _, e := range s.Endpoints {
		m, template, _ := strings.Cut(e, " ")
		template, _, _ = strings.Cut(template, "?")
		if m != method {
			continue
		}
		var pattern strings.Builder
		for i, literal := range paramRe.Split(template, -1) {
			if i > 0 {
				pattern.WriteString(`[^/]+`)
			}
			pattern.WriteString(regexp.QuoteMeta(literal))
		}
		if !regexp.MustCompile("^" + pattern.String() + "$").MatchString(path) {
			continue
		}
		if literal := len(paramRe.ReplaceAllString(template, "")); literal > bestLiteral {
			best, bestLiteral = e, literal
		}
	}
	return best, best != ""
}
