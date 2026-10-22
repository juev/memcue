# Recall misses: where a useful fact is lost, October 2026

Issue #221 asked why a fact the base holds does not reach the prompt that needs it. This note finds the step that
loses the most such facts on the gold set of [gold-set-2026-10.md](gold-set-2026-10.md), changes that step, and
measures again.

## Setup

- **Prompts and labels:** the gold set of 180 real prompts and its labels (DeepSeek Flash, one judge): for 157 of the
  prompts a USEFUL fact is somewhere in the candidate pool.
- **Base:** the copy of the Mac base the gold set was built on (2,571 active facts), with bge-m3 vectors from a local
  Ollama.
- **Recall:** the full path as configured on the author's machines: the words, the vector candidates, and the Jev
  judge (`typesafe/jev-1.14` on RouterAI, minimum 0.5, ten candidates from each source, two facts served).
- **Method:** `memcue recall -explain` for every prompt
  ([scripts/recall_explain.py](scripts/recall_explain.py)), then
  [scripts/recall_misses.py](scripts/recall_misses.py) counts the prompts that were served a USEFUL fact and, for
  every prompt that was not, the furthest step any of its USEFUL facts reached. A change lets the judge read facts
  the pool never held; those served pairs (39 over all runs below) were labelled by the same judge with the same
  prompt (`gold_set.py label`), so every served fact of the compared runs has a label.
- Every configuration ran twice; the judge is not fully deterministic, and two runs of the same build differ by about
  one prompt and four noise facts.

## Where the misses were

`main` at 848419c, 180 prompts:

| Served a USEFUL fact (top-1 / any) | USEFUL | RELATED | NOISE |
|---|---|---|---|
| 101 / 110 | 172 | 62 | 22 |
| 100 / 111 | 171 | 63 | 18 |

The 47 prompts with a USEFUL fact in the pool and none served (first run):

| Furthest step a USEFUL fact reached | Prompts |
|---|---|
| the words found it, the term gate turned it away, and it was not in the vector top ten | 19 |
| the judge scored it below 0.5 | 17 |
| neither the words nor the vectors found it | 5 |
| it passed the judge and was cut by the two-fact limit | 4 |
| recall stopped before searching (too few known terms) | 2 |
| the ticket gate turned it away | 1 |

The largest share is the lexical gate. It was tuned on a development set for recall by words alone, where it is the
only filter. With a judge, the gate decides which facts the judge may read at all: a fact that holds two of three
prompt terms never reaches it, however well it answers.

## What was tried

| Change | Served a USEFUL fact (top-1 / any) | NOISE |
|---|---|---|
| `main` | 100–101 / 110–111 | 18–22 |
| judge minimum 0.4 instead of 0.5 | 105 / 116 | 29 |
| 20 candidates from each source instead of 10 | 98 / 115 | 19, plus 35 unlabelled |
| **with a judge, facts below the gate take the free candidate places** | **114–116 / 121–124** | **17–18** |

- A lower judge minimum serves more useful facts and more noise with them.
- Twenty candidates from each source let the vector reader push out lexical candidates; the gain is smaller and part
  of it is not labelled.
- The kept change: with a judge, the candidates that passed the gate come first as before, and while they are fewer
  than the candidate limit, the free places go to the facts the gate turned away, highest coverage first. Recall
  without a judge keeps the gate. The judge then reads 18 facts a call on average instead of 12 (at most 20, as
  before); the recall time over 30 prompts was the same within the run-to-run spread (median 577–584 ms against
  598–648 ms).

After the change, the remaining misses (first run): the judge below 0.5 — 18, the two-fact cut — 5, not found — 5,
the term gate with every candidate place taken — 3, stopped before searching — 2, the ticket gate — 1.

## Not covered

- The three prompts from the issue come from the Hermes base on loffe, which this set does not hold. The diagnosis in
  the issue found their cause (facts without vectors, fixed by #223) and a second one this set does not measure: the
  judge scores relevance, not freshness, so a stale fact can win over the current one.
- The judge's misses are now the largest group. Its minimum is a trade between useful facts and noise (the 0.4 row);
  moving it needs a larger labelled set than one judge's labels on 180 prompts.
- Labels are one model's opinion, as in the gold set note.
