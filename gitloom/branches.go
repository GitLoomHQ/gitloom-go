package gitloom

import (
	"context"
	"net/url"

	"github.com/MelloB1989/karma/models"
)

// Branch is one line of a conversation.
type Branch struct {
	Name       string `json:"name"`
	ForkedFrom string `json:"forked_from,omitempty"`
	ForkedAt   int64  `json:"forked_at,omitempty"`
}

// Rewind forks a new branch after seq and switches to it. Nothing is deleted;
// rewinding past a compaction is an ordinary read because the turns were never
// destroyed.
func (conv *Conversation) Rewind(ctx context.Context, seq int64) error {
	var res struct {
		Branch string `json:"branch"`
	}
	err := conv.client.request(ctx, "POST",
		"/v1/conversations/"+url.PathEscape(conv.ID)+"/rewind",
		map[string]any{"to": seq, "branch": conv.Branch}, &res)
	if err != nil {
		return err
	}
	return conv.Load(ctx, LoadOptions{Full: true, Branch: res.Branch, At: seq})
}

// Edit replaces the message at seq on a NEW branch — the edit every chat UI
// offers. The original line is untouched; the conversation switches to the
// fork, whose replacement occupies the same seq.
func (conv *Conversation) Edit(ctx context.Context, seq int64, replacement models.AIMessage) error {
	wire, err := conv.toWire(ctx, replacement)
	if err != nil {
		return err
	}
	var res struct {
		Branch string `json:"branch"`
	}
	err = conv.client.request(ctx, "POST",
		"/v1/conversations/"+url.PathEscape(conv.ID)+"/edit",
		map[string]any{"seq": seq, "branch": conv.Branch, "message": wire}, &res)
	if err != nil {
		return err
	}
	return conv.Load(ctx, LoadOptions{Full: true, Branch: res.Branch})
}

// EditInPlace rewrites the message at seq on this branch, destroying the
// original — the one edit that does not fork, for content that must stop
// existing (a leaked secret, PII). Later turns that answered the original are
// left standing.
func (conv *Conversation) EditInPlace(ctx context.Context, seq int64, content string) error {
	err := conv.client.request(ctx, "PATCH",
		"/v1/conversations/"+url.PathEscape(conv.ID)+"/messages/"+itoa(seq),
		map[string]any{"branch": conv.Branch, "content": content}, nil)
	if err != nil {
		return err
	}
	idx := seq - conv.firstLiveSeq
	if idx >= 0 && idx < int64(len(conv.history)) {
		conv.history[idx].Message = content
	}
	return nil
}

// SetTitle names the conversation, overwriting any automatic title.
func (conv *Conversation) SetTitle(ctx context.Context, title string) error {
	err := conv.client.request(ctx, "PATCH",
		"/v1/conversations/"+url.PathEscape(conv.ID),
		map[string]string{"title": title}, nil)
	if err != nil {
		return err
	}
	conv.Title = title
	return nil
}

// Branches lists every line of the conversation.
func (conv *Conversation) Branches(ctx context.Context) ([]Branch, error) {
	var res struct {
		Branches []Branch `json:"branches"`
	}
	err := conv.client.request(ctx, "GET",
		"/v1/conversations/"+url.PathEscape(conv.ID)+"/branches", nil, &res)
	return res.Branches, err
}

// Ingest hands a range of turns to memory without compacting.
func (conv *Conversation) Ingest(ctx context.Context, from, to int64) error {
	return conv.client.request(ctx, "POST",
		"/v1/conversations/"+url.PathEscape(conv.ID)+"/ingest",
		map[string]any{"branch": conv.Branch, "from_seq": from, "to_seq": to}, nil)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
