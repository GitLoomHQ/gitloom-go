package gitloom

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Turn is one message of a conversation being remembered.
type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Memory is one retrieved memory — the whole memory, not a fragment of one.
// A memory whose sections matched separately is still one entry, with the
// matching sections named.
type Memory struct {
	Path    string `json:"path"`
	Tier    string `json:"tier"`
	Topic   string `json:"topic,omitempty"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content"`
	// Snippet is the query-focused excerpt, when the lexical arm matched.
	Snippet string `json:"snippet,omitempty"`

	// Score is a calibrated relevance in [0,1], comparable ACROSS queries: a
	// memory that answers the question outright scores near 1 whatever else
	// the namespace holds. It replaced a fused rank that only meant something
	// within one response.
	Score float64 `json:"score"`
	// Matched names the arms that produced this memory: lexical, cue, body,
	// graph. Matched only by graph means context that rode in beside a real
	// match rather than evidence, and Via names what pulled it in.
	Matched  []string `json:"matched"`
	Sections []string `json:"sections,omitempty"`
	Via      []string `json:"via,omitempty"`

	Tags       []string `json:"tags,omitempty"`
	Created    string   `json:"created,omitempty"`
	Updated    string   `json:"updated,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Cues       []string `json:"cues,omitempty"`

	Scores     *Scores     `json:"scores,omitempty"`
	Related    []Relation  `json:"related,omitempty"`
	Provenance *Provenance `json:"provenance,omitempty"`
}

type Scores struct {
	BM25      float64 `json:"bm25,omitempty"`
	Cue       float64 `json:"cue,omitempty"`
	Body      float64 `json:"body,omitempty"`
	GraphHops int     `json:"graph_hops,omitempty"`
	// Coverage is the fraction of the query's content terms the memory holds.
	Coverage float64 `json:"coverage,omitempty"`
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

// TraceEvent is one step of an agentic retrieval.
type TraceEvent struct {
	Type   string          `json:"type"`
	Tool   string          `json:"tool,omitempty"`
	ID     string          `json:"id,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Text   string          `json:"text,omitempty"`
	Millis int64           `json:"millis,omitempty"`
}

// Timings says where a retrieval spent its time. ModelMillis is set only when
// a mode ran one.
type Timings struct {
	LexicalMillis int64 `json:"lexical_ms"`
	VectorMillis  int64 `json:"vector_ms"`
	GraphMillis   int64 `json:"graph_ms"`
	ModelMillis   int64 `json:"model_ms,omitempty"`
}

