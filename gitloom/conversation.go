package gitloom

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/MelloB1989/karma/models"
)

// Summarizer turns evicted turns into one summary. Compaction is refused
// without one rather than silently dropping history.
type Summarizer func(ctx context.Context, evicted []models.AIMessage) (string, error)

// MemoryMode is how memory is consulted as the conversation runs.
type MemoryMode string

const (
	// MemoryQuery retrieves for every user message; Context() returns it.
	MemoryQuery MemoryMode = "query"
	// MemoryTools leaves retrieval to the model via function calling.
	MemoryTools MemoryMode = "tools"
	// MemoryOff stores and compacts only.
	MemoryOff MemoryMode = "off"
)

// ConversationOptions configure a managed conversation.
type ConversationOptions struct {
	// Model whose context window bounds the conversation, e.g. "gpt-4o".
	Model string
	// MaxTokens overrides the model's inferred window.
	MaxTokens int
	// ReserveForReply holds tokens back for the completion.
	ReserveForReply int
	// CompactAt is the fill fraction that triggers compaction. Default 0.85.
	CompactAt float64
	// CompactEvery compacts after this many exchanges regardless of tokens.
	// Default 5; 0 disables the cadence. Compaction is also the memory
	// trigger, so a huge window must not mean hours before anything is
	// remembered.
	CompactEvery int
	// Summarize produces the compaction summary locally, typically via the
	// caller's own KarmaAI — GitLoom never sees the conversation to compact it.
	Summarize Summarizer
	// SummarizeServer hands summarization to GitLoom's own model instead: the
	// turns are already stored there, and the client needs no model wired in.
	// Used when Summarize is nil. Costs one chat from the account's meter.
	SummarizeServer bool
	// Namespace memories land in and are recalled from.
	Namespace string
	// Memory selects how retrieval happens. Default MemoryQuery.
	Memory MemoryMode
	// Title names the conversation at creation. Left empty, ingestion
	// generates one.
	Title string
	// OnEvent observes the managed steps of a completion — memory retrieval
	// today, more later — so a caller can surface them (a chat UI showing
	// "searching memory…") without owning the steps. Called synchronously on
	// the completion's goroutine; nil means no observation.
	OnEvent func(WrapEvent)
}

// WrapEvent is one observable step of a managed completion.
type WrapEvent struct {
	// Kind names the step: "memory.recall" when retrieval starts,
	// "memory.recall.done" when it lands.
	Kind string
	// Hits is how many memories retrieval returned (recall.done only).
	Hits int
	// Err is set when the step failed; the completion itself continues.
	Err error
}

// Conversation is a stored chat with a rolling context window.
type Conversation struct {
	ID     string
	Branch string
	Title  string

	client       *Client
	opts         ConversationOptions
	history      []models.AIMessage
	summary      string
	nextSeq      int64
	firstLiveSeq int64
	exchanges    int
	// reportedTokens is the provider's own count of the held conversation,
	// from the last completion's usage. Real counts beat estimates.
	reportedTokens int
}

// NextSeq is the sequence the next appended message will take. After an
// exchange lands, the two turns it wrote sit at [NextSeq()-2, NextSeq()-1] —
// which is exactly the range a caller hands to Ingest for
// remember-as-you-go instead of waiting for the idle sweep.
func (conv *Conversation) NextSeq() int64 { return conv.nextSeq }

// NewConversation creates (or re-creates, idempotently) a stored conversation.
//
// The server answers 409 for an id that already exists; that is resumption,
// not failure — the caller gets the conversation shell and the Load that
// always follows hydrates it. Without this, every wrapper (WrapKarma and
// friends) broke the moment a conversation outlived one process.
func (c *Client) NewConversation(ctx context.Context, id string, opts ConversationOptions) (*Conversation, error) {
	if opts.Namespace == "" {
		opts.Namespace = c.namespace
	}
	body := map[string]any{"id": id, "namespace": opts.Namespace}
	if opts.Title != "" {
		body["title"] = opts.Title
	}
	if opts.Model != "" {
		body["model"] = opts.Model
	}
	var res struct {
		Branch  string `json:"branch"`
		NextSeq int64  `json:"next_seq"`
	}
	if err := c.request(ctx, "POST", "/v1/conversations", body, &res); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
			return &Conversation{ID: id, Branch: "main", client: c, opts: opts}, nil
		}
		return nil, err
	}
	return &Conversation{ID: id, Branch: res.Branch, client: c, opts: opts, nextSeq: res.NextSeq, Title: opts.Title}, nil
}

// DeleteConversation removes a stored conversation outright — its messages,
// compactions and branches. Memories already extracted from it live in the
// namespace's repository and survive; forgetting facts is the memories API's
// territory, not a side effect of tidying a chat list.
func (c *Client) DeleteConversation(ctx context.Context, id string) error {
	return c.request(ctx, "DELETE", "/v1/conversations/"+url.PathEscape(id), nil, nil)
}

// LoadConversation resumes a stored conversation from its last compaction.
func (c *Client) LoadConversation(ctx context.Context, id string, opts ConversationOptions) (*Conversation, error) {
	conv := &Conversation{ID: id, client: c, opts: opts}
	if err := conv.Load(ctx, LoadOptions{}); err != nil {
		return nil, err
	}
	return conv, nil
}

// LoadOptions shape one load.
type LoadOptions struct {
	// Full loads every message, ignoring compactions.
	Full bool
	// Branch reads a line other than the active one.
	Branch string
	// At reads the conversation as it stood at this seq. Negative means head.
	At int64
}

