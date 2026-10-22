# The main model in the judge's roles, October 2026

Status: **measured 2026-10-22 in two rounds.** Issue #274 asked whether the main model (`deepseek-flash`) can answer
the judge's questions in the two roles Jev still has: recall rerank and dream triage. If it could, memcue would need
one provider instead of two. The first round asked the main model Jev's own question, a probability per fact or
group, and it lost in both roles. The second round (#279) changed the shape of the question: for rerank, the main
model picks at most two facts from the list (**listwise**), and in this shape it matches Jev on useful prompts with
less noise. For triage, it names the pairs that need an action. Both samples were labelled blind by Opus and again by
DeepSeek. Aggregate numbers only: the base holds host names, paths and private repositories.

## Why measure again

The A/B of #66 ([main-reranker-2026-10.md](main-reranker-2026-10.md)) left reasoning on (`main_rerank.py` sends no
`thinking` switch), gave the judge 10 candidates on a base without expanded queries, and was graded by labels from the
main model's own family. Recall now gives 20 candidates in BM25 order on expanded facts (#232), and the main model
has run with reasoning off since #259. Another reason was an outside eval (Telegram, #ddeval): a decisions solver
built on a large model proposed fewer unneeded changes than Jev on a skill-tuning task. That is a different task, so
it only gave a reason to measure.

## How the main model answers a typed question

The main model gets the same question Jev gets and returns a probability, so the thresholds keep their meaning. Two
ways were tried:

- **logprobs** (`main-lp`): the model answers one token per item (`yes`/`no` for rerank, a letter for triage), and the
  probability is read from `top_logprobs`. DeepSeek returns logprobs with reasoning off, but at temperature 0 they
  are degenerate (0 for the chosen token, −9999 for every other), so this way runs at the default temperature and the
  sampled answer changes between runs.
- **written probabilities** (`main-json`): temperature 0, JSON output; the model writes a score from 0 to 1 per fact,
  or the three option probabilities per group.

## Recall rerank

The same candidate lists for every reader: 180 prompts of the gold set (`gold-set-2026-10.md`), a copy of the live
base (8,161 approved facts, 2,767 of them with expanded queries), the first 20 facts the words found in BM25 order, as
`RecallJudge` picks them ([judgecand](scripts/judgecand/main.go)). 171 prompts had at least two candidates, 3,377
pairs. One request per prompt with the whole list, as recall sends it, four in parallel
([judge_ab.py](scripts/judge_ab.py)). Jev got the request of `internal/rerank`, without a policy; the main model got a
short policy (a fact helps when the answer would use it).

Each reader served what recall would: facts at or above the threshold, approved before provisional, by score, at most
two. Opus (`claude -p`, `claude-opus-5-5`) labelled every pair any reader served at 0.3, 0.5 or 0.7: 511 pairs in 21
calls, $2.93, blind to the reader ([judge_ab_report.py](scripts/judge_ab_report.py)). Labels are those of the gold
set: USEFUL, RELATED, NOISE. "Top-1" and "any" count the prompts whose first or any served fact is USEFUL; "none" counts
the prompts served nothing.

| Reader, threshold | Top-1 | Any | USEFUL | RELATED | NOISE | None |
|---|---:|---:|---:|---:|---:|---:|
| **Jev 0.5** (the default), two runs | **92** | **101** | 154–155 | 74–77 | 40–44 | 23–25 |
| Jev 0.7 | 84 | 90–91 | 136–139 | 45–46 | 12–14 | 58–59 |
| main-json 0.3 | 85–88 | 96–100 | 148–152 | 64–68 | 21–24 | 39–41 |
| main-json 0.5 | 81–84 | 92–94 | 144–146 | 50–52 | 11–12 | 54–58 |
| main-lp 0.3 | 84–85 | 96–100 | 138–147 | 65–69 | 32–35 | 30–36 |
| main-lp 0.5 | 79–83 | 92–93 | 135–139 | 55–57 | 24 | 47–48 |

Paired at top-1:

| Comparison | Main rescues | Main loses |
|---|---:|---:|
| Jev 0.5 against main-json 0.3, run 1 | 6 | 13 |
| Jev 0.5 against main-json 0.5, run 1 | 6 | 17 |
| Jev 0.7 against main-json 0.5, run 1 / run 2 | 10 / 11 | 13 / 11 |
| run 1 against run 2 at 0.5: Jev / main-json / main-lp | 2 / 7 / 5 | 2 / 4 / 9 |

Time and tokens per run of 171 prompts:

| Reader | Input | Output | Median | p95 | Over 2 s |
|---|---:|---:|---:|---:|---:|
| Jev | 608k | 60k | 0.52–0.53 s | 0.93–1.08 s | 0–2 |
| main-lp | 337k | 10k | 1.42–1.43 s | 1.69–1.70 s | 0–4 |
| main-json | 342k | 18k | 1.47–1.50 s | 1.75–1.80 s | 0–5 |

