# The repository default against the whole base: keep `-in all` in Hermes, October 2026

Status: **measured on a copy of a live base, 2026-10-26.** The decision [#438](https://github.com/juev/memcue/issues/438)
asks for. Names of private hosts, repositories and services are placeholders; the numbers are the run's.

## Setup

- The base: **391 live facts** by id, 176 characters on average, schema 5, of which **255 carry the host scope** after
  #418 retired the installation-state facts.
- Both arms run the 0.15.0 binary on one copy of the base, from a checkout of the memory repository, `MEMCUE_HOST`
  naming the host the facts use (without it the host facts are not this machine's by name, and the default measures
  worse than it is).
- Arm 1 is the binary's default in a repository: the project's facts, this host's and the global ones. Arm 2 is
  `-in all`: the whole base. Top five per prompt, one grading pass by one rater, `U` answers, `R` is on the topic, `N`
  is unrelated.

## 20 user prompts

| | default (project, host, global) | `-in all` |
|---|---|---|
| facts delivered | 78 | **74** |
| useful (`U`) | 10 | **16** |
| on topic (`R`) | 33 | 29 |
| unrelated (`N`) | 35 | **29** |
| prompts keeping a useful fact | 8 of 20 | **11 of 20** |
| useful facts the answer left out | 7 | **1** |
| characters delivered | 15 939 | **14 274** |

## 8 agent queries

| | default | `-in all` |
|---|---|---|
| facts delivered | 30 | 27 |
| useful | 9 | **15** |
| unrelated | 7 | **4** |
| queries keeping a useful fact | 7 of 8 | **8 of 8** |
| useful facts left out | 7 | **1** |

## What the difference is made of

The default is not a subset that simply hides other repositories: it also loses facts that carry **no scope at all**
(3 of the 7 useful facts the prompts lost), facts of **another host** (2), and facts of **another repository** (1). The
scope-less ones are a defect of their own, filed separately: a fact with `scope = ''` is in no place, so `-in project`,
`-in host` and `-in global` all miss it and only `-in all` returns it. The rest of the gap is crowding: the host scope
holds 255 facts, so on a prompt about a host rule the five places fill with environment facts of the same host and the
rule that answers is pushed out by the cap, not by the filter.

## The decision

**`-in all` stays in the Hermes plugin.** The repository default of 0.15.0 is materially worse on both corpora — five
to six useful facts fewer out of twenty prompts, seven lost against one — and the reason the default was widened
(#418: the host and the global facts) is already carried by the whole base. The override is now documented with these
numbers in the provider, the tool description says the search takes the whole base because a chat is not about one
repository, and the session note keeps telling an agent in another harness to widen with `-in all` when its
repository's places have nothing.

What is not measured: a base whose facts are mostly one repository's, where the default would compete on equal terms;
and the Claude Code and DSH harnesses, whose defaults this decision does not touch.
