package gitloom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MelloB1989/karma/models"
)

// fakeAPI keeps the server's invariants: sequences advance, messages are never
// deleted, compactions are recorded rather than applied.
type fakeAPI struct {
	mu          sync.Mutex
	messages    []map[string]any
	compactions []map[string]any
	uploads     []string // content types, in order
	title       string
	nextSeq     int64
	branch      string
	remembered  int
}

func newFakeAPI(t *testing.T) (*fakeAPI, *Client) {
	t.Helper()
	f := &fakeAPI{branch: "main"}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	c := New("gl_test_key", WithBaseURL(srv.URL), WithNamespace("ns"))
	return f, c
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	p := r.URL.Path

	switch {
	case p == "/v1/media" && r.Method == "POST":
		f.uploads = append(f.uploads, body["content_type"].(string))
		send(map[string]any{"id": fmt.Sprintf("med-%d", len(f.uploads)), "bytes": 42})
	case p == "/v1/memories":
		f.remembered++
		send(map[string]any{"status": "accepted"})
	case p == "/v1/retrieve":
		send(map[string]any{"namespace": "ns", "hits": []map[string]any{
			{"path": "facts/a.md", "score": 0.5, "snippet": "the user prefers Go"},
		}, "millis": 3})
	case p == "/v1/conversations" && r.Method == "POST":
		send(map[string]any{"branch": "main", "next_seq": f.nextSeq})
	case strings.HasSuffix(p, "/messages") && r.Method == "POST":
		msgs := body["messages"].([]any)
		for _, m := range msgs {
			mm := m.(map[string]any)
			mm["seq"] = f.nextSeq
			mm["branch"] = body["branch"]
			f.messages = append(f.messages, mm)
			f.nextSeq++
		}
		send(map[string]any{"next_seq": f.nextSeq, "written": len(msgs)})
	case strings.HasSuffix(p, "/compact"):
		f.compactions = append(f.compactions, body)
		send(map[string]any{"compacted": true, "summary": "server summary"})
	case strings.HasSuffix(p, "/edit"):
		seq := int64(body["seq"].(float64))
		name := fmt.Sprintf("main-%d", seq)
		msg := body["message"].(map[string]any)
		msg["seq"] = seq
		msg["branch"] = name
		f.messages = append(f.messages, msg)
		f.branch = name
		send(map[string]any{"branch": name, "next_seq": seq + 1})
	case strings.Contains(p, "/messages/") && r.Method == "PATCH":
		send(map[string]any{"updated": true})
	case r.Method == "PATCH":
		f.title = body["title"].(string)
		send(map[string]any{"title": f.title})
	default: // load
		visible := []map[string]any{}
		branch := r.URL.Query().Get("branch")
		if branch == "" {
			branch = f.branch
		}
		for _, m := range f.messages {
			if m["branch"] == branch {
				visible = append(visible, m)
			}
		}
		send(map[string]any{"branch": branch, "title": f.title, "next_seq": f.nextSeq, "messages": visible})
	}
}

// fakeCompleter answers instantly and reports whatever token count it is told.
type fakeCompleter struct {
	reply  string
	tokens int
	calls  []models.AIChatHistory
}

func (f *fakeCompleter) ChatCompletion(h models.AIChatHistory) (*models.AIChatResponse, error) {
	f.calls = append(f.calls, h)
	return &models.AIChatResponse{AIResponse: f.reply, Tokens: f.tokens}, nil
}

