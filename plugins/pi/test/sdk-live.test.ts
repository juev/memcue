import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import type { ToolCall } from '@earendil-works/pi-ai';
import { getLiveBinary, liveFixture, successful } from './live-harness.ts';
import { currentPrompt, openPi } from './pi-harness.ts';

const live = { skip: !getLiveBinary(), timeout: 60_000 };
type Pi = Awaited<ReturnType<typeof openPi>>;

async function call(pi: Pi, name: string, args: ToolCall['arguments']) {
  const id = `${name}-${pi.requests.length}`;
  pi.enqueueTools([{ type: 'toolCall', id, name, arguments: args }]);
  await pi.prompt('Execute the next synthetic memory operation.');
  const result = pi.requests.at(-1)!.messages.find(message => message.role === 'toolResult' && message.toolCallId === id);
  assert.ok(result && result.role === 'toolResult', `no native result for ${id}`);
  return { isError: result.isError,
    text: result.content.filter(part => part.type === 'text').map(part => part.text).join('\n') };
}

async function ok(pi: Pi, name: string, args: ToolCall['arguments']) {
  const result = await call(pi, name, args);
  assert.equal(result.isError, false, result.text);
  return result.text;
}

/** A literal for a regular expression, for the fact text a rendering must carry word for word. */
const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** The version the model reads in the one line of a fact (#756): `id@version scope content`. */
function versionIn(text: string, id: string): number {
  const match = new RegExp(`${id}@(\\d+) `).exec(text);
  assert.ok(match, `the rendered search carries the version of ${id}: ${text}`);
  return Number(match[1]);
}

test('SDK + CLI: native CRUD survives a fresh session and refuses stale observed versions', live, async t => {
  const fixture = await liveFixture(t);
  const first = await openPi(fixture);
  const content = 'Prefer concrete concurrency examples in design explanations.';
  const id = (await ok(first, 'memcue_add', { text: content, source: 'user', kind: 'rule', scope: 'repo' })).trim();
  await first.dispose();
  const pi = await openPi(fixture);
  t.after(() => pi.dispose());
  const found = await ok(pi, 'memcue_search', { query: 'concurrency examples', minScore: 0 });
  assert.match(found, new RegExp(`^${id}@1 (?:\\S+ )?${escape(content)}$`, 'm'),
    `the written fact is rendered with its version: ${found}`);
  const corrected = 'Prefer short concrete concurrency examples in design explanations.';
  await ok(pi, 'memcue_correct', { id, text: corrected, expectedVersion: versionIn(found, id) });
  const refused = await call(pi, 'memcue_correct', { id, text: 'A stale decision must not be saved.', expectedVersion: 1 });
  assert.equal(refused.isError, true);
  assert.match(refused.text, /refused|version|changed|expected/i);
  const durable = JSON.parse(successful(await fixture.run(['search', '-json', '-min-score', '0', '--', 'concurrency examples'])));
  assert.equal(durable.facts.find((fact: { id: string }) => fact.id === id)?.content, corrected);
  assert.equal(durable.facts.find((fact: { id: string }) => fact.id === id)?.version, 2);
  await ok(pi, 'memcue_retire', { id, expectedVersion: 2 });
  const after = JSON.parse(successful(await fixture.run(['search', '-json', '-min-score', '0', '--', 'concurrency examples'])));
  assert.equal(after.facts.some((fact: { id: string }) => fact.id === id), false);
});

test('SDK + CLI: inherited remote backends cannot redirect synthetic writes', live, async t => {
  const names = ['MEMCUE_SERVER', 'MEMCUE_POSTGRES_DSN', 'MEMCUE_TAILSCALE_TEST'];
  const saved = Object.fromEntries(names.map(name => [name, process.env[name]]));
  process.env.MEMCUE_SERVER = 'http://127.0.0.1:1/must-not-be-contacted';
  process.env.MEMCUE_POSTGRES_DSN = 'postgres://127.0.0.1:1/must-not-be-contacted';
  process.env.MEMCUE_TAILSCALE_TEST = 'must-not-leak';
  t.after(() => {
    for (const name of names) {
      if (saved[name] === undefined) delete process.env[name]; else process.env[name] = saved[name];
    }
  });
  const fixture = await liveFixture(t);
  const pi = await openPi(fixture);
  t.after(() => pi.dispose());
  const content = 'Synthetic facts must remain in the temporary local base.';
  const id = (await ok(pi, 'memcue_add', { text: content, source: 'user', kind: 'rule', scope: 'global' })).trim();
  const found = await ok(pi, 'memcue_search', { query: 'temporary local base', minScore: 0 });
  assert.match(found, new RegExp(`^${id}@1 (?:\\S+ )?${escape(content)}$`, 'm'),
    `the written fact is rendered with its version: ${found}`);
});

test('SDK + CLI: repo pitfalls refresh on the next prompt and do not leak into another repository', live, async t => {
  const fixture = await liveFixture(t);
  const pi = await openPi(fixture);
  const marker = 'LIVE-REPOSITORY-PITFALL';
  const content = `${marker}: closing a channel before senders finish panics.\nWait for every sender before closing.`;
  await ok(pi, 'memcue_add', { text: content, source: 'tool', kind: 'pitfall', scope: 'repo' });
  await pi.prompt('Read the current repository snapshot again.');
  assert.match(currentPrompt(pi.requests.at(-1)!), /LIVE-REPOSITORY-PITFALL/);
  await pi.dispose();
  const other = await openPi(fixture, { cwd: fixture.other });
  t.after(() => other.dispose());
  await other.prompt();
  assert.doesNotMatch(currentPrompt(other.requests.at(-1)!), /LIVE-REPOSITORY-PITFALL/);
});

test('SDK + CLI: current-model dream tools apply the exact packet and reject replay', live, async t => {
  const fixture = await liveFixture(t);
  const pi = await openPi(fixture);
  t.after(() => pi.dispose());
  const content = 'Prefer concrete architectural examples in explanations.';
  const id = (await ok(pi, 'memcue_add', { text: content, source: 'user', kind: 'rule', scope: 'global' })).trim();
  const prepared = await ok(pi, 'memcue_dream_prepare', { all: true });
  const packet = prepared.match(/"packet":\s*"([^"\n]+)"/)?.[1];
  assert.ok(packet, 'the actual CLI must supply the packet identifier');
  assert.ok(prepared.includes(content));
  const answer = { packet, splits: [], actions: [], scopes: [] };
  const report = await ok(pi, 'memcue_dream_apply', { answer });
  assert.match(report, /dream: 0 split, 0 retired or corrected/);
  assert.equal((await call(pi, 'memcue_dream_apply', { answer })).isError, true);
  const durable = JSON.parse(successful(await fixture.run(['search', '-json', '-min-score', '0', '--', 'architectural examples'])));
  assert.equal(durable.facts.find((fact: { id: string }) => fact.id === id)?.content, content);
});

