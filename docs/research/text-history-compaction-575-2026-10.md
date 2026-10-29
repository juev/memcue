# Compacting the text history: what the merge needs, October 2026 (#575, step 1)

Status: **research only; no change to the base format or the merge. Owner's decision 2026-10-29: no compaction for
now; every fact file keeps its full history** (see [Decision](#decision)). #575 asks which versions of a fact a
compaction of the text history (#530) may drop, and what the base would weigh under each candidate rule.

In short:

- **No compaction is safe with today's merge.** `MergeHistories` finds the shared versions by position and numbers
  the merged ones by position. Once both machines drop versions, the merge picks the wrong current version, the two
  directions disagree, or it writes a file with two different versions under one number, which no command can read
  again. This holds for every rule tried, even the one that keeps the most.
- **A compaction on one machine does not stick.** Every merge takes the union of the versions on both sides: the git
  driver, a merge of two bases, and the sync between the cache and the text history. A machine that does not compact
  brings the dropped versions back at the next merge, and the cache does the same on the machine itself unless it is
  compacted together with the files.
- **`reopen` needs only the latest version.** A retirement copies the text, the labels and the expiry into the retired
  version, and `Restore` reads only that. The issue assumed it needs the version before the retirement; it does not.
- **What else reads older versions**:
  - The dream compares a fact's versions since the one it last saw. When that version is gone, the fact counts as
    changed and goes to the next dream again: extra work, nothing lost.
  - `migrate` reads every row for the notes it already migrated. The note it names and the prompt version are
    copied into every later version.
  - The cache build reads whole files, so its time follows the number of versions.
- **The size, Mac base 2026-10-29**: 4,838 facts, 21,156 versions, 18.7 MB. The latest versions alone are 27.5% of
  the bytes, the latest and the version before a retirement 43.8%, the last two 52.4%. Every version replaced less
  than 7 days ago, plus the latest, is 57.2%. A 14- or 30-day window keeps everything: the oldest version is from
  2026-10-19. The Hermes copy of the same base gives the same shares.

A compaction, if one is ever done, needs a change to the merge first. It would match versions by number, keep the
numbers, and take the base of a correction from the last shared version or from git's common ancestor. Only then
comes a rule.

## What reads older versions

| reader | what it reads | if older versions are gone |
|---|---|---|
| git merge driver, `memcue merge %O %A %B` → `mergeFactFiles` (`cmd/memcue/facts.go:798`) | both files whole; the ancestor `%O` is passed but not read (`runMerge`, `facts.go:763`) | see [The merge](#the-merge) |
| merge of two bases, `Store.Merge` (`internal/store/merge.go:28`) | every row of both bases | the same rule, `MergeHistories` |
| cache sync, `bringTogether` (`internal/store/textstore.go:305`) | the cached history and the file | union of the two (`MergeHistories(cached, file)`, line 309), written back to the file when it differs (line 319): a compacted file next to a full cache grows back |
| `memcue reopen`, `Store.Restore` (`internal/store/store.go:871`) | the latest version (`s.Get`) | nothing: a retirement keeps the text, labels and expiry (`Retire`, `store.go:885`) |
| dream, `onlyLabelsSince` (`cmd/memcue/dream.go:837`) | the versions from the one `dream.seen` names | the fact counts as changed and is reviewed again |
| dream apply: `Relabel`, `SetQueries`, `SplitFact` | the version number the packet saw | refuses a group whose number changed; a merge that renumbers (below) causes that |
| `migrate`, `notMigrated` (`cmd/memcue/migrate.go`) | every row: the note named in the origin, and the prompt version | none: every later version copies both; `Retag` rewrites only the repository part of the origin (`migrate_repos.go:73`) |
| cache build, `syncText` | every file | faster with fewer versions |
| `memcue dump` (git textconv of a legacy `facts.db`) | every row | only the legacy base |

## The merge

`MergeHistories` (`internal/store/merge.go:85`) assumes both sides hold the same leading versions:

- **The shared versions are a prefix by position** (line 87): `ours[i]` against `theirs[i]`. A side that dropped
  its first versions shares no prefix with one that did not, or with one that dropped a different number of them.
- **The merged versions are numbered by position** (line 121): `Version = i + 1`. After a compaction the numbers
  have a gap at the front (`[v7, v8]`), so a merged version gets a number below the ones before it. A file is read in
  number order (`ParseFacts`, `internal/store/text.go:116`), so the latest version is no longer last. When two versions
  get one number, the file is refused: "fact … has two different versions N" (line 121).
- **A correction is judged against the last shared text** (`correctionLast`, lines 114–116 and 163). A version first in
  its side's tail is a correction when its text differs from the last shared version. With no shared prefix that text
  is empty, and every such version counts as a correction. That includes a change of keywords or queries only, which
  #549 says must leave the retirement current.

### The replay

[`scripts/history575/`](scripts/history575/) replays the 13 merge scenarios on in-memory histories. They are those of
`TestMergeOfACorrectionAndARetirement` and `TestMergeOfAStaleSideKeepsOneCopyOfAVersion`, plus a query-only change, a
reopen, a one-sided change and a merge 40 days late. Each run goes through `store.MergeHistories` itself. After every
step the history is written as its fact file and read back, as the next command would. Each scenario starts from a
shared history of four versions: added, expanded, corrected, expanded again. It runs once with full histories, and
once per rule, with each side compacting after every write and merge. Each run merges both ways and compares the
current version with the full-history result.

```
go run ./docs/research/scripts/history575 cases
```

Both machines compact; the writes come 10 days after the shared history. Outcomes are same / number only / wrong
current / directions differ / unreadable file, of 13:

| rule | same | number | wrong | differ | unreadable |
|---|---|---|---|---|---|
| latest | 0 | 8 | 3 | 2 | 0 |
| latest + the version before a retirement | 0 | 6 | 3 | 4 | 0 |
| last two | 0 | 2 | 9 | 2 | 0 |
| from the version before the last text change | 0 | 5 | 3 | 3 | 2 |
| replaced less than 30 days ago | 12 | 0 | 1 | 0 | 0 |
| replaced less than 14 days ago | 12 | 0 | 1 | 0 | 0 |
| replaced less than 7 days ago | 0 | 0 | 7 | 3 | 3 |

The 30- and 14-day windows drop nothing in these scenarios, so they behave as full histories. Their one wrong result
is the merge 40 days late, where they too drop the shared version. The 7-day window drops the 10-day-old versions and
breaks like the others. "Number only" means the right text and status under another version number. That still
refuses a dream group that saw the old number, and leaves the two machines with different numbers for one write.

Only the Mac compacts, the server keeps full histories (the merge brings the dropped versions back):

| rule | same | number | wrong |
|---|---|---|---|
| latest, latest + before a retirement | 9 | 1 | 3 |
| last two, from the last text change | 13 | 0 | 0 |
| any window | 12 | 0 | 1 (the merge 40 days late) |

The three wrong results of the latest-only rules are the #549 cases. One side corrects the fact and retires it, the
other changes only its keywords or queries. With the shared version gone, the keyword change counts as a correction
and brings the old text back.

## The size of the base

```
go run ./docs/research/scripts/history575 count ~/.claude/memory/facts
```

Mac base, 2026-10-29 (commit `93cb4c5` of the base repository): 4,838 facts, 2,980 of them retired, 21,156 versions.

| rule | versions | bytes | share |
|---|---|---|---|
| full | 21,156 | 18,682,963 | 100% |
| latest | 4,838 | 5,128,813 | 27.5% |
| latest + the version before a retirement | 7,818 | 8,180,699 | 43.8% |
| last two | 9,578 | 9,792,902 | 52.4% |
| from the version before the last text change | 5,291 | 5,583,155 | 29.9% |
| replaced less than 30 days ago | 21,156 | 18,682,963 | 100% |
| replaced less than 14 days ago | 21,156 | 18,682,963 | 100% |
| replaced less than 7 days ago | 11,761 | 10,681,737 | 57.2% |

The Hermes copy on loffe (`/opt/data/home/.claude/memory/facts`, the same commit) has 4,833 facts and 21,149
versions, five facts fewer than the Mac. Every share is within a tenth of a percent of the Mac's (latest: 27.4%).

The versions by day show why the windows keep everything: 11,151 on 2026-10-19 (the earliest day in the base),
5,665 on 2026-10-25 (a `dream -all`), 2,072 on 2026-10-29 (an audit that retired 1,906 facts). A window rule saves
nothing in a young base and grows with its age. The latest-only rules save the most at once, and are the ones the
merge breaks first.

## What step 2 would need

These are the options the code suggests. None is measured; they are hypotheses for the owner's decision.

1. **Match versions by number, keep the numbers.** Shared versions are those with the same number and the same write;
   the merged tail is numbered after the highest shared number, not by position. This removes the unreadable files and
   the renumbering. The positional test `same(ours[i], theirs[i])` and line 121 are the places.
2. **Take the base of a correction from the last shared version by number**, or from git's common ancestor `%O`, which
   the driver already receives and ignores. With the ancestor, a version is judged against what both sides last
   agreed on, whatever either dropped since. The merge of two bases and the cache sync have no ancestor, so they would
   still need option 1.
3. **Compact the cache and the files together**, and only once every machine runs a merge that tolerates gaps:
   Mac, loffe and Hermes upgraded first, the compaction switched on after. Otherwise an old binary's merge brings the
   versions back, and its positional merge meets a gap.
4. **A rule after that.** With options 1 and 2 in place, "the latest version" keeps 27.5% of the bytes. A window rule
   ("the latest and every version replaced less than N days ago") protects a machine that merges up to N days late,
   at the cost of the bytes of the last N days.

## Step 2 as proposed, and a base without git

The proposal discussed after step 1, not built and not measured:

- **A three-way git driver.** It would read the ancestor `%O` and judge each side against it: a side corrected when
  its text differs from the ancestor's, and retired when its latest version is retired. That needs no older versions
  in the file, and on paper gives today's result in the #544 and #549 cases above.
- **One version per file.** The file would keep only the current version, its number never decreasing. The history
  would be `git log -p` on the fact file.
- **Rollout.** First a release whose driver tolerates gaps, on every machine. Then `memcue migrate -compact` on one
  machine, as `migrate -text` did in #530.
- **Measurements it would need.** The replay against a prototype of the three-way merge. A fuzz of random edits and
  merges on two or three machines. An end-to-end run in real git clones, one of them on 0.23.0. The cache build time
  and the bytes a day's pull carries, on a copy of the base. A clone would not get smaller: git keeps the old
  versions, and the compaction adds a commit.

**A base without git** changes the picture:

- **On one machine** nothing merges, but the file is the only history. A compaction would delete the one copy of a
  fact's older text, the one a person needs to undo a wrong correction. `reopen` and the dream do not need it.
- **Synced without git** (Syncthing and the like) is not supported today: a conflict copy is not merged. A merge of
  such copies could only be two-way, with no ancestor, and the #549 rule needs the history in the file for that.

So a compaction would apply only to a base in a git repository with the driver installed. memcue can tell this case
apart (`git rev-parse --show-toplevel`, `cmd/memcue/facts.go:860`). Every other base would keep the format of today,
and the merge would have to read both formats.

## Decision

The owner's decision, 2026-10-29: **no compaction for now; every fact file keeps its full history**, in git and
outside it. This report and [`scripts/history575/`](scripts/history575/) stay as the record of what a compaction would
need. If the size becomes a problem, start from the [step 2 proposal](#step-2-as-proposed-and-a-base-without-git) and
its measurements.

## Limits

- The replay runs `MergeHistories` on in-memory histories and round-trips them through the file format. It does not
  run git, the cache sync or a merge of two SQLite bases; all three call the same function.
- 13 scenarios from the tests and the issue, one shared history of four versions. A history with more versions or
  more machines was not tried.
- The sizes are one day of one base, which is ten days old.