// RecallResult is one retrieval's answer.
type RecallResult struct {
	Namespace string     `json:"namespace"`
	Query     string     `json:"query"`
	Mode      string     `json:"mode"`
	Memories  []Memory   `json:"memories"`
	Defined   []VocabHit `json:"defined,omitempty"`

	// Answer, Model and Trace are set by ModeSummary and ModeAgentic.
	// Truncated means the agent hit its budget before choosing to stop.
	Answer    string       `json:"answer,omitempty"`
	Model     string       `json:"model,omitempty"`
	Trace     []TraceEvent `json:"trace,omitempty"`
	Truncated bool         `json:"truncated,omitempty"`

	// Candidates is how many distinct memories any arm produced before the
	// relevance floor; FilteredOut how many that floor dropped. Many filtered
	// out with no memories is an unanswerable question rather than a miss.
	Candidates  int     `json:"candidates"`
	FilteredOut int     `json:"filtered_out"`
	Millis      int64   `json:"millis"`
	Timings     Timings `json:"timings"`
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

// Retrieval modes. Raw makes no model call beyond the query embedding and
// meters as a read; the other two run a model and meter as a chat.
const (
	ModeRaw     = "raw"
	ModeSummary = "summary"
	ModeAgentic = "agentic"
)

// RecallOptions shape one retrieval.
//
// Every filter here is applied INSIDE each retrieval arm and to graph
// neighbours, so confining a query to a directory is a boundary rather than a
// cut made after the fact.
type RecallOptions struct {
	Namespace string
	Limit     int
	Mode      string

	Tiers   []string // facts, incidents, rules, skills
	Paths   []string // directories, e.g. "facts/events"
	Tags    []string // any of these
	TagsAll []string // every one of these
	Since   time.Time
	Until   time.Time

	// MinScore drops memories below this relevance. NoContext drops graph
	// neighbours, leaving only what matched the query directly.
	MinScore  float64
	NoContext bool
	// Detail "full" adds revision history, the last diff, relation snippets
	// and cues.
	Detail         string
	IncludeExpired bool
}

func (o RecallOptions) query(fallbackNamespace string) url.Values {
	ns := o.Namespace
	if ns == "" {
		ns = fallbackNamespace
	}
	q := url.Values{"namespace": {ns}}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Mode != "" && o.Mode != ModeRaw {
		q.Set("mode", o.Mode)
	}
	for key, vals := range map[string][]string{
		"tiers": o.Tiers, "paths": o.Paths, "tags": o.Tags, "tags_all": o.TagsAll,
	} {
		if len(vals) > 0 {
			q.Set(key, strings.Join(vals, ","))
		}
	}
	if !o.Since.IsZero() {
		q.Set("since", o.Since.UTC().Format(time.RFC3339))
	}
	if !o.Until.IsZero() {
		q.Set("until", o.Until.UTC().Format(time.RFC3339))
	}
	if o.MinScore > 0 {
		q.Set("min_score", strconv.FormatFloat(o.MinScore, 'g', -1, 64))
	}
	if o.NoContext {
		q.Set("context", "0")
	}
	if o.Detail != "" {
		q.Set("detail", o.Detail)
	}
	if o.IncludeExpired {
		q.Set("include_expired", "1")
	}
	return q
}

// Recall retrieves what is known that bears on the query.
func (c *Client) Recall(ctx context.Context, query string, opts *RecallOptions) (*RecallResult, error) {
	o := RecallOptions{}
	if opts != nil {
		o = *opts
	}
	q := o.query(c.namespace)
	q.Set("q", query)
	var out RecallResult
	if err := c.request(ctx, "GET", "/v1/retrieve?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ErrNoAnswer means a model-backed mode returned no text.
var ErrNoAnswer = errors.New("gitloom: the model did not produce an answer")

// Answer returns one text answer drawn from the memory.
//
// A fast model summarizes one retrieval; with opts.Mode set to ModeAgentic a
// stronger model searches the memory itself with tools and returns its trace.
// The memories the answer rests on come back on the result. Both meter as
// chats rather than reads.
func (c *Client) Answer(ctx context.Context, query string, opts *RecallOptions) (*RecallResult, error) {
	o := RecallOptions{}
	if opts != nil {
		o = *opts
	}
	if o.Mode != ModeAgentic {
		o.Mode = ModeSummary
	}
	res, err := c.Recall(ctx, query, &o)
	if err != nil {
		return nil, err
	}
	if res.Answer == "" {
		return res, ErrNoAnswer
	}
	return res, nil
}

// Context renders retrieval as one system-message string, ready to prepend —
// empty when nothing relevant is stored.
func (c *Client) Context(ctx context.Context, query string, opts *RecallOptions) (string, error) {
	res, err := c.Recall(ctx, query, opts)
	if err != nil {
		return "", err
	}
	if len(res.Memories) == 0 {
		return "", nil
	}
	var b []byte
	b = append(b, "What you already know about this user, from earlier conversations. Treat it as background, not as something they just said:\n"...)
	for _, m := range res.Memories {
		text := m.Content
		if text == "" {
			text = m.Snippet
		}
		b = append(b, "- "...)
		b = append(b, text...)
		b = append(b, '\n')
	}
	return string(b), nil
}

// CreateNamespace makes a namespace exist. Idempotent.
func (c *Client) CreateNamespace(ctx context.Context, name string) error {
	return c.request(ctx, "POST", "/v1/namespaces", map[string]string{"namespace": name}, nil)
}
