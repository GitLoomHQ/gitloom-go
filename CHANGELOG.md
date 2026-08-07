# Changelog

## v0.3.0 — 2026-08-08

- **Streaming drop-in.** ChatCompletionStream mirrors karma's — chunks pass
  through your callback, the managed conversation stores the exchange at the
  end.
- **Added features on the wrapper.** kai.Conversation(ctx, chatID) exposes
  rewind/edit/redaction/titles/branches on the same managed conversation the
  completions flow through; kai.Memory() for direct recall/remember.
- Documentation leads with the drop-in only; the manual loop is gone.

## v0.2.0 — 2026-08-08

- **Drop-in mode.** `gitloom.WrapKarma(kai, client, opts)` has karma's own
  `ChatCompletion` signature — switch the receiver and change nothing else. A
  history with `ChatId` set becomes a managed conversation: pass only the new
  messages, the wrapper supplies the remembered window and memory context, and
  both turns are stored with karma's real token count. No `ChatId` passes
  straight through.
- **Server-side compaction.** `SummarizeServer: true` hands summarization to
  GitLoom's own model; a local `Summarize` function remains the
  private-by-default choice.

## v0.1.0 — 2026-08-08

- Conversations on karma with usage-timed compaction, branching, edits,
  redaction, titles, transparent media upload, and evidenced memory recall.
