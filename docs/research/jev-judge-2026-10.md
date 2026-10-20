# Jev as the retriever, October 2026

Measurements of 2026-10-21 on one private base of 270 active facts, mostly Russian, about 300 characters
on average. The document gives aggregate numbers only: the base holds host names, paths and private
repositories. Scripts are in [scripts/](scripts/); each section names the one that produced its numbers.

[The dream triage research](dream-triage-2026-10.md) asked whether Jev can judge the candidate pairs that
recall already produced. This document asks a different question: can Jev replace the step that produces
them, so a base needs no index at all.

## Question

`memcue` finds candidates with FTS5 terms weighted by IDF, and the vector branch (#48, PR #49) adds an
embedding index next to it. Both need something on our side to rank the base. Jev takes the base itself in
`state` and returns one typed answer per question, so the candidate step can disappear:

- dedup: "does stored fact [k] state the same claim as the new fact", one `noul` per fact;
- recall: "would stored fact [k] help answer the prompt", one `noul` per fact.

The question is what that costs, where it stops, and whether one `choice` over the base is enough.

## What Jev is and is not

- Jev is a decision model, not an embedder. It returns typed answers with probabilities: `noul` (probability
  of yes), `choice`, `score`, `box`. There is no embeddings endpoint, and
  `POST /api/v1/chat/completions` rejects the model by design ("is a decisions model and cannot be used with
  the chat/completions endpoint"). No vector can be precomputed for a fact, so no index can be built.
- Endpoints: `POST https://openrouter.ai/api/alpha/decisions` on OpenRouter, or
  `POST https://api.typesafe.ai/v1/systemone` (same request body) on TypeSafe directly. On OpenRouter the
  path `/api/v1/alpha/decisions` returns 404 — the Decisions path lives outside the `/api/v1` prefix.
- Price: $0.042 per million input tokens, output tokens are free.
- Limits: a `choice` or `score` dictionary holds at most 255 entries; requests over it fail with HTTP 422
  `Dictionary should have at most 255 items after validation`. Context is 64k per request and 32k per
  question, raised to 262k both by the `TypeSafe-Danger-Zone-Long-Context: true` header. In this measurement
  a 41k-token request went through without the header.
- `jev-latest` moved to 1.14 on 2026-10-21; 1.14 shipped as `jev-preview` and 1.13 stays available.

## Experiments

### 1. Hand-picked pairs, Jev against bge-m3 cosine ([jev_pairs.py](scripts/jev_pairs.py))

Nine pairs taken from the base on purpose: three duplicates, three "same subject, different claim", two
unrelated, and one duplicate across languages (Russian fact against an English rewrite). One `score` over
three classes and one `noul` per pair, one request, 2553 input tokens, $0.000107, 0.94 s. Embeddings of the
same 18 texts: `baai/bge-m3`, 1.49 s.

| Pair | Gold | P(same) | P(related) | P(unrelated) | P(dup, `noul`) | Cosine |
|---|---|---|---|---|---|---|
| duplicate, same command missing | same | 0.91 | 0.09 | 0.00 | 0.74 | 0.850 |
| duplicate, CLI not authenticated | same | 0.94 | 0.06 | 0.00 | 0.96 | 0.849 |
| duplicate, requirement worded twice | same | 0.99 | 0.01 | 0.00 | 0.97 | 0.865 |
| narrower subject (one service of two) | related | 0.33 | 0.66 | 0.01 | 0.42 | 0.890 |
| same subject, different claim (provider) | related | 0.00 | 0.89 | 0.11 | 0.04 | 0.630 |
| same subject, different claim (scope) | related | 0.00 | 0.98 | 0.02 | 0.09 | 0.715 |
| unrelated | unrelated | 0.00 | 0.01 | 0.99 | 0.02 | 0.455 |
| unrelated | unrelated | 0.02 | 0.04 | 0.94 | 0.03 | 0.502 |
| duplicate, Russian against English | same | 1.00 | 0.00 | 0.00 | 0.98 | 0.783 |

Cosine does not order these classes: the highest cosine of the nine (0.890) belongs to the pair Jev calls
"same subject, different claim", and the cross-language duplicate sits at 0.783, below two of the
"different" pairs. Jev puts every duplicate above 0.91 of "same" and every other pair below 0.34, and
separates the cross-language duplicate at 0.98 of `noul`. That reproduces the AUC table of the dream
research (0.965 for `noul` "same fact" against 0.867 for cosine) on nine pairs and with one `score` instead
of two `noul`.

### 2. Duplicate check over the whole base ([jev_whole_store.py](scripts/jev_whole_store.py))

A rewritten copy of one fact went in as the new fact, all 270 active facts went into `state`, one `noul`
per fact. 1.29 s, 41 384 input tokens, $0.001738.

| Rank | P | Fact |
|---|---|---|
| 1 | 0.960 | the fact the rewrite came from |
| 2 | 0.940 | second copy of the same fact |
| 3 | 0.940 | third copy of the same fact |
| 4 | 0.570 | the same missing command, cited as an error message |
| 5 | 0.110 | a different fact about the same environment variable |

The base genuinely holds three copies of this one fact (written by extraction and by the memory mirror on
different days), and all three are at the top. The fourth is the same knowledge in another shape, which a
human would probably retire too. Nothing else rises above 0.11, so a threshold in 0.5–0.9 sends two or
three facts to the model and no noise.

### 3. Recall over the whole base ([jev_whole_store.py](scripts/jev_whole_store.py))

Two prompts, every fact asked whether it would help answer. 1.23 s and 1.31 s, 37 582 and 37 569 input
tokens, $0.001578 each.

- "why does Claude Code authentication fail on the host": 0.95 and 0.93 for the two facts that name the
  error, 0.93 for the version fact, 0.86 for the fact about extraction failing through the same CLI.
- "how to look into the memcue base when there is no sqlite3": 0.97, 0.96, 0.96 for the three copies of that
  fact, 0.93 for the version that quotes the shell error.

Both prompts returned the right facts at the top, and both returned several duplicates of them, which is
what a `top-2` policy would waste a slot on. How many facts pass 0.5 depends on the prompt: 8 for the first,
25 for the second. One reader checked these two, no blind labels.

### 4. One `choice` over the base is not a duplicate check

The same new fact against 255 newest facts, first as one `choice` with 255 options, then as 255 `noul`.

| Shape | Time | Tokens | Cost | First three results |
|---|---|---|---|---|
| `choice`, 255 options | 1.20 s | 35 713 | $0.001500 | 0.90, 0.08, 0.02 — one duplicate found, the others suppressed |
| `noul` per fact, 255 facts | 1.26 s | 39 089 | $0.001642 | 0.95, 0.95, 0.94 — all three duplicates |

A `choice` answers "which one is closest": its probabilities sum to 1, so the second and third true
duplicates lose their share to the winner and fall to 0.08. Retiring duplicates needs every copy flagged,
so the shape is one `noul` per candidate. This is the same conclusion experiment 3 of the dream research
reached from the other side.

The 255 limit is hard: with all 270 facts the request fails with HTTP 422 before the model sees it.

### 5. Cost and ceiling

Measured 153 input tokens per fact for dedup and 139 for recall, questions included.

| Facts | Input tokens | Cost per check | Fits 64k | Fits 262k |
|---|---|---|---|---|
| 270 (measured) | 41 384 | $0.0017 | yes | yes |
| 500 | 76 637 | $0.0032 | no | yes |
| 1 000 | 153 274 | $0.0064 | no | yes |
| 1 700 | 260 566 | $0.0109 | no | yes |
| 2 000 | 306 548 | $0.0129 | no | no |

So a base of about 400 facts fits a default request and one of about 1 700 fits the long-context one. The
dream research's base has 2 500 facts: it cannot be checked in a single request, and would have to be
split, which turns one call into several and loses the global comparison. Money is not the limit at any
size; context is.

Latency stays around 1.2–1.3 s in all shapes above, 0.9 s for the nine pairs. It does not grow visibly
between 60 and 255 facts.

### 6. A `score` against a yes/no question, on labelled candidates ([jev_shapes.py](scripts/jev_shapes.py))

The threshold of experiment 2 was set on a rewritten probe: a near-verbatim copy of a fact scores 0.94–0.96 of
`noul`, which is not what extraction produces. Real paraphrases of an approved fact land near 0.4–0.6, so that
threshold would never fire.

Candidates: all 41 in the queue of the same base, 235 approved facts in `state`. Labels: the review model's own
verdict on those candidates from `memcue vet -dry-run -json` (15 drop, 26 keep), which is an opinion, not truth. Each
candidate was asked about every approved fact twice, as one `noul` and as one `score` over three levels; the strongest
answer of each shape is what a threshold would see. Two runs, $0.07 and $0.09.

| Shape | Run | AUC | ≥ 0.3: dup / false | ≥ 0.5: dup / false | ≥ 0.7: dup / false |
|---|---|---|---|---|---|
| `noul` "same claim" | 1 | 0.815 | 11 / 2 | 6 / 1 | 4 / 0 |
| `noul` "same claim" | 2 | 0.822 | 10 / 2 | 8 / 1 | 2 / 0 |
| `score` P(same claim) | 1 | 0.835 | 12 / 5 | 11 / 1 | 8 / 0 |
| `score` P(same claim) | 2 | 0.877 | 11 / 0 | 8 / 0 | 4 / 0 |

The score shape wins on AUC in both runs and its middle level says why a candidate stayed: "same subject, a
different or stale claim" is what the chat judge then has to read. Both shapes are noisier between runs than the
separation suggests: the same candidate moved by 0.1–0.2, and run 2 retired one duplicate fewer at 0.5 than run 1.
At 0.5 the score shape retires 8–11 of 15 duplicates and 0–1 of 26 facts worth keeping; at 0.3 it retires one or two
of the keeps. 0.5 is the default, and `MEMCUE_JUDGE_MIN` is how a base tunes it.

## Where the judge belongs

Three places differ in how often they run, not in what they ask.

| Place | Runs | Budget | Verdict |
|---|---|---|---|
| `vet` / candidate queue | once per extraction, a few candidates | seconds are free | yes — one `noul` per candidate, all duplicates flagged |
| `memcue search` on demand | per user command | a second is acceptable | yes — replaces the index for small bases |
| recall hook (`prefetch`) | every turn | p50 3.5 ms today | no — 1.2 s per turn, linear in the base, and an external call on the hot path is the failure that retired holographic |

The hook is the one place that must not carry it: holographic already ran an embeddings call per turn
through OpenRouter, timed out, and fell back to "words only for 60 s" before it was replaced by memcue
(recorded in the plugin's own history). If the judge is used for recall at all, it has to be asynchronous
or behind an explicit command.

## Mistakes in the method

- Nine pairs were chosen by the person who knew the answer, so experiment 1 shows the shape of the
  separation, not its rate. The rate is the AUC table of the dream research, on 300 blind pairs.
- The recall check is two prompts read by one reader. It shows that whole-base judging returns sensible top
  facts, not what a `top-2` policy would score on the labelled set.
- The ground truth of experiment 2 is a manual look at the base: three copies of one fact, and a fourth the
  author judged to be the same knowledge. There is no labelled sample behind it.
- Tokens per fact are extrapolated from 270 facts of one length; longer facts move the ceiling down.
- Each shape was run once or twice. The jitter is visible but not quantified: the same near-duplicate pair
  scored 0.23 and 0.74 of `noul` in two runs, and a 60-option `choice` flipped its winner when the
  long-context header was added. Ranking is stable; a single threshold near the top is not.
- 1.14 is `jev-preview`. Its release notes name multilingual decisions as the largest gain, and the
  cross-language pair above agrees, but the model can change under the same name until `jev-latest` moves.

## Conclusions

- **A decision model is not a vector store.** Jev cannot emit vectors, so nothing can be precomputed and no
  index can be searched; every operation is an inference over the base. For 270 facts that is 1.3 s and
  $0.0017 per operation, which is fine for `vet` and for a search command.
- **It is a better duplicate judge than cosine** on our material: 0.94–0.96 against 0.11 for the next
  candidate, where cosine ranks a "same subject" pair (0.89) above every true duplicate but one (0.85–0.87).
- **One question per fact, not one `choice`.** A `choice` returns the closest fact and suppresses the other
  copies, and it stops working at 256 options. A `score` over three levels beats a `noul` on labelled candidates
  (AUC 0.877 against 0.822 in the second run) and says whether a candidate stayed because it is a neighbour.
- **The ceiling is context, not money**: about 400 facts per default request, 1 700 with the long-context
  header, 153 tokens per fact.
- **Keep it out of the recall hook.** A 1.2 s external call on every turn is the shape that already failed
  here once.
- The next step is the judge behind a flag in `vet`, where the base is small, the call happens once per
  session, and the verdict needs probabilities rather than a chat answer.

## Reproducing

The key and the pairs file come from outside the repository; neither is committed.

```bash
python3 -I docs/research/scripts/jev_pairs.py --key-file KEY --pairs PAIRS.json --json out.json
python3 -I docs/research/scripts/jev_whole_store.py --facts FACTS.db --key-file KEY \
    --dedup "the rewritten fact" --top 10
python3 -I docs/research/scripts/jev_whole_store.py --facts FACTS.db --key-file KEY \
    --recall "a real prompt" --top 10
python3 -I docs/research/scripts/jev_whole_store.py --facts FACTS.db --key-file KEY \
    --dedup "the rewritten fact" --choice --limit 255   # to see the choice/limit behaviour
python3 -I docs/research/scripts/jev_shapes.py --facts FACTS.db --key-file KEY --labels labels.json
```

`jev_shapes.py` takes its labels from a `memcue vet -dry-run -json` run; the header of the script says how to cut them
out of it.

`--long-context` adds the danger-zone header. The scripts print the top rows and write the full answers with
`--json`. Inputs and outputs contain the facts of the base: keep them out of the repository.
