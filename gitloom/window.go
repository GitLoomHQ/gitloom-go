package gitloom

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/MelloB1989/karma/models"
)

// History renders the conversation for a completion: the compaction summary as
// context, then the live turns, guaranteed inside the model's window.
func (conv *Conversation) History(systemMsg string) models.AIChatHistory {
	h := models.AIChatHistory{
		ChatId:    conv.ID,
		Title:     conv.Title,
		SystemMsg: systemMsg,
		Messages:  conv.fitted(),
	}
	if conv.summary != "" {
		h.Context = "Earlier in this conversation: " + conv.summary
	}
	return h
}

// Context retrieves memory bearing on the user's message, per the
// conversation's memory mode. Empty when the mode is not MemoryQuery or
// nothing relevant is stored.
func (conv *Conversation) Context(ctx context.Context, userMessage string) (string, error) {
	mode := conv.opts.Memory
	if mode == "" {
		mode = MemoryQuery
	}
	if mode != MemoryQuery || strings.TrimSpace(userMessage) == "" {
		return "", nil
	}

	emit := conv.opts.OnEvent
	if emit != nil {
		emit(WrapEvent{Kind: "memory.recall"})
	}
	res, err := conv.client.Recall(ctx, userMessage, &RecallOptions{Namespace: conv.opts.Namespace})
	if err != nil {
		if emit != nil {
			emit(WrapEvent{Kind: "memory.recall.done", Err: err})
		}
		return "", err
	}
	if emit != nil {
		emit(WrapEvent{Kind: "memory.recall.done", Hits: len(res.Memories)})
	}
	if len(res.Memories) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("What you already know about this user, from earlier conversations. Treat it as background, not as something they just said:\n")
	for _, m := range res.Memories {
		text := m.Content
		if text == "" {
			text = m.Snippet
		}
		b.WriteString("- ")
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// Compact summarizes what the window can no longer hold and hands the evicted
// turns to memory. The summary is produced locally by the caller's model;
// GitLoom receives the turns for ingestion, so the flattened detail stays
// recallable.
func (conv *Conversation) Compact(ctx context.Context) (string, error) {
	if conv.opts.Summarize == nil && conv.opts.SummarizeServer {
		return conv.compactOnServer(ctx)
	}
	if conv.opts.Summarize == nil {
		return "", fmt.Errorf("gitloom: compaction needs a Summarize option or SummarizeServer; without one the evicted turns would be dropped")
	}
	evicted := conv.evictable()
	if len(evicted) == 0 {
		// The window has room, but the trigger knew better — the cadence
		// fired, or the provider reported more tokens than the estimate.
		// Compacting must compact: evict all but the latest exchange.
		if len(conv.history) < 2 {
			return "", nil
		}
		keep := 2
		if len(conv.history) <= keep {
			keep = len(conv.history) - 1
		}
		evicted = conv.history[:len(conv.history)-keep]
	}

	summary, err := conv.opts.Summarize(ctx, evicted)
	if err != nil {
		return "", fmt.Errorf("gitloom: summarize: %w", err)
	}
	from := conv.firstLiveSeq
	to := from + int64(len(evicted)) - 1
	err = conv.client.request(ctx, "POST",
		"/v1/conversations/"+url.PathEscape(conv.ID)+"/compact",
		map[string]any{"branch": conv.Branch, "summary": summary, "from_seq": from, "to_seq": to}, nil)
	if err != nil {
		return "", err
	}
	if conv.summary != "" {
		conv.summary = conv.summary + "\n\nThen: " + summary
	} else {
		conv.summary = summary
	}
	conv.history = conv.history[len(evicted):]
	conv.firstLiveSeq = to + 1
	conv.exchanges = 0
	conv.reportedTokens = 0
	return summary, nil
}

// compactOnServer asks GitLoom's own model to write the summary from the
// stored turns — the developer's choice against local summarization. The turns
// are already stored server-side, so summarizing them there adds no exposure;
// it costs one chat from the account's meter.
func (conv *Conversation) compactOnServer(ctx context.Context) (string, error) {
	evicted := conv.evictable()
	if len(evicted) == 0 {
		if len(conv.history) < 2 {
			return "", nil
		}
		keep := 2
		if len(conv.history) <= keep {
			keep = len(conv.history) - 1
		}
		evicted = conv.history[:len(conv.history)-keep]
	}
	from := conv.firstLiveSeq
	to := from + int64(len(evicted)) - 1
	var res struct {
		Summary string `json:"summary"`
	}
	err := conv.client.request(ctx, "POST",
		"/v1/conversations/"+url.PathEscape(conv.ID)+"/compact",
		map[string]any{"branch": conv.Branch, "auto": true, "from_seq": from, "to_seq": to}, &res)
	if err != nil {
		return "", err
	}
	if conv.summary != "" {
		conv.summary = conv.summary + "\n\nThen: " + res.Summary
	} else {
		conv.summary = res.Summary
	}
	conv.history = conv.history[len(evicted):]
	conv.firstLiveSeq = to + 1
	conv.exchanges = 0
	conv.reportedTokens = 0
	return res.Summary, nil
}

// fitted returns the live turns that fit the window, oldest evicted first.
func (conv *Conversation) fitted() []models.AIMessage {
	budget := conv.budget()
	kept := conv.history
	for len(kept) > 1 && estimateHistory(kept, conv.summary, conv.opts.Model) > budget {
		kept = kept[1:]
	}
	return kept
}

// evictable is what fitted() dropped: the oldest turns past the window.
func (conv *Conversation) evictable() []models.AIMessage {
	fittedLen := len(conv.fitted())
	return conv.history[:len(conv.history)-fittedLen]
}

func (conv *Conversation) budget() int {
	ceiling := conv.opts.MaxTokens
	if ceiling == 0 {
		ceiling = int(float64(contextLimit(conv.opts.Model)) * 0.9)
	}
	return ceiling - conv.opts.ReserveForReply
}

func (conv *Conversation) wouldOverflow(incoming []models.AIMessage) bool {
	frac := conv.opts.CompactAt
	if frac == 0 {
		frac = 0.85
	}
	threshold := int(float64(conv.budget()) * frac)
	held := conv.reportedTokens
	if held == 0 {
		held = estimateHistory(conv.history, conv.summary, conv.opts.Model)
	}
	return held+estimateHistory(incoming, "", conv.opts.Model) > threshold
}

func (conv *Conversation) cadenceDue() bool {
	every := conv.opts.CompactEvery
	if conv.opts.CompactEvery == 0 {
		every = 5
	}
	if every < 0 {
		return false
	}
	return conv.exchanges >= every
}
