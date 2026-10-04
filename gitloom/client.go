// Package gitloom is the Go SDK for GitLoom: conversations that cannot outgrow
// their context window, backed by a memory the model can consult.
//
// It extends github.com/MelloB1989/karma rather than shipping its own model
// client: karma already speaks to every provider and reports token usage on
// each call, which is exactly what compaction timing needs. The loop a
// developer writes is karma's own — this package supplies the conversation
// that remembers.
package gitloom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.gitloom.cloud"

// Client talks to the GitLoom API.
type Client struct {
	apiKey    string
	baseURL   string
	namespace string
	http      *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a different deployment (self-hosted, dev).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimSuffix(u, "/") } }

// WithNamespace sets the default namespace for memory operations.
func WithNamespace(ns string) Option { return func(c *Client) { c.namespace = ns } }

// WithHTTPClient substitutes the transport, for tests and custom timeouts.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a client. An empty key falls back to GITLOOM_API_KEY, so the
// zero-config path is `gitloom.New("")` with the key in the environment.
func New(apiKey string, opts ...Option) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("GITLOOM_API_KEY")
	}
	c := &Client{
		apiKey:    apiKey,
		baseURL:   defaultBaseURL,
		namespace: "default",
		http:      &http.Client{Timeout: 60 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is a refusal from the API, carrying its machine-readable code: the
// API's own, "unauthorized" for a key the gateway refused, "http_<status>" for
// any other error without one, or "missing_api_key" (Status 0) when the client
// has no key to send.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("gitloom: %s (%s)", e.Message, e.Code)
	}
	return fmt.Sprintf("gitloom: %s (%d %s)", e.Message, e.Status, e.Code)
}

// request performs one call. Writes are never retried: a retried write that
// half-succeeded double-charges the meter and double-stores the message, which
// costs more than surfacing the error.
func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	if c.apiKey == "" {
		return &APIError{Code: "missing_api_key",
			Message: "No API key. Pass one to gitloom.New or set GITLOOM_API_KEY."}
	}
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gitloom: encode request: %w", err)
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitloom: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("gitloom: read response: %w", err)
	}

	if res.StatusCode >= 400 {
		return apiErrorFrom(res.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("gitloom: %s %s returned undecodable JSON: %w", method, path, err)
	}
	return nil
}

// apiErrorFrom reads an error response. The API answers with the envelope
// {"error":{"code","message"}}; the gateway in front of it answers a refused
// or missing key with a bare {"message"}, and anything else may be text.
func apiErrorFrom(status int, raw []byte) *APIError {
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(obj["error"], &env) == nil && env.Code != "" {
		if env.Message == "" {
			env.Message = http.StatusText(status)
		}
		return &APIError{Status: status, Code: env.Code, Message: env.Message}
	}
	switch status {
	case http.StatusUnauthorized:
		return &APIError{Status: status, Code: "unauthorized",
			Message: "No API key was accepted (401 Unauthorized) — check GITLOOM_API_KEY."}
	case http.StatusForbidden:
		return &APIError{Status: status, Code: "unauthorized",
			Message: "The API key was not accepted (403 Forbidden) — check GITLOOM_API_KEY, or whether the key has been revoked."}
	}
	e := &APIError{Status: status, Code: fmt.Sprintf("http_%d", status), Message: http.StatusText(status)}
	var msg string
	text := strings.TrimSpace(string(raw))
	switch {
	case json.Unmarshal(obj["message"], &msg) == nil && msg != "":
		e.Message = msg
	case text != "" && text != "null":
		if r := []rune(text); len(r) > 300 {
			text = string(r[:300]) + "…"
		}
		e.Message = text
	}
	return e
}
