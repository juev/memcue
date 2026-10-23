# A stable cut for the judge: a tie band that does not work, October 2026

Status: **measured 2026-10-23; no knob shipped.** Issue #298 asked for a served pair that does not change between two
identical runs. The proposal was a tie band — facts whose judge scores are within `MEMCUE_JUDGE_RERANK_MARGIN` of their
band's best are ordered by lexical coverage instead of by the score. Measured on live prompts, the band changes which
facts are served but not how often the pair flips: the flips come from the judge's resolution and from the hard
threshold, and a band covers neither.

## Context

The base is the one the Hermes gateway uses (schema 5, 282 active facts, 1403 rows), copied, with `MEMCUE_FACTS` and
`XDG_STATE_HOME` pointed at the copy so that `recall` never touches the live base. Prompts are 12 real user messages
from the gateway session log, `cwd=/opt/data`.

What the pipeline costs per prompt, measured before the band (release 0.5.3 binary, one run per prompt):

- the words alone: p50 48 ms for the whole process, 4.4 ms of it startup — the scan of the base is not the cost;
- with Jev: p50 421 ms isolated (`recall` on the copy), 875 ms at the median in the live `recall.log`, which pays a
  new TLS connection per turn;
- the judge changes the answer on 7 of the 12 prompts and adds facts on 4 where the words served nothing, so the call
  earns its place; `MaxFacts` 2 is a cap, not a constant: 2 facts on 6 prompts, 1 on 4, 0 on 2.

## Method

A build of the branch with the band (`MEMCUE_JUDGE_RERANK_MARGIN`, `0` keeps the plain score order) against the 0.5.3
release binary. Each prompt runs six times per arm, each arm gets its own state directory, and the served facts are
read from `recall -explain`: the words give the same candidates for the same prompt, so any prompt with more than one
distinct pair across six runs was decided by the judge's noise. Tool: [judge_cut.py](scripts/judge_cut.py).

## Results

Prompts whose served pair was not the same in all six runs, out of 12:

| arm | unstable prompts | median recall |
|---|---:|---:|
| 0.5.3 release | 7 | 344 ms |
| margin 0.05 | 6 | 327 ms |
| margin 0.10 | 5 | 334 ms |
| margin 0.20 | 6 | 332 ms |

The baseline itself is not a constant: the 0.5.3 arm was measured three times in these sweeps and was unstable on 7,
5 and 6 prompts. The band therefore moves the number by less than the measurement does. It does change the pairs (the
arms disagree on 7–8 prompts of 12), so it is not a no-op: on one prompt the banded answer was stable while the release
served two different pairs.

## Why the band does not help

Two mechanisms, both visible in the per-run scores:

- **The hard threshold.** On one prompt a fact scored 0.51, 0.52 and 0.54 in one arm and 0.55, 0.48 and 0.46 in the
  other: it crosses `MEMCUE_JUDGE_RERANK_MIN` and appears or disappears. No ordering rule touches that.
- **Gaps wider than the jitter.** On the worst prompt four candidates scored 0.78, 0.75, 0.74 and 0.73, and every one
  of them moved by up to 0.05 between runs. The band fixes the order of facts inside one band, but a gap of 0.04–0.05
  sits exactly at the band edge, so the band membership itself flips: the same fact is inside on one run and outside on
  the next.

A wider band covers more of the second mechanism and nothing of the first, which is what the 0.10 and 0.20 rows show.

## What could change it

- **The threshold.** #278 measures 0.5 against 0.55, 0.6, 0.65 and 0.7 on Opus labels; a threshold in a flatter part of
  the score distribution flips fewer facts. This is the cheap direction and it is already open.
- **A cut at the largest gap** instead of a probability: serve the top fact, then keep the next only if the score drops
  by less than some gap. That is stable by construction where the scores are far apart, but it changes how many facts a
  prompt gets, so it needs the labelled set and is not measured here.
- **Asking Jev twice and averaging** halves the noise and doubles a 0.4 s call; not worth it while the threshold question
  is open.

## Reproducing

```sh
# a copy of the base, never the live one
python3 - <<'EOF'
import sqlite3
src = sqlite3.connect("file:$HOME/.memcue-live/facts.db?mode=ro", uri=True)
dst = sqlite3.connect("/tmp/facts-copy.db"); src.backup(dst); src.close(); dst.close()
EOF

export MEMCUE_JUDGE_URL=... MEMCUE_JUDGE_MODEL=typesafe/jev-1.14 MEMCUE_JUDGE_KEY=...
python3 -I docs/research/scripts/judge_cut.py --prompts prompts.json --base /tmp/facts-copy.db \
    --old ~/.local/bin/memcue --new /tmp/memcue-margin --runs 6 --margin 0.05
```

`prompts.json` is a JSON array of prompt strings; the run above used 12. The band build is the one from #298's branch,
`MEMCUE_JUDGE_RERANK_MARGIN` set per arm on the same binary.

## Limits

- One base, 12 prompts, one judge endpoint and one day: the numbers say how small the effect is, not what the stable
  share is in general.
- Six runs per prompt put the resolution of the measurement near one prompt in twelve.
- The prompts are the agent's own session, where the base describes the tool being asked about — the share of
  self-referential prompts is higher than in ordinary use.
