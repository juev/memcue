# The dream without one task, against real searches, November 2026 (#661)

Status: **measured 2026-11-06 on the replay of #643, six arms, 287 real queries, two judge passes, $44.62.**
**Over one day, no dream task makes a measurable difference to what search returns, and neither does the whole dream.**

The question of #661: [#643](dream-cost-643-2026-11.md) priced each dream task but not what it is worth. This run
leaves one task out at a time on the same day of the author's base and grades what real session queries get back.

In short:

- **No arm differs from the full dream.** On the 61 queries judged in both passes, every arm ties with full on 56 to
  61 of them; the widest mean difference is +0.06 of 2 (no sweep, interval [+0.00, +0.13]). A base with no dream at
  all scores 1.52, as full does: 3 wins, 56 ties, 2 losses.
- **The day's dream work reaches few searches.** The 88 facts sessions wrote that day appear in the results of 30 of
  the 287 queries, before any dream. The result lists of 92 queries differ between arms at all.
- **The merge sweep's duplicates rarely waste a place.** Over the live logs since 26 October, the 55 duplicates the
  sweep retired took 421 of 30,090 result places before they were retired, and stood next to the fact that covered
  them 40 times. In the other 381 places the duplicate stood in for that fact.
- **The arms cost** $7.0 (no sweep), $9.6 (no split), $4.2 (no expand and translate) and $10.7 (no scope), against
  $12.8 for full in #643. The scope check moved no fact in this window, so the no-scope arm is a second full run on the
  binary of #660: $10.7 against $12.8, and the same grades as full on all 61 queries.

## Arms

All arms start from the copy of #643 before its run (the text history as of 2026-11-05 09:27 UTC plus the 88 facts
sessions wrote up to 2026-11-06 12:10 UTC) and run `dream -run -all` on Opus 5.5 through `claude -p` until nothing is
left.

- **none**: the copy as it was, no dream.
- **full**: the run of #643.
- **no-sweep**, **no-split**, **no-expand-translate**, **no-scope**: a research patch, not merged, leaves the named
  tasks out of every packet (`MEMCUE_DREAM_SKIP`). Without queries a fact is not translated, so expand and translate
  go out together.

| arm | passes | splits | actions | expanded | translated | swept | cost |
|---|---|---|---|---|---|---|---|
| full | 7 | 21 | 21 | 215 | 299 | 1,400 | $12.77 |
| no-sweep | 4 | 23 | 4 | 207 | 290 | 0 | $6.98 |
| no-split | 7 | 0 | 17 | 171 | 256 | 1,400 | $9.59 |
| no-expand-translate | 4 | 20 | 8 | 4 | 0 | 800 | $4.16 |
| no-scope | 7 | 16 | 12 | 203 | 286 | 1,400 | $10.67 |

## Queries and grading

- **Queries**: the 287 distinct pairs of query and repository in the author's `search.log` from 2026-11-05 09:30 UTC
  on, which covers the window and the four hours after it: what the sessions searched for while the day's facts were
  written and used. A query with a repository searches that repository, the host and global, as `memcue search` does
  in a repository. One query came from a repository with no facts in the copy; it fails alike in every arm.
- **Grading**: queries whose lists are the same in every arm are ties and go to no judge. For each of the others one
  Opus call reads the query and the distinct lists under letters, in an order of its own each pass, and gives each
  list a score (2 holds what the query looks for, 1 related facts only, 0 nothing useful) and three flags: duplicate,
  outdated, harmful. A flag counts when both passes raise it; a score is the mean of the two passes.
- **Bound**: each pass stopped at $6.5. Pass 1 judged 61 of the 92 queries and pass 2 judged 75; the 61 judged in both
  are the result. The two passes gave the same score to 347 of 366 grades.

## Results

| arm | score | duplicate | outdated | harmful |
|---|---|---|---|---|
| none | 1.52 | 0 | 0 | 0 |
| full | 1.52 | 0 | 0 | 1 |
| no-sweep | 1.57 | 1 | 0 | 1 |
| no-split | 1.52 | 0 | 0 | 1 |
| no-expand-translate | 1.50 | 1 | 0 | 0 |
| no-scope | 1.52 | 0 | 1 | 1 |

By query, against full, 95% bootstrap interval of the mean difference (5,000 resamples):

| arm − full | wins / ties / losses | mean diff | 95% interval |
|---|---|---|---|
| none | 3 / 56 / 2 | +0.01 | [−0.04, +0.07] |
| no-sweep | 3 / 58 / 0 | +0.06 | [+0.00, +0.13] |
| no-split | 2 / 58 / 1 | +0.01 | [−0.04, +0.06] |
| no-expand-translate | 1 / 58 / 2 | −0.02 | [−0.06, +0.02] |
| no-scope | 0 / 61 / 0 | +0.00 | [+0.00, +0.00] |

## Decision

What the run supports, and what it does not:

- **The dream's daily value is too small to see on one day of real searches**, and its cost is $10–13 a day on this
  base. That argues for running it less often, not for removing a task: no task stands out, and the run cannot tell
  what each is worth over weeks (a translation helps a query in the other language, which a day may not hold; a
  duplicate costs a place every time it is found).
- **A proposal for the user to decide**: raise the default `MEMCUE_DREAM_EVERY` from an hour to a day, so a day's facts
  go to the model in one or two passes instead of five or more. Each pass pays the fixed cost of its calls and sweeps
  200 facts whether anything is fresh or not; #643 counted the fixed cost at about a third of a pass. A budget is not
  needed while the dream runs once a day.
- **Not supported**: that any task can go. The +0.06 of no-sweep is 3 queries, inside the noise of two full runs.

## Limits

- One day, one person's base, one run of each arm; the full arm ran on the binary before #660 and the others after it.
- 61 of 92 differing queries were judged in both passes; the 31 left out were not graded, by the cost bound.
- A day shows only what the day's work changes. Translation, splitting and duplicates act over the base's life; a run
  over a week of replay would show more and costs about seven times as much.
- The judge sees the query, not the session, so it grades whether a list answers the query, not whether the session
  went better.
- Fact texts, queries, grades and the patch stay out of the repository.
