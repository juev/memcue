You are tidying a software agent's knowledge base. It stores atomic facts and retrieves them by words before user
messages. Duplicates waste result slots, contradictory facts mislead the agent, and one-off records add noise.

The input contains groups of similar facts and singleton noise candidates. Each similar group includes at least one new
fact. Every fact has an `id`, an `origin` recording where and when it was learnt, an optional `scope` recording where
it applies, and a creation date.

Choose actions for each group:

- `retire` removes a fact. Use `kind: "duplicate"` when another fact in the group covers everything it says;
  `kind: "outdated"` when a later fact about the same system explicitly contradicts or replaces it;
  `kind: "resolved"` when the text explicitly says the problem was fixed or the workaround is no longer needed; or
  `kind: "noise"` when the fact itself is explicitly a one-off record rather than durable knowledge. For `duplicate`
  and `outdated`, `by` names the covering or contradicting fact. For `resolved`, `by` is optional when the fact's own
  text carries the evidence and otherwise names another fact in the group. For `noise`, `by` is unnecessary, and only
  facts marked as noise candidates are eligible.
- `correct` replaces a fact's text to merge unique details from a retired duplicate or to update an obsolete part
  using a later fact in the group. Return the new `content`.

A fact from another project is not a duplicate. When `scope` or the repository in `origin` differs, keep each project's
copy even if the texts match; do not merge or retire it. Memcue rejects cross-project retirement. A `dir:` path and a
`repo:` key for one clone, or an old and new name for one repository, count as the same project.

When merging duplicates, retire the duplicate with the lexicographically larger `id` and `correct` the smaller one if
the retired fact has unique details. Always keep the smaller id so every machine converges on the same copy.

Rules:

1. Build corrected text only from facts in the group. Add no new information, generalizations, or guesses. Preserve
   paths, commands, versions, ports, and identifiers byte for byte.
2. A corrected fact is self-contained and names its system, host, repository, or path: one statement in one to three
   sentences. If a merged text would be longer, do not merge.
3. Contradiction requires the same subject and an explicit later state: changed, fixed, or no longer working. Different
   aspects of one topic, different systems, and facts merely sharing words need no action.
4. When it is unclear which fact is correct, take no action.
5. After the actions, every ordinary similar group must retain at least one fact. A singleton explicitly marked as a
   noise candidate is the only group that may become empty.
6. Every action needs a one-phrase `reason`. Every `retire` also needs `kind`. `by` is required for `duplicate` and
   `outdated`, optional for `resolved`, and unnecessary for `noise`. Never retire a fact named as another action's
   `by`.
7. `resolved` requires explicit evidence in the packet: the fact itself or its `by` fact says the error was fixed, the
   workaround is obsolete, or a replacement superseded it. "Probably fixed", a PR number alone, and a finished
   experiment without an explicit resolution are not evidence. Never guess repository state outside the packet.
8. `noise` is limited to content that explicitly records task progress or a one-off observation, a finished experiment,
   a rejected candidate, a release note or one-off release incident, transient authentication/CI/process state,
   obsolete installation state, or a context-free fragment. Do not use it for reusable research limitations, platform
   behavior, durable host configuration, user rules, or decisions.
9. Never retire a fact merely because it is old, unique, long, unused in search, attached to a merged PR, or unlikely
   to recur. Do not retire research or operational knowledge merely to move it elsewhere; such a move is a separate
   verified task.

Most groups need no action; an empty action list is a normal answer.
