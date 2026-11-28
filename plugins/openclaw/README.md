# Experimental Memcue tools for OpenClaw (#838)

This additive tool plugin does not select or replace a memory provider. Its tool
descriptions still explain individual calls. OpenClaw also adds the conditional
recall and proactive capture instructions to the system prompt through scoped
`promptGuidance`, only while the corresponding tools are callable. This does
not query the database at prompt assembly or replace built-in memory.

## Install from GitHub Releases

Download the ready-to-install archive and its checksum from the
[OpenClaw adapter release](https://github.com/juev/memcue/releases/tag/openclaw-v0.1.0).
No repository checkout or npm registry is required. This is an experimental
release of the adapter, not a new Memcue binary release.

In the download directory on the Gateway host, verify and install the archive:

```sh
sha256sum -c juev-memcue-openclaw-0.1.0.tgz.sha256
openclaw plugins install "npm-pack:$PWD/juev-memcue-openclaw-0.1.0.tgz"
```

Stop if checksum verification fails. Run the installer against your authorized
profile and review the capability consent. Then follow the binary, configuration,
and verification steps below, skipping the checkout, packing, and install steps.

## Agent-assisted installation from a commit

Only proceed when the operator has authorized the target. For a first install,
use an isolated OpenClaw profile with a synthetic database. Do not use the
production Gateway, select a different memory provider, or connect a sync backend.

1. From a checkout of Memcue, use the full commit SHA approved by the operator.
   Pack its tracked files rather than your current working tree:

   ```sh
   checkout=$(mktemp -d)
   git archive "$COMMIT" plugins/openclaw | tar -xf - -C "$checkout"
   (cd "$checkout/plugins/openclaw" && npm pack --ignore-scripts --pack-destination "$checkout")
   ```

2. Use OpenClaw 2026.11.10 and Memcue 0.41.0 for the tested combination (not a
   minimum-version claim). Download the Memcue release for the Gateway's OS and
   architecture from [GitHub Releases](https://github.com/juev/memcue/releases/tag/v0.41.0).
   Verify its SHA-256 against GitHub release asset metadata or the release checksum
   file, extract it, and use an absolute path to the binary on the Gateway host.
   Create a separate, empty directory with mode 0700 for the database.
3. With the CLI explicitly targeting the approved test profile, install the
   reviewed archive. Follow the installer prompts; a noninteractive run may need
   `--force --accept-capabilities` after explicit approval of that archive and
   its capabilities. Do not use these flags for unreviewed sources.

   ```sh
   openclaw plugins install "npm-pack:$checkout/juev-memcue-openclaw-0.1.0.tgz"
   ```

4. Merge the following settings under `plugins.entries.memcue-tools.config`
   in that profile. Do not replace the rest of the config.

   ```json
   {
     "binary": "/absolute/path/to/memcue",
     "dataDir": "/absolute/path/to/private-test-data"
   }
   ```

   The plugin uses `facts.db` and `state/` under this directory. It does not
   inherit remote sync configuration.
5. Run `openclaw config validate` and
   `openclaw plugins inspect memcue-tools --runtime --json`. Confirm
   `status: loaded` and all four tools: `memcue_search`, `memcue_add`,
   `memcue_correct`, and `memcue_retire`. Registration is not proof of
   session access. On the test Gateway, save a synthetic rule in one owner
   session; find it in another, correct it, retire it, then verify it no longer
   appears in ordinary search. Confirm all four tools are unavailable in a
   group session. Report any skipped phase separately.

From the package source directory, `MEMCUE_BIN=/absolute/path/to/memcue node
--test --test-concurrency=1 test.mjs` checks the adapter without chat or model
calls. A timed-out mutation may already have landed; search before retrying.
