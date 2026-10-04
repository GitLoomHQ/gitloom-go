# Changelog

## v0.6.0 — unreleased

- **Tags and when it happened, on every write.** `NewMemory` and
  `RememberOptions` take `OccurredAt`, a `When`: `At(t)` for an instant, sent
  as epoch seconds; `Unix(sec)`; `Day(y, m, d)` for a calendar day; or
  `Date(s)` for text the server reads. `RememberOptions` also takes `Tags`,
  applied to every memory drawn from the conversation, and `Timezone`, which
  `WriteOptions` takes too. The `Date` fields still work and are deprecated.
- **Recall without a question.** An empty query with any filter — `Tags`,
  `TagsAll`, `Since`, `Until`, `Tiers` or `Paths` — lists every memory it
  matches, newest first, each scoring 1. With neither, `Recall` returns
  `ErrNoQuery` without calling the API, where it used to come back as a 400.
- **`RecallOptions.TimeField` and `TZ`.** `TimeField` (`TimeOccurred`,
  `TimeCreated` or `TimeUpdated`) picks the time `Since` and `Until` bound and
  orders a listing; `TZ` is the zone dates in a question are read in.
- **Memory times.** A `Memory` carries `UserTags`, and `CreatedAt`,
  `UpdatedAt`, `OccurredAt` and `ExpiresAt` as `time.Time`, zero when absent,
  with `OccurredSource` and `OccurredPrecision`. The `Created` and `Updated`
  strings remain, deprecated. A `Memory` marshals back to the API's shape, so
  a result kept as JSON reads back.
- **`RecallOptions` takes `Rank`, `MaxChars` and `Model`**, for `Recall` and
  `Answer` alike. `Rank` (`RankFused` or `RankJev`) retrieves on the lane
  path, which also reaches conversation turns and the dates in a question;
  `MaxChars` caps the memory content returned; `Model` (`ModelHaiku` or
  `ModelSonnet`) picks the reader in `ModeSummary` or `ModeAgentic`. None is
  sent unless set, so existing calls are unchanged.
- **Lane-path fields.** A `Memory` carries `Store`, `Said` and `Excerpted`;
  the result carries `Rank` and `RankFallback`, and `Timings` the lane path's
  `EmbedMillis`, `LanesMillis`, `RankMillis` and per-lane `Lane`.

## v0.5.0 — 2026-09-30

- **Direct memory primitives.** `Write`, `Get`, `Forget`, `Tree`, `Topics`
  and `Graph` — for a caller that already knows what a memory is and where it
  belongs, where `Remember` has a model decide. They lived on an unmerged
  branch until now, so no release had both them and the current `Recall`.
- **`NewMemory`** is what `Write` takes. It is the write-side twin of the
  `Memory` that `Recall` returns; the two branches had each named their type
  `Memory`, and the released one keeps the name.
- **`RecallOptions.NoProvenance` and `NoRelations`** skip the per-memory git
  history walk and the neighbour snippets. Provenance is computed by default
  and dominates latency once memories have history — 3.29s against 0.40s on
  one production namespace — so turn it off for anything that will not show
  a citation.

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
