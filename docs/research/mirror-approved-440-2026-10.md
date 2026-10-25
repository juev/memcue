# A memory note is searchable when it is written, October 2026

Status: **measured on a live installation, 2026-10-26.** One change for
[#440](https://github.com/juev/memcue/issues/440). Placeholders stand for private hosts and repositories.

## What was wrong

Every write of the built-in memory tool was mirrored into the base as a **candidate**, and a candidate has no index row
by design: `review = 'candidate'` is stored, not searched. So between the write and the next vet run the user's own
latest memory was invisible, and the symptom was a *stale* answer rather than an empty one — an older approved fact on
the same subject answered in its place.

Measured on the live base: **8 candidates in the queue, all 8 mirrors** of memory notes, the newest written in the same
hour. Asked for the current wording of one of them, the search returned the earlier corrected fact instead; the mirror
did not appear at all.

## What changed

- **The mirror is approved at the write.** The note is the user's own words, the source of truth is the note, and the
  queue is for judgement, not for a decision code can already make. The mirror is written with `-scope global`, so it is
  also reachable by `-in global` and by the repository default, and it is not a `rule`, so it does not double the card
  (the plugin's memory-copy filter would drop it there anyway).
- **The stale mirror is still retired, by rule.** Vet's suspects pass — which already looks at approved facts for
  variables, verbs, versions and past reminders — now also looks for an approved mirror whose text the notes no longer
  hold: `vet.HoldsNote` is the single question the queue's approval rule and the suspects' retirement rule both ask. A
  note deleted or rewritten by hand therefore still takes its mirror out of recall, without a model in the loop.

## What it gives

- A memory write is searchable in the next turn of the same session, not after the next vet run.
- The candidate queue no longer grows with mirrors: on an installation that only writes memory notes it stays empty.
- The retire-on-delete path is unchanged and covered by `TestVetRetiresAnApprovedMirrorWhoseNoteIsGone`; the plugin suite
  covers the write-then-search case (`test_a_mirror_is_approved_at_the_write_and_searchable`) and the replace and remove
  paths, which now look at the approved fact instead of the pending list.

## The existing queue

The 8 mirrors already waiting are approved in the same pass that installs this release, so the base stops carrying a
queue of its own memory. What is not measured: a note edited by hand in the file rather than through the memory tool —
the next vet run retires its mirror, and the new text is only mirrored when the tool writes it again.
