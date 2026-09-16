# Changelog

## v0.4.0 — 2026-09-16

- **Recall returns memories.** `RecallResult.Hits` becomes `Memories`, and
  each entry is one whole memory with its `Content` rather than a scattering
  of its sections. `Matched` names the arms that found it, `Sections` the
  headings that matched, `Via` what pulled in a neighbour.
- **Scores mean something.** `Score` is calibrated in `[0, 1]` and comparable
  across queries, replacing a fused rank that only ordered one response.
  `Scores.Coverage` says how much of the query a memory accounted for.
- **`RecallOptions`** carries filters — `Tiers`, `Paths`, `Tags`, `TagsAll`,
  `Since`, `Until`, `MinScore`, `NoContext`, `Detail`, `IncludeExpired` —
  applied inside every retrieval arm server-side rather than after the fact.
- **`Answer`** returns one text answer: a fast model over a retrieval, or with
  `ModeAgentic` a stronger model that searches the memory itself with tools
  and returns its `Trace`. Returns `ErrNoAnswer` rather than an empty string.
  Both meter as chats.
- **Vocabulary** — `LearnTerms`, `Vocabulary`, `LookupTerm`, `ForgetTerms`. A
  learned alias makes a query for any surface form find memories written with
  another; matched definitions come back as `Defined`.
- **Skills** — `StoreSkills` and `FindSkills`, stored as memories under the
  `skills/` tier.
- `Candidates`, `FilteredOut` and `Timings` report what retrieval did.


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
