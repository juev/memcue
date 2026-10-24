# The place of a fact in recall, October 2026

Status: **measured 2026-10-24 ([#345](https://github.com/juev/memcue/issues/345), part of
[#327](https://github.com/juev/memcue/issues/327)); shipped as a coverage gate of 0.25 for facts of other
projects.** Since #331 recall works without a judge. The place of a fact (`scope`) only broke ties. A session in
one repository got the facts of every other repository. The question was how much of that is noise, and what
removing it costs.

## What the place may act on

Only `repo:` and `dir:` scopes, and only in a session that has a repository key. A fact about a host or a service
holds from any repository: a server is worked on from many repositories. So `global`, `host:*`, `service:*` and
empty scopes are never filtered and never ranked down. A session with no repository key has no other project:
- the Claude config directory (`_global`);
- a directory outside git (`dir:`, every Hermes session since #343, see #347);
- a session with no key.

A fact is the session's own when its scope is `tier.Scope` of one of the session's keys. The keys are all
remotes, so an upstream and a mirror count.

## Method

[scope](scripts/scope/main.go) runs recall on a copy of a base once per prompt, with no candidate limit, and builds
from that trace what each variant would serve:

- **base**: recall before #345.
- **filter**: facts of other projects are left out of the search, before its limit of 80.
- **gate g**: a fact of another project must hold a share `g` of the prompt instead of 0.15 (g = 0.25, 0.35, 0.5).
- **tier g**: own and placeless facts first, then facts of other projects that hold `g` (g = 0, 0.25, 0.35, 0.5).
- **+a**, each of the above with anchors. A prompt that names a repository, by the last segment of a key in the
  base's scopes, makes that repository's facts its own.

Data:

- **Claude Code.** The archived base of 2026-10-23 (2,776 facts), moved onto project keys by
  `memcue migrate -repos -apply` on a copy. Scope families: `repo:*` 1,762, `global` 828, `host:*` 139,
  `service:*` 43, empty 4. The prompts are the 1,724 typed prompts of the transcripts, with their working directory
  ([prompt_pool.py](scripts/prompt_pool.py)). The directories are 62: 48 repositories, 8 the Claude config
  directory, 3 `dir:`, 3 with no key.
- **Hermes.** The copy of the live base and the 168 prompts of the measurement in #327, run on the Hermes host.

Recall without a model is deterministic, so a second run of the baseline has no jitter. Significance comes from a
two-sided sign test on the prompts where two variants differ.

Some variant serves other facts than the baseline in 432 Claude Code prompts. Every fact any variant serves there
was labelled U/R/N by [rank_label.py](scripts/rank_label.py), as in #330: Claude Opus through `claude -p`, blind to
the variant, prompt alone. 198 of the prompts carried labels from #330; the 568 missing pairs of the other 323 cost
$30.83.

## Results

### Hermes: nothing changes

No Hermes session has a repository key, so every variant serves the baseline on all 168 prompts. The project of a
Hermes session is #347.

### Claude Code, by the judge

432 prompts where some variant differs from the baseline:

| variant | U | R | N | noise | prompts with a U | gained / lost | p |
|---|---|---|---|---|---|---|---|
| base | 179 | 268 | 335 | 43% | 154 | | |
| filter | 122 | 145 | 212 | 44% | 103 | 18 / 69 | <0.001 |
| gate 0.25 | 172 | 242 | 270 | 39% | 149 | 4 / 9 | 0.27 |
| gate 0.35 | 172 | 217 | 256 | 40% | 147 | 10 / 17 | 0.25 |
| gate 0.50 | 162 | 192 | 235 | 40% | 141 | 13 / 26 | 0.05 |
| tier 0 | 184 | 240 | 358 | 46% | 153 | 18 / 19 | 1.0 |
| tier 0.25 | 176 | 217 | 291 | 43% | 148 | 18 / 24 | 0.44 |
| tier 0.50 | 164 | 183 | 242 | 41% | 138 | 19 / 35 | 0.04 |

- Anchors add one or two prompts with a U to any variant, so they were not taken.
- No variant loses a prompt whose only useful fact was a `service:` fact.
- The filter does cut the most noise facts (−123), but it also cuts the useful facts of other repositories in the
  same proportion. The served set shrinks, and its share of noise stays at 44%.
- The useful facts it drops are all `repo:` facts. In a first estimate on the #330 labels alone, half of them came
  from a repository of the same host and owner as the session's. Some came from a host's configuration repository
  whose facts the extraction stored as `repo`.
- The gate drops weak matches: a useful fact of another repository usually holds much of the prompt, and a noise
  fact holds two or three common words.

### Against what the agent did

[scope_outcome.py](scripts/scope_outcome.py) checks the same pairs with no model. A fact counts as used when one of
its distinctive tokens (a path, a host, an identifier, a number) shows up in the agent's text or tool calls after
the prompt. Tokens of the prompt itself do not count, and neither does the agent's common vocabulary (tokens it
wrote after more than 2% of the prompts).

| variant | prompts where a served fact was used | gained / lost | p |
|---|---|---|---|
| base | 53 | | |
| filter | 43 | 8 / 18 | 0.08 |
| gate 0.25 | 51 | 4 / 6 | 0.75 |
| gate 0.35 | 48 | 4 / 9 | 0.27 |
| gate 0.50 | 47 | 5 / 11 | 0.21 |

The direction is the judge's. The evidence agrees with the labels where it exists: 56 of 235 U, 19 of 342 R and 2 of
489 N were used. Only 53 prompts carry evidence at all, so none of these differences is significant on its own.

### By hand

The user labelled 37 disputed facts, every one of them U by the judge:
- all 14 facts of the 13 prompts where gate 0.25 changes whether a useful fact is served;
- 23 facts of 20 of the 69 prompts the filter loses, the ones without evidence of use first.

14 were needed, 22 not, 1 unsure: on disputed pairs the judge's U is right about 40% of the time.

- **Gate 0.25.** It loses a needed fact in 4 prompts and gains one in 1: −3, against −5 by the judge.
- **Filter.** 7 of the 20 sampled prompts lose a needed fact. The sample holds only prompts without evidence of use,
  and there are 60 such prompts among the 69; the other 9 carry evidence. That puts the confirmed losses at about
  20–27. The judge's 18 gains at the same 40% are about 7, so the filter loses about 20 prompts with a needed fact.

## Decision

**Gate 0.25 by default, `MEMCUE_FOREIGN_COVERAGE` to change it** (#345). In a repository's session, a fact scoped to
another repository or a directory needs 0.25 of the prompt's weight instead of 0.15.
- On the 432 prompts it cuts the noise facts by 19% (335 → 270) and the share of noise from 43% to 39%.
- Checked by hand, it loses a needed fact in 3 prompts.
- `0` turns it into the hard filter, inside the search and before its limit: about 37% less noise for about 20
  prompts that lose a needed fact.
- A value at or below `MEMCUE_MIN_COVERAGE` restores the recall before #345.

The criterion of #327 asked for at least 30% less noise with no loss of useful prompts. No variant meets both:
gate 0.5 reaches 30% (335 → 235) with 13 prompts fewer by the judge. The rest of the noise is left to the promotion of facts
learnt in several projects (#346) and to the hygiene of the base, not to the place.

In the shipped recall, the gate serves what the gate 0.25 variant does on every prompt of a repository's session,
and the filter what the filter does, except two ties of equal BM25. The 12 other differences are sessions of `dir:`
directories, which the measurement counted as projects and the shipped rule does not.

## Limitations

- One judge, prompt alone, no assistant reply; its U is right about 40% of the time on disputed pairs.
- The noise (N) was not checked by hand. Its only support is that 2 of 489 N facts were used.
- The percentages are over the 432 prompts where the variants differ. Over all traffic the noise drops less.
- The seen set was empty: a real session skips the facts already served.
- One person's base and prompts.

## Reproducing

```sh
python3 -I docs/research/scripts/prompt_pool.py /dev/null prompts.json ~/.claude/projects
cp ~/.claude/archive/memcue-2026-10-23/facts.db cc.db       # always a copy
MEMCUE_FACTS=cc.db MEMCUE_STATE_DIR=state memcue migrate -repos -apply -root ~/src
go run -tags research ./docs/research/scripts/scope cc.db prompts.json ~/.claude > scope.jsonl
python3 -I docs/research/scripts/rank_label.py scope.jsonl cc.db labels.json      # claude -p, resumable
python3 -I docs/research/scripts/scope_eval.py scope.jsonl labels.json cc.db
python3 -I docs/research/scripts/scope_outcome.py scope.jsonl cc.db labels.json ~/.claude/projects > outcome.json
```

The measurement used the 1,724 prompts of #330 (the pool before this session's transcripts), with the labels of
#330 as the start of `labels.json`.
