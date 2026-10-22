# Skipping the judge on a strong lexical hit, October 2026

Status: **measured 2026-10-22; no gate.** Issue #290 (from the Hermes report #287) asked whether recall can leave the
judge out when the words alone found a strong hit, since the judge costs about 0.4 s in an isolated probe and 0.9 s at
the median in the live Hermes gateway. A gate that loses no useful prompt saves the call on 5% of prompts; gates that
save more lose useful prompts and add noise.

## Method

The labelled set of #274/#279 ([main-judge-2026-10.md](main-judge-2026-10.md)): 171 gold set prompts with at least two
candidates on a copy of the live base, Jev scores from two runs, Opus labels. [judgecand](scripts/judgecand/main.go)
now also writes the words' own answer for each prompt: the facts recall serves without a judge, with their coverage.

A gate is a rule on that answer. Where it holds, the prompt gets the words' answer and the judge is not called;
elsewhere it gets Jev's answer at the default threshold of 0.5. Opus labelled the 108 pairs the words serve that had
no label (10 calls, $1.54), blind and with the same instructions ([judge_gate.py](scripts/judge_gate.py)).

## Results

"Skip" counts the prompts that do not call the judge. The prompts with fewer than two candidates are outside the set:
recall does not call the judge on them anyway.

| Gate | Skip | Top-1 | Any | USEFUL | RELATED | NOISE |
|---|---:|---:|---:|---:|---:|---:|
| none (Jev on every prompt) | 0 | 92 | 101 | 154–155 | 74–77 | 40–44 |
| top coverage ≥ 0.5 | 29 | 86–87 | 96–98 | 141–145 | 78–83 | 49–54 |
| top coverage ≥ 0.6 | 19 | 88–89 | 97–99 | 144–148 | 73–78 | 48–52 |
| top coverage ≥ 0.7 | 13 | 90–91 | 99–101 | 147–151 | 73–78 | 44–48 |
| top coverage ≥ 0.7, two facts | 10 | 91–92 | 100–102 | 150–154 | 73–78 | 43–47 |
| top coverage ≥ 0.8 | 9 | 92–93 | 101–103 | 152–156 | 72–77 | 42–46 |
| words only | 171 | 44 | 54 | 76 | 62 | 66 |

The top lexical coverage of the 111 prompts with a lexical answer: median 0.33, 75th percentile 0.51, 90th 0.71.

- A strong lexical hit is rare. Only 13 of the 171 prompts have a top coverage of 0.7 or more, so no gate skips many calls.
- Where the gate skips more, it loses. At coverage 0.5 it skips 29 calls (17%) and loses 5–6 useful top-1 prompts and
  10–13 USEFUL facts, with 9–10 more NOISE: a high coverage often belongs to a fact that repeats the prompt's words
  without answering it, and the judge is what turns those away.
- At coverage 0.8 it loses nothing and skips 9 calls (5%), which does not change the median or the p90 of a session.

## Decision

No gate. Recall keeps calling the judge whenever the words found at least two candidates.

The latency the report measured in the gateway (0.9 s median against 0.4 s in an isolated probe) is a separate question.
The report suggests a new TLS handshake per process, since the hook is a new process each turn. That is a hypothesis,
not measured here.

## Limits

- One base, one prompt set and one labeller; the Hermes base is smaller (265 facts) and its prompts are different, so
  its share of strong hits may differ.
- The words' answer was labelled only where a gate serves it; the table has no unlabelled pairs.

## Reproducing

```bash
go run -tags research ./docs/research/scripts/judgecand FACTS.db eval.json > cand.jsonl
python3 -I docs/research/scripts/judge_gate.py label  cand.jsonl labels.json jev-1.jsonl jev-2.jsonl
python3 -I docs/research/scripts/judge_gate.py report cand.jsonl labels.json jev-1.jsonl jev-2.jsonl
```

`jev-*.jsonl` and `labels.json` are the Jev runs and Opus labels of [main-judge-2026-10.md](main-judge-2026-10.md).
