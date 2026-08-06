package gitloom

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MelloB1989/karma/models"
)

// Completer is the slice of karma's KarmaAI this package calls. An interface
// so tests need no provider credentials; *ai.KarmaAI satisfies it as-is.
type Completer interface {
	ChatCompletion(messages models.AIChatHistory) (*models.AIChatResponse, error)
}

// Chat binds a karma model to a stored conversation: one Say() call runs the
// whole loop — recall memory, fit the window, complete, store both turns with
// the provider's real token count, compact and remember on cadence.
type Chat struct {
	AI        Completer
	Conv      *Conversation
	SystemMsg string
}

// NewChat wires a karma completer to a conversation. When the conversation has
// no Summarize option yet, compaction summaries are produced by the same
// completer — the model already in the loop is the model that summarizes it.
func NewChat(ai Completer, conv *Conversation, systemMsg string) *Chat {
	if conv.opts.Summarize == nil {
		conv.opts.Summarize = KarmaSummarizer(ai)
	}
	return &Chat{AI: ai, Conv: conv, SystemMsg: systemMsg}
}

// Say sends one user message and returns the assistant's reply.
func (ch *Chat) Say(ctx context.Context, message string) (*models.AIChatResponse, error) {
	return ch.SayMessage(ctx, models.AIMessage{Role: models.User, Message: message})
}

// SayMessage sends one full user message — text, images, files — and returns
// the reply. The heavy lifting in one place: memory context, window fit,
// completion, storage with real usage.
func (ch *Chat) SayMessage(ctx context.Context, msg models.AIMessage) (*models.AIChatResponse, error) {
	memoryCtx, err := ch.Conv.Context(ctx, msg.Message)
	if err != nil {
		// Retrieval failing must not fail the conversation; the model just
		// answers without background this turn.
		memoryCtx = ""
	}

	history := ch.Conv.History(ch.SystemMsg)
	if memoryCtx != "" {
		if history.Context != "" {
			history.Context += "\n\n" + memoryCtx
		} else {
			history.Context = memoryCtx
		}
	}
	history.Messages = append(history.Messages, msg)

	resp, err := ch.AI.ChatCompletion(history)
	if err != nil {
		return nil, fmt.Errorf("gitloom: completion: %w", err)
	}

	assistant := models.AIMessage{Role: models.Assistant, Message: resp.AIResponse}
	if len(resp.ToolCalls) > 0 {
		// Preserved so the stored conversation replays with its tool use.
		assistant.Metadata = map[string]any{"tool_calls": resp.ToolCalls}
	}
	if err := ch.Conv.Append(ctx, []models.AIMessage{msg, assistant}, resp); err != nil {
		return resp, fmt.Errorf("gitloom: reply produced but not stored: %w", err)
	}
	return resp, nil
}

// WrapKarma is the drop-in: it has karma's own ChatCompletion signature, so a
// caller switches from *ai.KarmaAI to this and changes nothing else. A history
// whose ChatId is set becomes a managed conversation — pass only the NEW
// messages; the stored conversation supplies the remembered window, memory
// supplies the context, and both turns are stored with the response's real
// token count. A history with no ChatId passes straight through.
type WrappedKarma struct {
	ai     Completer
	client *Client
	opts   ConversationOptions
	mu     sync.Mutex
	convs  map[string]*Conversation
}

// WrapKarma wraps a karma completer. opts configure the conversations the
// wrapper opens; Model is taken per call from karma's configuration when
// empty.
func WrapKarma(ai Completer, client *Client, opts ConversationOptions) *WrappedKarma {
	if opts.Summarize == nil && !opts.SummarizeServer {
		opts.Summarize = KarmaSummarizer(ai)
	}
	return &WrappedKarma{ai: ai, client: client, opts: opts, convs: map[string]*Conversation{}}
}

// ChatCompletion mirrors karma's. ChatId selects the managed conversation.
func (w *WrappedKarma) ChatCompletion(h models.AIChatHistory) (*models.AIChatResponse, error) {
	if h.ChatId == "" {
		return w.ai.ChatCompletion(h)
	}
	ctx := context.Background()
	conv, err := w.conversation(ctx, h.ChatId)
	if err != nil {
		return nil, err
	}

	fresh := h.Messages
	var lastUser string
	for i := len(fresh) - 1; i >= 0; i-- {
		if fresh[i].Role == models.User {
			lastUser = fresh[i].Message
			break
		}
	}
	memoryCtx, err := conv.Context(ctx, lastUser)
	if err != nil {
		memoryCtx = "" // retrieval failing must not fail the completion
	}

	out := conv.History(h.SystemMsg)
	if h.Title != "" {
		out.Title = h.Title
	}
	if memoryCtx != "" {
		if out.Context != "" {
			out.Context += "\n\n" + memoryCtx
		} else {
			out.Context = memoryCtx
		}
	}
	if h.Context != "" {
		out.Context = strings.TrimSpace(out.Context + "\n\n" + h.Context)
	}
	out.Messages = append(out.Messages, fresh...)

	resp, err := w.ai.ChatCompletion(out)
	if err != nil {
		return nil, err
	}
	stored := append([]models.AIMessage{}, fresh...)
	stored = append(stored, models.AIMessage{Role: models.Assistant, Message: resp.AIResponse})
	if err := conv.Append(ctx, stored, resp); err != nil {
		return resp, fmt.Errorf("gitloom: reply produced but not stored: %w", err)
	}
	return resp, nil
}

func (w *WrappedKarma) conversation(ctx context.Context, id string) (*Conversation, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.convs[id]; ok {
		return c, nil
	}
	c, err := w.client.NewConversation(ctx, id, w.opts)
	if err != nil {
		return nil, err
	}
	if err := c.Load(ctx, LoadOptions{}); err != nil {
		return nil, err
	}
	w.convs[id] = c
	return c, nil
}

// KarmaSummarizer compacts with the caller's own karma model.
func KarmaSummarizer(ai Completer) Summarizer {
	return func(ctx context.Context, evicted []models.AIMessage) (string, error) {
		var b strings.Builder
		for _, m := range evicted {
			fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Message)
		}
		resp, err := ai.ChatCompletion(models.AIChatHistory{
			SystemMsg: "Summarize this conversation fragment in under 150 words, keeping every " +
				"concrete fact — names, numbers, decisions, preferences. It becomes the model's " +
				"only record of these turns.",
			Messages: []models.AIMessage{{Role: models.User, Message: b.String()}},
		})
		if err != nil {
			return "", err
		}
		return resp.AIResponse, nil
	}
}
