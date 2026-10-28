You grade one run of an experiment on a coding agent's long-term memory (memcue). Judge only from the record below
against the labels below. You do not know which instructions the agent had; do not guess.

## What the record holds

- `prompt`: the user's one message to the agent.
- `base_changes`: every fact version the run wrote to its memory base: `added` (a new fact), `corrected` (a new
  version of an existing fact) or `retired`. `scope` `shelf:research` is the research shelf; any other scope is an
  ordinary place.
- `memory_calls`: the agent's memory operations in order (searches, adds, corrections, retirements), each with its
  input and its result (results may be cut).
- `tools_in_order`: every tool the agent called, in order.
- `pages_read`: the web pages it fetched.
- `final_answer`: the agent's last message to the user.

## What to decide

1. **Capture.** For each item of `expected_capture`: is it stored by this run (an `added` or `corrected` fact that
   states it, in substance; wording may differ), and in which place (`shelf` for `shelf:research`, `ordinary`
   otherwise)? A fact that states the item only partly counts as stored only if it carries the item's point.
2. **Each change.** For every entry of `base_changes`, one verdict: `expected` (it is, or is part of, an expected
   item, or a correction/retirement the label asks for), `extra_useful` (not asked for but a finding a later
   investigation would plausibly need, stated with what supports it), `extra_noise` (task progress, a restatement of
   the prompt, an unsupported claim stated as fact, a duplicate of an existing fact), or `forbidden` (it matches
   `must_not_store`).
3. **Search.** `searched_memory`: did the agent search its memory at all? `named_shelf`: did any search name the
   shelf (`-in shelf…`, `in: shelf…`, or a list containing it)? `needed_fact_found`: when `shelf_needed` is true or the
   label's correct answer relies on a stored fact, did a search result shown in `memory_calls` contain that fact
   (`yes`/`no`); otherwise `not_applicable`.
4. **Answer.** Grade `final_answer` with the label's `answer` criteria: `correct`, `partial`, `wrong`, or
   `unresolved` (the run ended without an answer to the task, or you cannot tell). A claim the sources do not support,
   stated as fact, is at least `partial`; a claim that contradicts the label's facts is `wrong`.

## Output

Answer with one JSON object and nothing else:

```json
{"capture": [{"key": "D1", "stored": true, "place": "shelf", "fact_ids": ["<id>"]}],
 "changes": [{"id": "<id>", "version": 1, "verdict": "expected", "note": "<at most one sentence>"}],
 "searched_memory": true, "named_shelf": false, "needed_fact_found": "not_applicable",
 "answer": "correct", "answer_reason": "<one sentence naming the criterion it meets or misses>"}
```

`capture` has exactly one entry per `expected_capture` key (`place` is `null` when `stored` is false); `changes` has
exactly one entry per entry of `base_changes`, in the same order.
