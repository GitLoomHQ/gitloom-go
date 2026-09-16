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

## Docs

https://docs.gitloom.cloud/documentation/go
