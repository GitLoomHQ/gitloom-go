package gitloom

import (
	"strings"

	"github.com/MelloB1989/karma/models"
)

// contextLimits by model prefix; longest prefix wins. Input limits — a caller
// who wants room for the reply sets ReserveForReply.
var contextLimits = []struct {
	prefix string
	limit  int
}{
	{"claude-opus-5", 1_000_000},
	{"claude-sonnet-5", 1_000_000},
	{"claude-fable-5", 1_000_000},
	{"claude-haiku-4-5", 200_000},
	{"claude-", 200_000},
	{"gpt-5", 400_000},
	{"gpt-4.1", 1_047_576},
	{"gpt-4o", 128_000},
	{"gpt-4", 8_192},
	{"o1", 200_000},
	{"o3", 200_000},
	{"gemini-1.5-pro", 2_000_000},
	{"gemini-", 1_000_000},
	{"llama-3", 128_000},
	{"mistral-", 32_000},
}

func contextLimit(model string) int {
	m := strings.ToLower(model)
	best, limit := -1, 128_000
	for _, e := range contextLimits {
		if strings.HasPrefix(m, e.prefix) && len(e.prefix) > best {
			best, limit = len(e.prefix), e.limit
		}
	}
	return limit
}

// Estimation constants, biased to overcount: an estimate that is too low
// produces a request the provider rejects, which is the one unacceptable
// failure. Real counts from karma's AIChatResponse override all of this.
const (
	charsPerToken      = 3.5
	perMessageOverhead = 4
	perImageTokens     = 1_100
)

func estimateText(s string) int {
	if s == "" {
		return 0
	}
	return int(float64(len(s))/charsPerToken) + 1
}

func estimateMessage(m models.AIMessage) int {
	n := perMessageOverhead + estimateText(m.Message)
	n += (len(m.Images) + len(m.Files)) * perImageTokens
	for _, tc := range m.ToolCalls {
		n += estimateText(tc.Function.Name) + estimateText(tc.Function.Arguments) + perMessageOverhead
	}
	return n
}

func estimateHistory(msgs []models.AIMessage, summary, _ string) int {
	n := 3 + estimateText(summary)
	for _, m := range msgs {
		n += estimateMessage(m)
	}
	return n
}
