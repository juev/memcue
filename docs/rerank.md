# The judge: order the facts the words and the vectors found

**Status 2026-10-22 (#232):** recall no longer reads vectors. The judge reads the first `MEMCUE_JUDGE_RERANK_CANDIDATES`
facts (default 20) the words found, in BM25 order, whether the lexical gate let them pass or not; on the gold set of
180 prompts this matched the vector candidates plus the judge
([research/one-model-2026-10.md](research/one-model-2026-10.md)). The rest of this note is the history of the
measurements.

**Status 2026-10-24 (#357):** the judge, the listwise reader and `MEMCUE_RERANK` are removed. Recall is the words
alone, in BM25 order; nothing below describes the current binary.

The words find the facts; they do not order them well. On a gold set built for the purpose, FTS5 put a useful fact
first for 44 of 120 prompts. A semantic retriever alone did worse (bge-m3 21, Jev over the whole base 12), but a useful
fact was among the candidates for 95 prompts: the loss was in the ranking, not in candidate generation.

This pass asks a typed decisions model (Jev) one yes/no question per candidate: *would this fact help answer the
prompt*. It orders the lexical candidates and, when an embeddings endpoint is configured, the vector candidates too;
the answer stays capped by `MaxFacts`.

## One judge for two roles

`MEMCUE_JUDGE_URL`, `MEMCUE_JUDGE_MODEL` and `MEMCUE_JUDGE_KEY` configure one Jev endpoint. The same endpoint serves:

- **recall rerank** — this document;
- **dream triage** — `MEMCUE_JUDGE_TRIAGE_MIN`;

There is no `MEMCUE_RERANK_*` or `MEMCUE_TRIAGE_*` endpoint pair. The role-specific variables are only thresholds,
request limits and timeouts under the judge namespace.

| Variable | Meaning |
|---|---|
| `MEMCUE_JUDGE_URL` | decisions endpoint, e.g. `https://openrouter.ai/api/alpha/decisions` |
| `MEMCUE_JUDGE_MODEL` | e.g. `typesafe/jev-1.14` |
| `MEMCUE_JUDGE_KEY` | bearer token |
| `MEMCUE_JUDGE_RERANK_MIN` | probability below which a fact is not given (default 0.5; 0.5 and 0.55 serve the same useful prompts, above 0.55 the cut starts to cost — [research/threshold-2026-10.md](research/threshold-2026-10.md)) |
| `MEMCUE_JUDGE_RERANK_CANDIDATES` | facts the judge reads, in BM25 order (default 20; 10 from each source before #232) |
| `MEMCUE_JUDGE_RERANK_TIMEOUT` | one rerank request (default 2 s) |
| `MEMCUE_JUDGE_TRIAGE_TIMEOUT` | one triage request (default 2 s) |

## Flow

```
lexical candidates (FTS5, gates)
  + vector top-N (when MEMCUE_EMBED_* is configured)
  -> deduplicate, apply the ticket exactness rule
  -> Jev: one noul per candidate
  -> keep >= MEMCUE_JUDGE_RERANK_MIN, sort, cap MaxFacts
```

**Update 2026-10 (0.6.0):** the vectors and the embedding knobs are gone (#232, #250), and `MEMCUE_RERANK` chooses the
rerank reader. Jev stays the default; `listwise` — the main chat model, one question per prompt — is an option, because on
the current base it served as many useful prompts as Jev with two more noise facts and three times the latency
([research/main-judge-2026-10.md](research/main-judge-2026-10.md) for the older, noisier base where it won,
[research/listwise-2026-10.md](research/listwise-2026-10.md) for the product-path check). `openai/gpt-6-luna-decisions`
on the same endpoint was measured as a judge and lost to Jev on speed, price and useful prompts
([research/luna-decisions-2026-10.md](research/luna-decisions-2026-10.md)). The flow above then reads:

```
lexical candidates (FTS5, gates)
  -> the reader MEMCUE_RERANK names, over the first MEMCUE_*_CANDIDATES in BM25 order
     listwise: "which of these would help, best first" -> the picks in the model's order
     judge:    one noul per candidate -> keep >= MEMCUE_JUDGE_RERANK_MIN
  -> approved before provisional, cap MaxFacts
  -> hits
```

**Update 2026-10 (#321):** a third reader, `local`, is built in: LightGBM trees over the words' own signals about each
candidate, trained to predict Jev's probability on 1442 prompts outside the gold set, and walked in pure Go. It reads
the same first 20 facts in BM25 order and keeps those scored at 0.5 or above; on the gold set it served as many useful
prompts as the words with half their noise ([research/local-readers-2026-10.md](research/local-readers-2026-10.md)).
It is opt-in: on a small fresh base it scored a fact that held every prompt term below the threshold. Without a Jev key
and without `MEMCUE_RERANK` the words decide; the listwise reader, the earlier fallback when the main model was
configured, is opt-in only. A paused Jev still falls back to the words.

**Update 2026-10 (#324):** the `local` reader is removed, with its model, the `leaves` dependency and the feature path
it read. End to end it changed nothing against the words, so it never paid for itself. `MEMCUE_RERANK=local` is an
unknown value now and gets the default.

**Update 2026-10 (#331):** the default is the words. Jev reads the candidates only with `MEMCUE_RERANK=judge`; a Jev
endpoint configured for triage and the dream no longer turns the judge on for every prompt. memcue works locally
unless a reader is asked for.

Any error, timeout or missing setting leaves the ordinary lexical answer in place, unchanged. One failure pauses
the shared judge endpoint for 60 s, doubling to 15 minutes. The pause lives in `judge.pause` in the binary's state
directory (`$XDG_STATE_HOME/memcue` for the default base, a per-base subdirectory otherwise, or `MEMCUE_STATE_DIR`)
and is shared by rerank and triage, because the hook is a fresh process on every prompt.

## What it measured

Gold set: 120 real prompts, candidates from three retrievers (lexical top-10, bge-m3 top-10, Jev-top-10-over-the-base),
2,538 pairs, shuffled, labelled blind by a judge that never saw which retriever proposed a fact.

| reader, top-1 | prompts with a USEFUL fact |
|---|---:|
| words (FTS5, BM25 order) | 43 / 118 |
| words + this pass | **70 / 118** |
| bge-m3 over the base | 21 / 120 |
| Jev over the whole base | 12 / 120 |

The 0.3.0 pilot on a 2,571-fact copy of the Claude Code base and 180 prompts measured the combined flow:

| reader, top-1 | useful prompts |
|---|---:|
| lexical only | 75 / 180 |
| Jev over lexical | 112 / 177 |
| **Jev over lexical + vector** | **129 / 180** |

The combined run rescued 18 prompts over Jev-over-lexical and lost 2; all 18 rescued top-1 facts were vector-only.
It cost 669k input tokens, $0.0281 and 0.87 s median for 180 prompts. The lexical-only judge run was 396k tokens,
$0.0166 and 0.79 s median.

## Known limits

- Cost and latency are paid per prompt while the judge is configured; on a laptop without a key it is off.
- The lexical gate stops filtering once a judge is configured: the judge reads the facts the gate turned away, so
  `MEMCUE_MIN_COVERAGE` and the matched-term count decide the words' own answer and the fallback only (#57, #232).
- The cut is not stable between two identical runs. Jev answers the same candidate a few hundredths differently, so the
  served pair changed on 5–7 of 12 live prompts over six runs; a tie band that orders near-equal scores by the words was
  measured and does not change that, because the flips come from the fixed threshold and from gaps wider than the
  jitter ([research/judge-cut-2026-10.md](research/judge-cut-2026-10.md)).
- The labels are one model's opinion; the later 60-fact truth sample was 52 TRUE_NOW / 8 STALE, but it was not random.
- Jev is a judge, not a retriever: do not ask it to search the whole base.
