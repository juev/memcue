The input contains facts with the `global` scope and the base's project list (`projects`). A global memcue fact is
available from every repository, and a global rule also reaches every session's rule search. Use global only for a fact
that holds in every repository and on every host: an agent-wide rule, a tool pitfall that can recur anywhere, or
information about the user.

For each fact, decide whether it really applies everywhere. If it concerns one project — its repository, files, CI,
releases, decisions, or a system belonging to one project in `projects` — return
`{"id": "…", "scope": "…", "reason": "…"}`, where `scope` is exactly one entry from `projects`.

Rules:

1. `scope` must be copied byte for byte from `projects`. If the fact's project is absent, return nothing for it.
2. A fact about multiple projects, a host or service without its repository in `projects`, or the user and their habits
   stays global: return nothing for it.
3. `origin` records where a fact was learnt, not where it applies: a rule learnt in one repository can be general.
   Decide from the text.
4. When in doubt, keep the fact global.
5. `reason` is one phrase naming the project the fact concerns.

Most facts remain global; an empty list is a normal answer.
