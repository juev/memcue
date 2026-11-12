import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, mkdtemp, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { isAbsolute, join } from 'node:path';
import test, { type TestContext } from 'node:test';
import { MemcueCli, type CliResult } from '../src/cli.ts';

// make test and CI pass the built binary as MEMCUE_BIN, as for the other plugins.
const binary = process.env.MEMCUE_BIN || process.env.MEMCUE_TEST_BINARY;
const live = { skip: !binary, timeout: 60_000 };
// The CLI inherits ambient MEMCUE_* settings; a developer's own (a server, a model) must not reach the test bases.
for (const key of Object.keys(process.env)) if (key.startsWith('MEMCUE_')) delete process.env[key];

async function fixture(t: TestContext) {
  assert.ok(binary && isAbsolute(binary), 'MEMCUE_BIN must name an absolute executable path');
  const dir = await mkdtemp(join(tmpdir(), 'memcue-pi-live-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const repo = join(dir, 'repo');
  const other = join(dir, 'other-repo');
  const plain = join(dir, 'not-a-repo');
  await mkdir(plain);
  for (const [path, name] of [[repo, 'memcue-pi'], [other, 'other-project']]) {
    execFileSync('git', ['init', '-q', path], { stdio: 'pipe' });
    execFileSync('git', ['-C', path, 'remote', 'add', 'origin', `https://example.com/test/${name}.git`], {
      stdio: 'pipe',
    });
  }
  const cli = new MemcueCli({
    binary,
    facts: join(dir, 'memory', 'facts'),
    stateDir: join(dir, 'state'),
    host: 'memcue-pi-live-test',
  });
  const run = (args: string[], mutation = false) => cli.run(args, { cwd: repo, mutation });
  return { dir, repo, other, plain, cli, run };
}

function successful(result: CliResult, mutation = false): string {
  assert.equal(result.exitCode, 0, JSON.stringify(result));
  assert.equal(result.outcome, mutation ? 'committed' : 'read', JSON.stringify(result));
  return result.stdout;
}

type Fact = { id: string; version: number; content: string; scope: string; created: string };

test('live: mutations persist, stale corrections are refused, and retire removes search results', live, async t => {
  const { repo, run } = await fixture(t);
  const initial = 'Prefer short examples when explaining concurrency trade-offs.';
  const corrected = 'Prefer short, concrete examples when explaining concurrency trade-offs.';
  const id = successful(await run([
    'add', '-cwd', repo, '-source', 'user', '-kind', 'rule', '-scope', 'repo', '--', initial,
  ], true), true).trim();
  assert.ok(id.length > 0);
  const search = async () => JSON.parse(successful(await run([
    'search', '-json', '-n', '10', '-cwd', repo, '-session', 'pi-live-mutations',
    '-min-score', '0', '--', 'concurrency examples',
  ]))) as { facts: Fact[] };
  const before = (await search()).facts.find(fact => fact.id === id);
  assert.ok(before, 'an independently launched search must find the added fact');
  assert.equal(before.content, initial);
  assert.equal(before.version, 1);
  assert.match(before.scope, /^repo:/);
  assert.ok(before.created);

  successful(await run(['correct', '-expected-version', '1', '--', id, corrected], true), true);
  const after = (await search()).facts.find(fact => fact.id === id);
  assert.equal(after?.version, 2);
  assert.equal(after?.content, corrected);
  const stale = await run(['correct', '-expected-version', '1', '--', id, 'This change must not be stored.'], true);
  assert.equal(stale.outcome, 'refused', JSON.stringify(stale));
  assert.notEqual(stale.exitCode, 0);
  assert.match(stale.stderr, /version|changed|expected/i);
  assert.equal((await search()).facts.find(fact => fact.id === id)?.content, corrected);

  successful(await run(['retire', '-expected-version', '2', '--', id], true), true);
  assert.equal((await search()).facts.some(fact => fact.id === id), false);
});

test('live: pitfall snapshots preserve whole facts and respect repository, expiry and retirement', live, async t => {
  const { repo, other, plain, cli, run } = await fixture(t);
  const fact = 'Closing a channel while workers are still sending caused a panic.\nWait for every sender before closing the shared channel.';
  const id = successful(await run([
    'add', '-cwd', repo, '-source', 'tool', '-kind', 'pitfall', '-scope', 'repo', '--', fact,
  ], true), true).trim();
  successful(await run([
    'add', '-cwd', repo, '-source', 'tool', '-kind', 'pitfall', '-scope', 'repo',
    '-expires', '2000-01-01', '--', 'Expired workaround: retrying the same request hid the original error.',
  ], true), true);
  const snapshot = async (cwd: string) => JSON.parse(successful(await cli.run([
    'session-card', '-pitfalls-json',
  ], {
    cwd,
    stdin: JSON.stringify({ session_id: 'pi-live-pitfalls', cwd, source: 'startup' }),
  }))) as { facts: string[]; context: string };

  const here = await snapshot(repo);
  assert.deepEqual(here.facts, [fact]);
  assert.match(here.context, /Repository pitfalls/);
  assert.deepEqual((await snapshot(other)).facts, []);
  assert.deepEqual((await snapshot(plain)).facts, []);
  assert.deepEqual((await snapshot(repo)).facts, [fact]);
  successful(await run(['retire', '-expected-version', '1', '--', id], true), true);
  assert.deepEqual((await snapshot(repo)).facts, []);
});

test('live: dream prepare and apply work without a model endpoint', live, async t => {
  const { dir, repo, run } = await fixture(t);
  const content = 'Prefer concrete examples when explaining architectural trade-offs.';
  const id = successful(await run([
    'add', '-cwd', repo, '-source', 'user', '-kind', 'rule', '-scope', 'global', '--', content,
  ], true), true).trim();
  const prepared = successful(await run(['dream', '-prepare', '-all'], true), true);
  assert.match(prepared, /Answer format/);
  assert.ok(prepared.includes(content));
  const packet = prepared.match(/"packet":\s*"([^"\n]+)"/)?.[1];
  assert.ok(packet, 'the CLI must provide an answer packet identifier');
  const answer = join(dir, 'dream-answer.json');
  await writeFile(answer, JSON.stringify({ packet, splits: [], actions: [], scopes: [] }), { mode: 0o600 });
  assert.equal((await stat(answer)).mode & 0o777, 0o600);
  const report = successful(await run(['dream', '-apply', answer], true), true);
  assert.match(report, /dream: 0 split, 0 retired or corrected/);
  const search = JSON.parse(successful(await run([
    'search', '-json', '-cwd', repo, '-session', 'pi-live-dream', '-min-score', '0', '--', 'architectural trade-offs',
  ]))) as { facts: Fact[] };
  assert.equal(search.facts.find(fact => fact.id === id)?.content, content);
  const replay = await run(['dream', '-apply', answer], true);
  assert.equal(replay.outcome, 'refused', JSON.stringify(replay));
  assert.notEqual(replay.exitCode, 0);
});
