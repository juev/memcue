# Vector search next to PostgreSQL, November 2026 (#682)

**On a real base, bge-m3 vectors combined with the words by RRF served a useful fact to more prompts than the words
alone: 218 against 188 of 300 at top-5 and 127 against 115 at top-1, with less noise (31% against 39%).
multilingual-e5-small did not: alone it lost to the words, and in a hybrid it traded first places for a small top-5
gain. The model runs as a separate CPU container that PostgreSQL calls; PostgresML inside PostgreSQL is not usable on
this host.**

This is the PostgreSQL stage of [#682](https://github.com/juev/memcue/issues/682). It follows the server-side lexical
search of [#700](https://github.com/juev/memcue/issues/700) and the owner's decisions of 2026-11-09: embeddings are
computed on the PostgreSQL side on loffe, not in clients; effectiveness is measured on the real base with real
prompts. Production, defaults and the PostgreSQL schema are unchanged.

## Stand

Everything ran on loffe (8 CPU, 31 GB RAM, no GPU) in separate containers, apart from the production database:

- `pgvector/pgvector:pg18` (pgvector 0.8.7) with the pgsql-http extension, holding a copy of the Mac base made by
  `memcue migrate -postgres` (7,975 facts, 3,700 active);
- Hugging Face text-embeddings-inference `cpu-1.8` serving `intfloat/multilingual-e5-small` (384 dimensions) and
  `BAAI/bge-m3` (1,024 dimensions);
- `ghcr.io/postgresml/postgresml:2.10.0` for `pgml.embed()` inside PostgreSQL.

PostgreSQL itself embeds: `bench.embed()` posts texts to the model container with `http_post` and returns `vector`
values, both for filling the facts and for each query. E5 gets the `passage: ` / `query: ` prefixes; bge-m3 none.
Facts are embedded by their content. Vector search is an exact cosine scan over the active facts that
`search -in all` sees (no shelf, not expired). Scripts: [scripts/vectors682](scripts/vectors682).

## Where the model runs

| | PostgreSQL → TEI container (http) | PostgresML (`pgml.embed`) |
|---|---|---|
| query embedding, new session | 37–105 ms (E5) | 15.9–19.2 s |
| query embedding, same session | 38–58 ms | 105–329 ms |
| filling 3,700 facts | E5 6.9 min (batch 32); bge-m3 ≈ 1.5 h | backend crashed |
| memory | E5 2.2–3.3 GiB; bge-m3 4.8 GiB under a 6 GiB limit | 1.9 GiB with one model |

PostgresML loads the model into every new backend, and a memcue command opens a new connection, so each search would
pay the load. A batch `INSERT … SELECT pgml.embed(…)` over the facts killed the backend with `signal 4: Illegal
instruction`: the image's native code needs instructions this CPU lacks. The 26 GB image was removed after the test.

bge-m3 needs its batches capped: at batch 16 without limits TEI grew to 19.3 GiB and about 750% CPU. With
`--max-batch-tokens 2048 --max-client-batch-size 8 --auto-truncate` and a 6 GiB container limit it stayed at 4.8 GiB.

## Quality

**Prompts.** 300 real user prompts sampled from 387 Claude Code sessions on the Mac (at most three per session, slash
commands, hook output and tool results excluded); 98% Russian, median 92 characters. They are not committed.

**Arms**, top-5 each:

- lexical: `memcue search -json -in all -n 5`, the production BM25 order with its score floor (it returns fewer than
  five facts for 62 prompts, none for 9);
- vector: the five nearest active facts by cosine;
- hybrid: reciprocal rank fusion, `1/(60+rank)`, of the lexical top-20 (`-min-score 0`) and the vector top-20;
- lexical plus vectors in the empty slots: the lexical answer unchanged, vector facts appended up to five;
- reranking: the lexical top-20 ordered by RRF of BM25 rank and E5 cosine rank.

**Labels.** For each prompt the union of all arms' candidates was shuffled deterministically and labelled blind by
`claude-opus-5-5` through `claude -p` (no tools, no MCP, no hooks), with the USEFUL / RELATED / NOISE definitions of
[gold_set.py](scripts/gold_set.py): 7,821 pairs, $84.83.

| arm | useful at top-1 | useful at top-5 | USEFUL facts | noise | won / lost against lexical |
|---|---:|---:|---:|---:|---:|
| lexical | 115 | 188 | 349 | 39% | — |
| E5 vector | 58 | 131 | 213 | 59% | 30 / 87 |
| E5 hybrid | 101 | 200 | 359 | 42% | 34 / 22 |
| lexical + E5 in empty slots | 115 | 198 | 362 | 43% | 10 / 0 |
| lexical top-20 reranked by RRF(BM25, E5) | 122 | 198 | 392 | 36% | 28 / 18 |
| bge-m3 vector | 118 | 202 | 371 | 36% | 52 / 38 |
| **bge-m3 hybrid** | **127** | **218** | **452** | **31%** | **47 / 17** |
| lexical + bge-m3 in empty slots | 116 | 205 | 381 | 40% | 17 / 0 |

"Useful at top-k" counts prompts with at least one USEFUL fact among the first k; "noise" is the NOISE share of the
facts an arm returned. The pool ceiling, a USEFUL fact anywhere among the candidates, is 228 prompts with the E5 arms
and 249 with the bge-m3 arms.

Cosine is no gate. E5's top-1 cosine sits between 0.85 and 0.90 for nearly every prompt; any threshold on the
appended facts removed useful ones in about the same proportion as noise (13 USEFUL of 190 appended at no threshold,
2 of 19 at 0.88).

## Latency

Measured on loffe, without the network, over the same 300 prompts:

| step | median | p95 |
|---|---:|---:|
| `memcue search` (whole command, server-side lexical) | 30–51 ms | 83–126 ms |
| query embedding through PostgreSQL, E5 | 53 ms | 145 ms |
| query embedding through PostgreSQL, bge-m3 | 97 ms | 307 ms |
| exact cosine scan, 3,700 facts, E5 / bge-m3 | 10 / 20 ms | 16 / 26 ms |

An HNSW index over the E5 vectors took 1.5 s to build and 7.4 MB. Over the tailnet from the Mac a lexical search
measured 260 ms median in #700; the bge-m3 hybrid adds the server's embedding and scan, about 120 ms at the median and
330 ms at p95, inside the four-second budget.

## Limits of the method

- One judge, no inter-judge agreement on this set. The labels measure agreement with Opus's notion of usefulness, not
  ground truth.
- The pool is the union of what the arms found: a fact none of them proposed counts as absent. More arms raised the
  ceiling (228 → 249), so the arms are compared within the same pools, not with older sets.
- The prompts come from the owner's own sessions and the base is the owner's; another base or another kind of prompt
  can rank differently. The October gold set on the Hermes base (233 facts) had bge-m3 far behind the words; that
  base was small and keyword-heavy, and the two sets must not be compared directly.
- The hybrid's lexical list ignores the score floor (`-min-score 0`); the production floor applies only to the plain
  lexical arm.
- Whole-command latency over the tailnet was not measured for the hybrid; the figure above adds measured server-side
  steps to the #700 measurement.

## What follows

bge-m3 in a TEI container next to PostgreSQL, fused with the lexical search by RRF, is the candidate for the
PostgreSQL backend. Implementation would need: a vector column with the model's tag and an HNSW index in the
PostgreSQL schema, embedding of each written fact by PostgreSQL (asynchronously or in the write), the fusion in the
server-side search statement, and an explicit switch with the lexical search as the fallback when the model service
is down. The local SQLite base is unchanged by this.