- The main model is a stricter judge, not a better one. main-json at 0.5 lands where Jev lands at 0.7: the same useful
  prompts (81–84 against 84) and the same NOISE (11–12 against 12–14), with as many rescues as losses. At any threshold
  it does not reach Jev's 92 useful prompts.
- It takes three times as long, and its p95 of 1.7–1.8 s sits next to the 2 s timeout of the rerank request.
- It is less stable: 11 top-1 prompts changed between two runs of main-json at temperature 0, against 4 for Jev.
- It reads about 45% fewer input tokens: Jev's 608k include the 20 typed questions in every request.

## Dream triage

150 of the 453 groups that `memcue dream -all -list` gives on the same copy, drawn at random (seed 274). Jev got the
request of `extract.TriageGroup`; the main model got the same policy and the three options
([triage_ab.py](scripts/triage_ab.py)). Opus answered the same question for every group, blind to the readers: 15 calls,
$2.81, 29 groups that need an action.

A group goes on to the dream model when P(duplicate) + P(outdated) reaches the threshold (`MEMCUE_JUDGE_TRIAGE_MIN`,
0.2 by default). "Kept" counts the 29 groups Opus would act on that were sent.

| Reader, two runs | 0.1: sent / kept | 0.2: sent / kept | 0.3: sent / kept | 0.5: sent / kept |
|---|---|---|---|---|
| **Jev** | 94–98 / 28 | **76–77 / 26–27** | 63–66 / 25 | 45–46 / 22–23 |
| main-lp | 32–34 / 14–15 | 26 / 12 | 19–23 / 8–12 | 15–18 / 8–9 |
| main-json | 126–131 / 28–29 | 38–44 / 19–20 | 34–35 / 16–19 | 29–30 / 14–15 |

Jev: 147k input tokens, 0.50–0.53 s median per group; main: 76–83k input, 0.97–1.15 s.

The main model answers "none" too readily. Its probabilities are almost binary: at any threshold where it keeps as many
of the Opus groups as Jev does at 0.2, it sends almost every group (main-json at 0.1: 126–131 of 150). A triage that
passes everything saves nothing; one that loses a third of the needed actions is wrong.

## Second round: questions that suit a chat model

