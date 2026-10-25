# memcue

Long-term memory for Claude Code. memcue keeps a base of short, self-contained facts in one SQLite file and works
through hooks:

- **At session start, and again after a compaction or `/clear`** it tells the agent to search memcue before it
  answers a question that may depend on earlier sessions, and sends a card: global rules and the rules and decisions
  of the current repository, the ones you stated first, within about 1.2 thousand tokens. The card is how a rule the
  agent would not think to search for still reaches it.
- **When the agent needs something from earlier sessions** it runs `memcue search` itself, with its own words:
  synonyms, the system's name, both languages. Nothing is injected with a prompt (#361): the agent's own queries
  answered more questions right than facts matched to the prompt's words (see [Results](#results)).
- **When a session is compacted or ends** it sends the new part of the transcript to Claude, which writes down what
  will still matter later: decisions, how systems are set up, confirmed causes, what did not work. Facts are only
  added; nothing is compared or overwritten.
- **When you ask** ("remember…", "that is outdated") the agent adds, corrects or retires a fact through the bundled
  skill.

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
- Claude Code with the `claude` command on `PATH`: extraction runs `claude -p`, unless an OpenAI-compatible
  endpoint is configured instead (see [Configuration](#configuration)).
- Go 1.27 or later, to build from source.

## Compatibility

This release is `0.9.0`; the fact-base schema is 5 (see [Upgrade](#upgrade)). The three plugin manifests carry the same
version, and a release bumps all of them together.

| memcue | schema | Claude Code plugin | DSH plugin | Hermes plugin |
|---|---|---|---|---|
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
`memcue version -json` at startup and turn the session card and extraction off with one warning on a binary older
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
   the agent its memory is memcue.

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

   It registers the driver in the repository's git config and adds `memory/facts.db merge=memcue diff=memcue` to
   `.gitattributes`. The driver is a small script in `.git` that finds the binary through `MEMCUE_BIN`, then `PATH`,
   then the path it had when `memcue install` ran, so an upgrade that moves the binary does not break the next merge.
   Without a repository memcue works the same; the base is just not synced. A merge carries no vectors.

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
- **No working directory** is no project: a host passes an empty `cwd` for such a session (`-cwd ""` to `add` and
  `extract-messages`). The Hermes plugin does so for a session outside git (#347); a session Hermes gives no `cwd`
  takes the directory Hermes falls back to (#354).
- **The Claude config directory** is the global tier; **a temporary directory** names nothing.

The extraction model chooses only whether a fact holds in the repository (`repo`), everywhere or on a host; memcue
writes the key. `memcue add -scope repo` does the same for the working directory.

The project only orders `memcue search`: of facts with equal scores, one learnt in the session's project comes before
a global one and the rest. `search -cwd DIR` names the session's directory; the default is the working directory.

## Upgrade

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
moves only by an explicit pair: `memcue migrate -repos -apply OLD=NEW`. `-apply` first copies the base to
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
[Review and vet](#review-and-vet)); facts about the host's own config files are still found only this way.

Schema 4 was the schema of 0.3.0 and 0.4.0; 0.2.1 and older refuse a newer base with `update memcue`: upgrade
every machine that shares the base before the first new memcue writes to it. If you ran `memcue install` before
0.4.0, run it once more after the upgrade: the merge driver is now a script in `.git` that survives a binary upgrade,
while an older install pinned the absolute path of the binary it ran from. A mixed-version sync is not safe: an
older binary refuses the version-4 base instead of reading it, and 0.2.1 does not know `fact_vectors`. Opening a version-3 base creates
`fact_vectors` and rebuilds the derived index once; version 4 adds the optional vectors on top of the version-3
stems, which keep the word of a versioned name such as `PostgreSQL-16`.

## Use

Nothing to do in a session: the agent searches the base when it needs to, and facts are extracted at the end.
Extracted facts are candidates: search finds them only after someone approves them — `memcue vet` by a rule or a model, or by hand (`memcue candidates`,
`memcue approve ID`; `memcue retire ID` rejects one). A fact you add yourself with `memcue add` is active at once. An explicit `correct` of
an approved fact also writes a new active version: it is a deliberate correction, not a new candidate. Only new
extracted and mirrored facts wait for review. By hand (`add` without `-origin` names the project of the working directory and
today; in a temporary directory the origin stays empty, because a scratch directory says nothing about where the fact
came from). See [Projects](#projects) for how a project is named. A fact of `kind: event` gets an expiry of `MEMCUE_EVENT_TTL_DAYS` days (7 by default) unless `-expires`
says otherwise: what happened in one run is not the knowledge a later session needs:

```bash
memcue search -json postgres port    # find facts and their IDs, best first (a JSON array with -json; -n N, default 10)
memcue add -keywords "demo, postgres" -origin "demo 2026-10-19" "The demo database is PostgreSQL 18 on port 55432."
memcue add -expires 2026-10-21 "Check how the nightly dream ran on 21 October."
memcue correct -expires 2026-10-28 ID "…"   # -no-expires clears it
memcue correct ID "The demo database is PostgreSQL 18 on port 55433."
memcue retire ID                     # no longer recalled; history keeps it
memcue reopen ID                     # undo a retire: back as it was before, approved or waiting for review
memcue candidates                    # extracted facts waiting for approval, newest first (-json for a JSON array)
memcue vet -dry-run                  # what the rules and the judge would decide about the queue
memcue vet                           # decide it: approve what belongs in the base, retire what does not
memcue vet -no-model                 # only the deterministic rules; undecided candidates stay for review
memcue approve ID [ID ...]           # take candidates into recall
memcue facts                         # every active fact, with expires= when it has one
memcue dream -since 2026-10-19 -list # groups of similar facts a dream would review
memcue dream -prepare                # the work of a dream, for the session's model (-all: the whole base)
memcue dream -apply answer.json      # check and carry out the model's answer to it
```

`memcue add` and `memcue correct` refuse text that looks like a token or a password; `-force` stores it anyway.
`migrate` and `import` skip such facts and report them without echoing the value, because the base may be synced
through git.

`memcue vet` is the automatic half of the same review, so the queue does not have to be cleared by hand. Three rules
decide first, without a model: a mirror of the built-in memory stays only while the notes still hold its text
(`-memory DIR` or `MEMCUE_MEMORY_DIR` names them; without that directory the rule is skipped rather than retiring every
mirror), an origin whose tier is exactly `test` (as in `test 2026-10-19`; a repository such as `test-utils` is not a
test run) is retired as a test trace, and a candidate whose words are all in an
approved fact is retired as covered. The rest goes to the `keep / drop / unsure / split` judge, twelve candidates a
call, each with the approved facts nearest to it, so a duplicate or a contradiction of an approved fact is visible in the
answer. `split` is for a candidate that still holds several claims, one of which can go stale while another stays
true: the candidate is retired, each part is stored as a candidate with its origin, scope, kind and source (not its
keywords, which were chosen for the whole text), and the same run reviews the parts. A part is not split again in that
run, and a split into fewer than two parts leaves the candidate in the queue. `vet.log` records the parts and their ids.
Everything else the judge cannot settle stays **provisional** by default when the model gave an explicit `unsure`
answer: that answer becomes a provisional fact for the TTL, and `unsure` is still an entry in the report rather than
an approval. A failed or empty answer does not: `vet` never turns a call that did not decide into a provisional fact,
so the candidate waits for the next run. A provisional fact is found by search like an approved
one, marked in the log, and expires after `MEMCUE_VET_TTL_DAYS` without a person. Set
`MEMCUE_VET_POLICY=quarantine` or run `memcue vet -quarantine` to keep the old review queue, and `-no-model` keeps
every candidate the rules cannot decide in that queue as well. Every verdict goes to `vet.log` in the state
directory with the rule or model and its reason; the page, the `approve` command and the page's undo (`store.Reopen`)
stay the manual way, unchanged.

The review runs on the main model: `MEMCUE_API_URL` when it is set, otherwise `claude -p`, as the extraction does. The
facts a session just stored are reviewed in the extraction process, after storing them
(`MEMCUE_VET=0` turns that off; without an endpoint and without `claude` on `PATH` nothing is reviewed); that process is
already detached from the session, so a turn never waits for the verdict. A run that finds another one busy is skipped
rather than queued, and its failures go to `vet.log`, not to the extraction.

Duplicates are the review's job too: it sees each candidate's nearest approved facts and drops a candidate that
repeats one. A separate Jev pass over those neighbours ran before the review until #266; on the 41-candidate sample of
#240, labelled by `claude -p`, the review alone retired 10 of the 11 drops and 1–2 of 29 facts worth keeping, and with
the Jev pass 8–10 and 1–4, for about 420k more input tokens
([docs/research/vet-neighbours-2026-10.md](docs/research/vet-neighbours-2026-10.md)).

The SessionStart card is a view of the active facts, not a copy: a rule or decision leaves it when corrected,
retired or expired. `card.log` records the ids it carried, so a rule shows as used even though no search found it.

Every rule and decision the user stated (`source` `user` or `user_confirmed`) is on the card, each cut to 400
characters (#363): since the agent searches itself, the card is the only memory it gets unasked. On the author's
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

Extraction never sees the base, so two sessions can store the same fact, or a fact and the one that disproved it. A
dream tidies the facts the last dream did not see. memcue starts no model for it: the model of a session does it.

1. `memcue dream -prepare` groups each fresh fact with the stored facts close to it in words and prints one packet:
   the instructions, the fresh facts to split, the facts without search queries to expand, and the groups to review.
   It keeps the packet in the state directory.
2. The model answers in one JSON object, with the packet's id.
3. `memcue dream -apply FILE` checks the answer against the packet and carries out what passes. It prints and logs
   to `dream.log` what it changed and what it refused, with the reason.

Who runs it:

- **Claude Code.** When a dream is due, the session start says so in one line, and the agent runs the `memcue-dream`
  skill when the user's task allows. It never interrupts a turn. A dream is due once `MEMCUE_DREAM_EVERY` (default
  `1h`) has passed since the last one and some fact is fresh. The first check on a machine only marks the start, so
  the facts it already had are not swept by surprise; `memcue dream -prepare -all` reviews them.
- **Hermes.** A cron job of Hermes calls the provider's dream tools on a schedule
  ([plugins/hermes/README.md](plugins/hermes/README.md#dream)).

A packet holds up to 40 fresh facts, 30 groups and 50 facts to expand. The fresh facts it leaves out are not counted
as seen, so the next packet takes them, and the session start still says facts wait.

What the answer may do:

- **Split** a compound fact: one part of it can go stale while another stays true. The fact is retired and its parts
  take its place, approved, with its origin, scope, kind, source and expiry (not its keywords, which were chosen for
  the whole text). `vet` splits candidates the same way, so this mostly matters for facts stored before it.
- **Expand** a fact without **search queries**: 6–10 short phrases a user would name the task with, the general name
  of what the fact is about, and how the problem or question sounds. The index searches them next to the fact's words
  and keywords. Extraction writes them with every fact it stores, and `add` and `correct` ask for them in one call when
  `MEMCUE_API_URL` is set. So the dream fills the facts written without an endpoint, those of `import`, and the parts
  of a split. Facts stored before extraction wrote queries are never fresh again, so each packet also offers the
  newest of them: a base upgraded from 0.4 fills over a few dreams. On the gold set of 180 prompts the queries added
  7–9 prompts that got a useful fact ([docs/research/one-model-2026-10.md](docs/research/one-model-2026-10.md)).
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

memcue calls one model, the main chat model, for extraction and review; search calls none, and the dream is done by the
model of a session ([Dream](#dream)). The judge and
embeddings roles of [docs/models.md](docs/models.md) are removed (#250, #357). The table is the contract this release
uses.

| Variable | Meaning | Default |
|---|---|---|
| `MEMCUE_FACTS` | the fact base; a leading `~/` or `$HOME/` is expanded, and the DSH and Hermes adapters require the result to be absolute | `$CLAUDE_CONFIG_DIR/memory/facts.db`, else `~/.claude/memory/facts.db` |
| `MEMCUE_STATE_DIR` | state directory for logs, locks, the dream schedule and session offsets; a non-default base gets a per-base subdirectory under `$XDG_STATE_HOME/memcue` | unset |
| `MEMCUE_MODEL` | model of the extraction and review | `claude-opus-5-5` |
| `MEMCUE_API_URL` | OpenAI-compatible endpoint for the main model instead of `claude -p`, e.g. `https://api.deepseek.com`; set `MEMCUE_MODEL` with it | unset |
| `MEMCUE_API_KEY` | key for that endpoint | unset |
| `MEMCUE_AUTO_INSTALL` | `0` stops the plugins from downloading the binary of their release (see [Binary from the plugin](#binary-from-the-plugin)) | on |
| `MEMCUE_EXTRACT` | `0` turns automatic extraction off in every host; it wins over the DSH `extract` setting and the Hermes `extract_url` | on |
| `MEMCUE_HOST` | name of this machine for fact scope and the search tie-break; set it where the host name is unstable, such as a container | `os.Hostname()` |
| `MEMCUE_SEARCH_LOG` | `1` logs every search and the facts it found; the log keeps the query | off |
| `MEMCUE_NUMBER_TERMS` | versions, dates, ports and addresses are indexed as one exact term (`1.27` becomes `nm1x27`, which never matches `1.20`), so a query that names a version finds the fact naming it: 0 of 11 version-shaped prompts before, 10 of 11 after, no change on 26 ordinary questions ([docs/research/number-terms-2026-10.md](docs/research/number-terms-2026-10.md)); `0` keeps the old word-only index, and a base indexed word-only is rebuilt on the first open without it | on |
| `MEMCUE_EVENT_TTL_DAYS` | days a fact of `kind: event` stays recalled; `0` keeps event facts without an expiry | `7` |
| `MEMCUE_DREAM_EVERY` | how long after a dream the session start says the next one is due (a Go duration, at least `1m`) | `1h` |
| `MEMCUE_VET` | `0` stops deciding the facts a session extracted; otherwise they are decided right after storing them, on `MEMCUE_API_URL` or `claude -p` | on |
| `MEMCUE_VET_POLICY` | `provisional` (default) turns `unsure` into provisional facts; `quarantine` keeps the human queue | `provisional` |
| `MEMCUE_VET_TTL_DAYS` | how long a provisional fact is recalled | `14` |
| `MEMCUE_MEMORY_DIR` | directory of the built-in memory notes the mirror rule checks | unset: the rule is skipped |
| `XDG_STATE_HOME` | where `memcue/search.log`, `memcue/extract.log`, `memcue/vet.log`, `memcue/card.log` and per-session read offsets are kept | `~/.local/state` |

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
by default because it keeps the text of the queries. `extract.log` has one JSON line per extraction run: session, part of the transcript
read, facts stored, and the cost Claude Code reports or the tokens the endpoint reports.

`cost_usd` in `extract.log`, `vet.log` and `dream.log` holds only what a caller reports in dollars: `claude -p`. A call to `MEMCUE_API_URL` reports tokens, not a price, so its `cost_usd` is 0 and its spend is
`tokens` (`input`, of which `cached` came from the prompt cache, and `output`) times the provider's price; memcue
keeps no price table, since prices change and differ by account (#258).

The extraction prompts (`internal/extract/*.md`) are written in Russian. Facts come out in the language of the
session or note either way.

## Cost

Extraction is the part that costs most. On ten recorded sessions it cost $0.18–1.33 per session on Opus,
about $5 per million characters of transcript; tool output is the bulk of it. Sonnet was half the price but kept
73 % of the facts worth keeping against 88 % for Opus, and nothing at all in two of the ten sessions. DeepSeek
(`deepseek-flash` through `MEMCUE_API_URL`) cost $0.17 for all ten and kept 74 % against 87 % for Opus. Set
`MEMCUE_EXTRACT=0` to keep search and turn extraction off in every host.

A review is the second model call, and a much smaller one: twelve candidates with a few neighbours each per call, and
no candidate at all on a session that taught nothing. `vet.log` records the tokens of every call, and the cost when
`claude -p` reports one; a rules-only run (`-no-model`) costs nothing. The review uses `MEMCUE_MODEL` and
`MEMCUE_API_URL`, or `claude -p` without it; there is no separate review model. On `api.deepseek.com` the review
turns reasoning off: it agreed with a `claude -p` reference as often and used a quarter of the output, while
extraction lost quality without it and keeps it
([docs/research/reasoning-off-2026-10.md](docs/research/reasoning-off-2026-10.md)).

A dream costs no call of its own: it is part of the session that runs it. One packet is about 20,000–36,000
characters on the author's two bases, instructions included; a dream with no fresh fact and nothing to expand prints
one line.

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
- **Session extraction on Opus:** no wrong facts in 102, 88 % of the durable facts of ten sessions kept.
- **Migration of 312 notes:** 2335 facts for $40; 2.5 % of facts carried the model's own conclusions after the
  prompt forbade them.

## Review and vet

Facts written by extraction, and facts mirrored from Hermes' built-in memory, are **candidates**: they are stored and
visible in `memcue candidates`, but recall ignores them until someone approves them. `memcue vet` applies the
deterministic rules and the review model to the queue, `memcue approve ID...` takes candidates into recall, and
`memcue retire ID` rejects one. A fact written by `memcue add` is active at
once, and an explicit `correct` of an approved fact writes a new active version. A retired fact cannot be corrected:
retirement is final, so a changed fact is added as a new fact, and `memcue reopen ID` is the way back from a
rejection. The local review page (`memcue review`) is removed (#325): its template alone cost 2.3 MB of the binary. The full rules are in [Use](#use).

`vet` also checks the approved facts that this binary finds suspect: a fact that names a `MEMCUE_*` variable or a
`` `memcue X` `` verb it does not have, a memcue version older than its own (a release build only, either right after
the word or a few words later behind "бинарь", "версия", "сборка", "релиз" or "тег"), or a reminder whose dates have all
passed. Code can find such a fact but cannot tell a stale one from a record of history, so the main model reads each
with what was found and retires those that describe a past state as current. The model is asked at temperature 0 and
every retirement needs a second, independent answer that agrees with the first: a wrong retirement cannot be undone
from the binary, and one bad sample must not take a true fact out of recall. A fact the two answers disagree about is
kept and logged as such. Every fact asked about is remembered with its version and the binary's version in `vet.checked`
in the state directory, so a kept fact is not asked about again until its text or the binary changes. `-no-model` only
lists the suspects; `-dry-run` asks and changes nothing.

## Harness support

The same base and binary serve all three hosts; the adapters differ at the edges:

| Capability | Claude Code | DeepSeek Harness | Hermes Agent |
|---|---|---|---|
| Search by the agent | `memcue search` (Bash) | `memcue_search` tool | `memcue_search` tool |
| Session card | `SessionStart` | `session-card` on first step and after compaction | `session-card` in the system prompt, rebuilt after compression |
| Session extraction | `PreCompact` / `SessionEnd` | `turn/end` / `session/disposed` | threshold, idle, pre-compress, session end |
| `add` / `correct` / `retire` | skill and CLI | four tools | four tools |
| Expiry | CLI and skill | `add` / `correct` | `add` / `correct` |
| Mirror of built-in memory | not applicable | no built-in memory files | `MEMORY.md` / `USER.md` mirrored as candidates |
| Candidate review and vet | binary | binary | binary |

State and logs for the default base live in `$XDG_STATE_HOME/memcue`; any other base gets a per-base subdirectory,
`$XDG_STATE_HOME/memcue/<hash>`, so two bases on one machine do not share a dream schedule or a vet lock. `MEMCUE_STATE_DIR` overrides the whole path. DSH keeps its extraction checkpoints in
`$XDG_STATE_HOME/memcue/dsh`, and Hermes keeps its mirror, seen and pending files under `$HERMES_HOME/memcue`; the
binary's logs, locks and dream schedule always follow the base. The fact base is `$CLAUDE_CONFIG_DIR/memory/facts.db`
(or `~/.claude/memory/facts.db`) by default, `$HERMES_HOME/memcue/facts.db` for Hermes, and memcue's default for DSH
unless `facts` is configured. A shared base is supported; point `MEMCUE_FACTS` or the plugin's `facts` option at the
same file.

## DeepSeek Harness

memcue also works as a native DSH plugin: the session card, a search tool the model is told to use, extraction at turn
end and on disposal, and three more fact tools. See [plugins/deepseek-harness](plugins/deepseek-harness/README.md).

## Hermes Agent

memcue also works as a memory provider for Hermes Agent: the session card, a search tool the model is told to use,
the built-in memory tool mirrored into the base, tools to fix facts, and optional extraction of sessions through an OpenAI-compatible endpoint. See
[plugins/hermes](plugins/hermes/README.md).

## License

MIT
