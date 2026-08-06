package gitloom

import (
	"context"
	"fmt"
	"strings"

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
