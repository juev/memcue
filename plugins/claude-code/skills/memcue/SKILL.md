---
name: memcue
description: Write to long-term memory (memcue) when the user explicitly states a lasting preference or asks to remember or forget something, or when a verified finding would change what a later session does and is not already kept in the session or repository docs; also when a stored fact turns out wrong or outdated.
---

# memcue: writing to memory

Memory is a base of atomic facts. You read it with `memcue search [flags] "<words>"`, as the session-start note says.
You also write it: nothing is extracted from the session afterwards, so a fact you do not store now is lost. Store it
when you learn it, not at the end of the session.

Write new and corrected facts in English regardless of the chat language. Preserve identifiers, commands, paths, versions, exact values and verbatim quotes. Keep the user's reply language unchanged.

Before storing, ask what a later session would do differently because it knows this fact. Keep task progress, completed outcomes, and paths or procedures already documented in the repository in the session or repository docs. Store a reusable pitfall only when its cause and fix are verified and a later fresh checkout would hit it again.

Write the first search query in English regardless of the chat language; if English misses, retry with synonyms and Russian or another legacy language.

## What to store

- **Standing rules and corrections explicitly stated by the user**, with the reason when the user gave one: `-kind rule`, `-scope global` unless
  the user ties it to one repository (`-scope repo`).
- **Decisions of the user** on goals, scope and approach that the code and the git history do not show:
  `-kind decision -scope repo`. `-scope global` only for a decision that holds in every repository and on every host:
  a session searches its repository and the global facts, so a global decision about a single project or task comes
  up in every other project.
- **Pitfalls** found by an error or a check: what happens, why, and the workaround: `-kind pitfall -source tool`.
- **Environment outside the repository**: hosts and what runs there, services, where logs and data live, ids and
  addresses of external systems: `-kind environment -source tool` with `-scope host:<name>` or `-scope service:<name>`.
- **Procedures the user asks to remember**: how something is installed, released or restarted, with the exact
  commands: `-kind procedure`. Only on request; a procedure you merely ran is not stored.
- **Events the next days need**: a release rolled out, an outage, a host taken down: `-kind event`. An event expires
  after a week (`MEMCUE_EVENT_TTL_DAYS`) unless `-expires` gives a date.

Use `-source user` only for a standing rule or decision the user explicitly states as a lasting preference, or asks you to remember. Choosing `-kind rule` or `decision` does not make an inference the user's words. Use `-source tool` for facts directly shown by a command or file, and `-source agent` for your conclusions. Name the source every time.

Do not store: the progress of the task, what the repository, its git history or a command shows, installation state
(versions, paths of binaries, defaults of a config), multi-step procedures the user did not ask to remember (they belong in a skill or the docs), and
your own guesses nothing confirmed.

When a command fails and you fix or work around it, store the cause and the fix as a pitfall before you finish if a later session in a fresh checkout would hit it again — even when the cause is in the repository's own files; what the repository shows is its documents, not a failure you found by running it.

Store it with `memcue add -kind pitfall -source tool -- "<cause and fix>"`.

A pitfall requires reproducible environmental, platform, tool, or repository behavior, a verified cause and fix or workaround, and relevance to a later session in a fresh checkout. Do not store typos or invocations resolved by the tool's own help; transient network/authentication or CI-runner state; a defect the current task just fixed; finished experiments, rejected candidates, release notes, or one-off observations; obsolete migration/install state; or unverified and context-free fragments. Design decisions, durable host configuration, and external research limitations remain capturable under their appropriate kind, scope, or shelf.

Agent/tool pitfall and environment facts expire after 30 days by default. Use `-expires YYYY-MM-DD` for a different review date, or `-no-expires` only for deliberately durable knowledge such as repeatable platform behavior.

## Remember

Search first: if a stored fact says the same or an older version of it, correct that fact instead of adding a second
one.

```bash
memcue search -in all "<words of the fact>"
memcue add -kind rule -scope global -source user -- "<fact>"
```

- One statement in one to three sentences; a rule goes with its reason.
- Self-contained: name the system, host, repository, path. No "it", "this project", "above".
- Exact values byte for byte: paths, commands with flags, versions, ports.
- The origin is the repository of the working directory and today's date; pass `-origin` only to name another.

## When search finds nothing

The agent of an earlier session may have forgotten to store what you need, and it is often still in that session's
transcript. Run `memcue sessions "<words>"` with the words of the empty search: it reads the text of this machine's
earlier sessions, with no index, and prints the newest passages that hold the words, each with its date and session.
A passage is history, not a fact: check it against the current state, and store what holds with `memcue add` so the
next session finds it by search.

## Correct or forget

Find the fact first; `search` prints IDs, best match first. `-in all` looks in every repository: by default search
looks at this repository, this host and the global facts and leaves out facts bound to other repositories, and
`-expired` adds facts whose expiry has passed:

```bash
memcue search -in all "<words of the fact>"
memcue correct -expected-version VERSION ID "<corrected fact>"   # new version
memcue retire -expected-version VERSION ID                       # no longer recalled; the history keeps it
memcue reopen ID                       # undo a retire made by mistake
```

Correct when the fact changed; retire when it no longer holds and nothing replaces it. Correct or retire a stored fact
only when the user says it is wrong or a command or a file of this session shows it: a newer fact that says otherwise
is not that evidence on its own. When nothing in the session settles it, answer from the facts and their days, name
the conflict, and ask before you correct or retire one. Tell the user which fact you changed.

## Findings from outside sources: the research shelf

What you read in a web page, another project's documentation, a changelog, a paper or a benchmark that is not part of
the repository, and a later investigation of the same subject would need (a limit, a behaviour, a trade-off or a
limitation of a named system), goes on the research shelf, which ordinary search leaves out:

```bash
memcue search -in shelf:research "<words of the finding>"
memcue add -shelf research -source tool -- "<finding>. Source: <document, version or date>."
```

- Store what your conclusion rests on and its limits, not every detail of the page.
- Store what the source states, not your conclusion for the task and not a decision the user did not make.
- Name the source; say when a claim is only a vendor's or an anonymous one, and leave out what nothing supports.
- A document in the repository is what the repository shows: do not store it. A pitfall, a host or a fact of this
  repository goes where it goes today, not on the shelf.
- Correct a shelf fact that says the same or an older version instead of adding a second one.
- When a later task returns to the subject, search the shelf (`-in shelf:research`) as well as the ordinary places.
  Before relying on a shelf fact, compare its version or date with what the workspace uses. An answer built on it
  says no more than the fact and its source state, and marks an inference as one.

Read `version` from the earlier `search -json` result and pass that caller-observed version to correct/retire. On conflict, read again and make a new decision; never fetch latest immediately before writing to conceal a stale decision, and never replay automatically. PostgreSQL requires the version; old local CLI calls remain compatible.
