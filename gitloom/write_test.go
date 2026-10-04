package gitloom

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// recorder captures what the client actually put on the wire, which is the
// only thing these methods are responsible for.
type recorder struct {
	method string
	path   string
	query  string
	body   map[string]any
	reply  any
}

func newRecorder(t *testing.T, reply any) (*recorder, *Client) {
	t.Helper()
	r := &recorder{reply: reply}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.method, r.path, r.query, r.body = req.Method, req.URL.Path, req.URL.RawQuery, nil
		_ = json.NewDecoder(req.Body).Decode(&r.body)
		if r.reply != nil {
			_ = json.NewEncoder(w).Encode(r.reply)
		}
	}))
	t.Cleanup(srv.Close)
	return r, New("gl_test", WithBaseURL(srv.URL), WithNamespace("ns"))
}

func TestWriteSendsMemoriesNotMessages(t *testing.T) {
	r, c := newRecorder(t, map[string]any{"status": "accepted"})

	err := c.Write(context.Background(), []NewMemory{{
		Path: "facts/people/maya.md", Content: "Maya rides a bicycle.",
		Tags: []string{"people"}, Confidence: 0.8, Date: "2026-07-19",
		Cues: []string{"how does Maya get around"},
	}}, nil)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if r.method != "POST" || r.path != "/v1/memories" {
		t.Fatalf("sent %s %s", r.method, r.path)
	}
	// The distinction the endpoint dispatches on: memories are stored as given,
	// messages are handed to a model. Sending the wrong key silently changes
	// which one happens.
	if _, ok := r.body["memories"]; !ok {
		t.Error("the request carried no memories key")
	}
	if _, ok := r.body["messages"]; ok {
		t.Error("a direct write must not send messages")
	}
	if r.body["namespace"] != "ns" {
		t.Errorf("namespace = %v, want the client default", r.body["namespace"])
	}

	got := r.body["memories"].([]any)[0].(map[string]any)
	if got["date"] != "2026-07-19" {
		t.Errorf("date = %v; dropping it stamps backfilled memories with today", got["date"])
	}
	if got["confidence"] != 0.8 {
		t.Errorf("confidence = %v, want 0.8", got["confidence"])
	}
}

func TestWriteRejectsBadPathBeforeSending(t *testing.T) {
	r, c := newRecorder(t, nil)
	// Caught locally, because the server's refusal arrives after a network
	// round trip and one bad path fails a whole batch.
	if err := c.Write(context.Background(), []NewMemory{
		{Path: "facts/ok.md", Content: "x"},
		{Path: "facts/not-markdown", Content: "y"},
	}, nil); err == nil {
		t.Fatal("a path not ending in .md should be refused")
	}
	if r.method != "" {
		t.Error("the request was sent despite the invalid path")
	}
}

