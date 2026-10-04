package gitloom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	// graph, and on the lane path time. Matched only by graph means context
	// that rode in beside a real match rather than evidence, and Via names
	// what pulled it in.
	Matched  []string `json:"matched"`
	Sections []string `json:"sections,omitempty"`
	Via      []string `json:"via,omitempty"`

	// Store, Said and Excerpted are set on the lane path: a curated "memory"
	// or a word-for-word conversation "turn", the days it was stated oldest
	// first, and whether Content was cut to fit MaxChars.
	Store     string   `json:"store,omitempty"`
	Said      []string `json:"said,omitempty"`
	Excerpted bool     `json:"excerpted,omitempty"`

	// Tags lists the caller's tags first, then the inferred ones; UserTags
	// holds the caller's alone.
	Tags     []string `json:"tags,omitempty"`
	UserTags []string `json:"user_tags,omitempty"`

	// The times travel as unix seconds and decode to UTC, zero when absent.
	// OccurredAt is when the memory's subject happened, and OccurredSource how
	// that is known: "user", "extracted", "said" or "written".
	// OccurredPrecision "day" means only the date is known, held as noon UTC
	// on it, so show OccurredAt.Format(time.DateOnly); otherwise "instant".
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	OccurredAt        time.Time `json:"occurred_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	OccurredSource    string    `json:"occurred_source,omitempty"`
	OccurredPrecision string    `json:"occurred_precision,omitempty"`

	// Deprecated: use CreatedAt.
	Created string `json:"created,omitempty"`
	// Deprecated: use UpdatedAt.
	Updated string `json:"updated,omitempty"`

	Confidence float64  `json:"confidence,omitempty"`
	Cues       []string `json:"cues,omitempty"`

	Scores     *Scores     `json:"scores,omitempty"`
	Related    []Relation  `json:"related,omitempty"`
	Provenance *Provenance `json:"provenance,omitempty"`
}

type memoryFields Memory

// memoryWire is Memory as the API sends it. Its times shadow Memory's.
type memoryWire struct {
	memoryFields
	CreatedAt  wireTime `json:"created_at,omitzero"`
	UpdatedAt  wireTime `json:"updated_at,omitzero"`
	OccurredAt wireTime `json:"occurred_at,omitzero"`
	ExpiresAt  wireTime `json:"expires_at,omitzero"`
}

func (m Memory) MarshalJSON() ([]byte, error) {
	return json.Marshal(memoryWire{memoryFields(m),
		wireTime(m.CreatedAt), wireTime(m.UpdatedAt), wireTime(m.OccurredAt), wireTime(m.ExpiresAt)})
}

func (m *Memory) UnmarshalJSON(b []byte) error {
	var w memoryWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*m = Memory(w.memoryFields)
	m.CreatedAt, m.UpdatedAt = time.Time(w.CreatedAt), time.Time(w.UpdatedAt)
	m.OccurredAt, m.ExpiresAt = time.Time(w.OccurredAt), time.Time(w.ExpiresAt)
	return nil
}

// wireTime is a memory time as the API sends it: unix seconds, or RFC 3339
// under time_format=iso. It decodes to UTC and encodes as unix seconds.
type wireTime time.Time

func (w wireTime) IsZero() bool { return time.Time(w).IsZero() }

func (w wireTime) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(time.Time(w).Unix(), 10)), nil
}

func (w *wireTime) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case nil:
		*w = wireTime{}
	case float64:
		*w = wireTime{}
		if s := int64(math.Floor(v)); s != 0 {
			*w = wireTime(time.Unix(s, 0).UTC())
		}
	case string:
		*w = wireTime{}
		if v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return fmt.Errorf("gitloom: time %q is not RFC 3339: %w", v, err)
			}
			*w = wireTime(t.UTC())
		}
	default:
		return fmt.Errorf("gitloom: a time is unix seconds or RFC 3339, not %s", b)
	}
	return nil
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
// a mode ran one; the rest after it only on the lane path.
type Timings struct {
	LexicalMillis int64        `json:"lexical_ms"`
	VectorMillis  int64        `json:"vector_ms"`
	GraphMillis   int64        `json:"graph_ms"`
	ModelMillis   int64        `json:"model_ms,omitempty"`
	EmbedMillis   int64        `json:"embed_ms,omitempty"`
	LanesMillis   int64        `json:"lanes_ms,omitempty"`
	RankMillis    int64        `json:"rank_ms,omitempty"`
	Lane          []LaneTiming `json:"lane,omitempty"`
}

// LaneTiming is one lane's search of one store.
type LaneTiming struct {
	Lane   string `json:"lane"`
	Store  string `json:"store"`
	Millis int64  `json:"ms"`
	N      int    `json:"n"`
	Err    string `json:"err,omitempty"`
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

	// Rank is the lane ranking asked for. RankFallback means RankJev could
	// not rank, so Memories are in lane order.
	Rank         string `json:"rank,omitempty"`
	RankFallback bool   `json:"rank_fallback,omitempty"`

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
	// Tags are applied to every memory drawn from the conversation.
	Tags []string
	// OccurredAt is when the conversation happened. Zero means now.
	OccurredAt When
	// Timezone is the IANA zone the conversation happened in, e.g.
	// "Asia/Kolkata". A time without an offset is read in it.
	Timezone string
	// Date the conversation happened, "2006-01-02".
	//
	// Deprecated: use OccurredAt, which wins when both are set.
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
	if len(o.Tags) > 0 {
		body["tags"] = o.Tags
	}
	if !o.OccurredAt.IsZero() {
		body["occurred_at"] = o.OccurredAt
	}
	if o.Timezone != "" {
		body["timezone"] = o.Timezone
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

// Lane-path rankings: by lane score, or with a ranking model.
const (
	RankFused = "fused"
	RankJev   = "jev"
)

// Models that can read the memories in ModeSummary or ModeAgentic.
const (
	ModelHaiku  = "haiku"
	ModelSonnet = "sonnet"
)

// The memory times a Since/Until range can bound, for RecallOptions.TimeField.
const (
	TimeOccurred = "occurred"
	TimeCreated  = "created"
	TimeUpdated  = "updated"
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
	// TimeField picks the time Since and Until bound, and orders a listing:
	// TimeOccurred, TimeCreated, or TimeUpdated (the server's default).
	TimeField string
	// TZ is the IANA zone, e.g. "Asia/Kolkata", the server reads offset-less
	// times and bare dates in when since and until arrive as text. Since and
	// Until go as UTC instants, so TZ never changes the range; it is sent for
	// parity, and is otherwise read only where the server reads question dates
	// by zone.
	TZ string

	// MinScore drops memories below this relevance. NoContext drops graph
	// neighbours, leaving only what matched the query directly.
	MinScore  float64
	NoContext bool
	// Detail "full" adds revision history, the last diff, relation snippets
	// and cues.
	Detail         string
	IncludeExpired bool

	// NoProvenance drops each memory's git history (commit, author,
	// revisions, diff). The server computes it by default, and it costs a
	// git-log walk PER MEMORY, which dominates the request once memories have
	// real history behind them — 3.29s with it against 0.40s without,
	// measured on one production namespace. Turn it off for anything
	// latency-sensitive that is not going to show a citation.
	NoProvenance bool
	// NoRelations drops each memory's neighbours and their snippets. Cheap to
	// leave on; this exists for bulk scans that only want the text.
	NoRelations bool

	// Rank retrieves on the lane path, which also reaches conversation turns
	// and the dates in a question. Not with ModeAgentic. MaxChars caps the
	// memory content returned; Model picks the reader.
	Rank     string
	MaxChars int
	Model    string
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
	if o.TimeField != "" {
		q.Set("time_field", o.TimeField)
	}
	if o.TZ != "" {
		q.Set("tz", o.TZ)
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
	if o.NoProvenance {
		q.Set("no_provenance", "1")
	}
	if o.NoRelations {
		q.Set("no_relations", "1")
	}
	if o.Rank != "" {
		q.Set("rank", o.Rank)
	}
	if o.MaxChars > 0 {
		q.Set("max_chars", strconv.Itoa(o.MaxChars))
	}
	if o.Model != "" {
		q.Set("model", o.Model)
	}
	return q
}

// Recall retrieves what is known that bears on the query.
//
// With an empty query and at least one filter (Tags, TagsAll, Since, Until,
// Tiers or Paths), it lists every memory the filters match instead, newest
// first by TimeField, each scored 1; Mode must then be raw and Rank unset.
// With neither it returns ErrNoQuery without calling the API.
func (c *Client) Recall(ctx context.Context, query string, opts *RecallOptions) (*RecallResult, error) {
	o := RecallOptions{}
	if opts != nil {
		o = *opts
	}
	q := o.query(c.namespace)
	if strings.TrimSpace(query) != "" {
		q.Set("q", query)
	} else if !o.filtered() {
		return nil, ErrNoQuery
	}
	var out RecallResult
	if err := c.request(ctx, "GET", "/v1/retrieve?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (o RecallOptions) filtered() bool {
	return len(o.Tags) > 0 || len(o.TagsAll) > 0 || !o.Since.IsZero() || !o.Until.IsZero() ||
		len(o.Tiers) > 0 || len(o.Paths) > 0
}

// ErrNoQuery means Recall was given neither a query nor a filter to list by.
var ErrNoQuery = errors.New("gitloom: Recall needs a query, or a filter (Tags, TagsAll, Since, Until, Tiers or Paths) to list by")

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
