package gitloom

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// The direct memory primitives.
//
// Remember hands GitLoom a conversation and a model decides what is worth
// keeping. These are the other half: the caller already knows what the memory
// is and where it belongs. That is the shape a migration from another store
// takes, and the shape an agent takes when it did its own reasoning and wants
// the conclusion filed rather than re-derived.

// Memory is one already-formed memory to store.
type Memory struct {
	// Path is repo-relative and ends in .md, under one of the four tiers:
	// facts/, incidents/, rules/ or skills/. The directory IS the topic, so
	// facts/people/maya.md files a memory about Maya under people.
	Path string `json:"path"`
	// Content is markdown. Its ## headers become separately addressable
	// sub-nodes, so retrieval can point at a section rather than a whole file.
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
	// Confidence in [0,1]. Retrieval uses it to break ties between memories
	// that contradict each other.
	Confidence float64 `json:"confidence,omitempty"`
	// TTL expires an incident, e.g. "30d". Only meaningful in incidents/.
	TTL string `json:"ttl,omitempty"`
	// Supersedes names a memory this one replaces, so an update wins over what
	// it contradicts instead of both being returned.
	Supersedes string `json:"supersedes,omitempty"`
	// Date is what the memory is ABOUT (YYYY-MM-DD), not when it was written.
	// Backfilling last year's facts without it stamps them all with today and
	// destroys every recency judgement retrieval makes.
	Date string `json:"date,omitempty"`
	// Cues are 2-5 short phrasings of how someone would later ASK for this.
	// They are embedded for semantic search, and they are the difference
	// between a memory that is found and one that is merely stored.
	Cues []string `json:"cues,omitempty"`
	// Related are paths of related memories, optionally labelled
	// ("spouse: facts/people/maya.md"). A link written before its target exists
	// heals itself once the target is written.
	Related []string `json:"related,omitempty"`
}

// WriteOptions shape one direct write.
type WriteOptions struct {
	Namespace string
}

// Write stores already-formed memories. Asynchronous, like Remember: the write
// is queued behind any other write to the same namespace and the memories
// appear in retrieval within seconds.
//
// Send them in batches. One call is one repository fetch, one commit round and
// one push, so a thousand memories sent a thousand at a time costs a thousand
// times what sending them in batches of a few hundred does.
func (c *Client) Write(ctx context.Context, memories []Memory, opts *WriteOptions) error {
	if len(memories) == 0 {
		return nil
	}
	ns := c.namespace
	if opts != nil && opts.Namespace != "" {
		ns = opts.Namespace
	}
	for i, m := range memories {
		if !strings.HasSuffix(m.Path, ".md") {
			return fmt.Errorf("gitloom: memory %d: path %q must end in .md", i, m.Path)
		}
	}
	return c.request(ctx, "POST", "/v1/memories",
		map[string]any{"namespace": ns, "memories": memories}, nil)
}

// StoredMemory is one memory read back by path.
type StoredMemory struct {
	Namespace  string   `json:"namespace"`
	Path       string   `json:"path"`
	Title      string   `json:"title,omitempty"`
	Tier       string   `json:"tier,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Content    string   `json:"content"`
	Tags       []string `json:"tags,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Cues       []string `json:"cues,omitempty"`
	Related    []string `json:"related,omitempty"`
	Created    string   `json:"created,omitempty"`
	Updated    string   `json:"updated,omitempty"`
}

