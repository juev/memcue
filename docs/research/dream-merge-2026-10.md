# The merge sweep: duplicates older than the last dream, October 2026

Status: **measured 2026-10-25; shipped in [#377](https://github.com/juev/memcue/issues/377).** A dream reviewed
only the facts written since the previous one, so two copies of one fact that both predate that mark never met
again — the log showed 58 `retire` actions over 16 runs and no way to say whether a given pair had been looked at.
The dream now also sweeps a bounded slice of the facts it has already seen, and records the groups it made by id.

## Context

`prepare` built its groups from the facts `dream.seen` did not hold at their current version. A duplicate written
in the same session as the copy it repeats was grouped and retired; a pair whose second copy was written before the
last run was invisible forever. The issue's live pair was the two forms of the Forgejo token command, written three
days apart, both active, 14 dreams after the second copy, both still returned for the same query.

Two mechanics made the fix cheap:

- the neighbour search is already there (`dreamNeighbours`: up to 80 candidates, coverage 0.35, 3 neighbours), so a
  swept fact costs one search and no model call until it forms a group;
- a group of two facts already reaches the model only when the words put them together, so the sweep cannot turn
  one dream into a per-fact model bill: the packet bound and the new `mergeGroups` cap stay in front of it.

## Method

One copy of the live base (375 active facts, schema 5), built from the branch. The pair's older copy sits at
position 27 of the id order and its younger copy at 192, 165 facts apart, so no single sweep slice could hold both
as seeds: if they meet, they meet through a neighbour.

- `dream.merge` was set to the id just before the older copy, so one default sweep of 50 facts covers it.
- `memcue dream -prepare -since 2027-01-01` made every fact old: nothing is fresh, every group comes from the sweep.
- The session model answered the packet (the CLI starts no model of its own), and `memcue dream -apply` applied it.
  The answer retired the younger copy in favour of the smaller id and proposed nothing for the other groups.

## Results

- 50 facts swept, 14 groups formed, 0 fresh facts: the whole run was the sweep.
- The pair landed in one group of four with two other facts about the same tool (`forgejo` container config, an
  issued API token). The answer retired `01M5JHBJAQ0EKS0S59429T2EER` (2026-10-22) by
  `01M5AVCNRZ6CTAN2XQZKWBDMZ5` (2026-10-19); the smaller id stayed active and no other fact of the group changed.
- The run record names the group under `grouped`: its four ids, `outcome: acted`, `acted: [the retired id]`, no
  fresh facts. The other 13 groups are `kept`, also by id, so "was this pair grouped in run N" is answerable from
  `dream.log` alone.
- The cursor after `-apply` is the id of the 50th swept fact, so the next run continues where this one stopped.
- `-prepare` alone moves neither the cursor nor a fact: the packet carries the cursor, and only `-apply` writes it.

## The grouping threshold

The issue's second half was that a fresh restatement in other words was not grouped either, so the coverage gate,
not only the freshness window, decides whether a merge is ever offered. `recall.Params` gained `MinContained`,
which lets a candidate pass below `MinCoverage` when the prompt holds nearly all of the candidate's own terms
(`0.8` of their IDF weight). The dream groups by that parameter set; search paths keep the field at zero, so their
behaviour is unchanged.

On the small base of the regression test (a short token note and a longer restatement that adds the host and the
full command), seeding the group with the long fact finds the short copy only with containment: the coverage gate
alone returns neither, and with `MinContained` the two are one group. On the live base above the sweep grouped the
pair with and without the field — building the same branch with `MinContained = 0` produced the identical group —
because a third fact of the tool already linked the two. The containment change is therefore not what makes the
live pair meet; it is what makes a long fresh restatement meet its short copy on a base where no neighbour links
them.

## Limitations

- One base, one sweep slice, one answer from the session model: the numbers describe the mechanism, not a merge
  rate. A base of 375 facts needs eight sweeps of 50 to walk once.
- The sweep groups by words alone, so a pair whose copies share few words stays invisible until a neighbour joins
  them; vectors were removed with the reranker (#232).
- A sweep group costs a model call when it forms, whether or not it holds a duplicate; `mergeGroups` bounds that
  at 20 groups a run, and `packetGroups` still bounds the packet as a whole.
