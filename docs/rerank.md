# The judge: order the facts the words and the vectors found

The words find the facts; they do not order them well. On a gold set built for the purpose, FTS5 put a useful fact
first for 44 of 120 prompts. A semantic retriever alone did worse (bge-m3 21, Jev over the whole base 12), but a useful
fact was among the candidates for 95 prompts: the loss was in the ranking, not in candidate generation.

This pass asks a typed decisions model (Jev) one yes/no question per candidate: *would this fact help answer the
prompt*. It orders the lexical candidates and, when an embeddings endpoint is configured, the vector candidates too;
the answer stays capped by `MaxFacts`.

## One judge for three roles

`MEMCUE_JUDGE_URL`, `MEMCUE_JUDGE_MODEL` and `MEMCUE_JUDGE_KEY` configure one Jev endpoint. The same endpoint serves:

- **recall rerank** — this document;
- **dream triage** — `MEMCUE_JUDGE_TRIAGE_MIN`;
- **duplicate check in vet** — `MEMCUE_JUDGE_DUP_MIN`, `_DUP_MAX_FACTS`, `_DUP_LONG_CONTEXT`.

There is no `MEMCUE_RERANK_*` or `MEMCUE_TRIAGE_*` endpoint pair. The role-specific variables are only thresholds and
request limits under the judge namespace.

| Variable | Meaning |
|---|---|
| `MEMCUE_JUDGE_URL` | decisions endpoint, e.g. `https://openrouter.ai/api/alpha/decisions` |
| `MEMCUE_JUDGE_MODEL` | e.g. `typesafe/jev-1.14` |
| `MEMCUE_JUDGE_KEY` | bearer token |
| `MEMCUE_JUDGE_RERANK_MIN` | probability below which a fact is not given (default 0.5) |
| `MEMCUE_JUDGE_RERANK_CANDIDATES` | candidates taken from each source (default 10) |
| `MEMCUE_JUDGE_RERANK_TIMEOUT` | one request (default 2 s) |

## Flow

```
lexical candidates (FTS5, gates)
  + vector top-N (when MEMCUE_EMBED_* is configured)
  -> deduplicate, apply the ticket exactness rule
  -> Jev: one noul per candidate
  -> keep >= MEMCUE_JUDGE_RERANK_MIN, sort, cap MaxFacts
```

Any error, timeout or missing setting leaves the ordinary lexical answer in place, unchanged. A failure pauses the
endpoint for 60 s, doubling to 15 minutes, with the pause in `$XDG_STATE_HOME/memcue/judge.pause` because the hook is
a fresh process on every prompt.

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
- The labels are one model's opinion; the later 60-fact truth sample was 52 TRUE_NOW / 8 STALE, but it was not random.
- A base larger than the duplicate cap (400 facts, 1,700 with the long-context header) skips the duplicate stage;
  that is a separate follow-up, not a rerank property.
- Jev is a judge, not a retriever: do not ask it to search the whole base.
