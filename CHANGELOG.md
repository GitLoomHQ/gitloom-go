# Changelog

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
