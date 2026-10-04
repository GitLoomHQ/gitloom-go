# gitloom-go

Go SDK for [GitLoom](https://gitloom.cloud) — a **drop-in replacement for
[karma](https://github.com/MelloB1989/karma)'s completion calls**. Same method
signatures; a `ChatId` makes the conversation manage itself: rolling context
window, memory retrieval, storage, compaction, titles.

```bash
go get github.com/GitLoomHQ/gitloom-go/gitloom
```

## Drop-in

```go
client := gitloom.New("") // reads GITLOOM_API_KEY

kai := gitloom.WrapKarma(
    ai.NewKarmaAI(ai.GPT4o, ai.OpenAI),   // the karma you already use
    client,
    gitloom.ConversationOptions{Model: "gpt-4o", Namespace: userID},
)

// karma's own signature — switch the receiver, change nothing else.
resp, _ := kai.ChatCompletion(models.AIChatHistory{
    ChatId:   "chat-42", // ← the only change
    Messages: []models.AIMessage{{Role: models.User, Message: "What camera do I own?"}},
})
```

That's the whole loop. Pass **only the new messages** — never append anything.
Behind the call: the stored conversation supplies the earlier turns, memory is
retrieved and injected as background, both turns are stored with karma's real
token count, compaction runs on cadence (default every 5 exchanges) or window
pressure, and every compaction feeds the summarized turns to memory ingestion.
No `ChatId` passes straight through. `ChatCompletionStream` works identically —
chunks stream through your callback, the exchange is stored at the end.

Compaction is your choice: `Summarize: gitloom.KarmaSummarizer(kai)` runs
locally on your model; `SummarizeServer: true` hands it to GitLoom's model, no
model wired into the client.

## Added features, on the same wrapper

```go
conv, _ := kai.Conversation(ctx, "chat-42")   // the SAME managed conversation

conv.Rewind(ctx, 6)                           // fork after seq 6
conv.Edit(ctx, 4, models.AIMessage{Role: models.User, Message: "ask differently"})
conv.EditInPlace(ctx, 4, "[redacted]")        // destroy the original (PII)
conv.SetTitle(ctx, "Camera shopping")
conv.Branches(ctx)
```

A rewind or edit here is what the next `ChatCompletion` continues from.

Data-URL images and files in `AIMessage` are uploaded transparently and stored
by reference.

## Recall, filtered and answered

```go
mem := kai.Memory()

// Ranked memories, no model call. Milliseconds.
res, _ := mem.Recall(ctx, "what camera do I own", &gitloom.RecallOptions{
    Tiers:    []string{"facts"},        // facts, incidents, rules, skills
    Paths:    []string{"facts/gear"},   // any directories
    Tags:     []string{"camera"},
    Since:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
    MinScore: 0.3,
    Limit:    8,
})
for _, m := range res.Memories {
    fmt.Printf("%.2f %s %v\n%s\n", m.Score, m.Path, m.Matched, m.Content)
}

// One text answer from a fast model over that retrieval …
ans, _ := mem.Answer(ctx, "what camera do I own", nil)
// … or let a stronger model search the memory itself with tools.
agentic, _ := mem.Answer(ctx, "which trip had the longest flight",
    &gitloom.RecallOptions{Mode: gitloom.ModeAgentic})
fmt.Println(agentic.Answer, agentic.Trace)
```

Each entry is one whole memory, not a scattering of its sections, and its
score is calibrated in `[0, 1]` — comparable across queries, so `MinScore`
means the same thing every time. `Detail: "full"` adds git history with the
last diff, labelled relation snippets and cues.

`Answer` meters as a chat rather than a read, and returns `ErrNoAnswer` rather
than an empty string when the model finds nothing to say.

### Listing without a question

```go
// Every memory tagged "lease" whose subject happened since March, newest first.
leases, _ := mem.Recall(ctx, "", &gitloom.RecallOptions{
    Tags:      []string{"lease"},
    TimeField: gitloom.TimeOccurred,
    Since:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
})
```

Leave the query empty and give at least one filter — `Tags`, `TagsAll`,
`Since`, `Until`, `Tiers` or `Paths` — to list every memory it matches, newest
first, each scoring 1. The mode must be raw and `Rank` unset. With neither a
query nor a filter, `Recall` returns `ErrNoQuery` without calling the API.

`TimeField` picks the time `Since` and `Until` bound, and orders the listing:
`TimeOccurred` (when the memory's subject happened), `TimeCreated`, or
`TimeUpdated` (the default). `TZ` is the IANA zone the server reads
offset-less times and bare dates in, for `since` and `until` sent as text.
`Since` and `Until` always go as UTC instants, so `TZ` never changes the range;
it is sent for parity, and is otherwise used only where the server reads
question dates by zone.

### The lane path

`Rank` retrieves on the lane path: lexical, cue, body, graph and time lanes each
search on their own, over the curated memories and the conversation turns, and
the time lane reads dates in the question ("last month", "in May"). `RankFused`
orders what they find by lane score; `RankJev` has a ranking model order it, and
sets `RankFallback` when it answers in lane order instead.

```go
res, _ := mem.Recall(ctx, "when did I stake the tomatoes",
    &gitloom.RecallOptions{Rank: gitloom.RankFused, MaxChars: 8000})
for _, m := range res.Memories {
    fmt.Println(m.Store, m.Said, m.Excerpted, m.Content)
}

ans, _ := mem.Answer(ctx, "what did I plant after the storm",
    &gitloom.RecallOptions{Rank: gitloom.RankJev, Model: gitloom.ModelSonnet})
```

Each memory then says which `Store` it came from (`"memory"`, or a word-for-word
conversation `"turn"`) and the days it was `Said`. `MaxChars` caps the memory
content returned: a memory that does not fit is cut to its opening sentence and
the sentences matching the question, and marked `Excerpted`. `Model` picks the
model that reads the memories in `ModeSummary` or `ModeAgentic`.

## Vocabulary and skills

```go
// Teach abbreviations and domain terms. A recall for "k8s" then also finds
// memories written "kubernetes", and the definition comes back as Defined.
mem.LearnTerms(ctx, []gitloom.Term{{
    Term:       "kubernetes",
    Aliases:    []string{"k8s", "kube"},
    Definition: "Container orchestration.",
}}, "")
mem.LookupTerm(ctx, "k8s", "")                                  // → Term, true, nil
mem.Vocabulary(ctx, &gitloom.VocabOptions{Like: "kube"})
mem.ForgetTerms(ctx, []string{"kubernetes"}, "")

// Store how things are done; find the skill that fits a task.
mem.StoreSkills(ctx, []gitloom.Skill{{
    Name:        "Deploy to production",
    Topic:       "ops",
    Description: "Ship a release.",
    Content:     "## Steps\n1. Tag the release.\n2. `make deploy ENV=prod`",
    Triggers:    []string{"how do I ship a release", "deploy to prod"},
}}, "")
skills, _ := mem.FindSkills(ctx, "release the new build", nil)
```

Skills are memories under the `skills/` tier, so a recall with
`Tiers: []string{"skills"}` reaches them too.

## Direct memory

`Remember` hands GitLoom a conversation and a model decides what is worth
keeping. These are the other half — for when the caller already knows what the
memory is and where it belongs: a migration from another store, or an agent
filing a conclusion it reasoned out itself.

```go
err := client.Write(ctx, []gitloom.NewMemory{{
    Path:       "facts/people/maya.md",
    Content:    "Maya rides a bicycle to work and prefers morning meetings.",
    Tags:       []string{"people", "colleague"},
    Confidence: 0.9,
    OccurredAt: gitloom.Day(2026, time.July, 19),   // what it's ABOUT, not now
    Cues:       []string{"how does Maya commute"},   // embedded for semantic search
    Related:    []string{"manager: facts/people/sam.md"},
}}, nil)
```

Send them in batches — one call is one commit round and one push, so batching
is where the cost goes.

```go
m, _   := client.Get(ctx, "facts/people/maya.md", nil)   // read one back
err     = client.Forget(ctx, []string{"facts/people/maya.md"}, nil)
tree, _ := client.Tree(ctx, &gitloom.TreeOptions{Path: "facts", Depth: 3})
tops, _ := client.Topics(ctx, &gitloom.TopicsOptions{Like: "databas"})
graph,_ := client.Graph(ctx, nil)
```

`Tree` is the table of contents a navigator descends instead of guessing at
search vocabulary. `Topics` is what you call before filing under a new topic,
so you don't invent `facts/databases` beside an existing `facts/database`.
`Forget` unpublishes a memory from retrieval; git keeps the history.

## Tags and when it happened

```go
// Every memory drawn from this conversation carries the tags, and is dated to
// when it happened rather than when it was sent.
turns := []gitloom.Turn{{Role: "user", Content: "We just checked in near Gion."}}
err = client.Remember(ctx, turns, &gitloom.RememberOptions{
    Tags:       []string{"trip", "#japan"},
    OccurredAt: gitloom.Date("2026-05-14T19:30"),   // read in Timezone
    Timezone:   "Asia/Kolkata",
})

err = client.Write(ctx, []gitloom.NewMemory{{
    Path:       "facts/travel/kyoto.md",
    Content:    "Stayed four nights in Kyoto, at a ryokan near Gion.",
    Tags:       []string{"trip", "#japan"},
    OccurredAt: gitloom.At(time.Date(2026, 5, 14, 14, 0, 0, 0, time.UTC)),
}}, nil)
```

`OccurredAt` takes `gitloom.At(t)` for an instant, sent as epoch seconds;
`gitloom.Day(2026, time.May, 14)` for a calendar day with no time of day; or
`gitloom.Date(s)` for text the server reads — a date, RFC 3339 with an offset,
or a datetime without one, read in `Timezone`. The `Date` fields it replaces
still work and are deprecated.

Tags are trimmed and lowercased, and hold letters, digits, spaces and
`- _ . : / # @` — up to 32 tags of 64 characters. One that breaks the rules
refuses the write with an `*APIError` whose `Code` is `invalid_tag` and whose
message names it, e.g. `memories[1].tags[0]`.

Recall reports them back:

```go
res, _ := client.Recall(ctx, "where did I stay in Kyoto", nil)
for _, m := range res.Memories {
    when := m.OccurredAt.Format(time.RFC3339)
    if m.OccurredPrecision == "day" {
        when = m.OccurredAt.Format(time.DateOnly)   // only the date is known
    }
    fmt.Println(m.Path, m.UserTags, when, m.OccurredSource, m.UpdatedAt)
}
```

`Tags` lists yours first, then the ones GitLoom inferred; `UserTags` holds
yours alone. `CreatedAt`, `UpdatedAt`, `OccurredAt` and `ExpiresAt` are
`time.Time` in UTC, zero when absent. `OccurredSource` says how the time is
known: `user` (you said), `extracted` (the memory names the day), `said` (when
its conversation happened) or `written`. The `Created` and `Updated` strings
are deprecated.

## Docs

https://docs.gitloom.cloud/documentation/go
