# Pull at 0.10.0 on a live installation: tests, adoption, and what the search hands over, October 2026

Status: **interim measurement, 2026-10-25.** Pull has been the mode of this installation for about fifteen hours and
0.10.0 for about five: the samples are small and the numbers below are a direction, not a rate. What they already
show: the release is sound (the plugin suite and the unit tests pass, the search answers in milliseconds, the five-fact
default costs half the characters of ten), and one defect in the base is measurable — a partly numbered index. A
second defect claimed here, four live facts missing from the index, was my own counting mistake and is corrected at
the end of the note.

[pull-hermes-2026-10](pull-hermes-2026-10.md) measured the first hours of pull at 0.8.0 on this same installation;
[pull-2026-10](pull-2026-10.md) measured pull against push end to end on another harness. This note is the third
point on that line: the same installation, the next two releases, the same stand, and the push era it replaced
counted from the logs that are still on disk.

## Setup

- memcue 0.10.0, binary and plugin. Since the 0.8.0 note, the releases that matter here: **0.9.0** made the search
  hand over five facts by default instead of ten (#374); **0.10.0** put versions, dates, ports and addresses into the
  index as exact terms with `MEMCUE_NUMBER_TERMS=0` as the opt-out (#378), and made the dream sweep what the previous
  dream saw (#379).
- The base: **383 live facts by id** under the store's own definition (`status = 'active'` and
  `review IN ('', 'provisional')`; a candidate waiting for approval is stored but deliberately not indexed), 385 if
  candidates are counted — the audit query of this run counted them, and that is the first of the two corrections at
  the end. 837 ids with their versions, 2 288 version rows, schema 5, 452 retired ids, 4 candidates, 0 duplicate
  groups, average fact 178 characters. Kinds: environment 166, pitfall 124, decision 22,
  rule 21, event 15, repo 15, procedure 7, other 2, untyped 13. Scopes: host 253, repo 59, service 35, global 25,
  unset 13.
- **20 real prompts** of the user: the newest from the session store, distinct, over 60 characters, no cron and no
  delegation wrappers. 17 of the 20 are about memory itself — the last week of this installation was a memory
  project — so the non-memory subset (3 prompts) is reported separately wherever it matters.
- One rater, the agent that answered them, grading every delivered fact from its text alone: `U` answers the prompt,
  `R` is on the topic without answering it, `N` is unrelated. One pass, so these are comparable with the 0.8.0 run
  and with nothing else.
- The stand: [search_stand.py](scripts/search_stand.py), top-10 per prompt, every search run with its own state
  directory so probes never land in the live `search.log`.

## Tests

| test | result |
|---|---|
| plugin suite (`tests/test_provider.py`, 65 cases) | all pass, 4.98 s |
| `go test ./internal/...` | 8 packages, all ok |
| live stand, 20 prompts × 3 repeats | 60 searches, 20/20 prompts got facts |
| latency, end to end (process start included) | p50 20.9 ms, p90 24.3 ms |
| latency, inside the binary (`search.log`) | p50 2.5 ms, p90 3.0 ms, max 3.5 ms |
| version-only prompts, number terms on / off | **10/10 vs 2/10** prompts return a fact naming the version |
| topic + version prompts | 23/23 own fact in the top five, both arms |

The two arms of the number-terms test are the same binary on the same base: `on` is the default of 0.10.0, `off`
passes `MEMCUE_NUMBER_TERMS=0`. Ten version-only prompts (`0.8.0`, `0.9.0`, `0.10.0`, `v0.22.2`, `go1.27.0`,
`2026-10-19`, `2.1.301`, `0.5.55`, `0.2.1-rc.2`, an address): with the knob **on**, every prompt returns facts and
18 of the 20 delivered facts contain the number that was asked for; with it **off**, eight of the ten return nothing
at all. The two that survive are the ones the lexer keeps as one word (`go1.27.0`, `0.2.1-rc.2`). Asked as
**topic + version**, the same prompts find their own fact 23 times out of 23 even with the knob off — the write-up of
#328 expected as much: the loss was in the push gates, and pull has no gates.

## Adoption

Pull has no hint to measure — the card is pushed, the rest must be asked for — so the denominator is sessions with a
user prompt and the numerator is sessions where `memcue_search` was called at least once.

| window | user sessions with a search | prompts | calls | cron sessions | subagent |
|---|---|---|---|---|---|
| since the 0.8.0 restart (about 15 h) | **6 of 6** | 11 | 14 (1.3 a prompt) | 1 of 6 (3 calls) | 0 of 1 |
| since the 0.10.0 restart (about 5 h) | **4 of 4** | 7 | 9 | 1 of 4 (3 calls) | 0 of 1 |

Eight session scopes, 21 calls in `search.log`. The honest reading: **six of six is not a rate.** Two of the six are
one-shot sessions that were told to call the tool (instrumented, not organic), and of the four organic sessions three
opened with a question about memory itself — the easiest case there is. What the window does show is that the
instruction in the session-start note is read and acted on when the prompt looks like memory, and that a cron session
can search too (three calls in a cron run), which the card and the note do not cover.

Push needed no adoption at all: it ran on every prompt. In the last three days before the switch, **54 of 93 hook
turns (58%)** delivered at least one fact, and the rest of the turns were paid for anyway — a model call each.

## What the search returns

Top-k sweeps over the same 20 prompts; every k is a prefix of the same top-10, so the rows are one labelling:

| top-k | prompts with a useful fact | useful | related | noise | useful share | noise share | chars | ~tokens |
|---|---|---|---|---|---|---|---|---|
| 1 | 7/20 | 7 | 5 | 8 | 35% | 40% | 3 546 | 1 108 |
| 2 | 9/20 | 14 | 14 | 12 | 35% | 30% | 7 082 | 2 213 |
| 3 | 12/20 | 19 | 25 | 16 | 32% | 27% | 10 818 | 3 381 |
| **5** (the default) | **13/20** | **23** | **45** | **32** | **23%** | **32%** | **18 271** | **5 710** |
| 10 (the 0.8.0 default) | 15/20 | 32 | 84 | 84 | 16% | 42% | 37 719 | 11 787 |

The five-fact default is the right point on this base. Against the 0.8.0 run at the same k, coverage is one prompt
lower (13 against 14), the useful share one point higher (23% against 22%), the noise share **16 points lower**
(32% against 48%) and the characters 3% lower — a base that grew more precise as vet and dream swept it, not a
search that changed. Of the 100 facts the ten-fact default adds (ranks six to ten), 9 are useful, 52 are noise and
39 are related: nine useful facts for 19 448 more characters.

Composition is the caveat. Five prompts have no useful fact in the top ten, and four of them are ordinary
operational questions — disk space, token rotation, how numbers are tokenized, where an origin comes from — answered
with host lore and memory changelog. On the three prompts that are not about memory at all, coverage is 2 of 3, and
the useful share at k=5 is **13%** against 24% on the memory ones.

## Push and pull, the same installation, side by side

| | push (0.7.0, 2026-10-22…24) | pull (0.10.0, 2026-10-25) |
|---|---|---|
| turns that got anything | 54 of 93 (58%) | 21 searches in 8 sessions, 1.3 a prompt |
| facts per delivery | 1.72 (93 facts over 54 turns) | 5 (the default; 8 when the caller asks) |
| median usefulness of what was delivered | coverage 0.26 per fact | 23% useful / 32% noise at k=5 |
| latency of the mechanism | p50 386 ms, p90 891 ms, max 1 890 ms | p50 2.5 ms in the binary, 20.9 ms with the process start |
| paid model calls | one per turn, whether or not a fact came | none |
| context cost | ~100 tokens a turn for the hint, plus the card | ~285 tokens a search, nothing on turns that do not search |
| the card, per session start | 34 builds, median 1 157 chars, max 2 478 (13 facts) | same |
| a token of a delivered fact in the answer (`usage.log`) | 24 of 92 (26%) | 48 of 123 (39%) |
| extraction, facts per run | 145 over 67 runs (2.2), 15 runs with none | 18 over 12 runs (1.5), 3 runs with none |

The end-to-end answer quality of these two modes was measured on another harness and is the only number of its kind:
**1.08 pull against 0.89 push**, 240 runs of Opus on 40 prompts ([pull-2026-10](pull-2026-10.md)). Nothing in this
window contradicts it, and nothing here repeats it: a Hermes end-to-end run on these prompts is not done yet.

The `usage.log` row is a proxy and should be read as one. The plugin counts a fact as used when a token of it — an
identifier, a path, a version — appears in the answer, so a fact can be counted twice or counted for a token the
answer would have contained anyway. The direction (three fifths of what the model asked for is read, against a
quarter of what was pushed) is nonetheless the interesting part: the model asked for what it used.

## What does not work yet

1. **Nothing is missing from the index.** This item claimed four live facts absent from `fact_index`. They were four
   candidates waiting for approval: `internal/store/store.go` defines a live fact as `status = 'active'` with
   `review IN ('', 'provisional')`, and a candidate is stored but deliberately not indexed. At the time of the run
   `ensureIndex` required 381 live facts and `fact_index` held 381 rows — a match, not a gap. The four were counted as
   live by an audit query that filtered on `status` alone (381 + 4 = 385, the number in the table above); the same
   query on the same base a few hours later gives 383 live facts, 383 index rows, 0 missing, 0 stale, in both
   directions. The lesson is in the correction at the end of the note.
2. **The index is only partly numbered.** 47 live facts carry a version or a date; 43 of them have an `nm` term in
   `fact_index` and 4 do not. Three of the four were indexed before 0.10.0 — the index is computed when a fact is
   written, and the release cannot go back — and the fourth names an address after an `@`, which the lexer's
   boundary class does not accept. A version-only prompt returns a fact naming that version in **10 of 10** cases
   where the term exists and in **0 of 4** where it does not, so those 4 facts are invisible to exactly the query the
   release was built for. There is no `reindex` verb, and `numbersMissing` only notices an index with no numeric
   terms at all, so a partial one is repaired only by hand.
3. **Precision still falls with k, and five is where it stops.** 32% noise at five facts, 42% at ten; nothing below
   the fifth fact earns its characters. The default is right; the ten-fact callers are not, and the tool's own
   description invites a caller to raise the limit.
4. **Adoption is not measured yet.** Six of six sessions searched, and every one of them was about memory. The number
   that matters — does the model search when the prompt does not look like a memory question — has no sample. On the
   three prompts here that are not about memory, coverage is 2 of 3.
5. **Fourteen live facts still name the machinery 0.8.0 removed** — the recall verb, the Jev judge, the reranker,
   embeddings, `MAX_FACTS`. Three of them phrase it as the working design (a scope-filter measurement, a request to
   repeat the recall measurement, the MaxFacts report); the rest are dated history or the vet judge, which survives.
   A base this size can afford the cleanup, and the cleanup is the difference between an agent that is told how
   memory works and one that is told how it worked.

## What follows

Two of these are worth a task each: the numbering gap — a rebuild path and a repair that notices a partial index
(#383) — and the ten-fact default of the CLI, which disagrees with the five the plugins use (#384). The counting
mistake of item 1 is closed as invalid (#382) and its cause is fixed in the audit scripts. The adoption number needs
weeks and cannot be filed.

- The five-fact default is confirmed on live data: 13 of 20 prompts against 14 at ten facts, for half the characters.
- Number terms are worth their default: 10 of 10 against 2 of 10 on version-only prompts.
- Pull costs nothing on turns that do not search and 20.9 ms on the ones that do, against 386 ms a turn and a paid
  call in push.

## Correction, same day

The note above first reported four live facts missing from `fact_index` and filed #382 for it. That was wrong, and
the way it went wrong is worth keeping: the counts were taken with `status = 'active'` and the newest version per id,
but the store's own definition of a live fact also requires `review IN ('', 'provisional')`. A candidate — a fact the
extraction stored and the vet has not yet approved — is written to `facts` and deliberately kept out of `fact_index`.
The four facts were the four candidates, and `ensureIndex` was right all along.

Recount on the same base: 387 ids with `status = 'active'` at their newest version, 4 of them candidates, **383 live
facts by the store's definition, 383 rows in `fact_index`, none missing, none stale**. #382 is closed as invalid.

What the correction changes here: the base line of the setup said 385 live facts where the store counts 383, and item
2 of "what does not work yet" said 19 of 72 numbered facts without an `nm` term where the correct figure is 4 of 47 —
the earlier count mixed candidates and history rows into the denominator as well. The top-k table is untouched: it
comes from labelled prompts, not from the base count.

The defect of item 2 survives the correction, and it is the reason #383 stays open: a base indexed before 0.10.0
keeps word-only stems for the facts it already had, `numbersMissing` only notices an index with *no* numeric terms at
all, and a version-only prompt on such a fact returns nothing. The audit scripts that produced the wrong count are
fixed in the same pass, because the mistake was theirs and not the base's.