type wireMessage struct {
	Seq        int64           `json:"seq"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	Parts      json.RawMessage `json:"parts,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// Load replaces local state with the server's.
func (conv *Conversation) Load(ctx context.Context, opts LoadOptions) error {
	q := url.Values{}
	if opts.Full {
		q.Set("full", "1")
	}
	if opts.Branch != "" {
		q.Set("branch", opts.Branch)
	}
	if opts.At > 0 {
		q.Set("at", strconv.FormatInt(opts.At, 10))
	}
	path := "/v1/conversations/" + url.PathEscape(conv.ID)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var res struct {
		Branch     string        `json:"branch"`
		Title      string        `json:"title"`
		NextSeq    int64         `json:"next_seq"`
		Messages   []wireMessage `json:"messages"`
		Compaction *struct {
			Summary string `json:"summary"`
		} `json:"compaction"`
	}
	if err := conv.client.request(ctx, "GET", path, nil, &res); err != nil {
		return err
	}
	conv.Branch = res.Branch
	conv.Title = res.Title
	conv.nextSeq = res.NextSeq
	conv.history = conv.history[:0]
	for _, m := range res.Messages {
		conv.history = append(conv.history, fromWire(m))
	}
	if len(res.Messages) > 0 {
		conv.firstLiveSeq = res.Messages[0].Seq
	} else {
		conv.firstLiveSeq = res.NextSeq
	}
	conv.summary = ""
	if res.Compaction != nil {
		conv.summary = res.Compaction.Summary
	}
	conv.reportedTokens = 0
	return nil
}

// Append stores messages, compacting first when the window or the exchange
// cadence demand it. resp may be nil; when it carries the completion that
// produced these messages, its token count times the next compaction.
func (conv *Conversation) Append(ctx context.Context, msgs []models.AIMessage, resp *models.AIChatResponse) error {
	if len(msgs) == 0 {
		return nil
	}
	if resp != nil && resp.Tokens > 0 {
		conv.reportedTokens = resp.Tokens
	}
	for _, m := range msgs {
		if m.Role == models.Assistant {
			conv.exchanges++
		}
	}
	if (conv.opts.Summarize != nil || conv.opts.SummarizeServer) && (conv.wouldOverflow(msgs) || conv.cadenceDue()) {
		if _, err := conv.Compact(ctx); err != nil {
			return err
		}
	}

	wire := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		w, err := conv.toWire(ctx, m)
		if err != nil {
			return err
		}
		wire = append(wire, w)
	}
	var res struct {
		NextSeq int64 `json:"next_seq"`
	}
	err := conv.client.request(ctx, "POST",
		"/v1/conversations/"+url.PathEscape(conv.ID)+"/messages",
		map[string]any{"branch": conv.Branch, "messages": wire}, &res)
	if err != nil {
		return err
	}
	conv.history = append(conv.history, msgs...)
	conv.nextSeq = res.NextSeq
	return nil
}

// toWire renders one karma message for storage. Images and files become media
// uploads referenced by id — data URLs are uploaded here, http(s) URLs pass
// through — so the stored message replays with the same attachments it ran
// with, without carrying their bytes.
func (conv *Conversation) toWire(ctx context.Context, m models.AIMessage) (map[string]any, error) {
	out := map[string]any{"role": string(m.Role), "content": m.Message}
	if m.ToolCallId != "" {
		out["tool_call_id"] = m.ToolCallId
	}
	if len(m.ToolCalls) > 0 {
		raw, err := json.Marshal(m.ToolCalls)
		if err != nil {
			return nil, fmt.Errorf("gitloom: encode tool calls: %w", err)
		}
		out["tool_calls"] = json.RawMessage(raw)
	}
	var parts []map[string]any
	if m.Message != "" && (len(m.Images) > 0 || len(m.Files) > 0) {
		parts = append(parts, map[string]any{"type": "text", "text": m.Message})
	}
	for _, img := range m.Images {
		p, err := conv.mediaPart(ctx, "image", img)
		if err != nil {
			return nil, err
		}
		parts = append(parts, p)
	}
	for _, f := range m.Files {
		p, err := conv.mediaPart(ctx, "file", f)
		if err != nil {
			return nil, err
		}
		parts = append(parts, p)
	}
	if len(parts) > 0 {
		raw, err := json.Marshal(parts)
		if err != nil {
			return nil, err
		}
		out["parts"] = json.RawMessage(raw)
	}
	return out, nil
}

// mediaPart turns one karma image/file entry into a stored part. Karma spells
// attachments as strings: either a URL or a base64 data URL.
func (conv *Conversation) mediaPart(ctx context.Context, kind, src string) (map[string]any, error) {
	if strings.HasPrefix(src, "data:") {
		contentType, b64, ok := splitDataURL(src)
		if !ok {
			return nil, fmt.Errorf("gitloom: unparseable data URL in %s attachment", kind)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("gitloom: %s attachment is not valid base64: %w", kind, err)
		}
		info, err := conv.client.UploadMedia(ctx, contentType, raw)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": kind, "media_id": info.ID}, nil
	}
	return map[string]any{"type": kind + "_url", "url": src}, nil
}

func splitDataURL(s string) (contentType, b64 string, ok bool) {
	rest, found := strings.CutPrefix(s, "data:")
	if !found {
		return "", "", false
	}
	meta, data, found := strings.Cut(rest, ",")
	if !found {
		return "", "", false
	}
	contentType = strings.TrimSuffix(meta, ";base64")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return contentType, data, true
}

func fromWire(w wireMessage) models.AIMessage {
	m := models.AIMessage{
		Role:       models.AIRoles(w.Role),
		Message:    w.Content,
		ToolCallId: w.ToolCallID,
	}
	if len(w.ToolCalls) > 0 {
		_ = json.Unmarshal(w.ToolCalls, &m.ToolCalls)
	}
	return m
}