test('SDK + CLI: a packed standalone package installs and loads via its manifest (#754)', live, async t => {
  const fixture = await liveFixture(t);
  const destination = join(fixture.root, 'installed');
  await mkdir(destination);
  const root = fileURLToPath(new URL('../', import.meta.url));
  const packed = JSON.parse(execFileSync('npm', ['pack', '--json', '--ignore-scripts', '--pack-destination', destination], {
    cwd: root, encoding: 'utf8', timeout: 30_000,
  }));
  const archive = join(destination, packed[0].filename);
  const names = execFileSync('tar', ['-tzf', archive], { encoding: 'utf8' }).trim().split('\n');
  assert.ok(names.every(name => !name.startsWith('/') && !name.split('/').includes('..')));
  // The shared transport is a published dependency since #754, and Pi's package manager installs dependencies
  // (installNpm runs npm install). Pack the transport too and install both tarballs into a clean directory, so the
  // test proves the released packaging without the developer's node_modules and without reaching the registry.
  const transportRoot = fileURLToPath(new URL('../../transport/', import.meta.url));
  const transportPacked = JSON.parse(execFileSync('npm', ['pack', '--json', '--ignore-scripts', '--pack-destination', destination], {
    cwd: transportRoot, encoding: 'utf8', timeout: 30_000,
  }));
  const transportArchive = join(destination, transportPacked[0].filename);
  execFileSync('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', archive, transportArchive],
    { cwd: destination, encoding: 'utf8', timeout: 60_000 });
  const installed = join(destination, 'node_modules', 'memcue-pi');
  // npm may satisfy the plugin's `memcue-transport@^0.37.1` range from the registry even when this checkout's
  // tarball is named on the command line (npm 11.6 on node 24 installs that copy nested), while this test exists to
  // prove this checkout's packaging: the transport tarball built here has to carry every module the plugin imports,
  // so it is unpacked where the plugin resolves the transport before the plugin is loaded.
  const transportNames = execFileSync('tar', ['-tzf', transportArchive], { encoding: 'utf8' }).trim().split('\n');
  assert.ok(transportNames.includes('package/render.mjs'), `the transport package ships render.mjs: ${transportNames}`);
  const transportDir = dirname(createRequire(join(installed, 'package.json')).resolve('memcue-transport'));
  execFileSync('tar', ['-xzf', transportArchive, '--strip-components=1', '-C', transportDir], { encoding: 'utf8' });
  const fact = 'Use a packed native extension to recall the release packaging decision.';
  const id = successful(await fixture.run(['add', '-source', 'user', '-kind', 'decision', '--', fact], true), true).trim();
  const pi = await openPi(fixture, { entry: installed });
  t.after(() => pi.dispose());
  const result = await ok(pi, 'memcue_search', { query: 'release packaging decision', minScore: 0 });
  assert.match(result, new RegExp(`^${id}@1 (?:\\S+ )?${escape(fact)}$`, 'm'), result);
  assert.deepEqual(pi.errors, []);
});