func TestChatSayRunsTheWholeLoop(t *testing.T) {
	api, client := newFakeAPI(t)
	conv, err := client.NewConversation(t.Context(), "c1", ConversationOptions{Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	ai := &fakeCompleter{reply: "hello there", tokens: 120}
	chat := NewChat(ai, conv, "be brief")

	resp, err := chat.Say(t.Context(), "what language do I prefer?")
	if err != nil {
		t.Fatal(err)
	}
	if resp.AIResponse != "hello there" {
		t.Fatalf("reply = %q", resp.AIResponse)
	}
	// Memory was consulted and reached the model as context.
	if len(ai.calls) != 1 || !strings.Contains(ai.calls[0].Context, "prefers Go") {
		t.Fatalf("memory context missing from the completion: %+v", ai.calls[0].Context)
	}
	// Both turns stored.
	if len(api.messages) != 2 {
		t.Fatalf("stored %d messages, want 2", len(api.messages))
	}
	if api.messages[1]["content"] != "hello there" {
		t.Fatalf("assistant turn not stored: %+v", api.messages[1])
	}
}

func TestAppendUploadsDataURLAttachments(t *testing.T) {
	api, client := newFakeAPI(t)
	conv, err := client.NewConversation(t.Context(), "c1", ConversationOptions{Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	err = conv.Append(t.Context(), []models.AIMessage{{
		Role:    models.User,
		Message: "look at this",
		Images:  []string{"data:image/png;base64,aGVsbG8="},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(api.uploads) != 1 || api.uploads[0] != "image/png" {
		t.Fatalf("data URL not uploaded: %v", api.uploads)
	}
	var parts []map[string]any
	raw, _ := json.Marshal(api.messages[0]["parts"])
	_ = json.Unmarshal(raw, &parts)
	found := false
	for _, p := range parts {
		if p["media_id"] == "med-1" {
			found = true
		}
		if _, hasData := p["data"]; hasData {
			t.Fatal("bytes landed in the stored message")
		}
	}
	if !found {
		t.Fatalf("stored parts reference no upload: %v", parts)
	}
}

// Compaction is also the memory trigger: the cadence must fire even when a
// million-token window has barely been touched.
func TestCadenceCompactionFiresWithTokensToSpare(t *testing.T) {
	api, client := newFakeAPI(t)
	conv, err := client.NewConversation(t.Context(), "c1", ConversationOptions{
		Model:        "claude-sonnet-5",
		CompactEvery: 2,
		Summarize: func(ctx context.Context, evicted []models.AIMessage) (string, error) {
			return "summarized", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		err = conv.Append(t.Context(), []models.AIMessage{
			{Role: models.User, Message: fmt.Sprintf("q%d", i)},
			{Role: models.Assistant, Message: fmt.Sprintf("a%d", i)},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(api.compactions) == 0 {
		t.Fatal("the cadence never compacted; nothing would ever reach memory")
	}
}

// The provider's count beats the estimator: a conversation that looks tiny but
// reports 9k tokens must compact.
func TestReportedUsageTriggersCompaction(t *testing.T) {
	api, client := newFakeAPI(t)
	conv, err := client.NewConversation(t.Context(), "c1", ConversationOptions{
		Model:        "gpt-4o",
		MaxTokens:    10_000,
		CompactAt:    0.5,
		CompactEvery: -1, // cadence off; only tokens can trigger
		Summarize: func(ctx context.Context, evicted []models.AIMessage) (string, error) {
			return "summarized", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = conv.Append(t.Context(), []models.AIMessage{
		{Role: models.User, Message: "short"},
		{Role: models.Assistant, Message: "also short"},
	}, &models.AIChatResponse{Tokens: 9_000})
	if err != nil {
		t.Fatal(err)
	}
	err = conv.Append(t.Context(), []models.AIMessage{{Role: models.User, Message: "tiny"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(api.compactions) == 0 {
		t.Fatal("9k reported tokens against a 5k threshold did not compact")
	}
}

func TestEditForksAndSwitches(t *testing.T) {
	api, client := newFakeAPI(t)
	conv, err := client.NewConversation(t.Context(), "c1", ConversationOptions{Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	err = conv.Append(t.Context(), []models.AIMessage{
		{Role: models.User, Message: "original"},
		{Role: models.Assistant, Message: "reply"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.Edit(t.Context(), 0, models.AIMessage{Role: models.User, Message: "edited"}); err != nil {
		t.Fatal(err)
	}
	if conv.Branch == "main" {
		t.Fatal("edit must switch to a fork")
	}
	// Original untouched on main.
	for _, m := range api.messages {
		if m["branch"] == "main" && m["seq"] == int64(0) && m["content"] != "original" {
			t.Fatalf("original mutated: %+v", m)
		}
	}
}

func TestAPIErrorBothShapes(t *testing.T) {
	for _, tc := range []struct{ body, wantCode, wantMsg string }{
		{`{"error":{"code":"quota_exceeded","message":"limit reached"}}`, "quota_exceeded", "limit reached"},
		{`{"error":"memory unavailable"}`, "http_error", "memory unavailable"},
	} {
		e := apiErrorFrom(429, []byte(tc.body))
		if e.Code != tc.wantCode || e.Message != tc.wantMsg {
			t.Fatalf("%s -> %+v", tc.body, e)
		}
	}
}

// The drop-in: karma's own signature, ChatId selects the conversation, only
// new messages are passed, and the wrapper supplies the remembered window.
func TestWrapKarmaIsADropIn(t *testing.T) {
	api, client := newFakeAPI(t)
	ai := &fakeCompleter{reply: "the reply", tokens: 50}
	kai := WrapKarma(ai, client, ConversationOptions{Model: "gpt-4o"})

	_, err := kai.ChatCompletion(models.AIChatHistory{
		ChatId:   "conv-1",
		Messages: []models.AIMessage{{Role: models.User, Message: "I like Go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != 2 {
		t.Fatalf("both turns should be stored, got %d", len(api.messages))
	}

	_, err = kai.ChatCompletion(models.AIChatHistory{
		ChatId:   "conv-1",
		Messages: []models.AIMessage{{Role: models.User, Message: "what do I like?"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The second completion carried the first exchange without the caller
	// passing it.
	second := ai.calls[1]
	var texts []string
	for _, m := range second.Messages {
		texts = append(texts, string(m.Role)+":"+m.Message)
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "user:I like Go") || !strings.Contains(joined, "assistant:the reply") {
		t.Fatalf("window not supplied by the wrapper: %s", joined)
	}
	// Memory context reached the model as background.
	if !strings.Contains(second.Context, "prefers Go") {
		t.Fatalf("memory context missing: %q", second.Context)
	}
}

// No ChatId means no management: the wrapper is invisible.
func TestWrapKarmaPassesPlainCallsThrough(t *testing.T) {
	api, client := newFakeAPI(t)
	ai := &fakeCompleter{reply: "r", tokens: 1}
	kai := WrapKarma(ai, client, ConversationOptions{Model: "gpt-4o"})
	if _, err := kai.ChatCompletion(models.AIChatHistory{
		Messages: []models.AIMessage{{Role: models.User, Message: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != 0 {
		t.Fatal("a plain call must store nothing")
	}
}

// Server-side compaction: the summary comes back from GitLoom's model.
func TestServerSideCompaction(t *testing.T) {
	api, client := newFakeAPI(t)
	conv, err := client.NewConversation(t.Context(), "c1", ConversationOptions{
		Model: "claude-sonnet-5", CompactEvery: 1, SummarizeServer: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := conv.Append(t.Context(), []models.AIMessage{
			{Role: models.User, Message: "q"}, {Role: models.Assistant, Message: "a"},
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(api.compactions) == 0 {
		t.Fatal("server compaction never asked the server")
	}
	if _, hasSummary := api.compactions[0]["summary"]; hasSummary {
		t.Fatal("auto compaction must not send a client summary")
	}
	if api.compactions[0]["auto"] != true {
		t.Fatalf("auto flag missing: %+v", api.compactions[0])
	}
}

// The features accessor returns the SAME conversation the completions flow
// through: an edit there is what the next completion continues from.
func TestWrapperFeaturesShareState(t *testing.T) {
	_, client := newFakeAPI(t)
	ai := &fakeCompleter{reply: "r", tokens: 1}
	kai := WrapKarma(ai, client, ConversationOptions{Model: "gpt-4o"})

	if _, err := kai.ChatCompletion(models.AIChatHistory{
		ChatId:   "conv-1",
		Messages: []models.AIMessage{{Role: models.User, Message: "original"}},
	}); err != nil {
		t.Fatal(err)
	}
	conv, err := kai.Conversation(t.Context(), "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.Edit(t.Context(), 0, models.AIMessage{Role: models.User, Message: "edited"}); err != nil {
		t.Fatal(err)
	}
	if _, err := kai.ChatCompletion(models.AIChatHistory{
		ChatId:   "conv-1",
		Messages: []models.AIMessage{{Role: models.User, Message: "continue"}},
	}); err != nil {
		t.Fatal(err)
	}
	last := ai.calls[len(ai.calls)-1]
	var joined string
	for _, m := range last.Messages {
		joined += string(m.Role) + ":" + m.Message + "|"
	}
	if !strings.Contains(joined, "user:edited") {
		t.Fatalf("the completion did not continue from the edited branch: %s", joined)
	}
	if strings.Contains(joined, "user:original|") {
		t.Fatalf("the original message leaked into the edited branch: %s", joined)
	}
}