// Get reads one memory by path — a file, or a file.md#section. This is what
// follows a retrieval hit: Recall returns paths, and this reads what they name.
func (c *Client) Get(ctx context.Context, path string, opts *RecallOptions) (*StoredMemory, error) {
	ns := c.namespace
	if opts != nil && opts.Namespace != "" {
		ns = opts.Namespace
	}
	q := url.Values{"path": {path}, "namespace": {ns}}
	var out StoredMemory
	if err := c.request(ctx, "GET", "/v1/memories?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Forget deletes memories by path.
//
// Asynchronous, like every other write. It unpublishes them from retrieval; it
// does not rewrite git history, so earlier revisions remain in the repository.
func (c *Client) Forget(ctx context.Context, paths []string, opts *WriteOptions) error {
	if len(paths) == 0 {
		return nil
	}
	ns := c.namespace
	if opts != nil && opts.Namespace != "" {
		ns = opts.Namespace
	}
	q := url.Values{"path": {strings.Join(paths, ",")}, "namespace": {ns}}
	return c.request(ctx, "DELETE", "/v1/memories?"+q.Encode(), nil, nil)
}

// TreeNode is one level of the hierarchical table of contents.
type TreeNode struct {
	Path     string     `json:"path"`
	Title    string     `json:"title,omitempty"`
	Kind     string     `json:"kind,omitempty"`
	Tier     string     `json:"tier,omitempty"`
	Summary  string     `json:"summary,omitempty"`
	Children []TreeNode `json:"children,omitempty"`
}

// TreeOptions shape one navigation call.
type TreeOptions struct {
	Namespace string
	// Path roots the tree; empty is the whole memory.
	Path string
	// Depth bounds how far down to descend. Default 2, maximum 8 — an
	// unbounded tree of a large memory is a second copy of it in one response.
	Depth int
}

// TreeResult is the table of contents rooted where it was asked for.
type TreeResult struct {
	Namespace string   `json:"namespace"`
	Depth     int      `json:"depth"`
	Tree      TreeNode `json:"tree"`
	Millis    int64    `json:"millis"`
}

// Tree returns the hierarchical table of contents: tier → topic → file →
// sections. This is the structure a reasoning-based navigator descends —
// reading summaries at each level and following the branch that matters —
// instead of guessing at the vocabulary a search would need.
func (c *Client) Tree(ctx context.Context, opts *TreeOptions) (*TreeResult, error) {
	o := TreeOptions{}
	if opts != nil {
		o = *opts
	}
	if o.Namespace == "" {
		o.Namespace = c.namespace
	}
	q := url.Values{"namespace": {o.Namespace}}
	if o.Path != "" {
		q.Set("path", o.Path)
	}
	if o.Depth > 0 {
		q.Set("depth", strconv.Itoa(o.Depth))
	}
	var out TreeResult
	if err := c.request(ctx, "GET", "/v1/tree?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Topic is one directory in the memory, with how much it holds.
type Topic struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	Parent   string `json:"parent"`
	Depth    int    `json:"depth"`
	Memories int    `json:"memories"`
}

// TopicsOptions filter the topic listing.
type TopicsOptions struct {
	Namespace string
	Tier      string // facts | incidents | rules | skills
	Prefix    string // only topics under this path
	Like      string // case-insensitive substring on the leaf name
	MaxDepth  int
	MinFiles  int
	Limit     int
}

// TopicsResult is one topic enumeration.
type TopicsResult struct {
	Namespace string  `json:"namespace"`
	Topics    []Topic `json:"topics"`
	Millis    int64   `json:"millis"`
}

// Topics enumerates the directory namespace with per-topic memory counts.
//
// Call it before filing a memory under a new topic. Topics are open-ended —
// nothing imposes a taxonomy — which is what lets a writer invent
// facts/databases next to an existing facts/database and split the subject in
// two. Checking first is how that is avoided.
func (c *Client) Topics(ctx context.Context, opts *TopicsOptions) (*TopicsResult, error) {
	o := TopicsOptions{}
	if opts != nil {
		o = *opts
	}
	if o.Namespace == "" {
		o.Namespace = c.namespace
	}
	q := url.Values{"namespace": {o.Namespace}}
	for k, v := range map[string]string{
		"tier": o.Tier, "prefix": o.Prefix, "like": o.Like,
	} {
		if v != "" {
			q.Set(k, v)
		}
	}
	for k, v := range map[string]int{
		"max_depth": o.MaxDepth, "min_files": o.MinFiles, "limit": o.Limit,
	} {
		if v > 0 {
			q.Set(k, strconv.Itoa(v))
		}
	}
	var out TopicsResult
	if err := c.request(ctx, "GET", "/v1/topics?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GraphNode is one memory in the relationship graph.
type GraphNode struct {
	Path  string `json:"path"`
	Tier  string `json:"tier"`
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
}

// GraphEdge is one declared relationship between two memories.
type GraphEdge struct {
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Label  string `json:"label,omitempty"`
	Origin string `json:"origin,omitempty"`
}

// GraphResult is the whole relationship graph of a namespace.
type GraphResult struct {
	Namespace string      `json:"namespace"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	Truncated bool        `json:"truncated"`
	Millis    int64       `json:"millis"`
}

// Graph returns the relationship graph: the second axis of the memory,
// orthogonal to the topic tree. Truncated reports that the graph was larger
// than one response, so a caller drawing it knows the picture is partial.
func (c *Client) Graph(ctx context.Context, opts *RecallOptions) (*GraphResult, error) {
	ns := c.namespace
	limit := 0
	if opts != nil {
		if opts.Namespace != "" {
			ns = opts.Namespace
		}
		limit = opts.Limit
	}
	q := url.Values{"namespace": {ns}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out GraphResult
	if err := c.request(ctx, "GET", "/v1/graph?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
