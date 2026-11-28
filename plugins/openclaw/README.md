# Experimental Memcue tools for OpenClaw (#838)

This additive tool plugin does not select or replace a memory provider. Its tool
descriptions still explain individual calls. OpenClaw also adds the conditional
recall and proactive capture instructions to the system prompt through scoped
`promptGuidance`, only while the corresponding tools are callable. This does
not query the database at prompt assembly or replace built-in memory.

Pack this directory with `npm pack` and install the tarball on an isolated test
Gateway with `openclaw plugins install npm-pack:/absolute/path/to/package.tgz`.
Set `plugins.entries.memcue-tools.config` to an object containing absolute
`binary` and `dataDir` paths. Use the Memcue binary from the tested revision
and an existing empty private directory (mode 0700), dedicated to that Gateway
and owner. Do not point it at production data. The tool process receives explicit
local storage paths and does not inherit remote sync configuration.

Run `node --test test.mjs` for the lightweight adapter contract. For the Gateway
smoke test, save a synthetic rule in one owner session, recall it from another,
correct it, and retire it. Verify a group session cannot call any of the tools.
A timed-out mutation may already have landed; search before retrying.
