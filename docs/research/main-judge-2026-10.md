# The main model in the judge's roles, October 2026

Status: **measured 2026-10-22; keep Jev in both roles.** Issue #274 asked whether the main model (`deepseek-flash`,
reasoning off) can answer the judge's typed questions in the two roles Jev still has: recall rerank and dream triage.
If it could, memcue would need one provider instead of two. Opus labelled both samples, so neither side is graded by
its own family. Aggregate numbers only: the base holds host names, paths and private repositories.

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

## Decision

- **Rerank stays on Jev.** The main model gives no quality gain at any threshold, takes three times as long while the
  user waits, and changes its answer more between runs.
- **Triage stays on Jev.** At the send rates where it saves anything, the main model loses a third (main-json) to more
  than half (main-lp) of the groups Opus would act on.
- No adapter is added to memcue. The scripts stay in [scripts/](scripts/) for a rerun with another main model.

## Limits

- One main model (`deepseek-flash`), one base and one prompt set; a larger model in the main role may behave
  differently, as the outside eval suggests.
- Opus is one labeller; no agreement between labellers was measured on these samples.
- Only the served pairs were labelled. "None" means the reader served nothing, not that the base had nothing.
- 150 triage groups with 29 positives: a difference of two or three kept groups is within the spread.
- Latency was measured with four requests in parallel from one Mac; the hook sends one.

## Found along the way

Under the Opus labels, Jev at 0.7 serves a third of the NOISE it serves at 0.5 (12–14 against 40–44) and loses 8
useful top-1 prompts, while 33–36 more prompts get nothing. Whether the default threshold should move is a separate
question (gold set labels of 2026-10-21 came from `deepseek-flash`, not Opus).

## Reproducing

```bash
go run -tags research ./docs/research/scripts/judgecand FACTS.db eval.json > cand.jsonl
python3 -I docs/research/scripts/judge_ab.py cand.jsonl jev jev-1.jsonl          # also main-lp, main-json
python3 -I docs/research/scripts/judge_ab_report.py label  cand.jsonl labels.json jev-1.jsonl main-json-1.jsonl ...
python3 -I docs/research/scripts/judge_ab_report.py report cand.jsonl labels.json jev-1.jsonl main-json-1.jsonl ...

MEMCUE_FACTS=COPY.db MEMCUE_STATE_DIR=STATE memcue dream -all -list > groups.md
python3 -I docs/research/scripts/triage_ab.py sample groups.md 150 274 sample.json
python3 -I docs/research/scripts/triage_ab.py ask    sample.json jev t-jev-1.jsonl   # also main-lp, main-json
python3 -I docs/research/scripts/triage_ab.py label  sample.json tlabels.json
python3 -I docs/research/scripts/triage_ab.py report sample.json tlabels.json t-jev-1.jsonl ...
```

The readers take `MEMCUE_JUDGE_*` and `MEMCUE_API_URL`, `MEMCUE_MODEL`, `MEMCUE_API_KEY` from the environment; the
labels need `claude` on the path.
