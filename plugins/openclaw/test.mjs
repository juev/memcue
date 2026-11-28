import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { test } from "node:test";
import plugin from "./index.js";

const owner = () => ({
  memoryAudience: { kind: "owner-private", agentId: "test" },
  assertInvocationCurrent() {},
  assertMemoryAudienceCurrent() {},
});

test("tool registration, privacy, bounded local CLI and failure semantics", async (t) => {
  const dataDir = await mkdtemp(join(tmpdir(), "openclaw-memcue-"));
  t.after(() => rm(dataDir, { recursive: true, force: true }));
  const binary = join(dataDir, "cli");
  await writeFile(binary, `#!${process.execPath}
const fs = require("node:fs");
const args = process.argv.slice(2);
fs.writeFileSync("last-call", JSON.stringify({args, remote: process.env.MEMCUE_SERVER, db: process.env.MEMCUE_FACTS}));
if (args.includes("fail")) process.exit(2);
console.log(args[0] === "search" ? JSON.stringify({facts:[], hints:[]}) : "ok");
`, { mode: 0o700 });
  const factories = new Map();
  const promptGuidance = new Map();
  plugin.register({ pluginConfig: { binary, dataDir }, registerTool: (tool, { name, promptGuidance: guidance }) => { factories.set(name, tool); promptGuidance.set(name, guidance); } });
  const manifest = JSON.parse(await readFile(new URL("./openclaw.plugin.json", import.meta.url)));
  assert.deepEqual([...factories.keys()], manifest.contracts.tools);
  assert.match(promptGuidance.get("memcue_search"), /Skip it for self-contained tasks/);
  assert.match(promptGuidance.get("memcue_add"), /proactively for durable agreed rules/);
  assert.equal(promptGuidance.get("memcue_retire"), undefined);
  for (const factory of factories.values()) {
    assert.equal(factory.contextVersion, 2);
    for (const ctx of [{}, { ...owner(), sandboxed: true },
      { ...owner(), memoryAudience: { kind: "conversation" } },
      { ...owner(), assertMemoryAudienceCurrent: undefined }]) {
      assert.equal(factory.create(ctx), null);
    }
  }
  const search = factories.get("memcue_search").create(owner());
  assert.deepEqual(JSON.parse((await search.execute("1", { query: "-literal" })).content[0].text).facts, []);
  assert.deepEqual(JSON.parse(await readFile(join(dataDir, "last-call"))), {
    args: ["search", "-json", "-n", "5", "--", "-literal"], db: join(dataDir, "facts.db"),
  });
  await assert.rejects(search.execute("2", { query: "fail" }), /search failed/);
  const revoked = factories.get("memcue_retire").create({
    ...owner(), assertMemoryAudienceCurrent() { throw new Error("revoked"); },
  });
  await assert.rejects(revoked.execute("3", { id: "x", reason: "test" }), /revoked/);
  assert.equal(JSON.parse(await readFile(join(dataDir, "last-call"))).args.at(-1), "fail");
});

test("real CLI: save, recall from a new context, correct, retire", { skip: !process.env.MEMCUE_BIN }, async (t) => {
  const dataDir = await mkdtemp(join(tmpdir(), "openclaw-memcue-real-"));
  t.after(() => rm(dataDir, { recursive: true, force: true }));
  const factories = new Map();
  plugin.register({
    pluginConfig: { binary: resolve(process.env.MEMCUE_BIN), dataDir },
    registerTool: (tool, { name }) => factories.set(name, tool),
  });
  const call = async (verb, params) =>
    (await factories.get("memcue_" + verb).create(owner()).execute("test", params)).content[0].text;
  const search = async () => JSON.parse(await call("search", { query: "cobalt" })).facts;
  await call("add", { content: "Use cobalt for synthetic fixture one." });
  const [fact] = await search();
  assert.ok(fact.id);
  await call("correct", { id: fact.id, content: "Use cobalt for synthetic fixture two." });
  assert.match(JSON.stringify(await search()), /fixture two/);
  await call("retire", { id: fact.id, reason: "Synthetic policy withdrawn" });
  assert.deepEqual(await search(), []);
});
