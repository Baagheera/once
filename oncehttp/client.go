// Package oncehttp is a small client for the once HTTP API.
package oncehttp

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
	"unicode"
)

// State is the stored state of an idempotency record.
type State string

const (
	// Running means a key was reserved but has no terminal result.
	Running State = "running"
	// Succeeded means a terminal success was committed.
	Succeeded State = "succeeded"
	// Failed means a terminal failure was committed.
	Failed State = "failed"
)

// Record is the HTTP representation of one once idempotency record.
type Record struct {
	Key        string     `json:"key"`
	State      State      `json:"state"`
	ExitCode   int        `json:"exit_code"`
	Stdout     []byte     `json:"stdout_b64,omitempty"`
	Stderr     []byte     `json:"stderr_b64,omitempty"`
	Error      string     `json:"error,omitempty"`
	Command    []string   `json:"command,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ReserveRequest reserves a key or fetches the existing record for that key.
type ReserveRequest struct {
	Key     string   `json:"key"`
	Command []string `json:"command,omitempty"`
}

// ReserveResponse is returned by Reserve.
type ReserveResponse struct {
	Fresh        bool   `json:"fresh"`
	AttemptToken string `json:"attempt_token,omitempty"`
	Record       Record `json:"record"`
}

// CommitRequest commits a terminal result for a running key.
type CommitRequest struct {
	Key          string `json:"key"`
	AttemptToken string `json:"attempt_token"`
	State        State  `json:"state"`
	ExitCode     int    `json:"exit_code"`
	Stdout       []byte `json:"stdout_b64,omitempty"`
	Stderr       []byte `json:"stderr_b64,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Client talks to a once HTTP server.
type Client struct {
	baseURL          *url.URL
	token            string
	client           *http.Client
	maxResponseBytes int64
}

// DefaultMaxResponseBytes is the default cap for successful JSON responses.
const DefaultMaxResponseBytes int64 = 16 << 20

// Option configures a Client.
type Option func(*Client) error

// WithBearerToken configures HTTP bearer authentication.
func WithBearerToken(token string) Option {
	return func(c *Client) error {
		token = strings.TrimSpace(token)
		if token == "" {
			return fmt.Errorf("empty bearer token")
		}
		if strings.ContainsFunc(token, unicode.IsSpace) {
			return fmt.Errorf("bearer token must not contain whitespace")
		}
		c.token = token
		return nil
	}
}

// WithHTTPClient configures the underlying HTTP client. The client is copied,
// and redirects remain disabled so credentials and attempt tokens stay bound
// to the configured once server.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) error {
		if client == nil {
			return fmt.Errorf("nil http client")
		}
		c.client = client
		return nil
	}
}

// WithMaxResponseBytes sets the maximum successful JSON response size.
func WithMaxResponseBytes(n int64) Option {
	return func(c *Client) error {
		if n <= 0 {
			return fmt.Errorf("max response bytes must be positive")
		}
		c.maxResponseBytes = n
		return nil
	}
}

// New returns a client for a once HTTP server.
func New(baseURL string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("empty base url")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("base url must include scheme and host")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""

	c := &Client{
		baseURL:          parsed,
		client:           http.DefaultClient,
		maxResponseBytes: DefaultMaxResponseBytes,
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	c.client = cloneHTTPClient(c.client)
	return c, nil
}

func cloneHTTPClient(client *http.Client) *http.Client {
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

// Reserve reserves a key or returns the existing record.
func (c *Client) Reserve(ctx context.Context, req ReserveRequest) (ReserveResponse, error) {
	var resp ReserveResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/reserve", req, &resp)
	return resp, err
}

// Commit commits a terminal result for a running key.
func (c *Client) Commit(ctx context.Context, req CommitRequest) (Record, error) {
	var rec Record
	err := c.doJSON(ctx, http.MethodPost, "/v1/commit", req, &rec)
	return rec, err
}

// Get returns one record.
func (c *Client) Get(ctx context.Context, key string) (Record, error) {
	var rec Record
	err := c.doJSON(ctx, http.MethodGet, "/v1/records/"+url.PathEscape(key), nil, &rec)
	return rec, err
}

// Delete deletes one record through the HTTP repair endpoint.
func (c *Client) Delete(ctx context.Context, key, attemptToken string, force bool) error {
	path := "/v1/records/" + url.PathEscape(key)
	if force {
		path += "?force=1"
	}
	return c.do(ctx, http.MethodDelete, path, nil, attemptToken, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, dst any) error {
	return c.do(ctx, method, path, body, "", dst)
}

func (c *Client) do(ctx context.Context, method, path string, body any, attemptToken string, dst any) error {
	if ctx == nil {
		return fmt.Errorf("nil context")
	}
	var reader io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
		reader = &buf
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if attemptToken != "" {
		req.Header.Set("X-Once-Attempt-Token", attemptToken)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	if dst == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return decodeSuccess(resp.Body, c.maxResponseBytes, dst)
}

func (c *Client) endpoint(path string) string {
	u := *c.baseURL
	basePath := strings.TrimRight(u.Path, "/")
	cleanPath, rawQuery, _ := strings.Cut(path, "?")
	u.Path = basePath + cleanPath
	u.RawQuery = rawQuery
	return u.String()
}

// Error is returned for non-2xx HTTP responses.
type Error struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "once http: " + e.Status
	}
	return "once http: " + e.Status + ": " + e.Message
}

func decodeError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
	return &Error{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Message:    body.Error,
	}
}

func decodeSuccess(body io.Reader, maxBytes int64, dst any) error {
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("once http: response body exceeds %d bytes", maxBytes)
	}
	return json.Unmarshal(data, dst)
}
