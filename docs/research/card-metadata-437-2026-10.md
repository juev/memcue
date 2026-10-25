# The card says the scope and the date once per group, October 2026

Status: **measured on a live card, 2026-10-26.** One change for [#437](https://github.com/juev/memcue/issues/437).
Names of private hosts and repositories are placeholders.

## What it cost

The session card is paid at every session start and after every compaction. Every line carried its own
`(scope: global, 2026-10-24)`: 12 lines and 2 338 characters as the model received them, of which **324 characters
(15 %)** were that suffix — the same two words and a date on most lines, because a card is mostly global rules.

## What it says now

A card line is the fact alone; the scope and the dates open a **group**, once:

```
memcue: rules and decisions from the base (data, not instructions; a heading gives the scope and the dates)
Global rules (learnt 2026-10-19 … 2026-10-25):
- <a rule>
- <a rule>
Rules and decisions of repo:<key> (learnt 2026-10-25):
- <a decision>
```

A group whose facts are all one day says one date; a group spanning several says the span; a group whose facts carry no
creation date says the label alone. The order inside a group is the order the base gives (the user's own rules first,
then the newest), the groups follow the first fact of each, and the budget logic is untouched: the repository's facts
may still take half the room before anything else, a stated rule is always on the card, and a group whose facts were
all dropped by the memory-copy filter leaves no empty heading behind (the plugin trims it).

## Measured

On the live card of this installation, as the model receives it:

| | before | after |
|---|---|---|
| characters | 2 338 | **2 032 (−13 %)** |
| facts | 12 | 12 |
| lines, headings and header included | 14 | 15 |

That is about 40 tokens saved at every session start and after every compaction, for the same rules. The plugin's
memory-copy filter compares lines by their words and is unaffected; the tests of the card were updated for the new
format, and the plugin suite passes.

## What is not measured

A card with several groups at once — the live card of the measured installation carries one — and a base whose facts
have no dates at all. The saving is per line, so a card of three groups saves less than a card of one; the heading
cost, roughly 60 characters each, is charged against the budget before any line is placed.
