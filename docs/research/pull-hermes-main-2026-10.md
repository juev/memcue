# Main after 0.16.0 on a live installation: the pre-release check, October 2026

Status: **pre-release measurement, 2026-10-26.** The same Hermes installation, the same copy of its base and the same
ten agent queries and twenty user prompts as [pull-hermes-016](pull-hermes-016-2026-10.md), run through the plugin of
each version: 0.16.0 as installed, and main at `3c4e368` (#459, #458, #454, #453, #442, #456, #457) with its own plugin.
Every call went to a copy of the base in a scratch directory; the live base and logs were not touched.

Verdict in one line: **the search answers are byte-identical to 0.16.0 on all 30 queries, the base-side fixes do what
they say on the live data (8 → 0 waiting mirrors, 14 → 0 facts without a scope, 1 → 0 expired), and main was 3 ms
slower per call until #467 took two scans out of every search; the installation-state rule of #457 retires nothing on
this base.**

## Setup

- 0.16.0: the installed binary and plugin. main: a binary built from `3c4e368` for linux/amd64 and the plugin of the
  same commit. For the latency rows both were also built the same way as a release (`-trimpath -ldflags "-s -w"`);
  the 0.16.0 rebuild has the installed binary's size to the byte.
- The base: the copy taken for the 0.16.0 note (414 live facts, 8 candidates, all mirrors of the memory notes).
  One copy per arm, so a run of one cannot change what the other reads.
- The memory notes: `MEMORY.md` and `USER.md` of the installation, copied into each arm's home, so the card filter of
  #424 and the mirror rules of #440/#458 see the real notes.
- No model call anywhere: vet ran with `-no-model`, and the extraction change of #456 is not exercised (see below).

## Search: the same answers

| | 0.16.0 | main |
|---|---|---|
| agent queries with the same facts and the same hint | 10 of 10 | |
| user prompts with the same facts and the same hint | 20 of 20 | |
| facts delivered, agent / user | 29 / 77 | 29 / 77 |

The ranking did not change in this range, so the labels of the 0.16.0 note carry over unchanged: 14 of 29 useful on
the agent queries, 14 of 77 on the user prompts. No new labelling was needed, and none was done.

The card the plugin builds is 2 727 characters in both arms: nothing in this range changes it.

## Latency: a regression, found and fixed

End to end (process start included), 10 agent queries × 8 interleaved rounds, all three built as a release:

| binary | p50 | p90 |
|---|---|---|
| 0.16.0 | 12.7 ms | 14.8 ms |
| main `3c4e368` | 15.8 ms | 17.6 ms |
| main + #467 | **9.9 ms** | 12.0 ms |

Process start is the same (`memcue version` p50 3.1 against 3.2 ms) and so is the time inside the search
(`search.log` p50 2.92 against 2.85 ms, 50 calls each). The difference was before the search: `RepoScopes` (#418)
and `OriginTiers` (#459) scanned the latest version of every fact on every call, for `ResolveRepos`, which changes
nothing unless `-in` names `repo:KEY`. #467 reads them only then; its answers are byte-identical to main's.

## The base: what vet does to it

One run of the main binary (`vet -no-model`, `MEMCUE_MEMORY_DIR` set to the notes, the binary labelled 0.16.0 so the
version rule of #457 is active) on a third copy:

| | before | after |
|---|---|---|
| mirrors waiting as candidates (#458) | 8 | **0** — 7 approved, 1 retired as stale |
| live facts without a scope (#442) | 14 | **0** |
| live facts past their expiry (#457) | 1 | **0** |
| approved mirrors whose note is gone (#440) | — | **5 retired** |
| installation state of an older memcue (#457) | — | **0 retired** |

The five stale mirrors are all installation state that the notes no longer hold ("the plugin from tag sha …",
"the gateway restarted on 23.10", "add expands queries", …): the rule of #440 removes them only now that the plugin
names the notes directory (#458).

What did not work as hoped:

- **The version rule retires nothing here.** 16 live facts name a memcue version. 13 of them are of a knowledge
  kind (pitfall, rule, decision, event), which the rule leaves alone by design; most describe behaviour that is still
  true ("in 0.14.0 an expired fact is cut from search"). Two are stale state the rule misses: "in memcue 0.9.0 there
  are no `sessions/<id>.recalled` files" (environment, but no installation word near the version) and "in plugin
  0.2.1 the key … is deprecated" (a pitfall). The 14 of the issue counted facts that name a version, not facts that
  should go; the rule of #457 is conservative and on this base its share is zero.
- **A mirror that restates a live rule in other words is approved.** #458 drops a mirror an approved fact covers word
  for word; the two duplicates the 0.16.0 note names are paraphrases, and the covers check does not see them.

## Errors of the 0.16.0 note, reproduced on main

| # | reproduction | 0.16.0 | main |
|---|---|---|---|
| #453 | a fact on the shelf, `search -json "zqxshelf"` | `[]` | `{"facts":[],"hints":["2 facts on the shelf match: -in shelf:research"]}` |
| #454 | `search -n 0 -json "…"` | `[]`, exit 0 | `search: -n must be >= 1`, exit 1 |
| #442 | facts without a scope | 14 | 0 after vet; a new one gets its origin's scope |
| #436 | a user message needs a ranker | 18 % useful | closed: the ranker needs a model call per search |

## Tests

| test | result |
|---|---|
| plugin suite of main, in the Hermes container, against the main binary | 83 tests, **1 failure**, fixed in #469 |
| `internal/bench` against the main binary | PASS |

The failure was the test, not the plugin: `test_search_passes_the_controls` added a fact without a scope and relied
on the checkout being a repository; outside one (#442) the fact is global and `-in global` returns it too.

## Not measured

- **#456**, the extraction reading the memory notes: it changes a model call, and only new sessions after the
  release show whether the card stops restating the notes.
- A second labeller; another base.

## Reproduction

```sh
# one copy of the base per arm, the notes copied into each arm's home
python3 probe.py <arm> <plugin dir> <binary> agent10.json out.jsonl    # imports the provider from <plugin dir>
python3 probe.py <arm> <plugin dir> <binary> card card.txt
# the base side, on its own copy
MEMCUE_FACTS=$COPY MEMCUE_MEMORY_DIR=$HOME/memories memcue vet -no-model -json
# latency: the binaries built with -trimpath -ldflags "-s -w", calls interleaved, process start included
```
