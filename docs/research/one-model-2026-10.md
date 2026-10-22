# One model or three: what recall and the background jobs need, October 2026

Issue #232 asked whether memcue can run on the main model alone, without the Jev judge and without the bge-m3
embedder, to cut cost and the time a user waits. This note keeps the measurements of 2026-10-22, what other
projects do, and what was decided, so the next step rests on numbers.

## What each model does

| Model | Where | When |
|---|---|---|
| main (`deepseek-flash` through `MEMCUE_API_*`, or `claude -p`) | extraction, vet review (keep/drop/unsure/split), dream (retire/correct, split), label, migrate | in the background, after a session |
| Jev (`typesafe/jev-1.14` on RouterAI) | recall rerank on every prompt; vet duplicates over the whole base; dream triage | rerank: the user waits |
| bge-m3 (RouterAI) | recall candidates, only in front of Jev | the user waits |

Vectors were tried as dream neighbours on 2026-10-20 (base of 2,501 facts) and did not help: pairs at cosine ≥ 0.80
were almost all found by words already, and the pairs only vectors saw (cosine 0.70–0.76) were neighbours by topic,
not duplicates.

## Where the time and the money go

**Time** (#213, `docs/evaluation.md`, section Latency): the recall hook costs about 45 ms of its own; the embedder
request takes 0.8–1.5 s (TLS 0.05–0.86 s of it) and the Jev request 0.37–0.53 s.

**Tokens, 2026-10-22, from the memcue logs on the author's Mac:**

| Command | Runs | Input | Cached | Output |
|---|---|---|---|---|
| vet | 42 | 11.6 M | 3 k | 3 k |
| vet `-dry-run` | 3 | 10.5 M | 0.4 k | 4 k |
| dream `-dry-run` (`-since` and `-all`) | 2 | 0.57 M | 66 k | 1.89 M |
| extract | 5 | 0.19 M | 16 k | 40 k |

- The vet input is the duplicate check (#240): every candidate goes to Jev with the whole approved base, 7 requests
  of 400 facts, about 580k input tokens per candidate.
- The dream output is the main model. The DeepSeek console showed 58.5 M tokens that day: 50.5 M cache hits (mostly
  other work, code review), 3.8 M cache misses and 4.2 M output, so the two dream dry runs gave about half of the
  output.

**Reasoning.** One split call (the split prompt, 20 facts), [`reasoning_probe.py`](scripts/reasoning_probe.py):

| Call | Output tokens | Reasoning | Time | Compound facts found |
|---|---|---|---|---|
| thinking on (the default), run 1 | 10,188 | 9,936 (97.5 %) | 44 s | 2 |
| thinking on, run 2 | 17,667 | 17,424 (98.6 %) | 78 s | 2 |
| `"thinking": {"type": "disabled"}` | 1,226 | 0 | 5 s | 9 |

The DeepSeek chat completions reference (api-docs.deepseek.com/api/create-chat-completion) lists `thinking` with
default `enabled`, and `reasoning_effort` with `none` / `low` / `high` / `max`. memcue sends neither, so every main
model call reasons. Turning it off cuts the output 8–14 times, but it changed the decisions here (9 compound facts
instead of 2), so each call needs a labelled sample before it is switched. Context caching covers only the shared
prefix, the system prompt, and does not make output cheaper.

## Recall with fewer models

Gold set of 180 prompts (`gold-set-2026-10.md`), the base copy it was built on, every served pair labelled: pairs
the gold pool did not hold were labelled by the same judge with the same prompt
([`recall_extra_pool.py`](scripts/recall_extra_pool.py), then `gold_set.py label`). "Useful" is the number of
prompts served a USEFUL fact; the scripts are [`recall_explain.py`](scripts/recall_explain.py) and
[`recall_misses.py`](scripts/recall_misses.py).

**Expansion at write time** (document expansion by query prediction, Nogueira et al., 2019): `deepseek-flash`
without reasoning wrote 6.5 search queries per fact into `keywords` — synonyms, Russian and English words, how the
problem is asked — 25 facts a call ([`expand_queries.py`](scripts/expand_queries.py),
[`expand_queries_prompt.md`](scripts/expand_queries_prompt.md), applied to a copy with
[`expand_apply.py`](scripts/expand_apply.py)): 2,571 facts in 2.5 minutes, 352k input and 199k output tokens.

| Recall | Useful (top-1 / any) | USEFUL | RELATED | NOISE |
|---|---|---|---|---|
| words only, current gate | 43 / 52 | 72 | 31 | 68 |
| words only, current gate, expanded | 46 / 56 | 79 | 38 | 72 |
| words only, no gate, BM25 order | 71 / 90 | 122 | 91 | 128 |
| words only, no gate, BM25 order, expanded | 75 / 91 | 129 | 93 | 121 |
| Jev, no vectors | 93 / 99 | 152 | 54 | 21 |
| Jev, no vectors, expanded | 98 / 108 | 165 | 46 | 19 |
| vectors (local Ollama bge-m3) + Jev, two runs | 114–116 / 121–124 | 189–194 | 66–67 | 17–18 |

Other gate settings (one or two matched terms, coverage 0.15 or 0, coverage or BM25 order) fall between these rows.
The gate experiments used a build with environment knobs that was not committed.

- **Recall on the main model alone does not work.** Expansion adds 1–4 prompts to recall by words. Loosening the gate
  and ordering by BM25 finds more, but the noise grows six to seven times: what Jev brings is mostly the refusal to
  serve what does not fit, and the words have no such signal.
- **The main model as the reranker** was measured earlier (`main-reranker-2026-10.md`): 108 useful top-1 prompts
  against Jev's 112, and a chat call per prompt takes seconds.
- **Without vectors, paraphrases are lost.** With Jev and expansion, the vectors still add about 14 prompts
  (108 → 121–124). In the gold set, Jev over word and vector candidates rescued 18 top-1 prompts against Jev over
  the words alone, and all 18 rescued facts were candidates only the vectors found.
- **Expansion helps the judge**: without vectors, 99 → 108 prompts at the same noise.

## Without vectors: more candidates in BM25 order

The rows above gave the judge ten lexical candidates in the gate's order: those that passed first, then the turned-away
ones by coverage. An experiment build (not committed) tried three ways to win back what the vectors found, on the
expanded base, with Jev and no embedder:

1. the judge reads the first facts of the search in BM25 order, passed and turned away together;
2. pseudo-relevance feedback: the 5 rarest stems shared by the top 5 facts go into a second FTS search;
3. a synonym dictionary applied to the query: the main model without reasoning gave replacements for the 4,923 words
   that occur in at least three facts (47 s, 27k input and 70k output tokens; 5,689 stem replacements).

| Recall (Jev, no vectors) | Useful (top-1 / any) | USEFUL | RELATED | NOISE |
|---|---|---|---|---|
| 10 candidates, gate order | 98 / 108 | 165 | 46 | 19 |
| 10 candidates, BM25 order | 107 / 113 | 181 | 46 | 16 |
| 20 candidates, gate order | 107 / 120 | 182 | 57 | 20 |
| **20 candidates, BM25 order**, two runs | **113 / 122–124** | 197–201 | 47–48 | 19–21 |
| 20 candidates, BM25 order, base not expanded | 105 / 115 | 186 | 49 | 22 |
| 20 candidates, BM25 order + PRF | 111 / 122 | 197 | 50 | 17 |
| 20 candidates, BM25 order + dictionary | 107 / 115 | 178 | 68 | 23 |
| vectors + Jev, two runs | 114–116 / 121–124 | 189–194 | 66–67 | 17–18 |

Twenty facts in BM25 order match the full path: the vector candidates mostly filled places the words would fill if
the judge read more of them, in the search's own order. Expansion adds 7–9 prompts here. PRF changes nothing beyond the
run-to-run spread, and the dictionary makes it worse, because synonyms pull other facts into the twenty places.

The product change (#232, step 1) was measured the same way: 114 / 123 prompts, NOISE 23, in a clean run; in another
run one judge request timed out at 2 s, the judge paused for 60 s as designed, and 10 prompts got the words' answer
(108 / 119, NOISE 17). The judged lists matched the experiment for 169 of the 180 prompts.

## What other projects do

Checked in their docs and source on 2026-10-22:

| Project | Dense | BM25 / full text | Fusion | Reranker | Local embeddings |
|---|---|---|---|---|---|
| mem0 v2 | yes | yes (BM25 sparse in Qdrant, full text in other stores) | normalised sum of semantic, BM25 and entity scores | optional: cross-encoder, LLM, hosted | yes, Ollama |
| Graphiti / Zep | yes | yes, BM25 | RRF by default; MMR, node distance | cross-encoder (OpenAI logprobs, Gemini, BGE-v2-m3) | yes, Ollama |
| Letta, archival memory (V1 server) | yes, pgvector | on the Turbopuffer backend only | RRF | not found | yes: Ollama, LM Studio, vLLM, Hugging Face |
| LangMem / LangGraph store | yes | not found | — | not found | yes, any embed function |

Sources: github.com/mem0ai/mem0 (release v2.0.0), docs.mem0.ai (reranker search, Ollama embedder);
help.getzep.com/graphiti/working-with-data/searching, github.com/getzep/graphiti; github.com/letta-ai/letta, branch
`archive` (`agent_manager.py`, `tpuf_client.py`, `embedding_config.py`); docs.langchain.com/oss/python/langgraph/stores,
langchain-ai.github.io/langmem.

None of them replaces embeddings with query or document expansion; LangMem can let a model rewrite the query, and
the search is still by embeddings. The common shape is dense plus BM25, fused (often with RRF), with an optional
reranker, and every one of them can embed locally.

## The cost of vectors is the remote call

Embedding one prompt with bge-m3:

| Where | Time |
|---|---|
| RouterAI | 0.8–1.5 s |
| local Ollama, model loaded | 23–35 ms |
| local Ollama, cold start | 1.6 s (Ollama unloads an idle model; `keep_alive` keeps it) |

The gold set rows above with vectors were measured with the local Ollama, so the local embedder's quality is the best
row of the table.

The two serve the same vectors: for 5 random facts of the Mac base, the stored RouterAI `baai/bge-m3` vector and the
local Ollama `bge-m3` vector of the same text had cosine 0.99997–0.99999, both 1024-dimensional. The tag a vector is
stored under is built from the endpoint URL and the model name (`embed.Settings.Tag`), so switching a machine to the
local Ollama would store a second set of the same vectors in the synced base (the RouterAI set is 10.8 MB of a
19.7 MB base) unless the tag stops depending on the URL.

## Decision and next steps

An earlier decision of the same day, to keep the vectors and move the embedder to a local Ollama, was dropped:

- the vectors a machine stores depend on the model its configuration names, so machines with different embedders
  cannot share them, and naming the tag explicitly does not change that;
- a local Ollama with bge-m3 holds 1.3 GB of memory (the `llama-server` process; the model itself is 0.69 GB, F16,
  567M parameters) and 1.2 GB on disk, for vectors alone;
- a static model computed inside memcue (Model2Vec `potion-multilingual-128M`, distilled from bge-m3, 256 dimensions)
  needs no server, but its matrix is 512 MB, and a pure-Go Unigram tokenizer would have to be written.

What stands:

1. **Recall without vectors** (#232, step 1): the judge reads the first 20 facts the words found, in BM25 order.
2. **Expansion at write time** (step 2): extraction writes search phrases for its facts in the same call, and every
   dream fills them for facts that have none (written by `add`, `correct` or `import`); `dream -all` does the base
   once. The phrases live in their own column (`queries`), so a fact without them is known, and a
   correction of the text drops them.
3. **Vet duplicates without the Jev stage** (#240, step 3).
4. **Remove the embedder** (step 4): `memcue embed`, `MEMCUE_EMBED_*` and the stored vectors.
5. **Reasoning off** per call where a labelled sample shows no loss.

Jev stays in recall: the main model as the reranker scored 108 against 112 and takes seconds per prompt
(`main-reranker-2026-10.md`); a local cross-encoder in its place is not measured.
