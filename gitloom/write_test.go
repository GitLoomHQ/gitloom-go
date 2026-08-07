package gitloom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
		r.method, r.path, r.query = req.Method, req.URL.Path, req.URL.RawQuery
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

	err := c.Write(context.Background(), []Memory{{
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
	if err := c.Write(context.Background(), []Memory{
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
