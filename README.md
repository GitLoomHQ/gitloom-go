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

Direct memory: `kai.Memory().Recall(ctx, ...)` / `.Remember(ctx, ...)` — every
hit carries per-arm scores, git history with the last diff, and relation
snippets. Data-URL images and files in `AIMessage` are uploaded transparently
and stored by reference.

## Docs

https://docs.gitloom.cloud/documentation/go

## Direct memory

`Remember` hands GitLoom a conversation and a model decides what is worth
keeping. These are the other half — for when the caller already knows what the
memory is and where it belongs: a migration from another store, or an agent
filing a conclusion it reasoned out itself.

```go
err := client.Write(ctx, []gitloom.Memory{{
    Path:       "facts/people/maya.md",
    Content:    "Maya rides a bicycle to work and prefers morning meetings.",
    Tags:       []string{"people", "colleague"},
    Confidence: 0.9,
    Date:       "2026-07-19",                       // what it's ABOUT, not now
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
