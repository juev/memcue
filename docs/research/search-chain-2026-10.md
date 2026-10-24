# The lexical search chain: what each step does to the answer, October 2026

Status: **measured 2026-10-24 ([#330](https://github.com/juev/memcue/issues/330)); nothing in the chain loses an answer
today; serving in BM25 order instead of by coverage is measured better on Claude Code; the per-term count is #337.** With recall local by default (#331), the words are the whole decision. They
go through two rankers and a cut between them. FTS5 returns facts by BM25 over any prompt term and cuts at 80. The
gates in Go then keep the facts that hold enough terms. Coverage picks two of those. On 1,724 Claude Code prompts and
168 Hermes prompts the cut at 80 never changed what a prompt got, and FTS5 and the gate never disagreed on a term. The
two rankers disagree on 30–42% of the prompts that get facts. On the 409 Claude Code prompts where they disagree,
labelled blind, the BM25 order serves a useful fact to 205 prompts against 180, with 30% noise against 41%. The one real cost is the per-term count, up to 24 ms on a long prompt.

## The chain

1. `text.Terms(prompt)`. `Store.DocFreq` runs one `MATCH` per term to weigh it. Terms no fact holds are dropped, and a
   long prompt keeps its 24 rarest.
2. `Store.Search`: one `MATCH "a"* OR "b"* OR ...`, `ORDER BY bm25`, `LIMIT 80`.
3. `recallCandidates`: seen, expired and other-ticket facts are skipped. A fact passes when it holds `MinMatched`
   distinct prompt terms (3, fewer for a short prompt) and at least 0.15 of their IDF weight. `text.Covers` decides
   whether a fact holds a term.
4. `Recall`: approved before provisional, then coverage, then place (host, session repository, global), then the newer
   fact. Two facts are served.

## Method

[chain](scripts/chain/main.go) runs recall on a copy of a base twice per prompt: with the shipped limit of 80 and with
none. It records:

- the facts that pass only without the limit, and their place by coverage among all passing facts;
- the facts FTS5 returned that hold no prompt term by `text.Covers`;
- the BM25 rank of each served fact, and the first passing facts in BM25 order;
- the time of `DocFreq`, of `Search`, and of the whole recall.

Recall ran words-only: no judge, `MEMCUE_NUMBER_TERMS` unset, no seen set, `Today` 2026-10-23.

Two bases:

- **Claude Code**: the archived base of 2026-10-23 (2,769 active facts). The prompts are 1,724 typed prompts from the
  transcripts in `~/.claude/projects`, collected by [prompt_pool.py](scripts/prompt_pool.py) with no gold set
  left out. Run on darwin/arm64.
- **Hermes**: the copy of the live base and the 168 prompts of the scope-filter measurement in #327. 113 of those
  prompts carry 322 `(prompt, fact)` labels U/R/N from a blind judge, made for the first four facts of the judge's
  pool. Run on loffe.

## Results

| | Claude Code | Hermes |
|---|---|---|
| prompts / searched | 1,724 / 1,624 | 168 / 164 |
| prompts that got a fact | 966 | 99 |
| facts passing the gates per searched prompt | median 1, mean 5.1 | median 1, mean 3.0 |
| facts FTS5 returns without the limit | median 320, p95 923 | median 78, p95 210 |

### 1. The limit of 80: harmless

| | Claude Code | Hermes |
|---|---|---|
| prompts where a passing fact sits below rank 80 | 58 (3.6%) | 1 |
| facts cut that way | 778 | 2 |
| prompts whose served facts change without the limit | **0** | **0** |
| cut facts among the first 5 / 10 by coverage | 2 / 3 | 0 / 0 |
| deepest BM25 rank of a passing fact | 682 | 113 |

The prompts that lose facts are long: a median of 13 known terms against 6 for all prompts, and a pool of 921 facts.
What they lose is the tail of a pool where 44–50 facts pass the gates. A later prompt of the same session skips the
facts already served, and could reach the third, fourth or fifth fact. That happens for 2 facts of 1,724 prompts.

**Decision proposed: keep the limit.** Without it the median search grows from 2.7 to 4.1 ms and p95 from 6.1 to
17.3 ms. The scope pre-filter of #327 still belongs inside the query, before the limit: a filter applied after it
would cut a pool that is already cut.

### 2. Two rankers: they disagree, and the labels cannot say who is right

BM25 orders the pool and coverage chooses from it. Served facts mostly come from the top of the BM25 order: in Claude
Code 1,013 of 1,699 sit at rank 0 or 1, 1,553 within the first 10 and 1,661 within the first 20. On Hermes the
numbers are 116, 161 and 167 of 167. The two served facts equal the first two passing facts in BM25 order in 557 of
966 Claude Code prompts (58%) and 69 of 99 Hermes prompts (70%).

On the labelled Hermes prompts:

| two facts per prompt | U | R | N | unlabelled | prompts with a U |
|---|---|---|---|---|---|
| by coverage (today) | 43 | 27 | 7 | 55 | 37 of 113 |
| first two passing in BM25 order | 44 | 29 | 6 | 53 | 37 of 113 |
| the 23 prompts where they differ: coverage | 9 | 10 | 1 | 26 | 8 |
| the 23 prompts where they differ: BM25 | 10 | 12 | 0 | 24 | 8 |

The two orders tie. 40% of the picks have no label: the labels cover the judge's pool, not the words'. One blind
judge and 23 differing prompts cannot carry a decision.

**Labelled on Claude Code (2026-10-24): BM25 order is better.** All 409 Claude Code prompts on which the two orders
pick different facts were labelled by [rank_label.py](scripts/rank_label.py). For each prompt, Claude Opus 5.5 got the
union of both picks through `claude -p`, shuffled, without knowing which order proposed which fact. The labels are U/R/N
as in the Hermes set, plus whether the prompt needs memory at all. There is no assistant reply in these transcripts,
so the judge saw the prompt alone. Cost: $40.48.

| two facts per prompt, 409 prompts | U | R | N | noise | prompts with a U | prompts with only N |
|---|---|---|---|---|---|---|
| by coverage (today) | 215 | 265 | 338 | 41% | 180 | 96 |
| first two passing in BM25 order | 262 | 314 | 242 | 30% | **205** | 69 |
| the 253 prompts that need memory: coverage | 210 | 163 | 133 | 26% | 175 | 25 |
| the 253 prompts that need memory: BM25 | 251 | 175 | 80 | **16%** | **194** | 13 |

Paired by prompt, only BM25 has a useful fact in 59 prompts and only coverage in 34; both have one in 146, neither in
170. The two-sided sign test on the 93 discordant prompts gives p = 0.012. Six facts of the base are provisional, and
only 3 of the 409 prompts touch one, so the approved-before-provisional order does not explain the gap.

Read plainly: the coverage gate is a good filter, and the coverage sort is a worse ranker than the BM25 order it
re-sorts. The Hermes tie above does not contradict this: it rests on 23 prompts, 40% of them unlabelled.

**Proposed:** serve the facts that pass the gates in BM25 order, still approved before provisional. Coverage stays as
the gate and as the trace's number. This is a change of behaviour for every prompt, so it comes as its own issue, with
the Hermes set rerun on the new order.

### 3. FTS5 against the gate's term test: they agree

On all 1,788 searched prompts, every fact FTS5 returned holds at least one prompt term by `text.Covers`. `Query` and
`Covers` share `text.Prefix`, so the prefix query and the Go test cannot drift on the word forms measured here.
Number terms (`MEMCUE_NUMBER_TERMS=1`) were not part of this run. **Nothing to fix.**

### 4. The per-term count: cheap on short prompts, the largest step on long ones

| | Claude Code | Hermes |
|---|---|---|
| `DocFreq`, median / p95 | 0.28 / 1.27 ms | 1.21 / 13.0 ms |
| `Search`, median / p95 | 2.67 / 6.07 ms | 2.69 / 4.62 ms |
| recall, median | 3.8 ms | 5.6 ms |
| prompt length, median | 87 B | 147 B |

`DocFreq` runs before the cut to 24 terms, so it counts every distinct term of a prompt of up to 4,000 characters. The
slowest Hermes prompt (5,481 B, 24 known terms) spent 24 ms there. A pasted log is exactly the prompt where most of
those counts buy nothing.

**Open, [#337](https://github.com/juev/memcue/issues/337).** The candidates are counting the terms in one query, or capping the number of terms before counting them.
The cap has to keep the rarest terms, and rarity is what the count measures, so it needs a cheaper proxy such as term
length or the stemmer's output. Either one wants a measurement against the same prompts.

## Limitations

- The Claude Code labels cover only the 409 prompts where the two orders differ, with one judge and no assistant
  reply. 156 of them do not need memory at all (many are delegation instructions from `claude -p`).
- The Hermes labels were made for the judge's pool. Words-only picks outside that pool are unlabelled.
- The times come from two machines: Claude Code on darwin/arm64, Hermes on loffe. They compare steps within one
  machine, not one machine against the other.
- The seen set was empty. A real session skips the facts already served, which is the only way the cut at 80 could
  matter, and it was bounded above by the coverage ranks instead of replayed.

## Reproducing

```sh
python3 -I docs/research/scripts/prompt_pool.py /dev/null prompts.json ~/.claude/projects
cp ~/.claude/archive/memcue-2026-10-23/facts.db cc.db       # always a copy: recall may migrate the base
env -u MEMCUE_NUMBER_TERMS go run -tags research ./docs/research/scripts/chain cc.db prompts.json > chain.jsonl
python3 -I docs/research/scripts/rank_label.py chain.jsonl cc.db labels.json   # claude -p, resumable
```

For Hermes, cross-compile the tool (`GOOS=linux GOARCH=amd64 go build -tags research`) and run it on a copy of
`facts-base.db`. Turn `prompts_all.json` of the scope-filter measurement into `{"prompts":[{"id","text"}]}` first.
The totals go to stderr. Per-prompt rows carry `lost`, `served_rank`, `bm25_top` and the times.
