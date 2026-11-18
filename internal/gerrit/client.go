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

// ListComments fetches the published inline comments of all revisions, by file path.
func (c *Client) ListComments(ctx context.Context, change string) (map[string][]CommentInfo, error) {
	var comments map[string][]CommentInfo
	if err := c.get(ctx, fmt.Sprintf("/changes/%s/comments", url.PathEscape(change)), &comments); err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	return withPaths(comments), nil
}

// PortedComments fetches the unresolved threads of earlier patch sets, with
// their lines mapped onto revision.
func (c *Client) PortedComments(ctx context.Context, change, revision string) (map[string][]CommentInfo, error) {
	var comments map[string][]CommentInfo
	p := fmt.Sprintf("/changes/%s/revisions/%s/ported_comments", url.PathEscape(change), url.PathEscape(revision))
	if err := c.get(ctx, p, &comments); err != nil {
		return nil, fmt.Errorf("list ported comments: %w", err)
	}
	return withPaths(comments), nil
}

// ListDrafts fetches the draft comments of the authenticated user on all revisions.
func (c *Client) ListDrafts(ctx context.Context, change string) (map[string][]CommentInfo, error) {
	var drafts map[string][]CommentInfo
	if err := c.get(ctx, fmt.Sprintf("/changes/%s/drafts", url.PathEscape(change)), &drafts); err != nil {
		return nil, fmt.Errorf("list drafts: %w", err)
	}
	for _, list := range drafts {
		for i := range list {
			list[i].Draft = true
		}
	}
	return withPaths(drafts), nil
}

// CreateDraft creates a draft comment on a revision.
func (c *Client) CreateDraft(ctx context.Context, change, revision string, in CommentInput) error {
	p := fmt.Sprintf("/changes/%s/revisions/%s/drafts", url.PathEscape(change), url.PathEscape(revision))
	if err := c.do(ctx, http.MethodPut, p, in, nil); err != nil {
		return fmt.Errorf("create draft: %w", err)
	}
	return nil
}

// UpdateDraft replaces a draft comment.
func (c *Client) UpdateDraft(ctx context.Context, change, revision, id string, in CommentInput) error {
	p := fmt.Sprintf("/changes/%s/revisions/%s/drafts/%s", url.PathEscape(change), url.PathEscape(revision), url.PathEscape(id))
	if err := c.do(ctx, http.MethodPut, p, in, nil); err != nil {
		return fmt.Errorf("update draft: %w", err)
	}
	return nil
}

// DeleteDraft deletes a draft comment.
func (c *Client) DeleteDraft(ctx context.Context, change, revision, id string) error {
	p := fmt.Sprintf("/changes/%s/revisions/%s/drafts/%s", url.PathEscape(change), url.PathEscape(revision), url.PathEscape(id))
	if err := c.do(ctx, http.MethodDelete, p, nil, nil); err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}
	return nil
}

func withPaths(comments map[string][]CommentInfo) map[string][]CommentInfo {
	for path, list := range comments {
		for i := range list {
			list[i].Path = path
		}
	}
	return comments
}

// Self fetches the account of the authenticated user.
func (c *Client) Self(ctx context.Context) (*AccountInfo, error) {
	var account AccountInfo
	if err := c.get(ctx, "/accounts/self", &account); err != nil {
		return nil, fmt.Errorf("get own account: %w", err)
	}
	return &account, nil
}

// SetReview posts votes and a message on a revision.
func (c *Client) SetReview(ctx context.Context, change, revision string, review ReviewInput) error {
	p := fmt.Sprintf("/changes/%s/revisions/%s/review", url.PathEscape(change), url.PathEscape(revision))
	if err := c.do(ctx, http.MethodPost, p, review, nil); err != nil {
		return fmt.Errorf("post review: %w", err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/a"+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.password)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.Unmarshal(bytes.TrimPrefix(respBody, xssiPrefix), out)
}
