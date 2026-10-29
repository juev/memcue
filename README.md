# memcue

Long-term memory for Claude Code. memcue keeps a base of short, self-contained facts in SQLite, with a text history git can sync, and works
through hooks:

- **At session start, and again after a compaction or `/clear`** it tells the agent to search memcue before it
  answers a question that may depend on earlier sessions, and sends a card: global rules and the rules and decisions
  of the current repository, the ones you stated first, within about 1.2 thousand tokens. The card is how a rule the
  agent would not think to search for still reaches it.
- **When the agent needs something from earlier sessions** it runs `memcue search` itself, with its own words:
  synonyms, the system's name, both languages. Nothing is injected with a prompt (#361): the agent's own queries
  answered more questions right than facts matched to the prompt's words (see [Results](#results)). A search returns
  at most five facts, and only those that fit: a fact whose BM25 is below 0.6 of the best one's is left out, so a
  query one fact answers gets that fact alone (#407, [search-floor](docs/research/search-floor-2026-10.md)).
- **When the agent learns something that will matter later** (a rule or a correction you gave, a decision the code
  does not show, a pitfall, a host or a service outside the repository) it stores it at once with `memcue add`,
  through the bundled skill. Nothing is extracted from the transcript afterwards (#494): the agent decides what is
  worth keeping, the way Claude Code's built-in memory does.
- **When you ask** ("remember…", "that is outdated") the agent adds, corrects or retires a fact the same way.

Search is lexical (SQLite FTS5 with Snowball stemming for English and Russian), runs offline in one binary and
takes about 10 ms per query. A fact keeps where and when it was learnt and where it holds (`scope`), and facts from
every repository are searched: what you learnt in one project is found in another. On equal scores a fact of the
current host comes first, then one learnt in the session's project, then a global one; no fact is excluded, because
working on a server from a laptop is normal. An uppercase
ticket key such as `PROJ-1234`, `#123` or `!45` is kept as one exact term, so a query naming it finds that ticket
and not another number; bare numbers, ports and versions are still left out of the index. A versioned name such as
`PostgreSQL-16` is not a ticket: it keeps `PostgreSQL` and drops `16`.

The base can live in a git repository and be synced between machines; memcue ships a merge driver for it. The same
base works for [Hermes Agent](plugins/hermes/README.md) and for
[DeepSeek Harness](plugins/deepseek-harness/README.md).

memcue is young: the 0.4 line has been measured on one developer's memory and sessions (see [Results](#results)) and
not yet run for long in daily work.

## Requirements

- macOS or Linux.
- Claude Code. Only `memcue migrate` and `memcue dream -run`, which a session start runs in the background when a
  dream is due, call a model, through the `claude` command on `PATH` or an OpenAI-compatible endpoint (see
  [Configuration](#configuration)).
- Go 1.27 or later, to build from source.

## Compatibility

This release is `0.24.0`; the fact-base schema is 6 (see [Upgrade](#upgrade)). A version-6 base does not open in 0.20.0
or older, so stop every older memcue and back the base up before the first open. A text history (0.22.0) is not read
by 0.21.2 or older, so upgrade every machine that syncs the base before the move. The three plugin manifests carry the
same version, and a release bumps all of them together.

| memcue | schema | Claude Code plugin | DSH plugin | Hermes plugin |
|---|---|---|---|---|
| 0.24.0 | 6 | 0.24.0 | 0.24.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.24.0 (`hermes-agent` 0.15.2) |
| 0.23.0 | 6 | 0.23.0 | 0.23.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.23.0 (`hermes-agent` 0.15.2) |
| 0.22.1 | 6 | 0.22.1 | 0.22.1 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.22.1 (`hermes-agent` 0.15.2) |
| 0.22.0 | 6 | 0.22.0 | 0.22.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.22.0 (`hermes-agent` 0.15.2) |
| 0.21.2 | 6 | 0.21.2 | 0.21.2 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.21.2 (`hermes-agent` 0.15.2) |
| 0.21.1 | 6 | 0.21.1 | 0.21.1 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.21.1 (`hermes-agent` 0.15.2) |
| 0.21.0 | 6 | 0.21.0 | 0.21.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.21.0 (`hermes-agent` 0.15.2) |
| 0.20.0 | 5 | 0.20.0 | 0.20.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.20.0 (`hermes-agent` 0.15.2) |
| 0.19.0 | 5 | 0.19.0 | 0.19.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.19.0 (`hermes-agent` 0.15.2) |
| 0.18.1 | 5 | 0.18.1 | 0.18.1 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.18.1 (`hermes-agent` 0.15.2) |
| 0.18.0 | 5 | 0.18.0 | 0.18.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.18.0 (`hermes-agent` 0.15.2) |
| 0.17.0 | 5 | 0.17.0 | 0.17.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.17.0 (`hermes-agent` 0.15.2) |
| 0.16.0 | 5 | 0.16.0 | 0.16.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.16.0 (`hermes-agent` 0.15.2) |
| 0.15.0 | 5 | 0.15.0 | 0.15.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.15.0 (`hermes-agent` 0.15.2) |
| 0.14.0 | 5 | 0.14.0 | 0.14.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.14.0 (`hermes-agent` 0.15.2) |
| 0.13.0 | 5 | 0.13.0 | 0.13.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.13.0 (`hermes-agent` 0.15.2) |
| 0.12.0 | 5 | 0.12.0 | 0.12.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.12.0 (`hermes-agent` 0.15.2) |
| 0.11.1 | 5 | 0.11.1 | 0.11.1 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.11.1 (`hermes-agent` 0.15.2) |
| 0.11.0 | 5 | 0.11.0 | 0.11.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.11.0 (`hermes-agent` 0.15.2) |
| 0.10.0 | 5 | 0.10.0 | 0.10.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.10.0 (`hermes-agent` 0.15.2) |
| 0.9.0 | 5 | 0.9.0 | 0.9.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.9.0 (`hermes-agent` 0.15.2) |
| 0.8.0 | 5 | 0.8.0 | 0.8.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.8.0 (`hermes-agent` 0.15.2) |
| 0.7.0 | 5 | 0.7.0 | 0.7.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.7.0 (`hermes-agent` 0.15.2) |
| 0.6.0 | 5 | 0.6.0 | 0.6.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.6.0 (`hermes-agent` 0.15.2) |
| 0.5.3 | 5 | 0.5.3 | 0.5.3 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.5.3 (`hermes-agent` 0.15.2) |
| 0.5.2 | 5 | 0.5.2 | 0.5.2 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.5.2 (`hermes-agent` 0.15.2) |
| 0.5.1 | 5 | 0.5.1 | 0.5.1 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.5.1 (`hermes-agent` 0.15.2) |
| 0.5.0 | 5 | 0.5.0 | 0.5.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.5.0 (`hermes-agent` 0.15.2) |
| 0.4.0 | 4 | 0.4.0 | 0.4.0 (`dsh >=0.2.0-rc.2 <0.3.0`) | 0.4.0 (`hermes-agent` 0.15.2) |

The DSH peer range is enforced by the harness compatibility check; the Hermes row names the version the provider
tests ran against. The binary and the adapters check each other's release: the DSH and Hermes plugins ask
`memcue version -json` at startup and turn the session card off with one warning on a binary older
than 0.4.0 (search keeps working), and the Claude Code `SessionStart` hook checks `memcue version -minimum 0.4.0` and
prints the same hint. A build from source reports a suffixed version and is treated as the current tree. Install the
plugin and the binary from the same release.

## Install

1. Optional: install the binary yourself and put `memcue` on `PATH`. Download it for your system from
   [Releases](https://github.com/juev/memcue/releases), or build it:

   ```bash
   go install github.com/juev/memcue/cmd/memcue@latest
   ```

   Without this step the plugin installs the binary of its own release (see [Binary from the plugin](#binary-from-the-plugin)).

2. Install the Claude Code plugin (hooks and skill) from this repository:

   ```text
   /plugin install memcue --marketplace juev/memcue
   ```

3. Turn off Claude Code's built-in memory, so the agent does not keep a second memory in files. Add to
   `~/.claude/settings.json`:

   ```json
   { "autoMemoryEnabled": false }
   ```

   or set `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`. With the plugin on, each session starts with a short note that tells
   the agent its memory is memcue and to write it. memcue does not change the setting itself: while neither is set,
   the session start says that built-in memory is still on (#494).

4. Optional: bring over what the built-in memory already holds. Each note goes through the model once; a note
   already in the base is skipped, so the command can be repeated.

   ```bash
   memcue migrate -list -all   # what would be migrated
   memcue migrate -all         # migrate; -j sets how many notes run at once (default 4)
   ```

5. Optional: if the base is in a git repository (for example `~/.claude` is one), set up the merge driver on every
   machine before the base is first committed:

   ```bash
   memcue install
   ```

   It registers the driver in the repository's git config and routes the base to it: a text history gets
   `facts/*/*.jsonl merge=memcue` in `memory/.gitattributes`, next to the text, and a base of the old format gets
   `memory/facts.db merge=memcue diff=memcue` in the root `.gitattributes`. The driver is a small script in `.git` that finds the binary through `MEMCUE_BIN`, then `PATH`,
   then the path it had when `memcue install` ran, so an upgrade that moves the binary does not break the next merge.
   Without a repository memcue works the same; the base is just not synced. A merge carries no vectors. A machine
   whose base is in git without the driver hears it on the session card, Hermes and DSH included, until
   `memcue install` runs there. When two machines change a fact apart, the merge keeps every version and the latest
   by creation time is current, with one exception: a correction of the text wins over a retirement made without
   knowing it, whichever is later (#544). A retirement made after the correction was merged, or one against a change
   of keywords or labels only, still retires the fact.

### The note file and the base

A built-in memory note file (`MEMORY.md` of Claude Code, `MEMORY.md` and `USER.md` of Hermes) is injected whole at
every turn, while a fact costs nothing until a search returns it. So the note file carries only what must hold before
any search can run; everything else is a fact. The Hermes mirror copies every note into the base on its own, which
means most entries end up in the file and in the base at once. `memcue notes` shows which:

```bash
memcue notes                 # one line per entry: ok ID kind/scope, or new with its size, then a summary
memcue notes -dir ~/.hermes/memories -json
```

It reads every `*.md` directly inside `-dir` (default `MEMCUE_MEMORY_DIR`, else `~/.claude/memory`), splits the files
into entries at a blank line or a `§`, and compares each entry with the live facts by a word rule: at least five
shared words, making up at least 0.30 of the words of both or 0.40 of the shorter one; `-min-share` replaces both
shares with one, which makes the rule stricter or looser. It writes
nothing and calls no model. An entry marked `ok` can leave the file; a `new` one moves with
`memcue add -kind K -scope S -- "TEXT"` first.

Do not empty the note file before the session card has a budget of its own, `MEMCUE_CARD_BUDGET` over the stated
rules too (#487): without it the card grows by exactly the characters the file lost, because the card lines left out
as copies of the notes come back.

### Binary from the plugin

All three plugins (Claude Code, DSH, Hermes) find the binary in this order: the one configured explicitly
(`MEMCUE_BIN`, or `binary` in the DSH and Hermes config); `memcue` on `PATH` when it is the plugin's release or newer;
the copy the plugin installed; an older `memcue` on `PATH`, so search keeps working. When none of them is the plugin's
release, the plugin downloads that release from GitHub, checks the archive against the release's `checksums.txt`,
and installs it:

| Host | Where | When |
|---|---|---|
| Claude Code | `${XDG_DATA_HOME:-~/.local/share}/memcue/<version>/memcue` | in `SessionStart`, before the first answer of the session; the directory goes on the session's `PATH`, so the skill finds `memcue` too |
| DSH | the same directory, shared with Claude Code | in the background at plugin start |
| Hermes | `$HERMES_HOME/memcue/bin/<version>/memcue`, which outlives the container | in the background at the first `initialize` |

A configured binary is never replaced. Set `MEMCUE_AUTO_INSTALL=0` to install nothing; the plugin then says that the
binary is missing, as before. The checksum guards against a broken download, not against a replaced release:
`checksums.txt` comes from the same release. Claude Code logs failed installs to
`${XDG_STATE_HOME:-~/.local/state}/memcue/install.log` and tries again after an hour, since `SessionStart` waits for
the download; DSH and Hermes download in the background and log failures as warnings. The Claude Code script
needs `curl`, `tar` and `sha256sum` or `shasum`. A binary installed this way is not on your shell's `PATH`: to run
`memcue install` or other commands by hand, call it by its path or add the directory to `PATH`.

## Projects

A fact's origin is `<project key> <date>`, and a fact that holds only in one project has the scope of that key. The key
comes from where the session works, read from the files and never by running git (#343):

- **A git repository** is named by its remote, normalized to `host/path`: `git@example.com:owner/name.git`,
  `ssh://git@example.com:443/owner/name.git` and `https://example.com/owner/name` are all `example.com/owner/name`.
  The remote named `origin` writes the key, or the only remote when there is no `origin`. A session holds the keys of
  all its remotes, so a fact learnt in a fork's `upstream` or on a mirror's other host holds in it too. Remotes are
  read from the repository's config (the main repository's for a linked worktree, plus `config.worktree` under
  `extensions.worktreeConfig`), with the `url.<base>.insteadOf` rewrites of that config and of the global one applied;
  `[include] path` is followed, `includeIf` is not. The directory's own name plays no part.
- **A repository without a remote** is `dir:<path of its main worktree>`.
- **Outside git** the project is the `module` of the nearest `go.mod`, or else `dir:<working directory>`.
- **No working directory** is no project: a host passes an empty `cwd` for such a session (`-cwd ""` to `add`). The Hermes plugin does so for a session outside git (#347); a session Hermes gives no `cwd`
  takes the directory Hermes falls back to (#354).
- **The Claude config directory** is the global tier; **a temporary directory** names nothing.

The agent chooses only whether a fact holds in the repository (`-scope repo`), everywhere or on a host; memcue writes
the key. A fact written with no scope gets the project its origin names, and `global` when the origin names none
(#442): an empty scope is in no place a search asks for, so only `-in all` found such a fact.

The project narrows and orders `memcue search`. In a session that works in a repository, search looks at that
repository's facts (those learnt in it or scoped to it, `repo:<key>`), this host's and the global ones by default
(#418). Outside a repository, and in the Hermes tool, the default is the whole base. `-in all` searches every
repository, `-in project` searches this repository only, and `-in host`, `-in global` or a scope such as
`-in repo:KEY` name another (#409); a key may be the end of the stored one, `-in repo:memcue`, or the tier of an origin,
`-in repo:_global` (#459). The default was
measured, not assumed: the project's facts alone answered a question about the host with 1 fact where the whole base
gave 5, three of them the host facts that answered it, and 255 of the 391 live facts of that base carry the host scope
([scope-418-2026-10](docs/research/scope-418-2026-10.md)) — so the host and the global facts are in the default, and a
fact of another repository still needs `-in all`. What the wider default costs in precision on a query corpus is not
measured; the 35 queries of #409, where the project alone left 22 useful facts against 32 for the whole base, are the
reason it is wider. An agent that finds nothing here searches again with `-in all`. Of facts with equal scores, one
learnt in the session's project comes before a global one and the rest.
`search -cwd DIR` names the session's directory; the default is the working directory.

**The shelf** holds what a session read from the outside world: another system's limits, versions and terms, wanted
only when the same subject comes up again (#433). Such a fact is stored in the same base under the scope
`shelf:<slug>`, and in practice there is one shelf, `shelf:research`. The agent puts a fact there itself:
`memcue add -shelf research` (the `shelf` parameter of `memcue_add` in Hermes and DSH) is `-scope shelf:research`;
a slug is `[a-z0-9][a-z0-9-]{0,63}`. Until #494 the extraction shelved a fact by the tool its quote came from.

Every host tells its model the same about the shelf (#567), in the words measured in #531
([research-shelf-confirm-531](docs/research/research-shelf-confirm-531-2026-10.md)): a finding from a web page,
another project's documentation, a changelog, a paper or a benchmark outside the repository goes on the shelf with its
source, as what the conclusion rests on and its limits; a document of the repository and a fact of this repository's
work do not; when a task returns to a researched subject, search the shelf too, and compare a finding's version or
date with what the workspace uses before relying on it. On the synthetic grid of #531, Claude Code (Opus 5.5) with
this text stored 21 of 24 outside findings on the shelf (3 without it), reached the shelf in 24 of 24 runs that needed
it (15 without it) and answered 57 of 57 correctly (52); Hermes on `deepseek-flash` stored 13 of 24 (3) and reached
the shelf 21 of 24 (15), with answers about the same. DSH carries the Hermes wording and was not measured.

A shelf is read only when a search names it: `-in shelf:research`, or `-in shelf` for every shelf. Every other place
leaves the shelves out, `-in all` included: a page read once is noise in a search about anything else, and an `all`
that still read the shelf would hide nothing. Places are a union, so `-in all,shelf:research` reads every fact off
the shelves and that one shelf, in either order; a shelf the search does not name stays out even when a named place
such as `project` would reach its fact through the origin (#532). A search that finds nothing, while its words fit a
shelved fact, says where to look: `1 fact on the shelf matches: -in shelf:research`. `search -json` carries each
fact's `scope`, so a fact from the shelf can be told from the rest, and prints one object,
`{"facts": [...], "hints": [...]}`, whose `hints` are the lines the plain answer prints after the facts, this one
included (#453); the Hermes and DSH plugins read both it and the bare array of an older binary.

## Upgrade

### 0.24.0: a fact without `-source` is the agent's, every host stores a failure it found (#574, #566, #577)

- **`add` without `-source` stores `agent`** (#574), not `user`. A script or a person that stores the user's own
  words without the flag must now pass `-source user`: it is the only source that reaches the session card. The
  Claude Code note says which source to name (`-source user` for the user's words, `-source tool` for what a command
  or a file showed) and gives the pitfall line its command. See [Use](#use).
- **Every host tells its model to store a failure it found by running something** (#566): the cause and the fix go
  in as a pitfall when a fresh checkout would hit it again, even when the cause is in the repository's own files.
  The Claude Code note and skill, the Hermes block and the DSH note carry the line measured in #566.
- **A refusal as installation state names the ways out** (#577): the research shelf for what another system does,
  or dropping an incidental version, path or time from the text. Hermes and DSH pass the same message to the model.
- No schema or format changed: the schema stays 6. Changed: the default of `add -source`. From 0.23.0 or 0.22.x,
  replace the binary and the plugins. From 0.21.x or older, follow
  [0.22.0](#0220-the-text-history-a-correction-beats-a-parallel-retirement-530-552-544-549).

### 0.23.0: the dream runs in the background, every host names the research shelf (#569, #568, #563, #562, #561, #570)

- **A due dream runs in the background** (#569). `session-start` (Claude Code) and `session-card` (Hermes, DSH)
  start one `memcue dream -run -background` pass in its own session and return at once, on the endpoint of
  `MEMCUE_API_URL` or on `claude -p` when `claude` is on `PATH`; the dream line is then left out. With neither, the
  line stays and the host's model runs the dream as before. `MEMCUE_DREAM_BACKGROUND=0` turns it off, and so does
  Hermes `dream: false`. A failed dream is tried again an interval later; its output goes to `dream.out`. See
  [Dream](#dream).
- **Hermes: the endpoint in `memory.memcue`** (#569). `api_url`, `model` and `api_key_env` reach the binary as
  `MEMCUE_API_URL`, `MEMCUE_MODEL` and `MEMCUE_API_KEY`, the key read from the named variable of the gateway.
- **Every host names the research shelf** (#568, #563). The Claude Code note and skill, the Hermes block and the DSH
  note carry the words measured in #531; the DSH `memcue_add` takes `shelf` and `memcue_search` names the shelf.
- **Hermes search and writing** (#562, #561). A shelf search names the facts below its floor; the `memcue_add`
  description leaves out what the repository shows, as the other hosts do.
- **`add` on the shelf** (#570). A shelf finding about another system's config default is stored instead of refused as
  installation state.
- No schema or format changed: the schema stays 6. New: the `dream -run -background` flag and the
  `MEMCUE_DREAM_BACKGROUND` variable. From 0.22.x, replace the binary and the plugins. From 0.21.x or older, follow
  [0.22.0](#0220-the-text-history-a-correction-beats-a-parallel-retirement-530-552-544-549).

### 0.22.1: a search on a text history no longer writes the base (#557)

- **An open that finds nothing new writes nothing** (#557). Every open of a text history, a search too, saved two
  stamps of the cache, each a commit with its own fsync. A stamp is now written only when it changes. On the loffe
  disk a search on a copy of the base takes 16.7 ms instead of 20.2 ms (a file base: 15.9 ms); see
  [text-open-cost-557](docs/research/text-open-cost-557-2026-10.md).
- No schema, flag or format changed: the schema stays 6. From 0.22.0, replace the binary and the plugins. From 0.21.x
  or older, follow [0.22.0](#0220-the-text-history-a-correction-beats-a-parallel-retirement-530-552-544-549).

### 0.22.0: the text history, a correction beats a parallel retirement (#530, #552, #544, #549)

- **The base is kept in git as text** (#530, #552). See the move below. Until `migrate -text` runs, memcue keeps
  working with `facts.db` as before. A cache built from the text is written in one transaction: on a copy of the Mac
  base a build from scratch takes 1.6 s.
- **A correction wins over a retirement made without knowing it** (#544, #549). When two machines merge, a fact
  corrected on one and retired on the other stays active with the corrected text, whatever the clocks say. The rule
  judges by the versions each side had seen, so it holds when a side merges the result of an earlier merge. A change
  of keywords alone does not bring a retired fact back.
- No schema changed: the schema stays 6. New command: `memcue migrate -text`. From 0.21.x, replace the binary and the
  plugins on every machine, then move. From 0.20.0 or older, first follow
  [0.21.0](#0210-no-fact-waits-for-approval-schema-6-514-456-521-522).

#### Moving to the text history

A base of the old format is one SQLite file, and git stores the whole file again at every commit: on one machine
that came to 354 MiB of loose objects in ten days ([git-growth-530](docs/research/git-growth-530-2026-10.md)). The
text history keeps one JSON-lines file per fact, `memory/facts/<last character of the ID>/<ID>.jsonl`, a line per
version, so a commit stores only the facts that changed and `git log -p` shows the history of a fact. Search still
runs on SQLite: the base becomes a cache, `facts.db` in the state directory, outside git. Every write goes to the
cache and to the file of the fact. An open reads the directories git changed and takes their versions into the
cache; a missing cache is built from the text. A fact changed on two machines is merged by the same driver and the
same rules as before.

The session card of a synced base of the old format suggests the move. Do it in this order:

1. Install this memcue on every machine that syncs the base, and run `memcue install` there.
2. Stop the sessions, dreams and gateways that write to the base, and sync every machine.
3. On one machine, run `memcue migrate -text`. It writes the text next to the file (`facts.db` becomes `facts/`),
   copies the base into the cache, removes the file and routes the text to the driver.
4. Commit `memory/` (the text, `memory/.gitattributes` and the removal of `facts.db`) and push.
5. Pull on the other machines. A pull removes their `facts.db`, and the next open builds the cache from the text; no
   command is needed.

`MEMCUE_FACTS` and the `facts` option of the plugins may keep naming `facts.db`: once the `facts` directory exists
next to it, memcue opens the text. A base outside the default keeps its dream schedule and logs: its state directory
moves to the key of the new path. A second `migrate -text` is safe; if a `facts.db` is left next to the text, it is
merged into the text and removed. To go back, check out the commit before the move on every machine and install the
older memcue.

### 0.21.2: a shelf named beside `all`, `correct ID -- TEXT`, no doubled versions after a merge (#532, #533, #540, #536)

- **`-in` places are a union with the shelves too** (#532). `-in all,shelf` and `-in all,shelf:research`, in either
  order, now read the named shelf; before, `all` ended the list and the shelf was silently dropped. A named shelf no
  longer lets the other shelves in: `-in project,shelf:research` used to return facts of every shelf learnt in the
  project through their origin. A place after `all` is checked, so `-in all,bogus` is an error. `-in all` alone and
  the default still leave every shelf out.
- **`memcue correct ID -- TEXT` stores TEXT** (#533). A lone `--` right after the ID is a separator, as in `add`;
  before, it was stored as the first word of the fact. A text that starts with the word `--` is written
  `ID -- -- TEXT`, and `ID --` with no text is a usage error. The plugins pass the text as one argument and are not
  affected.
- **A repeated merge no longer doubles a version** (#540). A machine that had not pulled an earlier merge and wrote
  again got a copy of the other machine's correction as a new version at its next merge. Copies that earlier merges
  already wrote stay in the history; the current state of a fact was right all along.
- **The Hermes dashboard shows the provider as ready** (#536). The plugin manifest no longer declares
  `external_dependencies`: the dashboard ran its `memcue version` check with its own `PATH`, so a binary set in
  `memory.memcue.binary` made a working provider look unavailable, and its install button could only fail. Readiness
  now comes from the provider.
- No schema or flag changed: the schema stays 6. From 0.21.0 or 0.21.1, replace the binary and the plugins. From
  0.20.0 or older, follow [0.21.0](#0210-no-fact-waits-for-approval-schema-6-514-456-521-522).

### 0.21.1: the Hermes provider tests run in a standalone install (#526)

- Only tests and this README changed. `tests/test_provider.py` of the Hermes plugin no longer reads the DSH and Claude
  Code plugin sources, so it runs in an installed `plugins/hermes/memcue` on its own; `plugins/test_cross_harness.py`
  keeps that check in the full repository. The binary and the plugins behave as in 0.21.0.
- **The backup and the rollback of 0.21.0 are a checklist now** (#527). The 0.21.0 text copied the base with `cp`
  and restored that copy, which is not safe; see [0.21.0](#0210-no-fact-waits-for-approval-schema-6-514-456-521-522).
  A copy made that way may lack committed writes or hold half of one: check its integrity and schema before relying
  on it.
- No schema or flag changed, and no new migration comes: the schema stays 6. From 0.20.0 or older, stop every older
  memcue, back the base up and upgrade as [0.21.0](#0210-no-fact-waits-for-approval-schema-6-514-456-521-522) says;
  its rollback applies too. From 0.21.0, replace the binary and the plugins.

### 0.21.0: no fact waits for approval; schema 6 (#514, #456, #521, #522)

- **Breaking: no fact waits for approval.** `memcue add -candidate`, `memcue candidates` and `memcue approve` are
  removed: `add` rejects the flag, and the two commands exit 1 and say why. A fact written with `add`, `memcue_add` or
  a Hermes memory mirror, and every fact `import` stores, goes into the search index in the same transaction. A
  search returns it only when it fits the search: see [Use](#use) for the places a search reads, the shelves, expiry
  and the `-n` limit.
- **Breaking: `import` ignores `"review"`.** A record with `"review": true`, or an older file whose facts carry a kind,
  no longer makes candidates: the facts are stored live with their kind, scope, source, evidence and prompt version.
  `migrate -dry-run` still writes `"review": false`, so an older memcue imports its output as live facts too.
- **Candidates and provisional facts already in a base are live.** A fact whose latest version is active is indexed,
  listed by `memcue facts` and read by the dream, whatever its review state. Its rows are not rewritten: it keeps its
  versions, origin, kind, scope, source, evidence and expiry, and `memcue dump` still shows `review=candidate` or
  `review=provisional`. The session card takes it on the same terms as any fact (see [Use](#use)). The next version
  memcue writes for it (`correct`, `reopen`, a dream, a split) drops the state and keeps the expiry; `correct
  -no-expires` clears the expiry as for any fact. Recall still ranks a row that is provisional after the approved ones.
- **The upgrade brings back nothing retired or expired.** It writes no fact row, so a fact whose latest version is
  retired stays retired until `memcue reopen ID`. A candidate or provisional fact past its expiry is handled as any
  expired fact, whatever its review state and before or after a `correct`: search, recall and the card leave it out;
  `search -expired` finds it within the scope the search reads (`-in`, see [Use](#use)); `memcue facts` lists it. The
  dream reads it as it reads an expired approved fact: the packet may ask to split it, expand its queries or translate
  it, but it never joins a group as another fact's neighbour, because the dream finds neighbours by recall.
- **The merge is unchanged: the latest version wins.** When two machines change a fact apart, the merge keeps the
  versions of both and the one created last is current, whatever the review state. So a retire on one machine and a
  correction on another resolve by their times: a correction created after the retire brings the fact back, active
  and searchable, with the correction's text; one created before it leaves the fact retired. A correction written by
  0.20.0 or older may keep `review=candidate` or `review=provisional`; the new memcue indexes that active fact all the
  same, and `memcue dump` shows the state. Both stay in the history,
  and `memcue retire ID` or `memcue reopen ID` settles it again. A retire carries the text its machine saw, so a
  reopen after such a merge brings that text back, not the correction. This is how a merge resolved any two
  concurrent changes before #514 too; a merged-in correction of a fact retired elsewhere is the one way a retired fact
  comes back without a reopen.
- **Breaking: the schema is 6.** Opening a version-5 base rebuilds the search index once and writes no fact row. memcue
  0.20.0 and older refuse a version-6 base with `fact base has schema 6, this memcue knows 5: update memcue`, for
  every command that opens the base, `memcue dump` (the git textconv) and the merge driver included.
- **Upgrade with no older memcue running.** A memcue 0.20.0 process that opened the base before the upgrade keeps
  working on it: its searches still leave the candidates out, and its index rebuild takes them out of the shared index.
  The next open by the new memcue notices this and rebuilds the index, but a process of the new memcue that is already
  open searches the damaged index until then. This README gives a checklist, not commands: no backup or restore
  procedure is shipped or tested, so adapt each item to your environment and tools. The migration needs a global
  barrier: do not let any memcue open any copy of the base until steps 1 and 2 are done on every machine that shares it.
  1. On every machine, stop every client of the base (the Hermes gateway, cron jobs and running dreams, agent
     sessions) and pause its synchronization (a git cron job, Syncthing and the like).
  2. On every machine, determine the exact database file from the configuration the services use, and the target if
     it is a symlink. The defaults differ: the CLI opens `MEMCUE_FACTS`, else `$CLAUDE_CONFIG_DIR/memory/facts.db`
     (`~/.claude/memory/facts.db`), and the DSH plugin falls back to it after its `facts` setting; the Hermes plugin
     opens its `facts` setting, else `MEMCUE_FACTS`, else `$HERMES_HOME/memcue/facts.db`. Check that no process has
     the file open.
  3. Back it up with a trusted SQLite-aware tool, one that takes in committed WAL content and recovers an interrupted
     write left in a hot journal. Run `PRAGMA integrity_check` on the snapshot and read its schema with
     `PRAGMA user_version`. Expect 5 only from 0.20.0 back to 0.5.0; an older release may have an earlier schema (see
     [Compatibility](#compatibility)). Record the schema and the release that wrote the base: a rollback needs both.
  4. Store the backup apart from the synchronization: outside the git repository of the base and every synced
     directory.
  5. Only when every machine has passed steps 1 to 4, install the new binary and the plugins of the same release,
     then resume the synchronization and start the services again.

  Copying `facts.db` alone is not a backup: with a `-wal` or `-journal` file next to it, the copy may lack committed
  writes or hold half of one.
- **Rollback is the backup.** No in-place downgrade is provided or tested, and 0.20.0 or older cannot open a schema-6
  base. Every fact written or changed after the backup is absent once you roll back, and candidates are hidden again.
  The rollback needs the same global barrier: every client and the synchronization stopped on every machine before
  any copy is touched.
  1. On every machine, keep a separate backup of the current schema-6 base, made and verified as above but with
     schema 6.
  2. If you want the newer facts, export them with the 0.21 binary before the older one is installed:
     `MEMCUE_FACTS=<path> memcue facts`, or `memcue dump <path>`, where `<path>` is the exact file from step 2 above.
  3. Move the base and its `-journal`, `-wal` and `-shm` files aside; do not delete them. SQLite applies a sidecar
     left in place to the restored file.
  4. Install the release that wrote the backup, the one whose schema matches it, and restore that verified backup to
     the exact path the services open. Before restarting the services, check that path and that no memcue process
     runs.
  5. Only when every machine has restored the same backup, resume the synchronization and start the services again.

  Restoring a copied `facts.db`, deleting the sidecars or just restarting on the old binary is not a safe rollback.
- **The Hermes card keeps lines that repeat the notes** (#456). The provider no longer drops a card line that shares
  enough words with `MEMORY.md` or `USER.md`: it puts the card in as the binary renders it. No flag or schema change
  comes with this.
- **The Hermes note covers searching during work** (#521). The note and the `memcue_search` description let the model
  search, when it decides it may help, on starting with a specific host, service or repository and before it diagnoses
  an unexpected error, with a short query naming the system and the problem, checking the result against the current
  state. Text only: no search runs on its own, and the binary, DSH and Claude Code are unchanged. Whether the text
  changes what the model does has not been measured.
- **The Claude Code session-start note covers searching during work** (#522). A new line of the `session-start` note
  lets the model search, when it decides it may help, on starting with a specific host, service or repository and
  before it diagnoses an unexpected error or result, with a short query naming the system and the problem; a fact
  found this way is a lead to check against the current state. The note grows by about 350 characters and still fits
  both the one-block text and part 1 of the split start. Text only: no search runs on its own, and `session-card`,
  Hermes and DSH are unchanged. Whether the text changes what the model does has not been measured.

### 0.20.0: add writes procedure and event facts (#505)

- `memcue add -kind procedure` stores steps the user asked to remember; `search -kind procedure` finds them. Until now
  such a fact had to go in as `environment`.
- `memcue add -kind event` stores what happened that the next days need. It expires after `MEMCUE_EVENT_TTL_DAYS`
  (default 7) unless `-expires` gives a date; before, `add` rejected the kind and the expiry of #287 never applied.
- `repo` and `other` are still refused by `add`. Neither new kind reaches the session card.
- The session-start note, the skill and the `kind` description of `memcue_add` (Hermes, DSH) say when to use them.
- The schema is unchanged.

### 0.19.0: the agent writes its memory itself; extraction and vet are removed (#494)

- **Breaking: nothing is extracted from sessions.** The agent stores a rule or a correction of the user, a decision
  the code does not show, a pitfall or an external pointer with `memcue add` (Claude Code) or `memcue_add` (Hermes,
  DSH) when it learns it; the session-start note, the skill and the tool descriptions say so. The Claude Code plugin
  has no PreCompact or SessionEnd hook any more, the Hermes provider and the DSH plugin send no messages to the binary.
- **Breaking: `memcue vet` and `memcue extract` are removed**, and `MEMCUE_EXTRACT`, `MEMCUE_VET`, `MEMCUE_VET_POLICY`
  and `MEMCUE_VET_TTL_DAYS` are no longer read. `hook-extract`, `extract-session` and `extract-messages` stay as
  commands that read their input and exit 0, so an older plugin logs no failure. Candidates the extraction left wait
  for `memcue approve` or `memcue retire`; a provisional fact is found until its expiry. #514 later removed the
  approval: those candidates are live unless retired or expired.
- **Turn the host's built-in memory off yourself**: `"autoMemoryEnabled": false` in Claude Code's settings, and
  `memory.memory_enabled: false` and `memory.user_profile_enabled: false` in Hermes's config. memcue does not change
  them; it says so while they are on, and a Hermes note file the memory no longer injects stops filtering the card
  (#502). #456 later removed that filter: the Hermes provider no longer drops a card line that shares enough words
  with `MEMORY.md` or `USER.md`, and puts the card in as the binary renders it.
- `memcue_add` of Hermes and DSH takes a `source`: a rule or decision is the user's by default, so it reaches the
  session card, which the `agent` source these tools always wrote never did.
- `add` refuses installation state written with `-source tool` as well as `-source agent` (#475).
- **Hermes:** the dream starts from a line of the card in the system prompt (`session-card -dream-tools`), which the
  model answers with `memcue_dream_prepare` and `memcue_dream_apply`; the provider no longer runs `dream -run`. The
  `extract_*`, `shelf` and old `sessions/`, `pending/` state go; the latter are removed at start.
- **DSH:** the `extract`, `minNewChars`, `stateDir` and `maxToolResultChars` settings go; a profile that sets them
  still loads.
- The schema is unchanged.

### 0.18.1: the Claude Code session start carries every stated rule again (#495)

- In 0.18.0 the budget of #487 also cut the stated rules and decisions at the Claude Code session start, against #480:
  on the author's base the card fell from 53 lines over two parts to 22. Now `session-start -part N` keeps the stated
  facts outside the 3,500-character budget and bounds them only by the three parts; the other facts share the budget.
- `session-card` (Hermes, DSH) and `session-start` without `-part` keep the budget of #487 over the stated facts.
- No schema or flag changed.

### 0.18.0: a card with a ceiling, in three parts, and a report of the notes already in the base (#475, #476, #477, #480, #481, #487, #488, #489)

- **`memcue notes`** reports which entries of the built-in memory note files are already facts, with their ids, and
  which are not (#489). It reads the notes, calls no model and writes nothing; an entry marked `ok` can leave the
  file, a `new` one moves with `memcue add` first.
- **The session card's budget bounds the stated rules too** (#487). `MEMCUE_CARD_BUDGET` sets it, 3500 characters by
  default. Before, a fact the user stated was placed outside that room, and the card could grow without a ceiling;
  now the oldest stated fact goes first, and a card the budget cuts ends with a line that counts what it left out.
  `card.log` records `budget`, `used` and `omitted`.
- **The Claude Code plugin reads the session-start text in three parts**, `session-start -part 1` to `-part 3`, one
  `SessionStart` hook each, at most 9,500 characters a part (#480). Claude Code keeps a hook's text of 10,000
  characters or more out of the context and shows a 2 KB preview, so in the busiest repositories the card never
  reached the model. `session-start` without `-part` prints the whole text, as before.
- The Hermes provider option `session_card_chars` passes that budget through as `MEMCUE_CARD_BUDGET`, and its memory
  mirror labels a note with its kind and scope instead of `global` with no kind (#488).
- `memcue_search` refuses a limit of 0 instead of serving the default (#477); a flag after the query text steers the
  search (#481).
- `memcue vet` retires the state moments of live facts only (#475).
- Hermes cron runs feed the shelf through `extract-messages -shelf-only` (#476): what a research job read stays on
  the shelf, and nothing else from the run reaches the base.

### 0.17.0: every fact in a place, `-json` carries the hints, vet clears what code can tell is gone (#442, #453, #454, #456, #457, #458, #459, #467)

- **Breaking: `search -json` prints one object**, `{"facts": [...], "hints": [...]}`, instead of a bare array (#453).
  `hints` are the lines the plain answer prints after the facts: the shelf hint, the CJK hint, the `-in all` line of
  an empty answer in a repository. The Hermes and DSH plugins of this release read both shapes and hand the hints to
  the model; any other consumer of `-json` has to read `facts`.
- **A fact written without a scope gets one** (#442): the project its origin names, or `global` when the origin names
  none. An empty scope was in no place a search asks for, so only `-in all` found such a fact. `memcue vet` gives the
  same scope to the facts stored without one before.
- **vet retires by rule what code can tell is gone** (#457): a live fact past its expiry, and installation state of an
  older memcue (a fact of no kind or `environment` that says which version is installed or where a binary lies, every
  version it names older than the running release). A fact that names an old version as a change of behaviour stays.
- **Mirrors of the memory notes queued before 0.16.0 are decided** (#458): the review after an extraction takes them
  along, a mirror of a rule the base already holds word for word is dropped, an approved one becomes `global`. The
  Hermes plugin names the notes directory for the extraction (`MEMCUE_MEMORY_DIR`), so the mirror rules run there.
- **The extraction reads the memory notes** (#456) and leaves out what they already say, so a new fact does not
  restate a note in other words; the facts stored before stay until the dream merges them.
- `-in repo:KEY` takes a key the base knows only as an origin tier, such as `_global` (#459); `search -n` below 1 is an
  error instead of an empty answer that said the base held nothing (#454); a search no longer reads the stored
  repository keys unless `-in` names one, 12.7 → 9.9 ms end to end on a live base (#467).

### 0.16.0: a shelf for what a session read outside, a card that says the scope once, a mirror searchable at the write (#433, #437, #440)

- **Facts read from outside sources are kept on a shelf.** A fact whose evidence is quoted from the result of a tool
  that reads the world outside — a name starting with `web_`, `browser`, `fetch`, `http`, `curl`, `arxiv` or `pdf` —
  is stored under the scope `shelf:<slug>` and read only by a search that names it: `-in shelf` for every shelf,
  `-in shelf:research` for one. Every other place leaves the shelves out, `-in all` included, because an `all` that
  still read the shelf would hide nothing. A search that finds nothing while its words fit a shelved fact says where
  to look (`1 fact on the shelf matches: -in shelf:research`), and `search -json` now carries each fact's `scope`. The
  decision is provenance, not a model: `extract-messages -shelf research` shelves a fact only when its quote is in an
  outside tool's result and in no other tool's result; a fact in the user's words is never shelved; without the flag
  (the Claude Code path) nothing changes. The Hermes plugin passes `-shelf research` by default (`shelf` in its
  config, empty turns it off) and `memcue_add` takes `shelf`. By hand, `add -shelf research` is `-scope
  shelf:research`.
- **The session card says the scope and the dates once per group**, not on every line: a card of twelve lines gave 324
  of its 2 338 characters to the same suffix. The facts are unchanged; only the heading carries what the lines shared.
- **A memory note is searchable when it is written.** Every write of the built-in memory tool was mirrored as a
  candidate, and a candidate has no index row: between the write and the next vet run the user's latest memory was
  invisible, and an older approved fact answered in its place. A mirror is now approved at the write, and a stale one
  is retired by rule, without a model call.

### 0.15.0: search listens to a message, the card stops repeating the memory, and installation state stops being a fact (#417, #418, #421, #423, #424, #425, #426)

- A thin answer says what it left out. When one fact is all the score floor allows, the Hermes `memcue_search` adds a
  hint naming the lever (`min_score 0`) and how many facts hold the same words below the floor; the tool description
  now says that fewer facts than `limit` means the floor or the scope, not that the words are wrong. The hint costs one
  more search on those answers only (p50 8.9 ms, ~150 characters). Measured on 9 thin queries with one model: 7 of 9
  reached an answer inside three turns against 3 of 9 without it, and tokens per answered question fell 44 %
  ([thin-answer-hint-2026-10](docs/research/thin-answer-hint-2026-10.md)). The DSH tool is unchanged.
- **A message is searched as a message** (#417). A query of more than eight words is a user's message, not a search: its
  floor is 0.45 of the best fact instead of 0.6, and a fact has to hold two of the message's terms, because one shared
  word with a message of dozens usually means something else. On the same twenty user prompts the answer keeps 17
  useful facts instead of 11 and loses 2 instead of 8; the eight agent queries return exactly what they did before.
  The unrelated facts of a message rise, 23 to 27 of the delivered set — the BM25 tail of a message is flat, and the
  write-up says why a floor cannot separate it
  ([message-floor-417-2026-10](docs/research/message-floor-417-2026-10.md)).
- **`-in repo:KEY` accepts the end of the stored key, and the default of a repository takes the host and the global
  facts** (#418). `-in repo:memcue` finds `repo:github.com/…/memcue`; a key that matches nothing fails with the stored
  keys named instead of answering an empty result. Inside a repository the default is now that repository, this host
  and the global facts: on the measured base the project alone was 1 of 391 facts while the host scope held 255, and a
  question about the host went answered by the project or not at all
  ([scope-418-2026-10](docs/research/scope-418-2026-10.md)).
- **The thin-answer hint counts the tail** (#423): its wide call asks for `min(4 × limit, 20)` facts, so the number it
  prints is the size of the tail and no longer `limit − served` — 19 more on the query that used to say 4
  ([thin-hint-tail-423-2026-10](docs/research/thin-hint-tail-423-2026-10.md)).
- **The session card leaves out what the memory block already says** (#424): a mirror of a built-in memory note, a
  line whose opening repeats one already on the card, and a line the memory notes state are dropped; `card.log` gains
  `excluded`. On the live card 16 lines and 3 181 characters became 11 and 1 995, at every session start and after
  every compaction ([card-memory-copies-424-2026-10](docs/research/card-memory-copies-424-2026-10.md)). #456 later
  removed the third rule, the plugin's filter against the built-in memory notes; the binary's filters are unchanged.
- **Five facts stay the search default** (#425): the higher limit adds the tail — 40 facts on twenty prompts, one of
  them useful — for 62 % more characters, so the schema says when to raise it instead
  ([limit-default-425-2026-10](docs/research/limit-default-425-2026-10.md)).
- **Installation state is not a fact** (#426): the extraction prompt and the review prompt no longer invite the version
  of something installed, the path of a binary or a wrapper, a config default or the size of a base, and `vet` drops
  such a candidate before the duplicate check. 13 of 386 approved facts of the live base match the rule and are
  retired in a separate pass ([installation-state-426-2026-10](docs/research/installation-state-426-2026-10.md)).
- No schema change: 5, as in 0.14.0. The scope default and the message floor are search-side; the plugins pass the
  same flags as before.

### 0.14.0: a dream over the whole base, and search hands over only what fits and where the agent asks (#405, #407, #409, #414)

- `memcue dream -run -all` answers packet after packet until the base has no work left, so the translations of
  0.12.0 need not wait for three days of hourly dreams. It uses the endpoint when `MEMCUE_API_URL` is set and
  `claude -p` on `MEMCUE_MODEL` (default Opus) otherwise. It stops when the work is done, after three passes that
  change nothing, or after 200 passes, and `dream.log` records each pass with its cost (#405).
- `memcue search -n` is now a maximum, five by default. A fact whose BM25 score is under 0.6 of the best fact's is
  left out; `-min-score` sets another share, `-min-score 0` turns the floor off. On 35 queries that agents wrote,
  the floor removed a third of the unrelated facts, kept a useful fact for every query that had one and cut the
  characters per search by 17% ([search-floor-2026-10](docs/research/search-floor-2026-10.md), #407). A search that
  finds nothing prints "No stored facts match." The session note and the search tools of the Hermes and DSH plugins
  say so.
- The agent steers search (#409): `-in` where to look (`project`, `all`, `global`, `host`, or a scope such as
  `repo:KEY`), `-kind`, `-since`, `-expired` and `-all-words`; the Hermes and DSH search tools take the same as `in`,
  `kind`, `since`, `expired`, `words` and `min_score`. **In a repository search now looks only at that repository's
  facts by default**, and a text answer that finds nothing there says that `-in all` searches every repository.
  Outside a repository, and in the Hermes tool, the default stays the whole base. Expired facts leave the default
  result. See [Projects](#projects) for what the narrow default costs on measured queries.
- A query in Chinese, Japanese or Korean finds facts only through the Latin names in it: the facts are in Russian and
  English. The session note and the search tools now say to write the query in one of them, and `search` adds that
  hint to the answer of a CJK query, found or not (`hint` in the Hermes and DSH tools). A Chinese question translated
  first finds its fact 54–60 times in 60, against 0–50 as it is ([chinese-2026-10](docs/research/chinese-2026-10.md),
  #410, #414).
- No schema change. The plugins pass the new parameters only to a 0.14.0 binary.

### 0.13.0: Hermes runs the dream itself (#401)

- `memcue dream -run` runs a whole dream through the extraction's endpoint (`MEMCUE_API_URL`). It checks what the
  Claude Code session start checks, `MEMCUE_DREAM_EVERY` and fresh or untranslated facts, and makes no model call when
  no dream is due. The answer goes through the same checks as `-apply`, and `dream.log` records the tokens. Without
  `MEMCUE_API_URL` it refuses: on Claude Code the dream stays with the session's model.
- The Hermes plugin starts `dream -run` after each extraction, so a Hermes base gets its dream, and the translations of
  0.12.0, without a cron job. `dream: false` in `memory.memcue` turns it off.
- The endpoint model reasons over the packet. Without reasoning, deepseek-flash retired a fact it should have kept,
  and on a large packet its answer was cut at the token limit; with reasoning a dream takes 1.5–2.5 minutes and
  22,000–29,000 output tokens in the background ([dream-run-2026-10](docs/research/dream-run-2026-10.md)).
- No schema change. Upgrade the binary and the plugin together: the plugin starts `-run` only with a 0.13.0 binary.

### 0.12.0: a question in either language, and a base that brings itself up to date (#390, #391, #395, #396)

- **Translation.** Every fact keeps its whole text in the other language, Russian or English, as one search query
  that starts with `= ` (#395). Before, an English question found a Russian fact 22 times out of 30, and every other
  combination 29–30. With the translation it is 30 out of 30, 23 of them at rank one instead of 12, measured on a
  copy of a live base of 2,855 facts ([language-2026-10](docs/research/language-2026-10.md)). The session
  extraction writes the translation with each new fact, for about 4% more per session. Facts stored before get theirs
  from the regular dream, 40 a dream (#396). A base of 2,855 facts therefore takes about 72 dreams, three days at
  the hourly default. Search output and the card do not change, and the base grows by one version per fact.
- **Opening the base.** Each index row is stamped with the version of the number terms it was built with (#390).
  An open restems only older rows, including rows written by an older memcue on another machine of the same base.
  The index check trusts its last count when nothing changed (#396). On the copy above, search took 57 ms on 0.8.0,
  90 ms on 0.11.1 and 10 ms here, and the translation does not change that. The first open restamps every row once,
  in about 0.6 s.
- **Numbers after `:` and `/`.** An image tag (`image:v1.106.1`) and a versioned path (`gradle/9.8.1/`) give a
  term (#391). On the copy above, 15 of 15 version-only prompts now find their fact, against 13 before.
- **The card names what this machine still owes.** That is a missing merge driver (`memcue install`) or old
  repository names in the facts (`memcue migrate -repos`). The line reaches Hermes and DSH too (#396).
- No schema change and no new flag. `reindex` is a diagnostic again, and nothing has to be run by hand after the
  upgrade.

### 0.11.1: the index repair asks a cheaper question

- 0.11.0 asked every live fact carrying a digit whether the lexer would give it a number term by stemming it, on
  every open of the base: search went from 17.0 ms to 39.7 ms end to end. `text.HasNumberTerm` answers the same
  question with one regexp match and the latest version per id is a grouped join, which brings it to 23.5 ms on the
  same prompts. The repair itself is unchanged — a base with 60 numbered rows and 2 without is repaired on the first
  search and answers both version-only queries.
- No schema change, no new flag: only the cost of the check.

### 0.11.0: a partly numbered index is repaired, and `reindex` (#383, #384)

- The index is rebuilt on the first open when a live fact whose text carries a version, date, port or address has no
  number term in its row. 0.10.0 only noticed an index with *no* term at all, so a base indexed by an earlier release
  kept word-only rows for the facts it already had: on a live base two facts were unreachable by the version alone
  and reachable as `topic + version`. The check stems only the facts whose text carries a number, so opening a base
  still costs what it cost.
- `memcue reindex [-dry-run] [-json]` reports live facts, index rows and rows carrying a number term, then rebuilds.
  `-dry-run` reads through a read-only open, so it does not repair what it measures.
- An address after an `@` (`dig @1.1.1.1`) is one exact term, as versions and dates already were; it never matches a
  neighbour address.
- `memcue search` hands over five facts by default, as both plugins do, instead of ten (#384). `-n N` still overrides.
- No schema change: bases stay at schema 5, and the repair writes only `fact_index`/`fact_fts`.

### 0.10.0: versions are terms by default, and the dream sweeps old facts (#376, #377)

- `MEMCUE_NUMBER_TERMS` is on unless it is `0` (`false`, `off`, `no`): versions, dates, ports and addresses are one
  exact term, so a prompt naming a version finds the fact naming it. A base indexed word-only by the previous
  release is rebuilt once on the first search, with no `reindex`. On a copy of a live base the share of top-5 facts
  carrying the queried version went from 1 of 30 to 10 of 30; 10 of 10 ordinary questions returned the same facts
  ([docs/research/number-terms-2026-10.md](docs/research/number-terms-2026-10.md)).
- A dream also sweeps up to 50 facts it has already seen, after a cursor in `dream.merge`, and adds at most 20
  groups of its own inside the packet bound: a duplicate pair whose copies both predate the last dream is reviewed
  at last. `dream.log` lists every group by id under `grouped`, with its fresh facts and its outcome, so the log
  says whether two facts were grouped in a run
  ([docs/research/dream-merge-2026-10.md](docs/research/dream-merge-2026-10.md)).
- Grouping passes a candidate whose own terms the text nearly all holds, below the search's coverage gate; search
  itself is unchanged.

### 0.9.0: five facts per search by default (#372)

- `memcue_search` hands over five facts unless the call asks for another number. On a live base ten facts doubled
  the characters of a search and reached no prompt five did not, with the useful share falling from 22% to 12%.
- The number is the provider option `search_facts` (default 5, 1–50); an installation that wants ten sets it back in
  its own config, no release needed.

### 0.8.0: the agent searches itself (#361)

- No facts come with a prompt. The Claude Code plugin has no `UserPromptSubmit` hook; the Hermes and DSH plugins
  deliver the session card only. The session-start note, the Hermes system prompt and the DSH system prompt tell the
  agent to search memcue before it answers from earlier sessions.
- `memcue hook`, `memcue recall` (with `-explain` and `-fact`) and `memcue eval` are removed, with `recall.log` and
  the per-session `.recalled` files. An older plugin that still calls `hook` gets "unknown command": Claude Code
  ignores it, Hermes and DSH log one warning per session. Install the plugins and the binary of the same release.
- `MEMCUE_RECALL_LOG`, `MEMCUE_MIN_COVERAGE`, `MEMCUE_MIN_TERMS`, `MEMCUE_MIN_MATCHED`, `MEMCUE_MAX_FACTS` and
  `MEMCUE_FOREIGN_COVERAGE` are no longer read; drop them from wrappers. The Hermes options `min_coverage`,
  `max_facts` and `suppress_mirrored` are ignored.
- `MEMCUE_SEARCH_LOG=1` logs every search to `search.log`, the record to judge the agent's searches by.
- Every rule and decision you stated is on the card, past its budget (#363). Hermes carries the card in its system
  prompt instead of the first turn.
- A Hermes session without a working directory (a one-shot or cron run) takes the directory Hermes falls back to,
  so a run inside a repository names that repository (#354).
- The base is untouched.

### 0.7.0: recall without a judge, no review page

- Recall uses the words alone unless `MEMCUE_RERANK=judge` asks for Jev (#331): a configured Jev endpoint no longer
  turns the judge on. A setup that relied on that needs `MEMCUE_RERANK=judge`.
- `memcue review`, the local review page, is removed (#325); `memcue candidates`, `approve`, `retire` and `vet` stay.
  #494 later removed `vet`, and #514 `candidates` and `approve`.
- `MEMCUE_RERANK=local` is gone with the tree reader (#324) and now keeps the words.
- Run the project-key migration below once on every machine that writes to the base.

### Hermes sessions (#347)

The Hermes plugin used to pass the constant `hermes` as the working directory, so its facts had the origin
`hermes <date>`, and on the author's base an empty one from 2026-10-23. Now a session in a git repository names its facts by
that repository, and every other session leaves the origin empty. Facts already stored keep their origin; nothing
needs to be moved.

### Project keys (#343)

Facts written before #343 carry the directory's name (`<name> <date>`, `repo:<name>`), and a session no longer
matches them. Move them once on every machine that writes to the base:

```sh
memcue migrate -repos -root ~/src           # report only: what maps, what does not
memcue migrate -repos -apply -root ~/src    # back the base up, then write the changes
```

The map comes from this machine's clones: the working directories of the Claude Code transcripts and every repository
under each `-root`. `repo:<owner>/<name>` scopes map by their suffix. A name with no clone here, or with two clones
that now have different keys, stays as it is and is listed. So is every key that facts hold and no clone here has: a
repository of another machine, or one whose remote changed, with the clones' keys it may have become. A changed key
moves only by an explicit pair: `memcue migrate -repos -apply OLD=NEW`. A session in a repository whose old name
facts still carry gets that pair on its card (#396). `-apply` first copies the base to
`backup/facts.db.pre-repos-<time>` in the state directory, and writes each change as a new version that keeps the
fact's date, so a base merged from another machine sees it.

### Schema 5

The fact base schema is 5: it adds the search queries of a fact, which the index holds next to its words. memcue
0.4.0 refuses a version-5 base with `update memcue`, because its merge, its writes and its index rebuild would drop the
queries: upgrade every machine that shares the base, the Hermes container included, before the first memcue 0.5.0
opens it. Opening a version-4 base adds the column and rebuilds the index once; `memcue dump` (the git
textconv) still reads older revisions of the file, with no queries.

Two things a base upgraded from 0.4 still needs. The facts stored before 0.5 have no queries; each dream now gives them
queries, a packet at a time, and `memcue dream -prepare -all` starts on the whole base. And facts that describe memcue 0.4
itself (its variables, verbs and versions) stay recallable and win lexically, because they are written in memcue's own
words: no dream or vet compares a fact with the release it is about. Find them and decide each one:

```sh
memcue facts | grep -E 'MEMCUE_EMBED|MEMCUE_(VET|RERANK|TRIAGE)_(URL|MODEL|KEY)|EMBED_MODE|memcue (embed|label|audit)|0\.4\.0'
```

`memcue retire ID` a fact that describes the removed state as current, `memcue correct ID TEXT` one that is partly
true, and keep one that records what was measured or decided then. Since 0.5.3 `vet` finds most of these itself (see
[Candidates](#candidates)); facts about the host's own config files are still found only this way.

Schema 4 was the schema of 0.3.0 and 0.4.0; 0.2.1 and older refuse a newer base with `update memcue`: upgrade
every machine that shares the base before the first new memcue writes to it. If you ran `memcue install` before
0.4.0, run it once more after the upgrade: the merge driver is now a script in `.git` that survives a binary upgrade,
while an older install pinned the absolute path of the binary it ran from. A mixed-version sync is not safe: an
older binary refuses the version-4 base instead of reading it, and 0.2.1 does not know `fact_vectors`. Opening a version-3 base creates
`fact_vectors` and rebuilds the derived index once; version 4 adds the optional vectors on top of the version-3
stems, which keep the word of a versioned name such as `PostgreSQL-16`.

## Use

Nothing to do in a session: the agent searches the base when it needs to and stores what it learns. A fact written
with `memcue add` or stored by `memcue import` is indexed at once; nothing waits for approval (see
[Candidates](#candidates)). A search returns it when it fits: by default a search in a repository reads that
repository's, this host's and the global facts, and one outside a repository reads every fact; a shelf is read only
by a search that names it; an expired fact only with `-expired`; and at most `-n` facts (5 by default) above the
`-min-score` floor come back. The session card carries a fact only if it is active and unexpired, a global `rule`, or
a `rule` or `decision` scoped to the session's repository, and its source is not `memory`; it fits the card budget
(`MEMCUE_CARD_BUDGET`). `memcue correct` writes a new version and `memcue retire` takes a fact out. By hand (`add` without `-origin` names the project of the working directory and
today; in a temporary directory the origin stays empty, because a scratch directory says nothing about where the fact
came from). See [Projects](#projects) for how a project is named. A fact of `kind: event` gets an expiry of `MEMCUE_EVENT_TTL_DAYS` days (7 by default) unless `-expires`
says otherwise: what happened in one run is not the knowledge a later session needs. `add` without `-source` stores
the fact as the agent's (`agent`, #574), so a fact in the user's own words takes `-source user`, the only source that
reaches the session card, and what a command showed takes `-source tool`:

```bash
memcue search -json postgres port    # the facts that fit, best first, with their IDs (-json: {"facts", "hints"}; at most -n, default 5)
memcue search -min-score 0 postgres  # every fact up to -n, the floor off
memcue search -in all postgres       # every repository; the default in a repository is its own, this host's and the global facts
memcue search -in shelf:research rate limit  # facts read from pages and documents, which no other place returns
memcue search -in all,shelf:research rate limit  # places are a union: every fact off the shelves and that shelf
memcue search -kind rule,decision -since 2026-10-01 release  # only rules and decisions learnt since that day
memcue search -all-words -expired postgres port  # every word required; facts past their expiry included
memcue add -source user -keywords "demo, postgres" -origin "demo 2026-10-19" "The demo database is PostgreSQL 18 on port 55432."
memcue add -source user -expires 2026-10-21 "Check how the nightly dream ran on 21 October."
memcue correct -expires 2026-10-28 ID "…"   # -no-expires clears it
memcue correct ID "The demo database is PostgreSQL 18 on port 55433."
memcue correct ID -- "…"             # a lone -- after the ID is a separator, as in add; -- -- keeps a leading "--"
memcue retire ID                     # no longer recalled; history keeps it
memcue reopen ID                     # undo a retire: the fact is searchable again
memcue facts                         # every active fact, with expires= when it has one
memcue notes                         # which entries of the built-in memory note files are already facts
memcue reindex -dry-run              # index rows against the live facts, and how many lack their number term
memcue reindex                       # rebuild the index from the live facts (-json for one JSON object)
memcue dream -since 2026-10-19 -list # groups of similar facts a dream would review
memcue dream -prepare                # the work of a dream, for the session's model (-all: the whole base)
memcue dream -apply answer.json      # check and carry out the model's answer to it
```

`memcue add` and `memcue correct` refuse text that looks like a token or a password; `-force` stores it anyway.
`migrate` and `import` skip such facts and report them without echoing the value, because the base may be synced
through git.

A fact is found by a question in either language. The dream stores, next to the search queries, the whole fact in
the other language, Russian or English, as one query that starts with `= `: search reads it, while the card and the
search output show the fact as it was written (#392, #395); it gives a translation to 40 facts a dream (#396). Until
then the keywords the agent writes in both languages are what a question in the other language finds.

Every host also tells its model to store a failure it found by running something (#566): when a command fails and the
agent fixes or works around it, the cause and the fix go in as a pitfall if a fresh checkout would hit it again, even
when the cause is in the repository's own files. "What the repository shows" means its documents, not such a failure.
On five synthetic troubleshooting scenarios ([pitfall-capture-566](docs/research/pitfall-capture-566-2026-10.md)),
Claude Code (Opus 5.5) with this line stored the expected pitfall in 12 of 15 runs (3 without it), every failure it
hit included; Hermes on `deepseek-flash` stored it in 6 of 15 (3). No run stored noise, and the answers did not get
worse. DSH carries the Hermes wording and was not measured.

The Claude Code note names the source of what the agent stores, and `add` without `-source` stores `agent` (#574).
Before, 5 of the 12 pitfalls Claude Code stored on the same scenarios got `source: user`, because the call left the
flag out; with the change all 12 named `-source tool`, and capture and answers stayed the same
([source-default-574](docs/research/source-default-574-2026-10.md)). Hermes and DSH name the source on every call.

Nothing judges a fact on its way in: the agent decided to write it. `add` refuses only installation state the agent
saw in a tool's output or concluded (`-source tool` or `agent`): a version, the path of a binary, a config default,
the size of a base, a restart time or PID, a line count, an mtime. A command reads each of these, and the stored copy
is stale after the next release or restart (#475); a fact in the user's words is stored as said. A fact for a shelf
(`-shelf` or `-scope shelf:<slug>`) skips the check: it says what an outside source states about another system, not
the state of the user's machines (#565). The refusal names the ways out (#577): what another system does, a version
range or a default of it included, goes on the research shelf with its source; a version, path or time that is only
incidental is dropped from the text and the rest stored. Hermes and DSH hand the model the same message, which fits the
500 characters DSH keeps of an error.

What goes stale later is handled in two places. A fact a newer one replaced meets it in the dream, which groups
similar facts and lets the session's model retire or merge the old one ([Dream](#dream)); a fact found wrong in use is
corrected or retired in the session. A fact the world made wrong without anyone saying so is not caught (#494).
`memcue vet`, its rules, its review model and its check of suspect facts are removed with the extraction they served;
the `vet.log`, `vet.checked`, `extract.log` and `extract.err` of an older release can be deleted.

The SessionStart card is a view of the active facts, not a copy: a rule or decision leaves it when corrected,
retired or expired. `card.log` records the ids it carried, so a rule shows as used even though no search found it.

Claude Code keeps a hook's `additionalContext` of 10,000 characters or more out of the context: it saves the text to
a file and shows the model a 2 KB preview. The limit holds per hook, so the plugin declares three `SessionStart`
hooks, `session-start -part 1` to `-part 3`, and the binary lays the note, the hints and the card out over them, at most
9,500 characters each (#480). The note and the hints open part 1. A part ends only between lines, and a part that
starts inside a group repeats its heading. A part with nothing to carry prints nothing. Only past all three parts,
about 28,000 characters, does the card leave lines out. The last part then says how many and how to search them,
and `card.log` counts them as `left`. `session-start` without `-part` prints the whole text in one block, as before.

Every rule and decision the user stated (`source` `user` or `user_confirmed`) is on the card, each cut to 400
characters (#363): since the agent searches itself, the card is the only memory it gets unasked. Where it holds them
depends on the host. At the Claude Code session start they sit outside the budget, bounded only by the three parts
(#480, #495). On `session-card`, which Hermes and DSH put in the system prompt of every turn, they share the budget
(`MEMCUE_CARD_BUDGET`, 3,500 by default): the oldest go first, and a cut card ends with a line that counts the rest
(#487). On the author's
archived base that is 9 global rules, about 1,300 characters. The other facts share a budget of about 3,500 characters,
a dozen facts, and a labelled base has more rules than that. They are chosen in this order:

1. Newest first. The date alone says little on a migrated base, where most rules share the day of the migration,
   which is why the stated facts come before the budget.
2. The rules and decisions of the current repository may take half of the budget before anything else is placed.
3. The rest of the budget is filled in the order of step 1, global rules and the repository's facts together, so a
   repository whose facts come first may take more than half.

Measured on the author's base (65 global rules after the labelling of #219, 129 facts for the largest repository): the
global card is unchanged; a repository with 49 facts, none of them stated, gets 6 lines instead of 4; a repository
with 11 stated decisions keeps its 10 lines. Ordering by recall hits was rejected: the rules recall already served
were the ones the card is not needed for.

An expiry is the last day a fact is worth recalling: after it the fact stays in the base and in `facts`, and leaves
the card. `memcue search` still finds it, and `search -json` shows its `expires`. The date is the local calendar day of the
machine that judges it. A correction keeps the expiry unless it names a new one.

### Dream

A session writes a fact without reading every neighbour, so two sessions can store the same fact, or a fact and the
one that replaced it. A dream tidies the facts the last dream did not see. memcue starts no model for it: the model of a session does it.

1. `memcue dream -prepare` groups each fresh fact with the stored facts close to it in words and prints one packet:
   the instructions, the fresh facts to split, the facts without search queries to expand, the facts without a
   translation to translate, and the groups to review.
   It keeps the packet in the state directory.
2. The model answers in one JSON object, with the packet's id.
3. `memcue dream -apply FILE` checks the answer against the packet and carries out what passes. It prints and logs
   to `dream.log` what it changed and what it refused, with the reason.

Who runs it:

- **The session start, in the background** (#569). A dream is due once `MEMCUE_DREAM_EVERY` (default `1h`) has
  passed since the last one and some fact is fresh or lacks a translation. The first check on a machine only marks
  the start, so the facts it already had are not swept by surprise; `memcue dream -prepare -all` reviews them. When a
  dream is due, `session-start` (Claude Code) and `session-card` (Hermes, DSH) start `memcue dream -run -background`
  in its own session and return at once: one pass, on the endpoint when `MEMCUE_API_URL` is set and on `claude -p`
  otherwise. The start is stamped in `dream.started`, so a dream that fails is tried again an interval later, not at
  every session start; what the dream prints goes to `dream.out`, its record to `dream.log`. A held `dream.lock`
  makes the background run leave quietly, so sessions started together run one dream.
- **The session's model.** Without an endpoint and without `claude` on `PATH`, or with
  `MEMCUE_DREAM_BACKGROUND=0`, the session start says in one line that a dream is due instead. On Claude Code the
  agent then runs the `memcue-dream` skill when the user's task allows; it never interrupts a turn. On Hermes the card
  in the system prompt says so (`session-card -dream-tools`), and the model runs `memcue_dream_prepare` and
  `memcue_dream_apply` (#494). Hermes builds the system prompt once and again after a compression or a reset, so the
  line is as fresh as the last rebuild ([plugins/hermes/README.md](plugins/hermes/README.md#dream)).
- **The whole base.** `memcue dream -run -all` answers packet after packet until the base has no work left: no
  interval, and the merge sweep, which never runs out of groups, does not keep it going. It uses the endpoint when
  `MEMCUE_API_URL` is set and `claude -p` on `MEMCUE_MODEL` (default Opus, no tools, no hooks) otherwise. It stops on
  the first failed pass, after three passes that change nothing, or after 200 passes; a second run continues. With an
  endpoint set, `env -u MEMCUE_API_URL -u MEMCUE_MODEL memcue dream -run -all` runs it on Opus (#405). Without
  `-all`, `-run` checks the interval and the facts the session start checks and makes no call when nothing is due
  (#401); started by hand without `MEMCUE_API_URL` it refuses, so a long run never starts on `claude -p` by
  surprise. `-background` is the run a session start starts: one pass, on `claude -p` without an endpoint.
- **Calls at once.** A pass of `dream -run` takes the work of up to `-j` packets (default 4) and sends it as calls at
  once: the fresh facts with the groups, the facts to expand and the facts to translate each go in their own calls of
  one packet's bounds. Their answers are applied as one, and one failed call fails the pass. Most of a call is the
  model writing its answer, so a pass of a base far behind does the work of four packets in about a third of the time
  of one; a pass with little work makes one or two calls. `dream.log` records the calls, the time of the packet, the
  calls and the apply, and the tokens of each pass (#471,
  [docs/research/dream-time-2026-10.md](docs/research/dream-time-2026-10.md)).

A packet holds up to 40 fresh facts, 30 groups, 50 facts to expand and 40 to translate. The fresh facts it leaves out are not counted
as seen, so the next packet takes them, and the session start still says facts wait.

What the answer may do:

- **Split** a compound fact: one part of it can go stale while another stays true. The fact is retired and its parts
  take its place, approved, with its origin, scope, kind, source and expiry (not its keywords, which were chosen for
  the whole text).
- **Expand** a fact without **search queries**: 6–10 short phrases a user would name the task with, the general name
  of what the fact is about, and how the problem or question sounds. The index searches them next to the fact's words
  and keywords. `add` and `correct` ask for them in one call when `MEMCUE_API_URL` is set, so the dream fills the
  facts written without an endpoint, every fact the agent writes on such a machine among them, those of `import`, and
  the parts of a split. An older fact without queries is never fresh again, so each packet also offers the newest of
  them: a base upgraded from 0.4 fills over a few dreams. On the gold set of 180 prompts the queries added
  7–9 prompts that got a useful fact ([docs/research/one-model-2026-10.md](docs/research/one-model-2026-10.md)).
- **Translate** a fact that has no translation among its queries: the whole text in the other language, Russian or
  English, kept as the query that starts with `= ` (#395). The dream gives it to every fact without one, the newest
  first, so a base is translated over a few dozen dreams without a command of its own (#396). A fact another part of the packet may
  change — fresh, without queries, or in a group — waits for a later packet, and a fact changed since the packet is
  refused, since its translation would describe the old text.
- **Retire** a fact of a group as a duplicate or as contradicted, naming the fact that covers it (`by`) and its kind,
  `duplicate` or `outdated`. Of two duplicates the one with the smaller id stays, whatever the model prefers, so
  machines dreaming over one shared base keep the same copy and never retire both. Every group keeps at least one
  fact; a retired fact stays in the history.
- **Correct** a fact of a group with text built only from the facts of the group. A correction drops the queries of
  the old text. With `queries` in the action the new ones are written at once; without them the next packet asks for
  them.

A duplicate learnt in another project than the fact that stays makes that fact global (#346): a fact learnt twice,
independently, in two projects holds in both, so it should not count as one project's fact in recall. memcue decides
this from the two facts' scopes and origins, not the model. `-apply` prints `global <id>` and lists the fact under
`promoted` in `dream.log`. In the author's dream log, 15 of the 21 duplicates retired before this change crossed
projects.

The checks:

- An answer to another packet is refused whole: a second `-prepare` replaced the packet it was for.
- An answer about a fact the packet did not give for that part is refused, as is a second answer about one fact or a
  split into fewer than two parts.
- A group with a fact changed since the packet (a new version, or retired) is refused whole, since its actions were
  decided on the old text; the other groups apply. Splits apply first, so a group formed around a compound fact is
  refused as changed, and the next dream groups the parts with their neighbours. Queries apply after the splits; the
  version they write does not count as a change of the groups.

A dream keeps the facts and versions it saw in `dream.seen`, next to `dream.at`. A fact pulled from another machine
after the last dream is new to the next one even when it was written earlier, and so is a correction made since.
Without `dream.seen` (a machine that last dreamt with an older memcue), the next dream goes by when facts were written
and writes the file.

A pair of facts that both predate the last dream would never meet again, so each dream also sweeps up to 50 facts it
has seen before, grouping them with their neighbours the way it groups fresh facts (#377). The sweep adds at most 20
groups of its own and only as many as the packet has room for after the fresh groups. It goes through the base in
id order, from the fact after the one kept in `dream.merge`, and starts over after the newest. A fact is grouped
with a neighbour when it holds enough of the neighbour's terms, or when the neighbour's terms are nearly all in it:
a short note and a longer restatement of it meet whichever of the two is fresh. `dream.log` lists every group by id
under `grouped`, with its fresh facts and what came of it (`acted`, `refused`, `kept` or `deferred`), so the log
says whether two facts were grouped in a run. Measured on a copy of the live base: a pair 165 facts apart in id
order met in one group of 50 swept facts, the younger copy was retired in favour of the smaller id, and the run
record names the group ([docs/research/dream-merge-2026-10.md](docs/research/dream-merge-2026-10.md)).

The Jev triage that sorted out the groups needing no action before the model saw them is removed with the rest of the
judge (#357); its measurements are in [docs/research/dream-triage-2026-10.md](docs/research/dream-triage-2026-10.md).

## Configuration

memcue calls one model, the main chat model, for `migrate`, for the search queries `add` asks for and for
`dream -run`; search calls none, and a dream is otherwise done by the model of a session ([Dream](#dream)). The judge and
embeddings roles of [docs/models.md](docs/models.md) are removed (#250, #357). The table is the contract this release
uses.

| Variable | Meaning | Default |
|---|---|---|
| `MEMCUE_FACTS` | the fact base; a leading `~/` or `$HOME/` is expanded, and the DSH and Hermes adapters require the result to be absolute | `$CLAUDE_CONFIG_DIR/memory/facts` when that directory exists (a text history, see [Moving to the text history](#moving-to-the-text-history)), else `$CLAUDE_CONFIG_DIR/memory/facts.db`; `~/.claude` without `CLAUDE_CONFIG_DIR` |
| `MEMCUE_STATE_DIR` | state directory for logs, locks and the dream schedule; a non-default base gets a per-base subdirectory under `$XDG_STATE_HOME/memcue` | unset |
| `MEMCUE_MODEL` | model of `migrate`, the queries of `add` and `dream -run` | `claude-opus-5-5` |
| `MEMCUE_API_URL` | OpenAI-compatible endpoint for the main model instead of `claude -p`, e.g. `https://api.deepseek.com`; set `MEMCUE_MODEL` with it | unset |
| `MEMCUE_API_KEY` | key for that endpoint | unset |
| `MEMCUE_AUTO_INSTALL` | `0` stops the plugins from downloading the binary of their release (see [Binary from the plugin](#binary-from-the-plugin)) | on |
| `MEMCUE_HOST` | name of this machine for fact scope and the search tie-break; set it where the host name is unstable, such as a container | `os.Hostname()` |
| `MEMCUE_SEARCH_LOG` | `1` logs every search and the facts it found; the log keeps the query | off |
| `MEMCUE_NUMBER_TERMS` | versions, dates, ports and addresses are indexed as one exact term (`1.27` becomes `nm1x27`, which never matches `1.20`), so a query that names a version finds the fact naming it: 0 of 11 version-shaped prompts before, 10 of 11 after, no change on 26 ordinary questions ([docs/research/number-terms-2026-10.md](docs/research/number-terms-2026-10.md)); `0` keeps the old word-only index, and a base indexed word-only is rebuilt on the first open without it | on |
| `MEMCUE_EVENT_TTL_DAYS` | days a fact of `kind: event` stays recalled; `0` keeps event facts without an expiry | `7` |
| `MEMCUE_CARD_BUDGET` | most characters of the session card, the facts the user stated included; when they do not fit, the oldest user-stated fact goes first, and a last line counts what is left out (`memcue search -in global -kind rule` finds it). The Hermes provider sets it from `session_card_chars` | `3500` |
| `MEMCUE_DREAM_EVERY` | how long after a dream the next one is due (a Go duration, at least `1m`) | `1h` |
| `MEMCUE_DREAM_BACKGROUND` | `0` keeps the session start from running a due dream in the background; it then says the dream is due instead | on |
| `MEMCUE_MEMORY_DIR` | directory of the built-in memory notes `memcue notes` reads | `$CLAUDE_CONFIG_DIR/memory` |
| `XDG_STATE_HOME` | where `memcue/search.log`, `memcue/card.log` and `memcue/dream.log` are kept | `~/.local/state` |

Search is the words alone: FTS5 over the query's terms, in BM25 order, with no threshold, because the agent reads
what it asked for. The gates that kept noise out of the facts injected with a prompt went with that injection (#361);
the BM25 order they used beat a coverage sort on 409 blind-labelled prompts (#338,
[docs/research/search-chain-2026-10.md](docs/research/search-chain-2026-10.md)). The readers that once ordered the
candidates are removed: the vectors (#250), the tree reader (#324), the Jev judge and the listwise main-model reader
(#357). None of them served more useful facts than the words for its cost
([docs/research/one-model-2026-10.md](docs/research/one-model-2026-10.md),
[docs/research/local-readers-2026-10.md](docs/research/local-readers-2026-10.md),
[docs/research/listwise-2026-10.md](docs/research/listwise-2026-10.md)). `MEMCUE_RERANK` and `MEMCUE_JUDGE_*` are
no longer read. Opening a base deletes the vectors an older memcue stored and compacts the file once (#260); the empty
`fact_vectors` table stays, so the schema and older binaries are unaffected.

With `MEMCUE_SEARCH_LOG=1`, `search.log` has one JSON line per search: the time, the session (when the host passes
`-session`), the project, the host, the first 500 characters of the query and the ids of the facts it returned,
searches that found nothing included. It stays on your machine and is what you judge the agent's searches by; it is off
by default because it keeps the text of the queries.

`cost_usd` in `dream.log` holds only what a caller reports in dollars: `claude -p`. A call to `MEMCUE_API_URL` reports tokens, not a price, so its `cost_usd` is 0 and its spend is
`tokens` (`input`, of which `cached` came from the prompt cache, and `output`) times the provider's price; memcue
keeps no price table, since prices change and differ by account (#258).

The prompts of `migrate` and the dream (`internal/extract/*.md`) are written in Russian; a fact keeps the language of
its note.

## Cost

Writing a fact costs no model call: the agent writes it as part of its work, and `add` asks for search queries in one
short call only when `MEMCUE_API_URL` is set. Before #494 an extraction of every session cost $0.18–1.33 per session
on Opus, about $5 per million characters of transcript, and a review followed it; both are gone.

A dream the session's model runs costs no call of its own: it is part of the session. A background dream (#569) is
one `dream -run` pass at most once per `MEMCUE_DREAM_EVERY`, and only while some fact is fresh or owed a translation;
a pass takes the work of up to four packets (`-j 4`). On Opus through `claude -p`, a background pass over a copy of
the author's Mac base with 141 fresh facts made six calls and cost $2.58; the passes of a whole-base run cost $0.22–1.97
([dream-run-2026-10](docs/research/dream-run-2026-10.md)), and a pass with little fresh work less. One packet is about 20,000–36,000
characters on the author's two bases, instructions included; a dream with no fresh fact and nothing to expand prints
one line. While facts are owed a translation, its 40 facts add about 10,000 characters (64,583 against 54,000 on a
copy of the author's base with 2,831 fresh facts waiting).

## Results

Measured while building memcue; details and method in [docs/evaluation.md](docs/evaluation.md).

- **The agent searching itself, end to end (#361):** on 40 real prompts, each run twice and graded blind, the agent
  that searched memcue itself scored 1.08 of 2, against 0.89 with facts injected with the prompt and 0.76 with no
  memory; the edge over injection is +0.19 per prompt, 95% interval [+0.03, +0.37]. Only Opus in Claude Code was
  measured ([docs/research/pull-2026-10.md](docs/research/pull-2026-10.md)).
- **Recall on 100 unseen real prompts (the injection before #361):** 33 % of injected facts useful and 38 % noise, against 4 % useful and 87 %
  noise for the holographic memory plugin's prefetch on a comparable set. Useful facts reached 12 of the 41 prompts
  that needed memory: lexical search misses prompts that share no rare words with the fact.
- **End to end, 30 questions with answers in memory:** 30 of 30 correct with memcue against 27 of 30 without, one
  turn fewer per answer; one run per question, so a direction rather than a significant result.
- **Session extraction on Opus (removed in #494):** no wrong facts in 102, 88 % of the durable facts of ten sessions
  kept.
- **Migration of 312 notes:** 2335 facts for $40; 2.5 % of facts carried the model's own conclusions after the
  prompt forbade them.

## Candidates

There are none any more (#514). The model decides what is worth storing: a fact written with `memcue add`,
`memcue_add` or a Hermes memory mirror, and every fact `memcue import` stores, is indexed at once and found by a search
it fits (see [Use](#use)). `memcue retire ID` takes a fact out, and `memcue correct ID TEXT` writes a new version. A
retired fact cannot be corrected: retirement is final, so a changed fact is added as a new fact, and `memcue reopen
ID` is the way back.

A base written by memcue 0.20.0 or older may hold candidates, facts that waited for `memcue approve`, and provisional
facts the removed `memcue vet` left with an expiry. From schema 6 an active one is live as it is, its rows unchanged;
a retired one stays retired, and an expired one is handled as any expired fact (see [Upgrade](#upgrade)).
The local review page (`memcue review`) went in #325, `vet` in #494, `candidates` and `approve` in #514.

## Harness support

The same base and binary serve all three hosts; the adapters differ at the edges:

| Capability | Claude Code | DeepSeek Harness | Hermes Agent |
|---|---|---|---|
| Search by the agent | `memcue search` (Bash) | `memcue_search` tool | `memcue_search` tool |
| Session card | `SessionStart` | `session-card` on first step and after compaction | `session-card` in the system prompt, rebuilt after compression |
| Writing facts | the agent, `memcue add` through the skill | the agent, `memcue_add` tool | the agent, `memcue_add` tool |
| `add` / `correct` / `retire` | skill and CLI | four tools | four tools |
| Expiry | CLI and skill | `add` / `correct` | `add` / `correct` |
| Dream | background on the session start; else the `memcue-dream` skill on its hint | background on `session-card` with `apiUrl` | background on `session-card` with `api_url`; else dream tools on the system-prompt hint |
| Mirror of built-in memory | not applicable | no built-in memory files | `MEMORY.md` / `USER.md` mirrored as live facts (#440) |

State and logs for the default base live in `$XDG_STATE_HOME/memcue`; any other base gets a per-base subdirectory,
`$XDG_STATE_HOME/memcue/<hash>`, so two bases on one machine do not share a dream schedule. `MEMCUE_STATE_DIR`
overrides the whole path. Hermes keeps its mirror file under `$HERMES_HOME/memcue`; the binary's logs, locks and dream
schedule always follow the base. The fact base is `$CLAUDE_CONFIG_DIR/memory/facts.db`
(or `~/.claude/memory/facts.db`) by default, `$HERMES_HOME/memcue/facts.db` for Hermes, and memcue's default for DSH
unless `facts` is configured; each becomes a `facts` directory after `memcue migrate -text`, and the cache of a text
history is `facts.db` in the state directory. A shared base is supported; point `MEMCUE_FACTS` or the plugin's `facts` option at the
same file.

## DeepSeek Harness

memcue also works as a native DSH plugin: the session card, a search tool the model is told to use, and the fact
tools it writes its memory with. See [plugins/deepseek-harness](plugins/deepseek-harness/README.md).

## Hermes Agent

memcue also works as a memory provider for Hermes Agent: the session card, a search tool the model is told to use,
the built-in memory tool mirrored into the base, the tools the model writes and fixes facts with, and the dream tools. See
[plugins/hermes](plugins/hermes/README.md).

## License

MIT