The first round made the main model imitate Jev: a probability for every fact. A chat model is not trained to give
calibrated probabilities; it is trained to compare and choose. The second round (#279) used the same candidates, groups
and readers' scripts with three changes:

- **listwise rerank** (`main-list`): the model gets the 20 candidates and answers `{"facts": [n, ...]}`, at most two
  facts, the most helpful first, or none. No threshold: what it picks is served.
- **triage by pairs** (`main-pairs`): the model names the pairs of the group that are duplicates or outdated; a group
  with a pair goes on to the dream.
- **short reasoning** (`-low`): the same two shapes with `reasoning_effort: "low"` instead of reasoning off.

Both rounds were then labelled a second time by `deepseek-flash` with the Opus instructions, reasoning at its default:
598 pairs in 22 calls and the 150 groups in 15 calls. Opus labelled the 87 new pairs ($1.11).

### Rerank

Jev at its default threshold of 0.5 against the new readers; two runs each:

| Reader | Opus: top-1 / any | Opus: NOISE | DeepSeek: top-1 / any | DeepSeek: NOISE | Median | p95 | Over 2 s |
|---|---|---:|---|---:|---:|---:|---:|
| Jev 0.5 | 92 / 101 | 40–44 | 105–106 / 115–117 | 22–26 | 0.52 s | 0.93–1.08 s | 0–2 |
| main-json 0.5 | 81–84 / 92–94 | 11–12 | 91–95 / 100–102 | 4–5 | 1.47–1.50 s | 1.75–1.80 s | 0–5 |
| **main-list** | **94 / 104** | **25–27** | **103 / 111** | **7–9** | **1.22–1.27 s** | **1.50 s** | **0** |
| main-list-low | 98–100 / 106–109 | 33 | 115–118 / 123–125 | 13–14 | 2.62–2.72 s | 4.55–5.84 s | 146–149 |

Paired at top-1 against Jev 0.5, same run number:

| Reader | Opus: rescues / losses | DeepSeek: rescues / losses |
|---|---|---|
| main-list, run 1 / run 2 | 9 / 7, 10 / 8 | 12 / 14, 10 / 13 |
| main-list-low, run 1 | 12 / 6 | 20 / 7 |

- **main-list matches Jev on useful prompts and serves a third less NOISE (Opus) to two thirds less (DeepSeek).** Opus
  puts it 2 prompts ahead of Jev, DeepSeek 2–3 behind; the paired rescues and losses are close to even under both.
- It is stable: the two runs served the same first fact for 167 of 171 prompts and the same list for 161, against 153
  and 132 for Jev; the useful top-1 prompts did not change at all.
- It reads 339k input tokens per run against Jev's 608k and writes 1.4k.
- It is slower than Jev: 1.2–1.3 s median against 0.5 s, p95 1.5 s, with no request over the 2 s timeout.
- main-list-low is the best reader under both labellers, but it takes 2.6–2.7 s at the median and almost every request
  is over the 2 s timeout, so it does not fit recall.
- main-list served two facts on 128–130 prompts and nothing on 36: it picks two whenever it picks.

### Triage

| Reader, two runs | Sent | Opus: kept of 29 | DeepSeek: kept of 25 | Median |
|---|---:|---|---|---:|
| Jev 0.2 (the default) | 76–77 | 26–27 | 22 | 0.50–0.53 s |
| Jev 0.5 | 45–46 | 22–23 | 18–20 | |
| main-json 0.2 | 38–44 | 19–20 | 16–18 | 1.15 s |
| main-pairs | 41–44 | 18–20 | 18–19 | 1.09–1.15 s |
| main-pairs-low | 31–32 | 21–22 | 19 | 4.16–4.22 s |

Naming pairs helps reasoning more than it helps the plain model. main-pairs-low sends 31–32 groups and keeps as many
needed groups as Jev does at 0.5 with 45–46; it still loses 7–8 of the 29 Opus groups (6 of the 25 DeepSeek ones)
that Jev at 0.2 keeps all but 2–3 of. Its median of 4 s does not matter in the background, but one group took 22 s
and 3.8k output tokens, and the two runs wrote 190–217k output tokens against Jev's 6k.

### The two labellers

On the 598 rerank pairs Opus and DeepSeek agree on 65% of the labels (kappa 0.43). DeepSeek is more generous: it calls
328 pairs USEFUL against 258 for Opus, mostly pairs Opus calls RELATED (88). On the 150 groups they agree on 134 about
whether an action is needed: 19 both, 10 Opus only, 6 DeepSeek only. The ranking of the readers is the same under both
labellers; the absolute numbers are not comparable between them.

## Decision

- **Rerank: listwise main model can replace Jev.** It serves as many useful prompts, much less noise, the same answer
  on every run, and fits the timeout. The cost is 0.7 s more per prompt while the user waits. Whether to switch is a
  product decision; it is not made in this note.
- **Triage stays on Jev.** No shape of the main model keeps the needed groups as well as Jev at 0.2 while sending
  fewer of them, short of reasoning, which writes over 30 times Jev's output tokens and still loses 7–8 groups of 29.
- The first-round shapes (one probability per fact or group) are not worth pursuing: they lose in both roles.

## Limits

- One main model (`deepseek-flash`), one base and one prompt set; a larger model in the main role may behave
  differently, as the outside eval suggests.
- Two labellers with moderate agreement (kappa 0.43); neither is ground truth.
- Only the served pairs were labelled. "None" means the reader served nothing, not that the base had nothing.
- 150 triage groups with 25–29 positives: a difference of two or three kept groups is within the spread.
- Latency was measured with four requests in parallel from one Mac; the hook sends one.

## Found along the way

Under the Opus labels, Jev at 0.7 serves a third of the NOISE it serves at 0.5 (12–14 against 40–44) and loses 8
useful top-1 prompts, while 33–36 more prompts get nothing. Whether the default threshold should move is a separate
question (gold set labels of 2026-10-21 came from `deepseek-flash`, not Opus).

## Reproducing

```bash
go run -tags research ./docs/research/scripts/judgecand FACTS.db eval.json > cand.jsonl
python3 -I docs/research/scripts/judge_ab.py cand.jsonl jev jev-1.jsonl  # also main-lp, main-json, main-list, main-list-low
python3 -I docs/research/scripts/judge_ab_report.py label  cand.jsonl labels.json jev-1.jsonl main-json-1.jsonl ...
python3 -I docs/research/scripts/judge_ab_report.py report cand.jsonl labels.json jev-1.jsonl main-json-1.jsonl ...

MEMCUE_FACTS=COPY.db MEMCUE_STATE_DIR=STATE memcue dream -all -list > groups.md
python3 -I docs/research/scripts/triage_ab.py sample groups.md 150 274 sample.json
python3 -I docs/research/scripts/triage_ab.py ask    sample.json jev t-jev-1.jsonl  # also main-lp, main-json, main-pairs, main-pairs-low
python3 -I docs/research/scripts/triage_ab.py label  sample.json tlabels.json
python3 -I docs/research/scripts/triage_ab.py report sample.json tlabels.json t-jev-1.jsonl ...
```

The readers take `MEMCUE_JUDGE_*` and `MEMCUE_API_URL`, `MEMCUE_MODEL`, `MEMCUE_API_KEY` from the environment; the
Opus labels need `claude` on the path, and `LABELLER=deepseek` in front of a `label` command asks the main model
instead.
