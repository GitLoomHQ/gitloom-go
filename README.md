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
