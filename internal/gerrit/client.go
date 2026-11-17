package gerrit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var xssiPrefix = []byte(")]}'")

// Client is an authenticated Gerrit REST API client.
type Client struct {
	baseURL  string
	user     string
	password string
	http     *http.Client
}

// NewClient creates a client authenticating with a Gerrit HTTP password.
func NewClient(baseURL, user, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		user:     user,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// BaseURL returns the Gerrit server URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// QueryChanges searches changes using the Gerrit query syntax.
func (c *Client) QueryChanges(ctx context.Context, query string, limit int) ([]ChangeInfo, error) {
	v := url.Values{
		"q": {query},
		"n": {fmt.Sprint(limit)},
		"o": {"LABELS", "DETAILED_ACCOUNTS"},
	}
	var changes []ChangeInfo
	if err := c.get(ctx, "/changes/?"+v.Encode(), &changes); err != nil {
		return nil, fmt.Errorf("query changes: %w", err)
	}
	return changes, nil
}

// GetChange fetches a change with its current revision, files, votes and messages.
func (c *Client) GetChange(ctx context.Context, id string) (*ChangeInfo, error) {
	v := url.Values{"o": {
		"DETAILED_LABELS",
		"DETAILED_ACCOUNTS",
		"CURRENT_REVISION",
		"CURRENT_COMMIT",
		"CURRENT_FILES",
		"MESSAGES",
		"SUBMITTABLE",
	}}
	var change ChangeInfo
	if err := c.get(ctx, "/changes/"+url.PathEscape(id)+"?"+v.Encode(), &change); err != nil {
		return nil, fmt.Errorf("get change %s: %w", id, err)
	}
	return &change, nil
}

// GetDiff fetches the full-file diff of a file in a revision against its parent.
func (c *Client) GetDiff(ctx context.Context, change, revision, path string) (*DiffInfo, error) {
	p := fmt.Sprintf("/changes/%s/revisions/%s/files/%s/diff?context=ALL",
		url.PathEscape(change), url.PathEscape(revision), url.PathEscape(path))
	var diff DiffInfo
	if err := c.get(ctx, p, &diff); err != nil {
		return nil, fmt.Errorf("get diff of %s: %w", path, err)
	}
	return &diff, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/a"+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.password)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(bytes.TrimPrefix(body, xssiPrefix), out)
}
