# FTS5 and vectors on paraphrased queries, November 2026 (#742)

**When a query reuses the fact's words, FTS5 and both vector models all find the gold fact (49/49 at Hit@5;
E5 misses one). When it shares one content term, FTS5 BM25 finds 51/71 and both vector models 71/71. When it
shares none, FTS5 cannot match the fact at all (0/76, by construction); bge-m3 finds 69/76 and E5 59/76. A query
in the other language: BM25 52/80, bge-m3 80/80, E5 73/80.** The RRF60 hybrid wins back far less than the vector
arm alone: 31/76 and 25/76 on zero overlap. Every fact BM25 matches through unrelated words outranks a gold fact
that only the vector list holds. The production `memcue search` default finds 9 of the 71 low-overlap queries:
a query of more than eight words must share two terms with a fact. These are authored fictional examples, so the
numbers bound what paraphrase costs on this set, not on a real base.

This study follows [#682](hybrid-682-synthetic-pilot.md), whose synthetic pilot hit a ceiling because its queries
reused the facts' words. Production search, defaults and the backend are unchanged.

## Corpus

The [fixture](paraphrase-742/fixture.json) holds 120 fictional facts in 40 groups of one subject each, 20 English
and 20 Russian. In each group one fact is the target and two are topical neighbours that share its host, service
and most of its words but hold another detail: the backup's start time, its retention, its restore password.
A query for the target cannot win on topic alone. The agent of this session (Claude Opus 5.5) wrote the
[corpus](scripts/paraphrase742/corpus.json); [build_fixture.py](scripts/paraphrase742/build_fixture.py) gave the
facts IDs F001-F120 in a seeded random order, so the ID tie order does not favour targets. All facts are active,
version 1, global and unexpired.

## Queries

[gen_queries.py](scripts/paraphrase742/gen_queries.py) asked `claude-opus-5-5` through `claude -p`, with no tools,
MCP servers, hooks or repository. Each call saw one target fact and wrote what someone who needs it would type:
1 literal query, 2 with at most one shared content word, 2 with none and 2 in the other language. One call per
group saw that group's three facts and wrote a question on the same subject that none of them answers. That gave
320 [candidates](paraphrase-742/candidates.jsonl) for $8.16.

Each candidate then passed three checks before the freeze:

1. **Overlap**, measured by the harness ([main.go](scripts/paraphrase742/main.go) `overlap`) with memcue's own
   tokenization: the distinct terms of `text.Terms(query)` that cover a stem of `text.Stems(gold fact)` by
   `text.Covers`, the prefix rule FTS5 matches with. The measured count, not the writer's intent, set the
   stratum: literal at 2 or more, low at 1, zero at 0, cross for the other language whatever its overlap.
2. **Unique answer**: `claude-sonnet-5-5` saw all 120 facts with IDs and the query, never the label, and listed
   every fact that answers it ([check_unique.py](scripts/paraphrase742/check_unique.py), $22.43). It named exactly
   the gold fact for all 280 positives and no fact for all 40 negatives, so this check dropped nothing. Either the
   writer kept the distinguishing detail every time, or the checker is lenient in the same way as the writer;
   this set cannot tell the two apart.
3. **Meaning**: 32 of the 316 kept queries (seed 742) were read for whether each still asks for its gold fact.
   The user delegated this read to the agent, so it is the agent's read, not a person's
   ([hand-read.json](paraphrase-742/hand-read.json)). It rejected none.

[select_queries.py](scripts/paraphrase742/select_queries.py) kept at most two queries per target and stratum,
which dropped 4. The frozen [queries](paraphrase-742/queries.json):

| Stratum | Queries | Targets |
|---|---:|---:|
| Literal (overlap ≥ 2) | 49 | 40 |
| Low (overlap 1) | 71 | 40 |
| Zero (overlap 0) | 76 | 40 |
| Cross-language | 80 | 40 |
| Negative | 40 | — |

Of the 80 "zero" candidates the writer intended, 75 measured 0 and 5 measured 1. Of the 80 "low" ones, 68
measured 1, 9 measured 2 and moved to literal, and 3 measured 0. Cross-language queries mostly keep the Latin
identifiers (WireGuard, quarry, EntityTooLarge): 65 of 80 share at least one term with their fact.

The fixture, queries and [method](paraphrase-742/method.json) were committed in `b0f8ba2` before any encoding
or ranking, and nothing in them changed afterwards. The search code under test is that of `dd953c6`, which
`b0f8ba2` only adds documentation to.

## Arms

- **bm25**: `Store.SearchFiltered` over the query's terms, OR-joined, `In=[global]`, up to 100 facts: the native
  FTS5 BM25 list.
- **production**: `recall.Search` with N=5, floor 0.6, `In=[global]`, what `memcue search` returns by default.
- **vector-e5**: exact cosine over all 120 facts with `intfloat/multilingual-e5-small` at revision
  `614241f622f53c4eeff9890bdc4f31cfecc418b3`, ONNX on the Mac CPU, `query:`/`passage:` prefixes, mean pooling
  and L2, as in #682.
- **vector-bge**: the same with `baai/bge-m3` from the RouterAI embeddings endpoint.
- **hybrid-e5**, **hybrid-bge**: equal-weight RRF, 1/(60+rank), of the bm25 list and that vector list.

Ties go by score, then by fact ID. RouterAI served bge-m3 from two upstreams (`BAAI/bge-m3` and
`parasail-bge-m3`). 80 repeated encodings across both returned vectors with cosine ≥ 0.999999 to the saved
ones, so the mix does not change a ranking.

## Quality

Hit@5, with Wilson 95% intervals in [results.json](paraphrase-742/results.json):

| Stratum | n | bm25 | production | vector-e5 | hybrid-e5 | vector-bge | hybrid-bge |
|---|---:|---:|---:|---:|---:|---:|---:|
| Literal | 49 | 49 | 49 | 48 | 49 | 49 | 49 |
| Low | 71 | 51 | 9 | 71 | 71 | 71 | 70 |
| Zero | 76 | 0 | 0 | 59 | 25 | 69 | 31 |
| Cross | 80 | 52 | 36 | 73 | 67 | 80 | 69 |
| All positive | 276 | 152 | 94 | 251 | 212 | 269 | 219 |

Hit@1:

| Stratum | bm25 | production | vector-e5 | hybrid-e5 | vector-bge | hybrid-bge |
|---|---:|---:|---:|---:|---:|---:|
| Literal | 42 | 42 | 44 | 44 | 48 | 44 |
| Low | 6 | 0 | 52 | 28 | 59 | 27 |
| Zero | 0 | 0 | 29 | 4 | 47 | 6 |
| Cross | 23 | 21 | 63 | 44 | 78 | 42 |

MRR@5 over all positives: bm25 0.359, production 0.271, vector-e5 0.775, hybrid-e5 0.566, vector-bge 0.893,
hybrid-bge 0.578.

**Rescues and losses against bm25** at Hit@5 (queries listed per stratum in results.json, `versus_bm25`):

| Stratum | vector-e5 | hybrid-e5 | vector-bge | hybrid-bge |
|---|---:|---:|---:|---:|
| Literal | +0 / −1 | +0 / −0 | +0 / −0 | +0 / −0 |
| Low | +20 / −0 | +20 / −0 | +20 / −0 | +19 / −0 |
| Zero | +59 / −0 | +25 / −0 | +69 / −0 | +31 / −0 |
| Cross | +23 / −2 | +15 / −0 | +28 / −0 | +17 / −0 |

On the literal stratum nothing separates the arms; the only loss is Q012 for E5 ("why did we drop events on
orders.v2 after a restart?", gold at rank 6 behind its two neighbours). On low overlap BM25 still lists the gold
fact for all 71 queries, but for 20 of them below rank 5: the one shared term is usually the host or service
name, which the two neighbours share as well. On zero overlap BM25 has nothing to match the gold fact with,
so its 0/76 follows from the definition of the stratum. That row measures only what the vectors win back. Cross
splits the same way: BM25 finds 52 of the 65 cross queries that keep an identifier and none of the 15 that do
not; bge-m3 finds all 80.

Each target contributes up to two queries to a stratum, so the 276 positives are not independent: there are 40
targets per stratum. The language halves behave alike except on zero overlap, where both hybrids do better on
Russian facts (23/39 and 19/39) than on English ones (8/37 and 6/37).

## Why the hybrid gives the gain back

Equal-weight RRF adds 1/(60+rank) from each list. A gold fact that only the vector list holds gets at most
1/61 = 0.0164. Any fact BM25 lists and the vector list also ranks scores more, and a fact at BM25 rank 1 alone
ties it. A paraphrase almost always shares some word with some fact of the corpus. Q172 ("freshly checked-out
project dies immediately on launch complaining about a bad image format, what am I missing?") matched four
unrelated facts from other groups. bge-m3 ranked the git-lfs fact first, and the hybrid put it
fifth, after those four. When BM25 lists five or more such facts, the gold fact leaves the top five. That is
why the hybrid gives up 38 of bge-m3's zero-overlap hits and 11 of its cross ones while adding none. The hybrid
helps only where BM25 already has the gold fact near the top. On real prompts, as #682 measured, that holds more
often than here.

## The production floor

`recall.Search` treats a query of more than eight words as a message and keeps only facts that share at least two
of its terms (`messageTerms`, #417). A low-overlap query shares one term with its gold fact by definition, so all
60 such queries longer than eight words returned no gold fact; of the 11 shorter ones, 9 did. That is a property
of the default floors on paraphrases, not of FTS5: the raw BM25 list on the same queries found 51 of 71.

## Negatives

All 40 negatives returned something in every arm. BM25 filled 189 of 200 slots, because a negative asks about
the same subject as its group and shares its words. The production floor cut that to 115. Vectors always fill
all five. The top cosine does not separate answerable from unanswerable: the median top-1 cosine of a negative
(bge-m3 0.619, E5 0.875) is near that of a positive whose gold fact ranked first (0.628 and 0.864), and 193 of
232 such bge-m3 positives sit at or below the highest negative. No threshold was tuned.

## Time

Search alone, in process, query vectors read from the file. 316 queries × 21 passes (1 first, 20 warm) per arm,
arm order rotated; every pass ranked identically.

| Arm | First median / p95 (ms) | Warm median / p95 (ms) |
|---|---:|---:|
| bm25 | 0.409 / 0.580 | 0.405 / 0.556 |
| production | 0.420 / 0.588 | 0.415 / 0.570 |
| vector-e5 | 0.051 / 0.058 | 0.049 / 0.057 |
| vector-bge | 0.121 / 0.137 | 0.121 / 0.133 |
| hybrid-e5 | 0.472 / 0.628 | 0.468 / 0.621 |
| hybrid-bge | 0.545 / 0.700 | 0.538 / 0.691 |

The vector arms scan 120 vectors held in Go memory, unlike #682's SQL scan. They show the cost of exact cosine
on this corpus size, not that of a vector index on a real base. A hybrid runs both arms.

Query encoding, each query alone, 6 times (1 first, 5 warm), [encoding.json](paraphrase-742/encoding.json):

| Model | First median / p95 (ms) | Warm median / p95 (ms) |
|---|---:|---:|
| E5 small, ONNX on the Mac (arm64), 2 threads | 6.4 / 8.3 | 6.3 / 8.2 |
| bge-m3, one RouterAI HTTP request | 1299 / 2055 | 1284 / 2032 |

The bge-m3 time is the round trip from this machine, network included, with no retries. Encoding costs far more
than the search: at these numbers bge-m3 adds over a second to a query, E5 about 6 ms.

## Limits

- The facts and queries are authored fiction: one model wrote the corpus and another wrote the queries for it. A
  model-written paraphrase may suit an embedding model better than a person's question would.
- The writer was told to avoid the fact's words; real agent queries are mostly closer to the literal stratum.
  The strata show what each arm does at a given overlap, not how often a real query has it.
- The unique-answer check dropped nothing and the meaning read was the agent's, not a person's.
- 120 facts are a small corpus; on a real base of thousands, more unrelated facts compete in both lists.
- The negatives use one writer, one checker and one read; there is no calibrated abstention.

## Files and how to rerun

The data is in [paraphrase-742/](paraphrase-742/), the scripts in [scripts/paraphrase742/](scripts/paraphrase742/).
[query-results.csv](paraphrase-742/query-results.csv) holds one row per query and arm with the gold rank and the
top five. [run.json.gz](paraphrase-742/run.json.gz) holds every ranked list and pass time.
[vectors.json.gz](paraphrase-742/vectors.json.gz) holds all fact and query vectors of both models, at six
decimals. The rankings were computed from those values.

From the repository root, the ranking and metrics rerun without a model:

```bash
go build -tags research -o /tmp/paraphrase742 ./docs/research/scripts/paraphrase742/
D=docs/research/paraphrase-742
/tmp/paraphrase742 run $D/fixture.json $D/queries.json $D/vectors.json.gz 21 /tmp/run.json.gz
python3 docs/research/scripts/paraphrase742/analyze.py $D/queries.json /tmp/run.json.gz $D/encoding.json /tmp
```

The rankings must match run.json.gz; the times will differ. The full chain also reruns `build_fixture.py`,
`gen_queries.py`, `paraphrase742 overlap`, `check_unique.py`, `select_queries.py` and `embed.py` (usage in each
script). A rerun of the model steps writes new queries, not these.

| File | SHA256 |
|---|---|
| fixture.json | `524e035017ded48f5d02482f77a0697664f85ca6babb77e865ab28a124f4e338` |
| queries.json | `bfa9bfad6b9516a14cfa251512fd8f840c338d26239bbe29d7803b40a4dfcd2b` |
| method.json | `eb83ab761431932a5fc0149e4ace876c5ce7c38e319fe7a46cf13b6a64420cf1` |
| candidates.jsonl | `63896e6337d2ebd9ac853acc988237a6e45b70f1ec93486075f7246499bca12c` |
| vectors.json.gz | `461b1b040ee6268487274bcfad49933adb9339794f589c11b27234a1354780e4` |
| run.json.gz | `03c19b0d93b4942676e5fba31f7ed8528d6290013ca34093c0ac6b9a78c1a160` |
