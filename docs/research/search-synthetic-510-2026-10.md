# The search alone on labelled synthetic cases, October 2026 (#510)

Status: **measured 2026-10-27 on a test host**, memcue at `52195fd` (0.20.0 plus one docs commit), `bin/memcue` built from
that tree (version string `dsh-v0.20.0-1-g52195fd-dirty`, sha256 `89af5e3e…6f12f`; the tree had no Go changes).
Every base was a new file in a new scratch directory, and no real prompt, transcript or fact was used. The binary ran
without a sandbox; [Isolation](#isolation) says what the stand does and does not guarantee.

In one line: **on 46 synthetic queries a useful fact is in the top 5 for 15 of 15 short agent queries, 7 of 15
paraphrases and 9 of 10 full prompts; 51% of the returned facts are useful and 42% are noise. `-min-score 0` finds two
more queries at the price of 76% more text and one more misleading fact; `-in all` gains nothing.**

Scope. #510 was narrowed to the search: whether an agent stores a fact, finds it and answers with it is #511, and
nothing here measures it. The earlier end-to-end prototype, its grader and its sampling of private transcripts were
removed from this branch without a result: its figures were not independently verified.

## What is measured

[`search_stand.py --cases`](scripts/search_stand.py) on
[`search_stand/synthetic.json`](scripts/search_stand/synthetic.json): 32 facts of an invented team in three
repositories, 46 distinct queries in three classes — 18 short agent queries, 16 paraphrases without the fact's words,
12 full user prompts. Six of them have no relevant fact in the base.

For each arm the stand creates a directory, three git repositories with the case's project keys and a new base, writes
the facts with `memcue add` and runs every query five times as a full `memcue search -json -n 5` process from its
repository. A process that exits non-zero or times out, an answer that is not a list of facts (the `{"facts": …}`
object since 0.17.0 or the bare list before it; `null` is refused, not read as no facts), a fact the case did not
write, an unknown or doubly-assigned label or a repeated query text stops the run without a result.

### Isolation

What the stand does: memcue sees an allowlisted environment, `PATH` and the locale from the caller and everything else
(`HOME`, base, state, XDG paths, host name) under the arm's directory, so `MEMCUE_API_URL`, its key and the caller's
`MEMCUE_FACTS` are not passed on. A `claude` first on the `PATH` records any call and fails the run. The stand itself
never opens a live base.

What it does not do: the binary is not sandboxed. It can open the network, read or write any path the user can, and
reach a model by any route other than `claude` on the `PATH`; the stand neither blocks nor observes that. An arm with
another `--binary` is trusted to the same degree as running that binary by hand.

For this run: `bin/memcue` built from this tree. Read from the code, not observed: its only network client is the
model endpoint in `internal/extract/api.go`, which `add` uses only when `MEMCUE_API_URL` is set, and `search` calls no
model. The `claude` trap was not called. Whether the live base changed was not checked.

**Labels.** Every query names its useful, related and misleading facts by key. They were written by the case author,
an agent, against all 32 facts before any run; nobody else checked them. Labels are complete: a returned fact with no
label is noise, and a query without a useful fact has no relevant memory. "Unknown" is reserved for partial labels and
is 0 here.

- **useful in top k**: answerable queries with at least one useful fact in the first k. A query that needs three facts
  counts once it has one.
- **precision, noise**: shares of all returned facts in the first k that are useful, unrelated. Related facts count
  in neither.
- **misleading returned**: a stale fact or another system's value in the first k.
- **no-memory queries given facts**: queries with no useful fact that still got something back.
- **~tokens**: characters of the returned facts in the first k over all 46 queries, divided by 3.2. An estimate, not
  a tokenizer.
- **latency**: wall clock of each `memcue search` process, 230 per arm, nearest-rank percentiles.

## Results

| arm | class | useful in top 1 | useful in top 5 | precision at 5 | noise at 5 | misleading at 5 | no-memory queries given facts | ~tokens at 5 | p50 / p90 ms |
|---|---|---|---|---|---|---|---|---|---|
| baseline | short | 13/15 | 15/15 | 71% | 19% | 2 | 1/3 | 818 | 6.4 / 8.0 |
| baseline | paraphrase | 7/15 | 7/15 | 41% | 53% | 0 | 0/1 | 695 | 5.9 / 6.8 |
| baseline | prompt | 7/10 | 9/10 | 41% | 52% | 1 | 2/2 | 981 | 6.2 / 7.4 |
| baseline | all | 27/40 | 31/40 | 51% | 42% | 3 | 3/6 | 2494 | 6.1 / 7.4 |
| `-min-score 0` | all | 28/40 | 33/40 | 31% | 61% | 4 | 3/6 | 4398 | 5.9 / 6.8 |
| `-in all` | all | 25/40 | 31/40 | 43% | 47% | 4 | 4/6 | 2747 | 5.8 / 6.8 |

The full table, per class and for k = 1, 3, 5, is what the command below prints.

Against the baseline, per query, at k=5:

- **`-min-score 0`** gains p05 and f04 and loses nothing; it adds the misleading old staging host to p01. The top 5
  changed for 23 queries, all by facts the default score floor leaves out: noise goes from 42% to 61%.
- **`-in all`** gains and loses nothing at k=5, but at k=1 it loses s13 and p04. It adds another service's Redis port
  to p10 and gives n06 ("elasticsearch index mapping") a fact where the baseline gave none.

The nine baseline misses at k=5: eight paraphrases (p01, p05–p11) that share no word with their fact, such as "which
JavaScript runtime release does the storefront need" for the Node 22 fact, and the prompt f04 "checkout fails since
this morning". The three misleading facts of the baseline (s01, s05, f09) rank first, above the right one: the stale
fact shares the query's words.

Reproducibility. A second run on new bases gave the same facts in the same order for all 138 arm-query pairs and the
same table except latency; no query returned different facts between its five repeats. The slowest of the 690
searches of the first run took 10.8 ms. After both runs the scratch base held 32 active facts and the `claude` trap had
not been called.

## Limits

- 46 queries are one small invented corpus from one author. The counts are exact for these cases and say nothing
  about real bases; a difference of one or two queries between arms is within what another author's labels could
  change.
- The case author also wrote the facts, so short queries share their words by construction; that class is an upper
  bound for real agent queries. Paraphrases are the case author's idea of a query without shared words.
- The base holds 32 facts. A real base of thousands has more competing facts, so precision and latency here are not
  those of a real base.
- Coverage counts a query as served by one useful fact; f05 and f09 need two or three.
- Related facts are neither useful nor noise; a reader of precision should look at the related column too.
- Latency is a short-lived process on the test host with the base in the page cache; it is not an agent's wait for a tool
  call.
- No real data: the real-prompt labels of the earlier draft came from one model judge and were dropped. A real
  measurement needs labels checked by a person and a privacy review first.

## How to repeat

```bash
make build
python3 -I docs/research/scripts/search_stand.py --cases docs/research/scripts/search_stand/synthetic.json \
  --out "$(mktemp -d)/run" --binary bin/memcue \
  --arm baseline --arm 'wide:-min-score 0' --arm 'in-all:-in all' --sweeps 1,3,5 --repeat 5
```

`--out` must be a new path outside the repository; `results.json` there holds every query's facts and timings. An arm
is `NAME[@BINARY][:ARGS]`, so another build is compared with `--arm old@/path/to/memcue`. `make test` runs
`test_search_stand.py`, whose fake memcue covers each refused case and both answer shapes, and whose `MEMCUE_BIN` run
checks that two runs on new bases agree.
