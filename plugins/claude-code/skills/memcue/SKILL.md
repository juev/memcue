---
name: memcue
description: Write to long-term memory (memcue). Use as soon as the user states a rule or a correction ("не так, делай так", "always", "never"), makes a decision the code does not show, or you find a pitfall, a host, a service or a pointer outside the repository that a later session will need; also when the user asks to remember or forget something ("запомни", "remember this"), or a stored fact turns out wrong or outdated.
---

# memcue: writing to memory

Memory is a base of atomic facts. You read it with `memcue search [flags] "<words>"`, as the session-start note says.
You also write it: nothing is extracted from the session afterwards, so a fact you do not store now is lost. Store it
when you learn it, not at the end of the session.

## What to store

- **Rules and corrections of the user**, with the reason when the user gave one: `-kind rule`, `-scope global` unless
  the user ties it to one repository (`-scope repo`).
- **Decisions of the user** on goals, scope and approach that the code and the git history do not show:
  `-kind decision -scope repo`.
- **Pitfalls** found by an error or a check: what happens, why, and the workaround: `-kind pitfall -source tool`.
- **Environment outside the repository**: hosts and what runs there, services, where logs and data live, ids and
  addresses of external systems: `-kind environment -source tool` with `-scope host:<name>` or `-scope service:<name>`.
- **Procedures the user asks to remember**: how something is installed, released or restarted, with the exact
  commands: `-kind procedure`. Only on request; a procedure you merely ran is not stored.
- **Events the next days need**: a release rolled out, an outage, a host taken down: `-kind event`. An event expires
  after a week (`MEMCUE_EVENT_TTL_DAYS`) unless `-expires` gives a date.

Rules and decisions take `-source user` (the default): they are the user's words, and only they reach the session
card, which carries the global rules and the decisions of the repository even when a prompt shares no word with them.

Do not store: the progress of the task, what the repository, its git history or a command shows, installation state
(versions, paths of binaries, defaults of a config), multi-step procedures the user did not ask to remember (they belong in a skill or the docs), and
your own guesses nothing confirmed.

## Remember

Search first: if a stored fact says the same or an older version of it, correct that fact instead of adding a second
one.

```bash
memcue search -in all "<words of the fact>"
memcue add -kind rule -scope global -keywords "word1, word2, синоним, english word" -- "<fact>"
```

- One statement in one to three sentences; a rule goes with its reason.
- Self-contained: name the system, host, repository, path. No "it", "this project", "above".
- Exact values byte for byte: paths, commands with flags, versions, ports.
- Keywords: 3–8 words someone would ask with, including synonyms and both languages: until a dream translates the
  fact, a search in the other language finds it only through these words.
- The origin is the repository of the working directory and today's date; pass `-origin` only to name another.

## Correct or forget

Find the fact first; `search` prints IDs, best match first. `-in all` looks in every repository: by default search
looks at this repository, this host and the global facts and leaves out facts bound to other repositories, and
`-expired` adds facts whose expiry has passed:

```bash
memcue search -in all "<words of the fact>"
memcue correct ID "<corrected fact>"   # new version; keywords stay unless -keywords is given
memcue retire ID                       # no longer recalled; the history keeps it
memcue reopen ID                       # undo a retire made by mistake
```

Correct when the fact changed; retire when it no longer holds and nothing replaces it. Tell the user which fact you
changed.

## Findings from outside sources: the research shelf

What you read in a web page, another project's documentation, a changelog, a paper or a benchmark that is not part of
the repository, and a later investigation of the same subject would need (a limit, a behaviour, a trade-off or a
limitation of a named system), goes on the research shelf, which ordinary search leaves out:

```bash
memcue search -in shelf:research "<words of the finding>"
memcue add -shelf research -source tool -keywords "word1, word2, синоним" -- "<finding>. Source: <document, version or date>."
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
