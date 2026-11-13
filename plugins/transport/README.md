# memcue-transport

The memcue CLI transport, the binary install and the compact search rendering shared by the native harness plugins
(#754, #756). One implementation, imported by the Pi and OpenCode plugins from npm and by the DeepSeek Harness plugin
through the `memcue-dsh/transport` subpath of the unified root package, so a fix reaches every host at once. The
conformance suite in `conformance/` runs the same contract against every host adapter, the shell wrapper and the
Python provider included.

Plain ESM JavaScript with a hand-written declaration file: the DeepSeek Harness plugin loads from `node_modules`,
where Node refuses to strip TypeScript types, and OpenCode runs plugins inside Bun. Node 24 and Bun are both
supported. There is no build step.

## Transport contract

`new MemcueCli({binary, facts?, stateDir?, host?, caller?, env?}).run(args, {cwd, stdin?, mutation?, signal?, timeoutMs?})`

- one spawn per memcue call, `shell: false`, a detached process group on POSIX;
- a hard timeout (default 15 s, at most 2147483647 ms) and cancellation kill the whole process group, but only while
  the direct child has not exited, so a reused PID is never signalled;
- stdout and stderr share a 4 MiB budget; overflow interrupts the call;
- a mutation gets a fresh 16-byte nonce in `MEMCUE_OUTCOME_NONCE` and its `MEMCUE_OUTCOME_V1` receipt is checked:
  version, nonce, byte count and SHA-256 over stdout, and byte count and SHA-256 over the diagnostics before the
  marker; the receipt must end the stderr with a newline;
- `success` with exit 0 is `committed`, `refused` with a positive exit is `refused`, anything else is `unknown`;
  an unknown mutation is never retried, and its stderr says so;
- a command that never started is `not-started`, distinct from an unknown mutation;
- ambient `MEMCUE_*` settings reach the CLI; the configured ones override them, and the ambient
  `MEMCUE_OUTCOME_NONCE` is always removed.

## Search rendering

`renderSearch(answer, options)` turns a parsed `memcue search -json` answer into the text a native host hands the
model (#756): one line per fact — `<id>@<version> <scope> <content>` — and one trailer line with the count, the
widening and the hints the binary printed, word for word. Nothing is dropped silently: a cut answer says how many
facts it left out, and a fact served under the score floor carries `(below the floor)`.

- `contentChars` (default 600) clips one fact's content with an ellipsis, and a fact never costs two lines;
- `totalChars` (default 4000) bounds the fact lines; the trailer line is added on top of that budget;
- `maxFacts` (default 10) bounds the number of fact lines.

The form is the same in every host, and `conformance/render-vectors.json` pins the exact text for this renderer and
for the Python one of the Hermes provider: `conformance/render.test.mjs` and `conformance/hermes_contract.py` run the
same vectors, so the two implementations cannot drift apart. The rendering is host-side by design: no CLI flag, no
argv and no receipt changes with it.

## Install contract

`install(version)` puts the release's binary into the machine's `~/.local/bin/memcue`, the one binary every plugin
of the machine shares (#718):

- a machine binary of this release or newer stays (`version -minimum`); so does a symlink or anything else that is
  not a plain file;
- `MEMCUE_AUTO_INSTALL=0` turns the install off;
- the archive must match its line in the release's `checksums.txt`;
- a lock directory in memcue's state directory keeps two plugins from installing at once; a lock left by a crash
  expires after ten minutes;
- the binary is unpacked next to the target and renamed over it, so a reader never sees half a binary and a memcue
  that is running keeps its file;
- `MEMCUE_RELEASE_URL` replaces the download base, for a mirror or a test.

The version is the calling plugin's release: each plugin reads its own manifest and passes the version in.

## Conformance

`node --experimental-strip-types --test conformance/*.test.mjs` from this directory, or `make test-conformance`
from the repository root, which also runs the shell and Python contracts. The suite needs the built CLI
(`MEMCUE_BIN`) for the cases that execute a real binary, and the Pi and OpenCode dependencies for their probes.
