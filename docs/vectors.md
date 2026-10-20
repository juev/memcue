# Search by meaning

Recall reads facts by words: the FTS5 index over the stems of every active fact, then a gate on how much of the
prompt a fact holds. It is fast (3-8 ms), local, and exact on the things an agent usually asks about — a ticket key, a
path, a port, a hash. What it cannot do is find a fact the prompt describes in other words. Measured on 120 real
prompts from the Hermes gateway with blind-judge labels, 35 prompts that needed memory got nothing at all.

This is the optional second reader. It never replaces the words: a fact it offers is added next to what the words
found, and nothing it does can make recall worse. With no endpoint configured — the default, and the only sane setup
for a laptop — none of this code runs and no request leaves the machine.

## Turning it on

An OpenAI-compatible `/embeddings` endpoint is all it takes; a local server, a hosted router and a vendor API are the
same three settings.

```sh
# a local server: Ollama serves the OpenAI shape under /v1 (its /api/embeddings is a different format)
export MEMCUE_EMBED_URL=http://127.0.0.1:11434/v1
export MEMCUE_EMBED_MODEL=bge-m3

# or a hosted OpenAI-compatible router
export MEMCUE_EMBED_URL=https://routerai.ru/api/v1
export MEMCUE_EMBED_MODEL=bge-m3
export MEMCUE_EMBED_KEY_ENV=ROUTERAI_API_KEY   # name of the variable holding the key, not the key itself
```

Then fill the vectors once, and again after adding facts or changing the model:

```sh
memcue embed -dry-run   # how many active facts have no vector for this model
memcue embed            # 32 facts per request; -batch N, -limit N and -timeout 2m change that
```

233 facts against a local `bge-m3` took 54 seconds on one host (8 requests of 32, model warm-up included).

`add` and `correct` never call the endpoint: a fact without a vector simply does not take part in the meaning pass
until the next `memcue embed`.

| Variable | Meaning |
|---|---|
| `MEMCUE_EMBED_URL` | base address; `/embeddings` is appended |
| `MEMCUE_EMBED_MODEL` | model name |
| `MEMCUE_EMBED_KEY_ENV` | variable holding the key (default `OPENAI_API_KEY`) |
| `MEMCUE_EMBED_TIMEOUT` | one request (default 2 s, clamped to 0.2-10 s) |
| `MEMCUE_EMBED_MARGIN` | how far above chance a fact must stand (default 1.0; see below) |
| `MEMCUE_EMBED_MIN_SCORE` | optional absolute similarity floor, off by default |
| `MEMCUE_EMBED_MAX_FACTS` | cap on facts per prompt, lexical and vector together (default: recall's own limit) |
| `MEMCUE_EMBED_SPACE` | label of the vector space, for a server that changes model under one name |

## What decides a fact is offered

Not an absolute similarity. On the measured set a raw threshold does not separate a prompt that needs memory from one
that does not: at 0.55 it kept 20 of the 35 misses but also 33 of 37 prompts that needed nothing, because any
technical text scores 0.5-0.6 against any technical fact, and the model's own baseline differs from model to model
(a paraphrase of a stored fact scores 0.38-0.66 on one and 0.68-0.84 on another).

The spread inside one query does separate them. A fact is offered when its similarity stands `Margin` standard
deviations above the closest of `n` unrelated facts would reach by chance, `sqrt(2 ln n)`:

```
threshold = mean + Margin * std * sqrt(2 ln n)
```

The bar therefore rises as the base grows, while a paraphrase of a stored fact keeps standing out. Vectors from
another model, or made from another version of the text, are never compared: each vector carries its model tag and
the hash of the text it was made from.

## When the endpoint is down, slow or wrong

Recall falls back to the words, silently and with the same answer it would have given before the feature existed.
After a failure the endpoint is left alone for 60 s, doubling to 15 minutes on repeated failures. The pause is kept
in `$XDG_STATE_HOME/memcue/embed.pause`, because the hook is a fresh process on every prompt and would otherwise pay
the timeout again on each of the next turns.

The pass only runs when the words found fewer facts than the limit, so a normal prompt costs nothing.

## Baseline to beat

Measured on 322 facts and 120 prompts with blind-judge labels (`memcue eval`):

- vector top-2 overlaps the lexical top-2 by 0.20 — the two readers look at different facts;
- vector top-2 alone keeps only 10 of the 21 facts judged USEFUL that FTS5 finds, so a replacement is not an option;
- on the 35 prompts that got nothing, a bge-m3 top-1 offers a useful fact for about 11 of them.

What the pass actually does on that set (233 vectors of `bge-m3`, Ollama on the same host, 120 prompts), counting the
facts `memcue eval` reports after the lexical pass:

| setting | prompts with a fact | labelled useful | all facts | into prompts needing no memory |
|---|---|---|---|---|
| words only | 73 | 13 | 127 | 45 |
| margin 0.75 | 115 | 15 | 214 | 64 |
| margin 1.0 (default) | 87 | 13 | 159 | 52 |
| margin 1.5 | 73 | 13 | 127 | 45 |
| floor 0.62, margin 0.1 | 82 | 13 | 147 | 51 |

Read this honestly: at 1.0 the pass adds 32 facts and changes no labelled verdict, at 0.75 it adds 87 facts for two
more useful prompts and 19 more facts into prompts that need nothing, and at 1.5 nothing clears the bar at all. The
labelled set cannot show the gain, because it labels only the pairs the lexical pass produced — the facts the pass
adds on the 35 misses are exactly the unlabelled ones. Judging those pairs is the next step before the default is
tuned; the mechanism, not the number, is what this change ships.

A margin above 1.5 making the pass silent is not a bug: `sqrt(2 ln n)` treats the base as isotropic, while real
embeddings are clustered, so the chance maximum on a small homogeneous base is higher than the formula assumes. The
floor (`MEMCUE_EMBED_MIN_SCORE`) is the per-model answer, at the price of being model-specific.

## Schema

Schema 4 adds `fact_vectors`, keyed by `(id, tag)`. A base opened by this version cannot be read by an older memcue
(`fact base has schema 3, this memcue knows 4` is the error in reverse), so upgrade the binary before pointing it at a
shared base.
