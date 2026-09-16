package gitloom

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Vocabulary and skills: the two kinds of knowledge a namespace holds beside
// its memories. Both are written through the same queue as every other
// mutation, so both are asynchronous — they land within seconds.

// Term is one vocabulary entry: a canonical form, the surface forms that mean
// the same thing, and what it means.
type Term struct {
	Path       string   `json:"path,omitempty"`
	Term       string   `json:"term"`
	Aliases    []string `json:"aliases,omitempty"`
	Definition string   `json:"definition,omitempty"`
}

// Accepted is the acknowledgement every asynchronous write returns.
type Accepted struct {
	ID        string   `json:"id"`
	Namespace string   `json:"namespace"`
	Status    string   `json:"status"`
	Paths     []string `json:"paths,omitempty"`
}

// LearnTerms teaches a namespace terms and their aliases. Once learned, a
// query for any surface form also finds memories written with another, and a
// query mentioning a defined term gets the definition back on Defined.
//
// Learning a term the namespace already has rewrites it.
func (c *Client) LearnTerms(ctx context.Context, terms []Term, namespace string) (*Accepted, error) {
	if namespace == "" {
		namespace = c.namespace
	}
	var out Accepted
	body := map[string]any{"namespace": namespace, "terms": terms}
	if err := c.request(ctx, "POST", "/v1/vocab", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VocabOptions filter a vocabulary listing.
type VocabOptions struct {
	Namespace string
	Like      string
	Limit     int
}

// Vocabulary lists a namespace's learned terms.
func (c *Client) Vocabulary(ctx context.Context, opts *VocabOptions) ([]Term, error) {
	o := VocabOptions{}
	if opts != nil {
		o = *opts
	}
	if o.Namespace == "" {
		o.Namespace = c.namespace
	}
	q := url.Values{"namespace": {o.Namespace}}
	if o.Like != "" {
		q.Set("like", o.Like)
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	var out struct {
		Terms []Term `json:"terms"`
	}
	if err := c.request(ctx, "GET", "/v1/vocab?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Terms, nil
}

// LookupTerm resolves any surface form to its term. The bool is false when the
// word is unknown, which is not an error.
func (c *Client) LookupTerm(ctx context.Context, word, namespace string) (*Term, bool, error) {
	if namespace == "" {
		namespace = c.namespace
	}
	q := url.Values{"namespace": {namespace}, "word": {word}}
	var out struct {
		Found bool  `json:"found"`
		Term  *Term `json:"term"`
	}
	if err := c.request(ctx, "GET", "/v1/vocab?"+q.Encode(), nil, &out); err != nil {
		return nil, false, err
	}
	if !out.Found || out.Term == nil {
		return nil, false, nil
	}
	return out.Term, true, nil
}

// ForgetTerms removes terms by canonical form. A term never learned is
// skipped rather than failing the batch.
func (c *Client) ForgetTerms(ctx context.Context, terms []string, namespace string) (*Accepted, error) {
	if namespace == "" {
		namespace = c.namespace
	}
	q := url.Values{"namespace": {namespace}, "term": {strings.Join(terms, ",")}}
	var out Accepted
	if err := c.request(ctx, "DELETE", "/v1/vocab?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Skill is procedural know-how: what something is called, when it applies, and
// how to do it. Stored as an ordinary memory under the skills tier, so Recall
// with Tiers: []string{"skills"} reaches it too.
type Skill struct {
	Path        string `json:"path,omitempty"`
	Topic       string `json:"topic,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Content is the procedure, as markdown; ## headings become sections.
	Content string   `json:"content,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	// Triggers are how someone would ask for this skill. They become its
	// retrieval cues, so write them as the question rather than the topic:
	// "how do I ship a release", not "deployment". Empty falls back to the
	// name and description.
	Triggers   []string `json:"triggers,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Date       string   `json:"date,omitempty"`

	// Set on results, not on input.
	Score   float64  `json:"score,omitempty"`
	Matched []string `json:"matched,omitempty"`
	Created string   `json:"created,omitempty"`
	Updated string   `json:"updated,omitempty"`
}

// StoreSkills writes skills. Each becomes a memory at
// skills/<topic>/<slug>.md; the returned Paths say where.
func (c *Client) StoreSkills(ctx context.Context, skills []Skill, namespace string) (*Accepted, error) {
	if namespace == "" {
		namespace = c.namespace
	}
	var out Accepted
	body := map[string]any{"namespace": namespace, "skills": skills}
	if err := c.request(ctx, "POST", "/v1/skills", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SkillOptions narrow a skill search.
type SkillOptions struct {
	Namespace string
	// Paths restricts to topics under skills/, e.g. "ops".
	Paths []string
	Tags  []string
	Limit int
}

// FindSkills returns the skills that bear on a task, best first. An empty task
// lists every skill instead.
func (c *Client) FindSkills(ctx context.Context, task string, opts *SkillOptions) ([]Skill, error) {
	o := SkillOptions{}
	if opts != nil {
		o = *opts
	}
	if o.Namespace == "" {
		o.Namespace = c.namespace
	}
	q := url.Values{"namespace": {o.Namespace}}
	if task != "" {
		q.Set("q", task)
	}
	if len(o.Paths) > 0 {
		q.Set("paths", strings.Join(o.Paths, ","))
	}
	if len(o.Tags) > 0 {
		q.Set("tags", strings.Join(o.Tags, ","))
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	var out struct {
		Skills []Skill `json:"skills"`
	}
	if err := c.request(ctx, "GET", "/v1/skills?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Skills, nil
}
