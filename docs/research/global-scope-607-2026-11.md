# Global facts that belong to one project, November 2026 (#607)

Status: **measured 2026-11-02 on a copy of the author's base, against the branch of #607.** The examples below are
invented and show the kind of fact, not its text; the labelled facts stay out of the repository.

#607 found that many facts with `scope=global` hold in one project only. They came from two places: the agent writing
`-scope global` for a project fact, and the dream promoting a cross-project duplicate to global (#346). The branch
changes three things:

- The dream keeps a duplicate learnt in another project, and makes no fact global. Two keys of one repository (the
  remotes of one clone, the `dir:` path of a checkout that has a remote now, a clone's pre-#343 name) are one project.
- The first dream of a machine takes back the promotions its `dream.log` records.
- A fifth task of the dream packet checks the scope of global facts and moves a fact of one project to that project.

In short:

- **Promotions taken back: 5 of the 6** still global; the sixth was global before the promotion and stays. The other
  three of the nine had been moved by hand or retired since. Five duplicates came back; the dream that followed refused to retire them again, as designed.
- **The scope check moved no fact that holds everywhere** (0 of 25) and moved 10 of the 31 facts of one project, each
  to its own project.
- **19 of the 21 facts it left global had no project to go to**: 14 are about the Claude config directory, which
  memcue treats as the global tier, and 5 name a repository no fact of the base was learnt in. The model missed 2
  whose project was offered.
- **Cost**: the scope check of 56 facts took two calls of Opus 5.5 through `claude -p`, $0.37.

## Method

- **Base**: a copy of the author's text history and of the dream state (`dream.log`, `dream.at`, `dream.seen`,
  `dream.merge`), with `MEMCUE_FACTS` and `MEMCUE_STATE_DIR` pointing at the copy. About 540 of its 3,000 active facts
  were global.
- **Labels**: the 57 active global rules and decisions with `source=agent` that #607 labelled with one question,
  "does it hold in every repository and on every host?": 26 everywhere, 31 one project. One of the 26 had been
  retired since, which leaves 56.
- **Promotions**: the first `dream -prepare` of the branch on the copy, which runs the take-back once.
- **Scope check**: every other global fact of the copy was retired, so the check gave only the 56 labelled facts. Each
  packet of `dream -prepare` was cut down to its scope section and sent to `claude -p` on Opus 5.5 with the system
  prompt and JSON schema of `dream -run`; `dream -apply` applied the answer. Two packets (30 and 26 facts).
- **Grading**: a moved fact counts as right when its new scope is the project its text names, read by hand.

A first run used `dream -run -all` on a copy and offered the scopes active facts hold in. It moved six facts about the
Claude config directory to the scope of the config repository's GitHub remote. memcue names that directory the global
tier whatever its remotes, so no session sees that scope and the facts would have been lost to every session. The
branch now offers only the projects some active fact was learnt in, by the key of its origin, and the run above is on
that code. The `-all` run also spent 14 passes on the copy's own backlog of translations and search queries ($17.69)
and was stopped: a measurement of one task should not be a whole-base run.

## Promotions

`dream.log` of the author's Mac records nine promotions since #346. The take-back on the copy:

| | facts |
|---|---|
| promoted, still global | 6 |
| of them moved back to the project scope they had before | 5 |
| of them global before the promotion, left global | 1 |
| promoted, since moved by hand or retired | 3 |
| duplicates reopened, learnt in another project | 5 |

Of the five reopened duplicates, three were learnt under the old name of a renamed repository. The dream has no pair
`OLD=NEW` for it, so it counts them as another project and keeps both copies; `memcue migrate -repos -apply OLD=NEW`
moves them onto the key, after which the merge sweep can retire them as ordinary duplicates. The dream that followed
was offered five of these pairs as duplicates and refused each retirement ("a duplicate learnt in …, … in …: each
project keeps its copy").

## The scope check

| label | moved | left global |
|---|---|---|
| holds everywhere (25) | 0 | 25 |
| one project (31) | 10 | 21 |

All ten moved facts went to the project their text names. The 21 left global:

| why | facts |
|---|---|
| about the Claude config directory (hooks, skills, the config's own decisions) | 14 |
| names a repository no active fact was learnt in | 5 |
| the project was offered, the model kept the fact global | 2 |

A fact of the first row reads like "the review hook of the agent's config fires on two triggers". Sessions in the
config directory see the global tier and nothing narrower, so such a fact has no scope a session there would see. The
check leaves them global; giving the config directory a project of its own is a separate question, settled by #617:
the config directory is now a repository named by its remotes, and these facts moved to it.

A fact of the second row reads like "repository B has no CI on purpose" where no session of the base ran in B. The
check cannot offer a project it has no key for, and keeps the fact global.

## Before and after

Global facts that name one project, of the 56 labelled:

| | before | after |
|---|---|---|
| global and about one project | 31 | 21 |
| of them with a project the base knows | 12 | 2 |

The rest of the base's global facts (about 480, mostly environment facts and pitfalls) were not labelled. The live
base gets the same check from its background dream, 30 facts a packet in the order of their ids.

## Limits

- One labeller and one run of the model. The two misses were not repeated to see whether they are stable.
- The 56 facts are rules and decisions; the check was not graded on environment facts and pitfalls, which make up most
  of the global facts.
- Hermes and DSH keep their own `dream.log`; their promotions are taken back on their first dream after the update,
  which this measurement did not run.