func TestWriteNothingIsNoOp(t *testing.T) {
	r, c := newRecorder(t, nil)
	if err := c.Write(context.Background(), nil, nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	if r.method != "" {
		t.Error("writing nothing should not call the API")
	}
}

func TestForgetUsesQueryStringNotBody(t *testing.T) {
	r, c := newRecorder(t, map[string]any{"status": "accepted"})
	if err := c.Forget(context.Background(), []string{"facts/a.md", "facts/b.md"}, nil); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if r.method != "DELETE" {
		t.Fatalf("method = %s, want DELETE", r.method)
	}
	// Several HTTP clients decline to send a body on DELETE, so the paths ride
	// in the query string.
	if r.query == "" || r.body != nil {
		t.Errorf("query = %q, body = %v; paths belong in the query", r.query, r.body)
	}
}

func TestGetReadsByPath(t *testing.T) {
	r, c := newRecorder(t, map[string]any{
		"path": "facts/people/maya.md", "content": "Maya rides a bicycle.",
		"tags": []string{"people"}, "confidence": 0.8,
	})
	m, err := c.Get(context.Background(), "facts/people/maya.md", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if r.method != "GET" || r.path != "/v1/memories" {
		t.Fatalf("sent %s %s", r.method, r.path)
	}
	if m.Content == "" || m.Confidence != 0.8 {
		t.Errorf("decoded %+v", m)
	}
}

func TestTreeAndTopicsCarryTheirFilters(t *testing.T) {
	r, c := newRecorder(t, map[string]any{"tree": map[string]any{"path": ""}})
	if _, err := c.Tree(context.Background(), &TreeOptions{Path: "facts", Depth: 3}); err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if r.path != "/v1/tree" {
		t.Fatalf("path = %s", r.path)
	}
	for _, want := range []string{"path=facts", "depth=3", "namespace=ns"} {
		if !contains(r.query, want) {
			t.Errorf("query %q is missing %q", r.query, want)
		}
	}

	r2, c2 := newRecorder(t, map[string]any{"topics": []any{}})
	if _, err := c2.Topics(context.Background(), &TopicsOptions{Like: "databas", Tier: "facts", MinFiles: 2}); err != nil {
		t.Fatalf("Topics: %v", err)
	}
	for _, want := range []string{"like=databas", "tier=facts", "min_files=2"} {
		if !contains(r2.query, want) {
			t.Errorf("query %q is missing %q", r2.query, want)
		}
	}
}

func TestGraphDecodesNodesAndEdges(t *testing.T) {
	_, c := newRecorder(t, map[string]any{
		"nodes": []map[string]any{{"path": "facts/a.md", "tier": "facts"}},
		"edges": []map[string]any{{"src": "facts/a.md", "dst": "facts/b.md", "label": "spouse"}},
	})
	g, err := c.Graph(context.Background(), nil)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(g.Nodes) != 1 || len(g.Edges) != 1 || g.Edges[0].Label != "spouse" {
		t.Errorf("decoded %+v", g)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestRecallCanDropProvenance(t *testing.T) {
	r, c := newRecorder(t, map[string]any{"hits": []any{}})
	// Provenance is a git-log walk per hit and dominates the request once a
	// memory has history; a latency-sensitive caller has to be able to say no.
	if _, err := c.Recall(context.Background(), "anything", &RecallOptions{NoProvenance: true}); err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if !contains(r.query, "no_provenance=1") {
		t.Errorf("query %q did not carry the opt-out", r.query)
	}
	if contains(r.query, "no_relations") {
		t.Error("relations were dropped without being asked to be")
	}
}

func TestWriteSendsTagsAndWhenItHappened(t *testing.T) {
	r, c := newRecorder(t, map[string]any{"status": "accepted"})
	ist := time.FixedZone("IST", 5*3600+1800)

	err := c.Write(context.Background(), []NewMemory{
		{Path: "facts/a.md", Content: "a", Tags: []string{"#work", "q3 plan"},
			OccurredAt: At(time.Date(2026, 7, 31, 15, 30, 0, 0, ist))},
		{Path: "facts/b.md", Content: "b", OccurredAt: Day(2026, time.July, 19)},
		{Path: "facts/c.md", Content: "c", OccurredAt: Date("2026-07-19T09:30")},
		{Path: "facts/d.md", Content: "d", OccurredAt: Unix(1785492000)},
		{Path: "facts/e.md", Content: "e", OccurredAt: At(time.Date(1965, 3, 1, 0, 0, 0, 0, time.UTC))},
		{Path: "facts/f.md", Content: "f", Date: "2026-07-19"},
	}, &WriteOptions{Timezone: "Asia/Kolkata"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if r.body["timezone"] != "Asia/Kolkata" {
		t.Errorf("timezone = %v", r.body["timezone"])
	}
	mems := r.body["memories"].([]any)
	at := func(i int) any { return mems[i].(map[string]any)["occurred_at"] }

	// A time.Time goes as epoch seconds: a JSON number, whatever its zone.
	if at(0) != float64(1785492000) || at(3) != float64(1785492000) {
		t.Errorf("instants = %v, %v; want the number 1785492000", at(0), at(3))
	}
	if at(1) != "2026-07-19" {
		t.Errorf("day = %v; a date alone is what marks day precision", at(1))
	}
	if at(2) != "2026-07-19T09:30" {
		t.Errorf("text = %v, want it sent as given", at(2))
	}
	// Too few digits for the server to read as epoch seconds.
	if at(4) != "1965-03-01T00:00:00Z" {
		t.Errorf("1965 = %v, want RFC 3339", at(4))
	}
	last := mems[5].(map[string]any)
	if _, sent := last["occurred_at"]; sent || last["date"] != "2026-07-19" {
		t.Errorf("deprecated date: %v", last)
	}
	if tags := mems[0].(map[string]any)["tags"].([]any); len(tags) != 2 || tags[0] != "#work" {
		t.Errorf("tags = %v", tags)
	}
}

func TestRememberSendsTagsAndWhenTheConversationHappened(t *testing.T) {
	r, c := newRecorder(t, map[string]any{"status": "accepted"})
	ctx := context.Background()
	turns := []Turn{{Role: "user", Content: "we landed in Kyoto"}}

	if err := c.Remember(ctx, turns, &RememberOptions{
		Tags: []string{"trip"}, OccurredAt: Date("2026-05-14T19:30"), Timezone: "Asia/Kolkata",
	}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if r.body["occurred_at"] != "2026-05-14T19:30" || r.body["timezone"] != "Asia/Kolkata" {
		t.Errorf("body = %v", r.body)
	}
	if tags, _ := r.body["tags"].([]any); len(tags) != 1 || tags[0] != "trip" {
		t.Errorf("tags = %v", r.body["tags"])
	}
	if _, ok := r.body["memories"]; ok {
		t.Error("a conversation must not send memories")
	}

	if err := c.Remember(ctx, turns, &RememberOptions{OccurredAt: At(time.Unix(1778767200, 0))}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if r.body["occurred_at"] != float64(1778767200) {
		t.Errorf("occurred_at = %v, want epoch seconds", r.body["occurred_at"])
	}

	if err := c.Remember(ctx, turns, &RememberOptions{Date: "2026-05-14"}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	for _, key := range []string{"tags", "occurred_at", "timezone"} {
		if _, ok := r.body[key]; ok {
			t.Errorf("%s sent unasked", key)
		}
	}
	if r.body["date"] != "2026-05-14" {
		t.Errorf("deprecated date = %v", r.body["date"])
	}
}

// A batch of NewMemory kept as JSON, as a migration does, must read back.
func TestWhenRoundTrips(t *testing.T) {
	in := `[{"path":"facts/a.md","content":"a","occurred_at":1785492000.7},
		{"path":"facts/b.md","content":"b","occurred_at":"2026-07-19"},
		{"path":"facts/c.md","content":"c"}]`
	var mems []NewMemory
	if err := json.Unmarshal([]byte(in), &mems); err != nil {
		t.Fatal(err)
	}
	if !mems[2].OccurredAt.IsZero() {
		t.Error("an absent time should be zero")
	}
	out, err := json.Marshal(mems)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"occurred_at":1785492000}`, `"occurred_at":"2026-07-19"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("%s is missing %s", out, want)
		}
	}
	if strings.Count(string(out), "occurred_at") != 2 {
		t.Errorf("an unset time was sent: %s", out)
	}
}

func TestWriteSurfacesARefusedTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":"invalid_tag","message":"memories[1].tags[0] \"a,b\" has a character tags may not hold"}}`))
	}))
	defer srv.Close()
	c := New("k", WithBaseURL(srv.URL), WithNamespace("ns"))

	err := c.Write(context.Background(), []NewMemory{
		{Path: "facts/a.md", Content: "a"}, {Path: "facts/b.md", Content: "b", Tags: []string{"a,b"}},
	}, nil)
	var api *APIError
	if !errors.As(err, &api) || api.Status != 400 || api.Code != "invalid_tag" ||
		!strings.Contains(api.Message, "memories[1].tags[0]") {
		t.Errorf("err = %v", err)
	}
}
