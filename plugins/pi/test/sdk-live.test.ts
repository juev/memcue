import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
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

test('SDK + CLI: native CRUD survives a fresh session and refuses stale observed versions', live, async t => {
  const fixture = await liveFixture(t);
  const first = await openPi(fixture);
  const content = 'Prefer concrete concurrency examples in design explanations.';
  const id = (await ok(first, 'memcue_add', { text: content, source: 'user', kind: 'rule', scope: 'repo' })).trim();
  await first.dispose();
  const pi = await openPi(fixture);
  t.after(() => pi.dispose());
  const found = JSON.parse(await ok(pi, 'memcue_search', { query: 'concurrency examples', minScore: 0 }));
  assert.equal(found.facts.find((fact: { id: string }) => fact.id === id)?.version, 1);
  const corrected = 'Prefer short concrete concurrency examples in design explanations.';
  await ok(pi, 'memcue_correct', { id, text: corrected, expectedVersion: 1 });
  const refused = await call(pi, 'memcue_correct', { id, text: 'A stale decision must not be saved.', expectedVersion: 1 });
  assert.equal(refused.isError, true);
  assert.match(refused.text, /refused|version|changed|expected/i);
  const durable = JSON.parse(successful(await fixture.run(['search', '-json', '-min-score', '0', '--', 'concurrency examples'])));
  assert.equal(durable.facts.find((fact: { id: string }) => fact.id === id)?.content, corrected);
  assert.equal(durable.facts.find((fact: { id: string }) => fact.id === id)?.version, 2);
  await ok(pi, 'memcue_retire', { id, expectedVersion: 2, reason: 'The synthetic rule was superseded.' });
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
  const found = JSON.parse(await ok(pi, 'memcue_search', { query: 'temporary local base', minScore: 0 }));
  assert.equal(found.facts.find((fact: { id: string }) => fact.id === id)?.content, content);
});

test('SDK + CLI: external pitfall writes refresh on the next prompt and stay repository-scoped', live, async t => {
  const fixture = await liveFixture(t);
  const pi = await openPi(fixture);
  await pi.prompt('Load the initial empty snapshot.');
  const marker = 'LIVE-REPOSITORY-PITFALL';
  const content = `${marker}: closing a channel before senders finish panics.\nWait for every sender before closing.`;
  const id = successful(await fixture.run([
    'add', '-cwd', fixture.repo, '-source', 'tool', '-kind', 'pitfall', '-scope', 'repo', '--', content,
  ], true), true).trim();
  await pi.prompt('Read the externally added pitfall.');
  assert.match(currentPrompt(pi.requests.at(-1)!), /LIVE-REPOSITORY-PITFALL/);
  successful(await fixture.run(['retire', '-expected-version', '1', '-reason', 'The synthetic pitfall was resolved.', '--', id], true), true);
  await pi.prompt('Read the externally retired pitfall.');
  assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /LIVE-REPOSITORY-PITFALL/);
  await pi.dispose();
  const other = await openPi(fixture, { cwd: fixture.other });
  t.after(() => other.dispose());
  await other.prompt();
  assert.doesNotMatch(currentPrompt(other.requests.at(-1)!), /LIVE-REPOSITORY-PITFALL/);
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
  for (const module of ['index.mjs', 'transport.mjs', 'install.mjs']) {
    assert.ok(names.includes(`package/vendor/transport/${module}`), `the Pi package ships ${module}`);
  }
  execFileSync('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', archive],
    { cwd: destination, encoding: 'utf8', timeout: 60_000 });
  const installed = join(destination, 'node_modules', 'memcue-pi');
  const fact = 'Use a packed native extension to recall the release packaging decision.';
  const id = successful(await fixture.run(['add', '-source', 'user', '-kind', 'decision', '--', fact], true), true).trim();
  const pi = await openPi(fixture, { entry: installed });
  t.after(() => pi.dispose());
  const result = JSON.parse(await ok(pi, 'memcue_search', { query: 'release packaging decision', minScore: 0 }));
  assert.equal(result.facts.find((value: { id: string }) => value.id === id)?.content, fact);
  assert.deepEqual(pi.errors, []);
});
