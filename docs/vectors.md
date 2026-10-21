# Vector candidates for the judge

Recall reads facts by words: the FTS5 index over the stems of every active fact. Words are exact on a ticket key, a
path, a port or a version, but they cannot find a fact described in other words. The optional vector model proposes
such facts as **candidates**; the Jev judge decides whether they reach the prompt.

The vector model is not a standalone reader. It never adds a fact by itself: without `MEMCUE_JUDGE_*`, configuring an
embeddings endpoint changes nothing in recall.

## Turning it on

An OpenAI-compatible `/embeddings` endpoint is enough:

```sh
export MEMCUE_EMBED_URL=http://127.0.0.1:11434/v1
export MEMCUE_EMBED_MODEL=bge-m3
# for a hosted endpoint:
# export MEMCUE_EMBED_KEY=...
```

Fill the vectors once, and again after adding facts or changing the model:

```sh
memcue embed -dry-run   # how many active facts have no vector for this model
memcue embed            # 32 facts per request; -batch N, -limit N and -timeout 2m change that
```

`add` and `correct` never call the endpoint: a fact without a vector simply does not take part in the candidate pass
until the next `memcue embed`.

| Variable | Meaning |
|---|---|
| `MEMCUE_EMBED_URL` | base address; `/embeddings` is appended |
| `MEMCUE_EMBED_MODEL` | model name |
| `MEMCUE_EMBED_KEY` | key for a hosted endpoint; empty for a local server |
| `MEMCUE_EMBED_TIMEOUT` | one request (default 2 s) |
| `MEMCUE_EMBED_SPACE` | label of the vector space, when a server changes model under one name |

## What it contributes

For a prompt, the vector model returns its top-N active facts by cosine similarity. The lexical candidates and the
vector candidates are deduplicated and passed together to Jev; the judge orders them and keeps what clears
`MEMCUE_JUDGE_RERANK_MIN`. The total is capped by recall's `MaxFacts`.

There is no absolute similarity threshold, no margin and no `only` mode. A similarity gate could not separate "needs
memory" from "does not" on the measured set, and the judge is better at that decision. The vector model only has to
propose; Jev decides.

Vectors from another model, another address or another space under the same model name are never compared: each
vector carries its tag and the hash of the text it was made from. A corrected fact therefore has no comparable vector
until `memcue embed` runs again, which is deliberate.

## Failure behaviour

A missing endpoint, a timeout or a wrong answer only removes the vector candidates; the lexical candidates and the
judge still run. After a failure the endpoint is left alone for 60 s, doubling to 15 minutes, and the pause is kept in
`$XDG_STATE_HOME/memcue/embed.pause` because the hook is a fresh process on every prompt.

## What the pilot measured

On a copy of the 2,571-fact Claude Code base and 180 real prompts, Jev over lexical candidates found a useful top-1
fact for 112 prompts; adding the vector candidates raised that to **129**, with 18 rescues and 2 losses against
Jev-over-lexical. Vector candidates alone were much weaker (35 useful top-1), which is why they go to the judge rather
than into the prompt. The details are in [models.md](models.md) and the research under `docs/research/`.

## Schema

Schema 4 adds `fact_vectors`, keyed by `(id, tag)`; the table is derived data and can be rebuilt with `memcue embed`.
A base opened by this version cannot be read by an older memcue. The vector model is optional and can be left
unconfigured.
