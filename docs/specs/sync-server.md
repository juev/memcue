# Sync server over the tailnet

State: target contract for [#714](https://github.com/juev/memcue/issues/714), partly implemented: the merge, the
server and replica bases (`internal/store/synced.go`), the server (`internal/server`, `memcue server`), the helper's
sync (`internal/replica`) and the client commands (`cmd/memcue/server_mode.go`), dream on the server and `migrate -server`;
the plugins and the documentation are not. Sections are marked implemented as the steps land.
Sources: the #714 discussion and RFC of 2026-11-10 (owner's decisions), the measurements of
[daemon-703](../research/daemon-703-2026-11.md), the merge method of juev/lists (`docs/specs/sync.md`, S1-S3 there).

## Purpose and boundaries

memcue in server mode keeps one shared fact base in SQLite and hands its changes to clients over the tailnet. Each
machine keeps a local SQLite replica; agents read and write only the replica, and a single background process per
machine (the extended tailnet helper) keeps it in step with the server.

Out of scope: removing the PostgreSQL backend or the text history (both stay, unused in server mode); search ranking;
vector search; an OS service for the helper; the cutover of Mac, loffe and Hermes, which follows the release.

## Requirements

- R1: In server mode `search`, `session-start`, `session-card`, `add`, `correct`, `retire` and `reopen` work on the
  replica only and never wait for the network. With the server unreachable they answer without a connection error.
- R2: A change accepted by the server reaches the replica of every client whose helper runs, by a long poll the
  server answers as soon as the change exists. Target: about one second end to end.
- R3: A write made without a connection is stored in the replica, found by search at once, and kept in the replica's
  outbox until the server accepts it. Then the outbox entry is removed and the change carries the server's `seq`.
- R4: Every outgoing operation carries an `op_id`. A retry after a lost reply gets the earlier answer, and the server
  applies the change once.
- R5: Each mergeable field of a fact (`content`, `kind`, `scope`, `expires`, `status`, `origin`) is a last-writer-wins
  register stamped with a hybrid logical clock (HLC). For each pair "fact, field" the greater stamp wins. Applying a
  change is idempotent and independent of the order of delivery.
- R6: A change of `content` carries the `content` stamp it was based on. When it is concurrent with the stored value
  (its base differs from the stored `content` stamp and the texts differ), the losing text is kept as a new fact that
  refers to the original and copies its other registers. A sequential correction, made after seeing the previous one,
  creates no new fact.
- R7: Retire is the `status` register. A fact retired on one machine and corrected on another stays retired with the
  correction kept, and `reopen` brings back the corrected text.
- R8: The server rejects a change whose stamp is ahead of its own clock by more than a limit. The change stays in the
  outbox, and the helper reports it through `connect status` and the session hints (R15).
- R9: When the server's `base_id` differs from the replica's, or the replica's `last_seq` is above the server head,
  the helper rebuilds the replica from the server and keeps the outbox.
- R10: If session-start or session-card finds no helper socket, it starts the helper detached and answers without
  waiting for it. If it finds a helper of another binary version, it asks that helper to exit and starts a new one.
- R11: In server mode a client rejects `dream`, starts no background dream and shows no dream hint. The server runs
  dream on a schedule. A fact is fresh for dream when its `content` stamp changed since the previous pass; a change of
  labels alone does not make it fresh.
- R12: `memcue migrate -server` reads the local base of `MEMCUE_FACTS`, reports what it would send, and with `-apply`
  sends the latest version of every fact to the empty server of `MEMCUE_SERVER` through the helper. Stamps come from
  `created`, with the server's device id. A server that holds any fact refuses and stores nothing.
- R13: The server listens on the tailnet only (`tsnet`), names each client in its log through `WhoIs`, and leaves
  access control to the tailnet ACL. It opens no port of the host.
- R14: The plugins work in server mode: Codex passes `MEMCUE_SERVER` through, Hermes gives hints without a local base
  file. DSH asks for `expected_version` only under a DSN, so it needs no change; the Claude Code skill's
  `-expected-version` is checked against the replica's version, as on a local base today.
- R15: In server mode session-start and session-card add one line when the helper waits for a tailnet login ("run
  memcue connect up"), when the replica has never synced, and when the outbox holds changes the server rejected.
- R16: All agents of a machine with the same `MEMCUE_SERVER` share one replica, whatever their `MEMCUE_FACTS` and
  `MEMCUE_STATE_DIR`. A write of one is found by another's search at once, and a helper started by any of them syncs
  that replica.

## Invariants and compatibility

- Without `MEMCUE_SERVER` nothing changes: SQLite, the text history and PostgreSQL behave as before.
- The server and the replicas keep one row per fact, without history. `version` counts changes for display and takes
  part in no check. `history` shows the current version only, as under PostgreSQL.
- Fact ids are client ULIDs (48-bit time, 80 random bits). The server still rejects a create whose id exists with other
  content; the client then issues a new id.
- The server and the client merge with the same function, so a replica that applies the server's row and then its own
  outbox reaches the state the server will reach.
- The replica's HLC is shared by every process of the machine: its last stamp lives in the replica and advances in
  the write transaction. Applying received rows moves it past the greatest stamp seen.

## Decisions

- **Server.** `memcue server` runs in the foreground under systemd or Docker, keeps its base in SQLite at
  `MEMCUE_FACTS`, and serves HTTP with JSON over `tsnet.Listen`:
  - `GET /v1/changes?since=N&wait=25s&limit=L` returns the rows with `seq > N` in order, the head and the `base_id`;
    it answers at once when a change exists, after `wait` otherwise. The helper waits 25 s and gives up on an answer
    5 s later: a connection whose server went away without closing it (a restart, a laptop that slept) leaves the
    replica deaf for 30 s at most. Its first pull, and the first after a failure, asks with `wait=0`, so a new replica
    learns the base and the status loses a failure as soon as the server answers.
  - `POST /v1/changes` takes a batch of stamped field changes with `op_id` and returns each result with its `seq`.
  - `POST /v1/import` loads facts into an empty base only (R12).

  It listens on port 80 of the tailnet; WireGuard of the tailnet encrypts the traffic, so the HTTP is plain. Its
  tailnet identity is its own `MEMCUE_TAILSCALE_STATE_DIR`, owned like a helper's (`owner.lock`). A change stamped
  more than five minutes ahead of the server's clock is refused as `ahead` (R8) and not remembered, so the same
  operation is accepted once the clock catches up; the other refusals (`malformed`, `id_taken`, `not_found`) are
  answered again on a retry (R4). Implemented.

  A custom binary protocol was rejected: payloads are megabytes at most and HTTP is easier to inspect. A push started
  by the server was rejected: a client would have to listen on the tailnet, for no gain in latency over the long poll.
- **Change number.** `seq` is a signed 64-bit integer the server raises on every write, `max(seq)+1` inside the write
  transaction. Dream runs as a separate process on the server's base; the long poll notices its commits through
  `PRAGMA data_version`.
- **Client.** The CLI opens the replica at `$XDG_STATE_HOME/memcue/server-<server name>/replica.db`, derived from
  `MEMCUE_SERVER` alone (R16). Only the helper uses the network: it long-polls, applies rows, and sends the outbox.
  An agent's state directory keeps the agent's own files (Codex pitfall snapshots, logs, `search.log`).
- **Outbox.** A replica table of unsent field changes with their stamps, base stamp and `op_id`. A row received from
  the server replaces the local row, and the outbox entries of that fact are applied on top again. Implemented in
  `internal/store/synced.go`: the stamps, `seq`, `updated` and `loser_of` (the fact a losing text came from, R6) live
  in `sync_rows`, one row per fact beside its one row in `facts`. An ordinary open refuses a server or replica base,
  and a base with unsynced facts cannot become one.
- **Device id.** A random id created with the replica and stored in it. A tailnet identity was rejected: it does not
  exist before the first login and is shared by every base of the machine.
- **Fields.** The server and the replica carry `id`, `version`, `content`, `origin`, `created`, `status`, `expires`,
  `kind`, `scope`, `source`, the HLC stamps, `seq` and `updated`. `review`, `channel`, `write_id`, `author`,
  `prompt_version`, `evidence` and `fact_vectors` carry no value in server mode; columns that the shared `Store` code
  lists stay present and empty.
- **Helper.** One per machine, as today (`owner.lock` in the tailnet identity directory). Its profile names either one
  PostgreSQL or one server; a second server on the same machine needs its own `MEMCUE_TAILSCALE_STATE_DIR`. `status`
  reports the binary version, the last sync time, the outbox length and rejected changes. Its `dial` operation also
  reaches the server, which `migrate -server` uses; that admin command has no four-second budget.
- **Client commands.** Rejected in server mode: `install`, `dream`, `migrate` except `-server`, `merge`, `dump`. Working
  on the replica: `import`, `notes`, `reindex`. `MEMCUE_SERVER` needs no `MEMCUE_TAILSCALE_MODE`: the helper is the only
  transport there; with `MEMCUE_POSTGRES_DSN` too, every command refuses. A session start asks the helper for its
  status for at most half a second; a helper of another version is told to exit, and its replacement
  (`connect serve -takeover`) waits up to ten seconds for the identity. Implemented.
- **Migration.** From one base: loffe and Hermes are merged into the Mac base once by the existing git sync, then
  `migrate -server` loads that base. Each fact goes with the text and labels of its latest version and the creation
  time of its first. Implemented in `cmd/memcue/migrate_server.go`.
- **Dream on the server.** `memcue server` looks every ten minutes whether a dream is due (`MEMCUE_DREAM_EVERY`,
  the model of `MEMCUE_API_URL` or `claude -p`, off with `MEMCUE_DREAM_BACKGROUND=0`) and runs `memcue dream -run
  -background` as a process of its own on the server base. A server row keeps `content_version`, the version at which
  its text last changed, so new labels alone leave a fact as the last dream saw it. `memcue server` refuses to run
  with `MEMCUE_SERVER` or `MEMCUE_POSTGRES_DSN`, which would send its dream to a client backend. Implemented.

## Verification scenarios

Tests are named as the steps land.

- R1: server mode, server down → search, session-start and add succeed without network delay
  (`TestServerModeKeepsOneReplicaPerMachine`, `TestSessionStartWithoutAHelperStartsOneAndAnswersAtOnce`).
- R1, R2, R3, R5, R6, R7, R10: end-to-end on a real tailnet, a disposable Headscale with a server and two clients built
  from the tree (`TestSyncServerOverTheTailnet`, opt-in with `MEMCUE_FINAL_HEADSCALE_BINARY` and `MEMCUE_TEST_BIN`).
- R4, R6, R8, R12: server handler tests through `httptest` (`internal/server/server_test.go`:
  `TestRetryAfterALostReplyGetsTheEarlierAnswer`, `TestConcurrentContentReachesReplicasWithTheLoser`,
  `TestStampFarAheadIsRefusedAndNotRemembered`, `TestImportLoadsAnEmptyBaseOnly`); the long poll
  (`TestLongPollAnswersACommitOfAnotherProcess`, `TestLongPollAnswersAPushAtOnce`); store level in
  `internal/store/synced_test.go` (`TestRetriedOperationIsAppliedOnce`, `TestConcurrentCorrectionsKeepTheLosingText`).
- R5: property test, every permutation of one set of changes gives the same row
  (`TestAnyOrderOfChangesGivesTheSameRegisters`).
- R3 (local part), R7, R9: replica store tests (`TestReplicaWriteIsFoundAtOnceAndQueued`,
  `TestServerRowKeepsTheReplicasPendingChange`, `TestRetireAgainstACorrectionStaysRetiredAndReopensCorrected`,
  `TestResetReplicaKeepsTheOutbox`); the helper's loop with a fake server follows.
- R10: test with a built binary (`buildBinary`), as in `cmd/memcue/connect_up_test.go`
  (`TestSessionStartStartsAndReplacesTheRealHelper`, `TestSessionStartReplacesAHelperOfAnotherVersion`).
- R11: command tests and a freshness unit test (client side: `TestServerModeRefusesLocalCommands`,
  `TestSessionStartWithoutAHelperStartsOneAndAnswersAtOnce`; server side:
  `TestDreamOnTheServerSeesANewTextAndNotNewLabels`, `TestServerDreamsWhenADreamIsDue`).
- R12: `TestImportLoadsAnEmptyBaseOnly`, `TestMigrateServerReportsAndLoadsAnEmptyServerOnly`.
- R14: `make test-plugins`.
- R15: command tests with a fake helper (`TestSessionStartReportsLoginAndRefusedChanges`); the helper's status of a new
  replica and after a failure (`TestNewReplicaOfAnEmptyServerSyncsWithoutWaiting`,
  `TestStatusClearsTheFailureWhenTheServerIsBack`).
- R16: `TestServerModeKeepsOneReplicaPerMachine`, `TestPathDependsOnTheServerOnly`.
