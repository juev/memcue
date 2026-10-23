# How many facts one prompt should get: the MaxFacts cap, October 2026

Status: **measured 2026-10-23; the knob ships, the default stays 2.** Issue #307 asked whether the cap of two facts
throws away facts that would have helped, and why the number is not reachable from the environment. Measured on live
prompts: a third fact enriches an answer that already got a useful fact and never turns a prompt with no useful fact
into one, so the default stays; the cap becomes `MEMCUE_MAX_FACTS` so that another value can be tried on the live path.

## Context

`recall.Default.MaxFacts` is 2 (`internal/recall/recall.go`), applied after the ranking. `recallFor` read only
`MEMCUE_MIN_COVERAGE`, and `-max-facts` existed only on `memcue eval`, so nobody could test another value against the
path a user runs. With the judge configured, 20 candidates are scored and every fact above `MEMCUE_JUDGE_RERANK_MIN`
is eligible — the cap is the only thing that decides how much of that reaches the prompt. On this traffic 64 of 129
prompts have three or more eligible facts, so the question is not academic.

## Method

- **Sample.** `state.db`, `messages.role='user'`, length > 45, deduped by normalized text, sessions with
  `source not in ('cron','subagent')`: 168 prompts. 39 of them are harness notices (`[ASYNC DELEGATION …`,
  `[Cron delivery: …`, `Gateway message origin …`, `[System note …`) and are dropped, leaving 129 real prompts.
- **Base.** A copy of the live base (321 active facts), judge on (Jev through RouterAI), `MEMCUE_STATE_DIR` in a
  scratch directory so no live log is touched.
- **Arms.** `memcue recall -explain` prints the `final:` block with every above-threshold fact in the judge's order,
  marked `served` or `cut by max facts`. Caps 1–4 are therefore prefixes of one run: no re-ranking between arms, and
  the same run answers every arm.
- **Noise floor.** Two identical passes: the same top-2 in 109 of 129 prompts (84 %), the same eligible set in 102
  (79 %), three-or-more 64 against 61, any fact 101 against 100. A difference of one or two prompts is inside this
  spread.
- **Labels.** Blind: prompt, the first assistant reply to it, and the facts of every arm with no arm and no score.
  One label per fact — USEFUL (the reply needed it), RELATED (on topic, not needed), NOISE (off topic, stale or
  wrong) — plus whether the prompt needed memory at all. Main labeler `deepseek-flash`; a second blind labeler
  (`anthropic/claude-sonnet-4.5`) over 40 prompts measures the agreement.

## Results

101 of the 129 prompts get at least one fact, 75 get two, 64 get three (the longest list is 16). Clean sample, 101
prompts with facts:

| cap | facts | useful | related | noise | prompts with ≥1 useful | injected chars |
|---|---|---|---|---|---|---|
| 1 | 101 | 54 (53 %) | 33 | 14 (14 %) | 54 | 9 727 |
| 2 | 176 | 84 (48 %) | 66 | 26 (15 %) | 62 | 16 854 |
| 3 | 240 | 103 (43 %) | 100 | 37 (15 %) | 64 | 22 978 |
| 4 | 295 | 119 (40 %) | 123 | 53 (18 %) | 64 | 28 161 |

1. **The cap does not cost prompt-level coverage.** On the 68 prompts that needed memory, the number with a useful fact
   is 58 at cap 2 and 58 at cap 3; cap 3 adds 16 useful facts to prompts that already had one. The overall 62 → 64
   comes from prompts that needed no memory (4 → 6), which is where the labeler's own noise lives. Cap 4 adds nothing
   at the prompt level either.
2. **The third fact enriches, it does not rescue.** Of the 64 prompts with a third eligible fact it is useful in 19,
   related in 34, noise in 11 — and 17 of those 19 sit next to a pair that already had a useful fact. Two prompts
   whose top-2 held no useful fact gain one from slot 3, both had a related pair before.
3. **The extra slots are not dirtier than the second one.** Noise share is flat at 14–15 % for caps 1–3 (18 % at cap
   4); what falls is the useful share, 53 % → 43 %.
4. **The price is small:** cap 3 injects 61 more characters per prompt on average, +37 % of the injected text, about
   15 tokens.
5. **Misses are not a cap problem.** 28 of 129 prompts get no fact at all; that is the candidate list and the gate,
   not the cut. A third of the sample needed no memory, and those prompts still receive facts — the cost this note
   cannot fix.

### How much the labels matter

Exact agreement between the two blind labelers is 63 % of 131 pairs, Cohen's kappa 0.43; the disagreement is almost
all RELATED↔USEFUL (26 pairs) and RELATED↔NOISE (16), and `claude` is the more generous reader: of the 54 pairs the
first labeler called useful 51 are confirmed, and it adds 26. On the same 40 prompts the arms behave the same way
under both:

| labeler | cap 2: useful / prompts with a useful fact | cap 3: useful / prompts with a useful fact |
|---|---|---|
| deepseek-flash | 33 / 26 | 45 / 27 |
| claude-sonnet-4.5 | 49 / 33 | 70 / 33 |

The per-fact counts are soft; "no prompt-level gain, enrichment only" holds under both.

## Verdict

The default stays 2. The step from two to three is the weakest measured (1 → 2 adds 30 useful facts and 8 prompts,
2 → 3 adds 19 and 2, 3 → 4 adds 16 and 0), and a per-fact label does not show an answer getting better — only the
prompt getting more context to work from. What was missing is the ability to try: `MEMCUE_MAX_FACTS` (1..10, anything
else ignored, `0` does not mean "no cap") now reaches `recallFor` and is passed through by the Hermes plugin as
`max_facts`. A score margin for the extra slot was not proposed: Jev flips the pair between identical runs (#298,
[judge-cut-2026-10.md](judge-cut-2026-10.md)), so the margin needs its own measurement first.

## Limitations

- One turn per prompt. The effect of an extra fact on the answer itself was not measured; that takes an end-to-end run
  with and without memory, as in [evaluation.md](../evaluation.md).
- The sample is Hermes traffic on one host, mostly Russian, and 101 prompts with facts is small: ±1–2 prompts is
  inside the run-to-run spread above.
- The 28 prompts that get no fact at all are out of scope here; #221 holds them.

## Reproduction

```sh
cp /opt/data/memcue/facts.db /tmp/facts.db   # a copy: recall opens and may migrate the base it is given
python3 docs/research/scripts/max_facts.py prompts --db ~/.hermes/state.db --out work/prompts.json
python3 docs/research/scripts/max_facts.py arms --bin ./bin/memcue --base /tmp/facts.db \
    --prompts work/prompts.json --out work/pass1.json --state work/state1
python3 docs/research/scripts/max_facts.py arms --bin ./bin/memcue --base /tmp/facts.db \
    --prompts work/prompts.json --out work/pass2.json --state work/state2
python3 docs/research/scripts/max_facts.py label-tasks --prompts work/prompts.json --arms work/pass1.json \
    --cap 4 --out work/label-tasks.json   # label them blind, then merge-labels
python3 docs/research/scripts/max_facts.py report --prompts work/prompts.json \
    --arms work/pass1.json work/pass2.json --labels work/labels.json
```

`report` prints the three blocks above — the sample sizes and the noise floor, the arms table, the third-fact
breakdown and the needed/not-needed split. The judge comes from `MEMCUE_JUDGE_URL` / `_MODEL` / `_KEY`; without it
only the words decide and the arms collapse to one or two facts.
