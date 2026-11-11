# Optional remote facts and embedded Tailscale

SQLite and text history plus SQLite remain the default. A memcue server is selected by a nonempty `MEMCUE_SERVER` (see [Server mode](#server-mode-a-memcue-server-on-the-tailnet)). PostgreSQL is selected only by a nonempty `MEMCUE_POSTGRES_DSN`. Whitespace is invalid. A remote error never opens, migrates, imports or writes the local fact base. Transport selection is independent: PostgreSQL works over ordinary networking with no embedded node.

These remote-backend commands are included in v0.32.0 and later and require matching binary and adapter versions. For a source checkout, build with `make build` and use `bin/memcue` (or explicitly install that build on your PATH). Publishing or installing a release does not perform live data migration or production enrollment.

## Server mode: a memcue server on the tailnet

`MEMCUE_SERVER` selects server mode (#714, contract in [docs/specs/sync-server.md](specs/sync-server.md)). One
machine runs `memcue server` and keeps the shared base in SQLite. Every other machine keeps a local SQLite replica of
it and works only on the replica. The CLI never uses the network in this mode: search, session start, add, correct,
retire and reopen answer from the replica, with or without a connection. A single background process per machine,
the tailnet helper, keeps the replica in step with the server.

### The server

`memcue server` runs in the foreground under systemd or Docker and logs to stderr. It serves the base of
`MEMCUE_FACTS`, a SQLite file, over plain HTTP on port 80 of its own tailnet node (the tailnet encrypts the traffic).
It opens no port of the host. Give it a tailnet identity of its own and a stable name:

```sh
export MEMCUE_FACTS=/srv/memcue/server.db
export MEMCUE_TAILSCALE_STATE_DIR=/srv/memcue/tailnet   # private, 0700, not shared with a helper
export MEMCUE_TAILSCALE_HOSTNAME=memcue-server
export MEMCUE_TAILSCALE_CONTROL_URL=https://headscale.example.org
export MEMCUE_TAILSCALE_AUTH_KEY_FILE=/srv/memcue/auth.key   # owner-only; only with -bootstrap
memcue server -bootstrap   # the first start logs in with the key; later starts need no flag
```

Without `-bootstrap` the server logs the login URL until someone approves the node. The server refuses to start
with `MEMCUE_SERVER` or `MEMCUE_POSTGRES_DSN` in its environment, and it refuses a base that already keeps a history
of versions: such a base is loaded with `migrate -server`.

Access is the tailnet ACL alone: allow the client machines to reach the server node on TCP port 80, and nothing
else. The server names each client in its log by its tailnet node name.

The server is the only machine that dreams. Every ten minutes it checks whether a dream is due (`MEMCUE_DREAM_EVERY`,
24h by default) and runs `memcue dream -run -background` on its base, with `claude -p` or `MEMCUE_API_URL` as on any
machine; `MEMCUE_DREAM_BACKGROUND=0` turns that off. A change of labels alone does not make a fact fresh for the
next dream.

### A client

```sh
export MEMCUE_SERVER=memcue-server             # the server's tailnet node name, short or full
export MEMCUE_TAILSCALE_CONTROL_URL=https://headscale.example.org
memcue connect up                               # once: log the helper in, approve the device
memcue search "$QUERY"
```

`MEMCUE_SERVER` needs no `MEMCUE_TAILSCALE_MODE`; with `MEMCUE_POSTGRES_DSN` set too, every command refuses. The
replica is `$XDG_STATE_HOME/memcue/server-<name>/replica.db`: one per server, shared by every agent of the machine
whatever `MEMCUE_FACTS` and `MEMCUE_STATE_DIR` they set. An agent's state directory keeps the agent's own files.

The helper long-polls the server: a change written on one machine reaches the replicas of the others in about a
second. A write made offline is found at once on its own replica and waits in the replica's outbox until the server
accepts it. Each field of a fact merges on its own, so concurrent edits of different fields both survive. When two
machines correct the text at once, the later text wins and the other one is kept as a new fact. A fact retired on one
machine and corrected on another stays retired with the correction, and `reopen` brings back the corrected text.

A session start or session card that finds no helper starts one in the background and answers without waiting. One
that finds a helper of an older release asks it to exit and starts its own; a helper of a newer release stays, so two
agents of different releases do not replace each other's helper (#718). A write (`add`, `correct`, `retire`,
`reopen`, `import`) also starts a helper that is not running. They add a line when the helper
waits for a login, when the replica has never synced, and when the server refused changes of the outbox (a clock far
ahead, for example). `memcue connect status` prints the helper's version, the last sync, the outbox length and the
refused changes.

### Commands in server mode

| Commands | Server mode |
| --- | --- |
| `add`, `correct`, `retire`, `reopen`, `facts`, `history`, `search`, `import`, `notes`, `reindex` | The replica. `history` shows the current version only: the server and the replicas keep one row per fact. |
| `session-start`, `session-card` | The replica, the helper's status and the hints above; no dream hint and no background dream. |
| `migrate -server [-apply]` | Loads the local base of `MEMCUE_FACTS` into the empty server; see below. |
| `dream`, `install`, `merge`, `dump`, `migrate` (other forms) | Refused: they work on a local base or on git. |
| `connect`, `version`, `sessions`, help | As without server mode. |

### Moving to a server

Server mode starts from one base. While the machines still sync through git, merge them into one base as usual
(for example, let loffe and Hermes push and pull into the Mac's base) and stop every writer. Then, on the machine that
holds the merged base:

```sh
export MEMCUE_FACTS=~/.local/state/memcue/memory/facts   # the merged local base (~/.claude/memory/facts before it moved)
export MEMCUE_SERVER=memcue-server
memcue connect up
memcue migrate -server          # report: facts, active and retired, and versions
memcue migrate -server -apply   # load them into the empty server
```

Each fact goes with the text and labels of its latest version and the creation time of its first; older versions
stay in the local base. A server that holds any fact refuses, and nothing is stored. After the load, set
`MEMCUE_SERVER` for every agent of every machine; writes made to a local base afterwards stay local.

In a container such as Hermes, the helper runs inside the container: give it `MEMCUE_SERVER`, a private
`MEMCUE_TAILSCALE_STATE_DIR` on a volume and `MEMCUE_TAILSCALE_CONTROL_URL`, run `memcue connect up` once in the
container, and let the session starts keep the helper running.

## Ordinary PostgreSQL

Keep the connection URL in an owner-only secret file, outside the fact base and Git. The URL must explicitly specify a single TCP host, port, user, database and `sslmode=verify-full` (plus the appropriate trusted root certificate). `sslmode=disable` is accepted only as an explicit operator decision; a private WireGuard path does not supply PostgreSQL server certificate validation or database authorization. Other TLS modes, Unix sockets, multiple hosts and service-file settings are rejected.

```sh
export MEMCUE_POSTGRES_DSN="$(cat "$HOME/.config/memcue/postgres.dsn")"
export MEMCUE_TAILSCALE_MODE=off
memcue search -json "$QUERY"
memcue history -json "$FACT_ID"
```

The DSN takes precedence over `MEMCUE_FACTS`. Empty/unset DSN retains the original offline local selection. All ambient `PG*` variables are rejected after explicit PostgreSQL selection. No `DATABASE_URL` activates this backend. pgx password-file discovery is disabled; password and certificate settings must come from the explicit URL. Connection errors do not print the URL, credentials or fact payloads.

PostgreSQL schema version 2 keeps one row per fact in `memcue.facts`: its current version as JSONB, keyed by `id`, with the stems of its content. PostgreSQL keeps no fact history: a correction, retirement or restore replaces the row, and `history` shows the current version only. `memcue.version` identifies the schema. The first connection to an empty schema initializes it transactionally; incompatible versions fail. It does not inspect a local base. A provisioning role needs schema creation rights only for the first empty database. Once both version/facts tables exist, an ordinary open checks the version with one statement, without DDL or writes. A runtime role needs schema USAGE, SELECT on memcue.version, and SELECT/INSERT/UPDATE on memcue.facts. Schema upgrades remain an operator action.

A base of schema 1 (memcue 0.32.0–0.33.x: every version, search on the client) is refused with `PostgreSQL base uses schema 1`; there is no in-place upgrade. Recreate it from the local base: drop the schema with the owner role (`DROP SCHEMA memcue CASCADE`), then run `memcue migrate -postgres -apply` (see [Moving a local base](#moving-a-local-base)) and grant the runtime role UPDATE on `memcue.facts`.

Search runs on the server and never reads the whole base (#700). The client stems a fact's content when it writes it, as the local index does; PostgreSQL derives the filter columns (status, kind, scope, origin, created, expiry) and a `tsvector` of the stems from the stored row, and a partial GIN index covers the active facts. A search sends its stems; one statement finds the candidates through the index, applies the scope, shelf, kind, date and expiry filters, and ranks them by BM25 with the parameters of SQLite FTS5 (k1 1.2, b 0.75, IDF over all active facts), returning the score and stems recall uses. Long Russian terms still match by prefix. Document frequencies, scope discovery, origin tiers and repository pitfalls are single statements too. Results agree with the local base for the same facts; the order can differ only where FTS5 breaks ties by its own row order (the server breaks them by id) or rounds a sum differently. `facts` and `Current` still read every active fact, because they list them.

ID lookups read one indexed row. Mutations read only their targeted IDs and write under the transaction lock: a first version inserts the row, a later one updates it where the stored version is the one before it. The limits remain 100,000 facts and 64 MiB of payload, checked in the same transaction; refused writes roll back.

The CLI creates one invocation context before supported prework. Explicit PostgreSQL commands share the existing four-second deadline across input/discovery, all store opens, retries, server queries, ranking work and cleanup, leaving headroom inside the shortest five-second Hermes recall timeout (DSH hooks use ten seconds; explicit tools fifteen). Local invocations retain their existing startup behavior and have signal cancellation without a new four-second limit. The cycle 3 section below describes cooperative CPU/input/output cancellation and its finite units; it does not assert instantaneous preemption of kernel calls or arbitrary external marshalers. Multiple PostgreSQL store opens in one command share the same invocation deadline and one connection: the CLI opens the base once per invocation, checks the schema on that connection and lends it to every later open, so a command pays for TCP, TLS and authentication once. Statements run in pgx's exec mode, one round trip each, without a separate prepare.

Library `OpenPostgres` is a reusable handle with a fresh ten-second budget per operation. `OpenPostgresContext(ctx, dsn)` explicitly binds schema startup and all calls to the caller-owned request context; close it at request end. `OpenPostgresRequest(dsn, budget)` owns a bounded (at most ten-second) request context and cancels it on Close. Do not reuse either request handle after its context expires. A handle keeps one connection for its operations, which run one at a time; each read sees the base as it is then, and there is no request snapshot. An operation whose budget runs out closes the connection, and the next one reconnects. Each connection owns every primary and pgx asynchronous cancellation stream it allocated: they are closed when the connection is dropped or the handle closes, without closing foreign helper streams. Rollback/Close share the remaining budget. Uncertain mutation outcomes remain explicit ambiguity.

Mutations acquire a database advisory transaction lock before reading, apply the normal version rules and write atomically. Rows are never deleted through this adapter. Expected-version operations reject stale versions. There is no automatic mutation retry. A lost commit reply returns an explicit ambiguous-outcome error: inspect the fact and its version before retrying, especially for add. This is not durable request-key idempotency. Direct external writes that bypass this protocol are unsupported.

## Explicit embedded login and lifecycle

The helper is a Memcue process owning one durable, non-ephemeral tsnet identity. It is not a host VPN daemon and requires no root, system `tailscaled`, service installation or host route changes. Subprocess tools connect to its private Unix socket; ordinary fact operations never launch a node themselves. `connect up` explicitly starts it in the background, so one terminal is enough. `connect serve` remains the foreground alternative for service managers.

Use a separate private directory per profile/control server and backend. The stored profile binds the control URL, stable hostname and backend host/port. Reusing it with different settings fails; credentials can rotate without changing the identity. The entire identity directory is private node material, not a memory backup to copy to another machine.

```sh
export MEMCUE_POSTGRES_DSN="$(cat "$HOME/.config/memcue/postgres.dsn")"
export MEMCUE_TAILSCALE_MODE=embedded
memcue connect up
# If a URL is displayed, open it and complete login/device approval.
memcue connect up
# Only "Tailnet ready" means authorization is complete.
memcue search -json "$QUERY"
```

The default private identity directory and hostname need no extra settings. For separate profiles or a custom control server, set the overrides **before the first `up`**, and keep them for subsequent calls:

```sh
export MEMCUE_TAILSCALE_STATE_DIR="$HOME/.config/memcue/tailnet-work"
export MEMCUE_TAILSCALE_HOSTNAME=memcue-work
export MEMCUE_TAILSCALE_CONTROL_URL=https://control.example.test
memcue connect up
```

`up` reuses a matching helper and does not reauthorize a ready node. Otherwise it starts the same executable as a detached `connect serve`, with private startup acknowledgement and no inherited terminal or consumer pipes. It prints a login URL only when necessary; pending authorization is not readiness. Startup has the configured connection deadline; failure before acknowledgement terminates only the newly launched process. If login/control access fails after startup, the error explicitly says the helper remains running: retry `up` or stop it with `off`. It never kills a pre-existing owner.

`up` does not persist your DSN or exports, modify a shell/profile, install a service, automatically restart a crashed process, or start on system boot. After a crash/reboot, run `up` again with the same settings. An approved durable identity normally needs no new login or bootstrap key. Tools/plugins must inherit the exports too; launching the helper cannot enable routing in another application's environment. Keep the executable installed at a stable path. For service supervision, use `memcue connect serve` in the foreground; explicit `memcue connect login` remains available separately. Both commands use the same settings as `up`.

`MEMCUE_TAILSCALE_MODE=embedded` still enables routing for future Memcue calls; `up` owns/authorizes the connection, not host routing. There is no `connect on` alias. `connect status` emits JSON with `mode`, `running`, `state` and, when available, `node_id`, without auth URLs or credentials. `running` describes the helper process; `state=Running` describes tailnet readiness, not database health. A missing helper with retained state can report `Unavailable`. Ordinary search/add/hook calls never print auth URLs or prompt; they return bounded login/helper errors.

Unattended enrollment uses an explicit bootstrap action and a permission-checked file:

```sh
export MEMCUE_TAILSCALE_AUTH_KEY_FILE="$HOME/.config/memcue/bootstrap.key"
# Provision this file privately (0400 or 0600), without putting the key in argv or Git.
memcue connect up -bootstrap
# Or: memcue connect serve -bootstrap, when foreground supervision is desired.
```

A key setting alone never selects PostgreSQL, creates a helper or enrolls a local SQLite invocation. `serve` without `-bootstrap` does not read the bootstrap file. After enrollment, restart with `memcue connect serve` without the file/key. The approved node identity persists. Key revocation/expiry and node authorization are different: rotating or deleting a bootstrap key does not revoke an already enrolled node. Remove/revoke the node in the control plane when authorization must end.

Stop embedded networking and use the ordinary network explicitly:

```sh
memcue connect off
export MEMCUE_TAILSCALE_MODE=off
```

`off` acknowledges `Stopping` independently of node status/health, closes idle and active helper connections before the node, and releases ownership while preserving private identity. Wait for the serve process to exit before restarting the owner. Changing the environment to `off` switches future clients to ordinary networking; it does not stop an already running helper. A host-installed Tailscale route can still be used in off mode. No logout/reset/delete command is implemented. Destructive reset and remote node revocation are separate, explicitly authorized operations; stopping a helper never performs them.

Configuration:

| Setting | Behavior |
| --- | --- |
| `MEMCUE_TAILSCALE_MODE` | `off` by default; `embedded` uses an existing helper only for explicit PostgreSQL calls. |
| `MEMCUE_TAILSCALE_STATE_DIR` | Absolute durable private directory; defaults to the user config directory's `memcue/tailnet`. No symlink root; owner-only directory, files and socket. |
| `MEMCUE_TAILSCALE_HOSTNAME` | Stable node hostname, default `memcue`. |
| `MEMCUE_TAILSCALE_CONTROL_URL` | Explicit custom HTTPS coordination server, otherwise the Tailscale default. |
| `MEMCUE_TAILSCALE_AUTH_KEY_FILE` | Optional private bootstrap file, read only with `serve -bootstrap`. |
| `MEMCUE_TAILSCALE_CONNECT_TIMEOUT` | Helper request/startup deadline, 1s–30s; default 5s. |
| `MEMCUE_TAILSCALE_ALLOW_LOOPBACK_CONTROL` | `1` permits HTTP only to a literal loopback control address for disposable fixtures. |

Inherited `TS_*` variables are rejected before node startup, except `TS_NO_LOGS_NO_SUPPORT=true`. This implementation enables tsnet's supported no-logs/no-support transport policy in the explicit helper process and suppresses local upstream loggers. The pinned release normally uploads logs; merely setting `Logf` would not disable that. This policy does not mean the coordination server or relay sees no connection metadata.

The Unix protocol is version 1. A nonblocking file lock owns the identity for the whole helper lifetime; a stale or missing socket is not an identity lock. Socket mode is 0600 in a directory of mode 0700, with ownership checks. Restart can replace a stale socket only while owning the lock. There is no automatic process supervisor. A helper crash leaves durable identity; restart `serve` with the same configuration. DB streams close before tsnet. Tunnel lifetime has a five-minute ceiling; ordinary operations have shorter PostgreSQL contexts.

## DNS, destinations and authorization

The pinned tsnet v1.104.5 does not expose the inspected upstream `LookupIP` API. Memcue therefore resolves only the configured backend peer's name/address from the authenticated netmap. It never calls the host resolver for that backend. Full MagicDNS names, unambiguous short peer names and peer IPv4/IPv6 literals are supported. Only known peer addresses in Tailscale's IPv4/IPv6 ranges and the configured TCP port can be dialed. Public, loopback, unknown peers and subnet-routed destinations are rejected. This is deliberately narrower than arbitrary DNS or subnet routing.

pgx gets both `LookupFunc` and `DialFunc`; the original DSN hostname and TLS ServerName are retained. No generic proxy, inbound tailnet listener, Funnel, exit node or global HTTP transport is enabled by the production helper. Restrict tailnet grants/ACLs to the application identity reaching only the database identity and database port. Device approval and tag ownership remain operator controlled. PostgreSQL roles, passwords and TLS remain separate authorization layers.

## Command capability matrix

| Commands | PostgreSQL selection |
| --- | --- |
| `add`, `correct`, `retire`, `reopen`, `facts`, `history`, `search` | Remote authoritative facts. `history -json` exposes the current version using exported Fact field names; PostgreSQL keeps no older ones. |
| `session-start`, `session-card` | Remote fact reads; local session state. Remote repository failure is unavailable, not an empty snapshot. Background dream maintenance is disabled. |
| `sessions` | Local session history, independent of the fact backend. |
| `dump FILE`, `merge BASE OURS THEIRS` | Explicit historical/local files; no implicit remote redirection. |
| `migrate -postgres [-apply]` | Copies the latest version of every local fact into an empty PostgreSQL base; see [Moving a local base](#moving-a-local-base). |
| `migrate` (other forms), `import`, `install`, `notes`, `dream`, `reindex` | Explicitly rejected before implicit-base maintenance. Unset the DSN for a deliberate local invocation. |
| `connect`, `version`, help | Lifecycle/metadata; no implicit local fact migration. |

The store API supports atomic `AddBatch` and expected-version relabeling. `memcue add -batch FILE` accepts one JSON array of content-only FactInput objects (exported field names, embedded metadata flattened), validates the whole input, then writes all facts atomically and prints one ID per fact. Only optional `-force` can accompany `-batch`; other flags/positional text are rejected. The input must be a regular, non-symlink file no larger than 64 MiB. Scope/origin/expiry are explicit per input; auxiliary keywords/queries and unknown fields are rejected. Remote split/retag, historical file import/backup and remote reindex workflows remain explicit unsupported/local maintenance operations. These refusals are the supported maintenance matrix; they do not require a remote implementation. Search JSON retains its existing integration contract. Default session state is namespaced by remote endpoint/database/user/transport, excluding passwords; an explicit `MEMCUE_STATE_DIR` remains the caller's override.

## Moving a local base

`memcue migrate -postgres` copies the latest version of every fact of the local base (`MEMCUE_FACTS`, a text history
or a SQLite file) into the PostgreSQL base of `MEMCUE_POSTGRES_DSN`, keeping ids, version numbers, timestamps,
authors, retired and expired facts, with the search terms of each. Older versions stay in the local base: PostgreSQL
keeps no history. Without `-apply` it reports the source and the target and writes nothing, not even the schema. A
target of schema 1 is refused with the way out: drop the `memcue` schema with the owner role and run it again.

```sh
export MEMCUE_POSTGRES_DSN="$(cat "$HOME/.config/memcue/postgres-owner.dsn")"
memcue migrate -postgres          # report: facts and versions in the source, state of the target
memcue migrate -postgres -apply   # copy, read back, compare, commit
```

The target must hold no facts: a base already in use is refused (`ErrTargetNotEmpty`), so the copy never becomes a
second writer. An empty database gets its schema as on a first open, so the role needs schema creation rights there;
use the provisioning role, not the runtime one. The copy runs in one transaction under the PostgreSQL write lock: one
COPY, then every fact is read back and compared byte for byte with the source before the commit, so a mismatch
leaves the base empty. A lost commit reply is reported as an unknown outcome: inspect the base before running it
again. The limits (100,000 facts, 64 MiB) are checked before connecting. A missing or empty local base is
refused rather than copied as nothing. The command is bounded by signals, not by the four-second budget of a fact
operation.

Stop every writer of the local base before the copy, and switch every client to the DSN after it: writes made to the
local base after the copy stay local. The local base is read, never changed, except that opening a text history
brings its cache up to date as any local open does.

## Ownership, backups and verified limits

PostgreSQL is the only shared fact authority; users/operators own its deployment, access controls and backups. Use PostgreSQL logical backup/restore tools and test a restore in an isolated database. Do not use Memcue's SQLite file backup or Git history as a PostgreSQL backup. No live migration, import, production database operation or tailnet enrollment was performed for this implementation.

Current client pins are Go 1.27.3, pgx v5.9.2 and tsnet v1.104.5 (see `go.mod` for the full dependency graph). tsnet v1.98.6 could not decode a netmap carrying an SSH policy under Go 1.27's `encoding/json` (`SessionDuration has unsupported format tag option`), so a node of a tailnet with SSH rules in its ACL never left `NeedsLogin` (#697). v1.104.5 was verified on macOS against Headscale 0.29.7 with SSH rules: login, approval, PostgreSQL access and approved identity across `connect off`/`up`. The security dependency update was checked with the default regression suite and `govulncheck`; it did not repeat the opt-in PostgreSQL/Headscale fixtures below.

The original isolated client verification used Go 1.27.0, pgx v5.7.6, tsnet v1.98.6, with go-json-experiment/json v1.27.0 for Go 1.27 compatibility. Isolated Linux verification used PostgreSQL 17.11 and Headscale 0.29.7. A local DERP fixture with a self-signed test exception was needed for the demonstrated two-node connectivity; this is not production DERP/TLS certification. Headscale bootstrap, identity restart and a CLI write/search through tsnet were exercised. Ordinary PostgreSQL logical restore and subsequent CLI CRUD/history were exercised separately. macOS and production Tailscale/vendor integration remain unverified. Later isolated fixture packets separately qualify ACL, outage and enrollment coverage; they are not vendor certification. Ordinary-network database TLS chain/name rejection, SCRAM success/failure, database role denial, real IPv6 loopback, version incompatibility, and ambiguous commit response were exercised in the repair fixture. Model-free Hermes/DSH search and DSH pre-step were exercised through the embedded route, including actual inner IPv6; this does not certify the full released Hermes runtime. See the implementation handoff for exact commands, failed attempts, measurements and remaining acceptance blockers.

Repair cycle 1 adds TCP peer preservation and actual PostgreSQL cancellation over embedded DNS/IPv6 routes, health-independent shutdown and unconditional IPC setup cleanup, nonblocking key/profile opens, transactional post-write cap refusal, and distinct unavailable native snapshots. Fault fixtures exercised read/write rollback, blocked Terminate/Close, late cleanup dials, expired read publication and a committed mutation with its reply withheld. That cycle left CPU cancellation, ACL/revocation/control-loss/concurrent-owner coverage and capacity latency open. Later cycle packets record their own evidence; historical results are not final-source acceptance.


Repair cycle 2 uses native targeted history/mutations, request-scoped current-version query snapshots, native SQLite cancellation, checked tokenizer batches and ownership of already-open pgx cancellation sockets. The isolated embedded route additionally exercised PostgreSQL TLS hostname/chain negatives and positive TLS, plus concurrent native searches. See the repair-02 handoff for exact final-source evidence, measurements, failed attempts and remaining gates; neither issue is declared accepted by implementation alone.


## Cycle 3 caller/version and cancellation contract

`search -json` now includes each fact's integer `version`; library `Get` and history retain `Fact.Version` (history JSON uses the existing `Version` name). PostgreSQL correction and retirement require that **earlier caller-observed** version:

```sh
memcue search -json -in all "$QUERY"
# Copy the selected result's id and version after reviewing its content.
memcue correct -expected-version "$VERSION" "$FACT_ID" -- "$CORRECTED_TEXT"
memcue retire -expected-version "$VERSION" "$FACT_ID"
```

Hermes and DSH `memcue_correct`/`memcue_retire` use integer `expected_version`; both expose `version` from search. Claude/Codex CLI adapters forward the same `-expected-version` flag. Codex also preserves the explicit DSN and documented client transport/profile variables across its otherwise filtered subprocess environment; invalid remote selection cannot silently become a local invocation. Its local facts/state/host settings still come from the explicit adapter configuration. Bootstrap keys remain a separate helper-owner action. Library `Correct`, `CorrectExpiring` and `Retire` accept the expected version argument. Comparison happens after acquiring the PostgreSQL write lock, before append. A missing/nonpositive remote version is an actionable error; a stale one returns `fact changed since it was read` without appending. Read and make a new decision on conflict; never fetch latest solely to hide a stale decision or replay automatically. Old local CLI/library calls without a version remain compatible; supplied local versions are checked too. The Hermes built-in note mirror stores the ID and version acknowledged by its own remote add/correct. Stale mirror edits/removals preserve the mapping and report failure without replay. Legacy remote mirror mappings containing only an ID cannot authorize a mutation: they fail with the missing-version message rather than reading latest or silently adding a replacement. Local mirror mappings remain compatible.

A CLI invocation now owns its context before flags/input/discovery, and shares it across repository-pitfall and upkeep opens, query retries, lexical work, state locks and cleanup. PostgreSQL keeps the existing four-second invocation budget; persistent `connect serve` has its separate explicit owner lifetime. Context cancellation/signals propagate to owned input/output file deadlines and nonblocking state-lock waits. Library `OpenPostgres` retains independent operation calls, while explicit request handles retain their caller context.

Supported lexical scans, normalization and long-word Snowball loops check cancellation in bounded units (at most 256 runes); ordinary short stems use the pinned library on at most 256 bytes. The MIT-licensed English/Russian Snowball v0.10.0 adaptation preserves its steps, with context checks and checkable rune conversion. Native JSON strings use at most 4096-byte standard-library chunks, while fact structures/arrays/maps and ranking/containment loops check context. Known scalar/time formatting calls remain synchronous and bounded; kernel syscalls and arbitrary external/custom marshalers are not claims of instantaneous preemption. There is no detached projection worker or semantic token truncation.

Hook control JSON has an explicit 64 MiB envelope ceiling and excessive input is refused rather than truncated. Batch input retains its original 64 MiB ceiling. Immutable accepted PostgreSQL history keeps its existing exact 100000-row/64 MiB caps and wire semantics; this introduces no fact/history format migration. Existing local SQLite/text-history authority is unchanged.

The isolated Headscale operator flow opened the explicit login URL, demonstrated pending `NeedsLogin` refusal, completed operator registration, exercised TLS add/search, restarted with the same approved identity and expired an already-enrolled node. This is Headscale registration pending, not certification of vendor `NeedsMachineAuth` UI. At that checkpoint, lost caller-output ambiguity remained an acceptance blocker; a failed response must never trigger automatic replay. The cycle 3 result records its historical evidence and process qualification. The response contract below describes the subsequent repair; independent final acceptance remains separate.


## Mutation receipt delivery and recovery

A database commit and delivery of its CLI receipt are separate events. If a supported fact mutation finishes storage but its stdout receipt is closed, short, or blocked past the invocation deadline, the CLI reports `UNKNOWN OUTCOME` on stderr and exits nonzero. SIGPIPE is handled so that a closed stdout pipe can reach this response boundary. Inherited blocking output pipes receive an owned pollable descriptor for the write; their original flags are restored afterwards. The four-second remote invocation deadline is unchanged.

Hermes and Codex also report explicit unknown outcomes when a mutation process times out, is terminated by a signal, or returns an incomplete mutation receipt. DSH exposes `stored: null` and `outcome: "unknown"` with recovery guidance for this uncertainty; ordinary refusal uses `stored: false` and an acknowledged write uses `stored: true`. A read-response failure is a read failure, not a mutation-outcome claim. Completed receipts and pre-commit input/authentication/version failures retain their distinct classifications. No caller automatically replays a mutation.

For a known fact ID, inspect the durable version with `memcue history -json "$FACT_ID"`. Compare the stored version and content privately, then make a new decision. Correct/retire still require the version observed by that decision; replaying the old expected version is refused without appending another version. If an add receipt was lost and its ID is unavailable, inspect the durable base/history to identify whether it was stored before considering another add. There is no durable request-key idempotency guarantee. Recovery diagnostics do not include the DSN, password, API key or fact body.

The embedded helper's JSON reply acknowledges transport establishment, not a fact mutation. The client waits for the dial reply before exposing the stream to pgx, and the helper sends that reply before forwarding PostgreSQL protocol bytes. Losing that reply therefore fails before a database session/query/commit can be issued on that stream. Once the stream is established, PostgreSQL commit-response uncertainty uses the existing explicit ambiguous-commit error.

Coordination/relay outage alone does not revoke an already established, cached direct peer path. An isolated control-plus-relay shutdown can leave cached database access working. ACL denial of the configured database port and loss of the backend listener must still refuse access without opening a local fact base; restoring the ACL permits recovery while the owned control plane is healthy.


Supported Hermes, Codex and DSH mutation callers negotiate an opt-in versioned completion frame on stderr with a fresh per-attempt nonce. The frame binds the status to the byte count and SHA-256 of the complete stdout receipt, including every batch ID or a validated empty batch. Ordinary CLI stdout remains unchanged. Missing, truncated, mismatched or unsupported frames mean `UNKNOWN OUTCOME`, even with exit 0 or exit 1; a complete native refusal frame remains a refusal. The callers never replay a mutation. Recovery applies to the entire uncertain batch or local dream plan, which may be partially applied. Inspect durable facts and per-ID history before deciding what to do next. This protocol does not add request-key idempotency or remote dream support.

Native diagnostics, including flag errors, share the invocation output deadline. If both output channels fail, bounded termination is possible even when no uncertainty message can be delivered. POSIX Python callers with `waitid`/`WNOWAIT` retain an unreaped session leader while stopping its owned process group; DSH uses an invocation-owned Node supervisor that stops its own group and closes the caller streams within the same deadline. Direct children are reaped; terminated orphan descendants are reaped by their adopting OS parent. Processes that deliberately create a separate session are outside this group, including the separately owned persistent tsnet helper. No global reaper, process-name sweep or shared proxy signalling is used. Platforms without that wait API retain Python direct-child cleanup; Windows retains DSH direct-child cleanup. The demonstrated group-cleanup contract is Linux/POSIX. Native stdout delivery reserves 50 ms of the existing aggregate budget for diagnostics and cleanup; it does not extend the invocation deadline.
