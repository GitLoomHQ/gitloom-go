# gitloom-go

Go SDK for [GitLoom](https://gitloom.cloud) — conversations that cannot outgrow
their context window, backed by a memory the model can consult.

Built as an extension of [karma](https://github.com/MelloB1989/karma): karma
speaks to every provider and reports token usage on each call; this package
supplies the conversation that remembers.

```bash
go get github.com/GitLoomHQ/gitloom-go/gitloom
```

## The whole loop in one call

```go
client := gitloom.New("") // reads GITLOOM_API_KEY

kai := ai.NewKarmaAI(ai.GPT4o, ai.OpenAI)
conv, _ := client.NewConversation(ctx, "chat-42", gitloom.ConversationOptions{
    Model:     "gpt-4o",
    Namespace: userID,
})
chat := gitloom.NewChat(kai, conv, "You are a helpful assistant.")

resp, _ := chat.Say(ctx, "What camera do I own?")
```

Every `Say`:
- retrieves relevant memories and hands them to the model as background,
- fits the history inside the model's window,
- stores both turns — with the provider's **real** token count,
- compacts on cadence (default every 5 exchanges) or when the window fills,
  and each compaction feeds the summarized turns to memory ingestion.

## Multimodal

karma's `AIMessage` carries images and files as URLs or data URLs. Data URLs
are uploaded to GitLoom transparently on append; the stored message references
the attachment, so the conversation replays with the media it ran with.

```go
chat.SayMessage(ctx, models.AIMessage{
    Role:    models.User,
    Message: "what's in this photo?",
    Images:  []string{"data:image/png;base64," + b64},
})
```

## Branching, edits, rewind

```go
conv.Rewind(ctx, 6)                             // fork after seq 6, switch to it
conv.Edit(ctx, 4, models.AIMessage{...})        // replace seq 4 on a new branch
conv.EditInPlace(ctx, 4, "[redacted]")          // destroy the original, for PII
conv.SetTitle(ctx, "Camera shopping")           // or let ingestion title it
```

Nothing is ever deleted by `Rewind` or `Edit` — the old line keeps its
messages and compactions. `EditInPlace` is the one deliberate exception.

## Memory, directly

```go
client.Remember(ctx, []gitloom.Turn{{Role: "user", Content: "I moved to Pune."}}, nil)

res, _ := client.Recall(ctx, "where do I live?", nil)
for _, h := range res.Hits {
    fmt.Println(h.Snippet, h.Scores.Arms, h.Provenance.When)
}
```

Every hit carries its evidence: per-arm scores, git history with the last
diff, and labelled relation snippets — the same shape every GitLoom surface
returns.

## Docs

https://docs.gitloom.cloud/documentation/go
