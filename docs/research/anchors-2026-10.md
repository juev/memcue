# A read channel keyed on tool calls: anchors, October 2026

Status: **measured 2026-10-23 (#318); no-go in this form.** The question was whether facts could reach the agent
through what it touches (a file, a command, an error, a host) rather than through the prompt, with no model call at
read time ([local-recall](local-recall-2026-10.md), section 2.5). The main model wrote anchors for every active fact,
and the tool calls of the gold-set turns were replayed against them without injecting anything. Anchors are easy to
get and fire far too often: with the strictest variant a turn gets a median of eight facts, and two of three are
noise.

## Setup

- Base: the gold-set copy of #274, 2689 active facts.
- Anchors: `deepseek-flash` through `MEMCUE_API_URL`, reasoning off, 25 facts a call
  ([anchors.py](scripts/anchors.py), [anchors_prompt.md](scripts/anchors_prompt.md)); 367k input and 135k output
  tokens, 72 s.
- Replay ([anchor_replay.py](scripts/anchor_replay.py)): for 157 of the 180 gold prompts the turn was found in the
  transcripts, 4155 tool calls in all. A fact fires at most once a turn. A fact counts as present if its first version
  is older than the prompt, or if it was migrated from the notes (they existed before the migration date).
- Labels: Opus, blind, per fired pair with the prompt and the tool call that triggered it; a random sample of 400 of
  the 1672 fires of the strictest variant, $3.08.

## Coverage

The model gave an anchor to 1695 of 2689 facts (63 %): 64 % of pitfalls, 73 % of environment facts, and also 45 % of
decisions, which the prompt said usually have none. Many anchors are shared by dozens of facts. `loffe` alone stands
on 257 facts, and `**/CLAUDE.md`, `~/.claude/**`, `curl` and `git push` are common.

| variant | facts with an anchor | fires | turns with a fire | fires per turn, median | distinct facts fired |
|---|---|---|---|---|---|
| all anchors | 1695 | 11 715 | 144 / 157 | 48 | 852 |
| anchors held by at most 3 facts | 1312 | 3088 | 144 | 17 | 391 |
| anchors held by one fact | 993 | 1672 | 144 | 8 | 237 |

Even the unique anchors include `grep`, `python3`, `**/*.md` and `git diff`.

## What the fires are worth (unique anchors, sample of 400)

| | USEFUL | RELATED | NOISE |
|---|---|---|---|
| all | 45 (11 %) | 90 | 265 (66 %) |
| path anchors | 31 of 134 (23 %) | 39 | 64 |
| command and host anchors | 10 of 202 (5 %) | 45 | 147 |
| error anchors | 4 of 64 (6 %) | 6 | 54 |

The 95 % interval of the share of USEFUL is about 8–15 %. The channel does find something the prompt channel misses.
Of the 400 sampled fires, Jev at 0.5 and the words had served only two each for the same prompt. In 14 of the 32
sampled turns with a USEFUL fire, Jev served no USEFUL fact. That gain costs about six noise facts for every useful
one, and on the whole turn about eight injected facts where the prompt channel gives at most two.

## Decision

Anchors do not go into extraction, and there is no `memcue trigger` hook. The literature's precedent (path rules in
Claude Code, Windsurf, Kiro) works because a person writes a narrow glob for a rule they mean to fire; a model writing
anchors for every fact produces broad ones. A second attempt would have to start from the one kind that came close,
paths, and would need a precision above one half on the same replay before it costs a turn anything. Error anchors
fire on unrelated output and should not come back.

## Limits

- One labeller pass on a sample; the per-kind cells are small.
- The visibility rule counts every migrated fact as present, so a note written after a prompt could fire on it.
- The labeller judges a fact against one tool call and the prompt, not against what the agent did next.

## Reproducing

```sh
python3 -I docs/research/scripts/anchors.py base.db docs/research/scripts/anchors_prompt.md anchors.json
MAX_SHARED=1 python3 -I docs/research/scripts/anchor_replay.py fire base.db anchors.json cand.jsonl ~/.claude/projects fires-k1.json
# sample 400 fires with seed 318 into fires-k1-s400.json, then
GOLD=cand.jsonl python3 -I docs/research/scripts/anchor_replay.py label fires-k1-s400.json labels.json
python3 -I docs/research/scripts/anchor_replay.py report fires-k1-s400.json labels.json gold-labels.json jev-1.jsonl words.jsonl
```
