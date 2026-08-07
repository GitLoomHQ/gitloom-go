package gitloom

import (
	"context"
	"net/url"
	"strconv"
)

// Turn is one message of a conversation being remembered.
type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Hit is one retrieved memory, with the evidence every GitLoom surface
// returns: why it ranked, its git history, and its neighbours.
type Hit struct {
	Path       string      `json:"path"`
	Score      float64     `json:"score"`
	Snippet    string      `json:"snippet"`
	Scores     *Scores     `json:"scores,omitempty"`
	Provenance *Provenance `json:"provenance,omitempty"`
	Relations  []Relation  `json:"relations,omitempty"`
}

type Scores struct {
	BM25      float64  `json:"bm25,omitempty"`
	Cue       float64  `json:"cue,omitempty"`
	Body      float64  `json:"body,omitempty"`
	GraphHops int      `json:"graph_hops,omitempty"`
	Arms      []string `json:"arms,omitempty"`
}

type Provenance struct {
	Commit    string     `json:"commit"`
	Author    string     `json:"author,omitempty"`
	When      string     `json:"when"`
	Message   string     `json:"message,omitempty"`
	Revisions int        `json:"revisions,omitempty"`
	History   []Revision `json:"history,omitempty"`
	Diff      string     `json:"diff,omitempty"`
}

type Revision struct {
	Commit  string `json:"commit"`
	Author  string `json:"author,omitempty"`
	When    string `json:"when"`
	Message string `json:"message,omitempty"`
}

type Relation struct {
	Label   string `json:"label,omitempty"`
	Path    string `json:"path"`
	Snippet string `json:"snippet,omitempty"`
	Since   int64  `json:"valid_from,omitempty"`
	Until   int64  `json:"valid_to,omitempty"`
}

// VocabHit is a custom-vocabulary term the query matched.
type VocabHit struct {
	Path       string   `json:"path"`
	Term       string   `json:"term"`
	Definition string   `json:"definition,omitempty"`
	Matched    []string `json:"matched,omitempty"`
}

// RecallResult is one retrieval's answer.
type RecallResult struct {
	Namespace string     `json:"namespace"`
	Hits      []Hit      `json:"hits"`
	Defined   []VocabHit `json:"defined,omitempty"`
	Millis    int64      `json:"millis"`
}

// RememberOptions shape one ingestion.
type RememberOptions struct {
	Namespace string
	SessionID string
	// Date the conversation happened, "2006-01-02". Empty means today.
	Date string
}

// Remember submits a conversation for ingestion. Asynchronous by design —
// extraction runs model calls the caller must not wait on; the memories appear
// in retrieval within seconds.
func (c *Client) Remember(ctx context.Context, turns []Turn, opts *RememberOptions) error {
	o := RememberOptions{}
	if opts != nil {
		o = *opts
	}
	if o.Namespace == "" {
		o.Namespace = c.namespace
	}
	body := map[string]any{"namespace": o.Namespace, "messages": turns}
	if o.SessionID != "" {
		body["session_id"] = o.SessionID
	}
	if o.Date != "" {
		body["date"] = o.Date
	}
	return c.request(ctx, "POST", "/v1/memories", body, nil)
}

// RecallOptions shape one retrieval.
type RecallOptions struct {
	Namespace string
	Limit     int
	// NoProvenance drops each hit's git history (commit, author, revisions,
	// diff). On by default because a hosted answer should be citable — but it
	// costs a git-log walk PER HIT, which dominates the request once a memory
	// has real history behind it. Turn it off for anything latency-sensitive
	// that is not going to show a citation.
	NoProvenance bool
	// NoRelations drops each hit's neighbours and their snippets. Cheap to
	// leave on; this exists for bulk scans that only want the text.
	NoRelations bool
}

// Recall retrieves what is known that bears on the query.
func (c *Client) Recall(ctx context.Context, query string, opts *RecallOptions) (*RecallResult, error) {
	o := RecallOptions{}
	if opts != nil {
		o = *opts
	}
	if o.Namespace == "" {
		o.Namespace = c.namespace
	}
	q := url.Values{"q": {query}, "namespace": {o.Namespace}}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.NoProvenance {
		q.Set("no_provenance", "1")
	}
	if o.NoRelations {
		q.Set("no_relations", "1")
	}
	var out RecallResult
	if err := c.request(ctx, "GET", "/v1/retrieve?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Context renders retrieval as one system-message string, ready to prepend —
// empty when nothing relevant is stored.
func (c *Client) Context(ctx context.Context, query string, opts *RecallOptions) (string, error) {
	res, err := c.Recall(ctx, query, opts)
	if err != nil {
		return "", err
	}
	if len(res.Hits) == 0 {
		return "", nil
	}
	var b []byte
	b = append(b, "What you already know about this user, from earlier conversations. Treat it as background, not as something they just said:\n"...)
	for _, h := range res.Hits {
		b = append(b, "- "...)
		b = append(b, h.Snippet...)
		b = append(b, '\n')
	}
	return string(b), nil
}

// CreateNamespace makes a namespace exist. Idempotent.
func (c *Client) CreateNamespace(ctx context.Context, name string) error {
	return c.request(ctx, "POST", "/v1/namespaces", map[string]string{"namespace": name}, nil)
}
