import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import test from 'node:test';
import { SessionManager } from '@earendil-works/pi-coding-agent';
import { currentPrompt, openPi, piFixture } from './pi-harness.ts';

test('missing executable reports unavailability without breaking ordinary prompts', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture, { binary: join(fixture.root, 'missing-executable') });
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt();
  assert.deepEqual(await fixture.calls(), []);
  assert.match(currentPrompt(pi.requests.at(-1)!), /unavailable|could not start/i);
  assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/);
});

test('invalid settings fail closed before starting any executable', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture, { memcueSettings: { enabled: 'false' } });
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt();
  assert.deepEqual(await fixture.calls(), []);
  assert.equal(pi.session.getActiveToolNames().some(name => name.startsWith('memcue_')), false);
  assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /Long-term memory is available|PI-SDK-FACT/);
});

test('resumed sessions honor current settings and use the replacement cwd', async t => {
  const fixture = await piFixture();
  t.after(() => fixture.dispose());
  const sessions = join(fixture.root, 'sessions');
  const first = await openPi(fixture, { sessionManager: SessionManager.create(fixture.cwd, sessions) });
  await first.prompt();
  const file = first.session.sessionManager.getSessionFile()!;
  await first.dispose();
  const other = join(fixture.root, 'other-project');
  await mkdir(other);
  const resumed = await openPi(fixture, {
    sessionManager: SessionManager.open(file, sessions, other), cwd: other,
    sessionStartEvent: { type: 'session_start', reason: 'resume', previousSessionFile: file },
  });
  await resumed.prompt('Resume in another cwd.');
  const last = (await fixture.calls()).filter(call => call.args[0] === 'session-card').at(-1)!;
  assert.equal(JSON.parse(last.input).cwd, other);
  assert.match(currentPrompt(resumed.requests.at(-1)!), /PI-SDK-FACT/);
  await resumed.dispose();
  const count = (await fixture.calls()).length;
  const disabled = await openPi(fixture, {
    sessionManager: SessionManager.open(file, sessions), memcueSettings: { enabled: false },
    sessionStartEvent: { type: 'session_start', reason: 'resume', previousSessionFile: file },
  });
  t.after(() => disabled.dispose());
  await disabled.prompt();
  assert.equal((await fixture.calls()).length, count);
  assert.doesNotMatch(currentPrompt(disabled.requests.at(-1)!), /PI-SDK-FACT/);
});

test('persisted forks deny the main entrypoint but allow an explicitly bound worker', async t => {
  const fixture = await piFixture();
  t.after(() => fixture.dispose());
  const sessions = join(fixture.root, 'sessions');
  const parent = await openPi(fixture, { sessionManager: SessionManager.create(fixture.cwd, sessions) });
  await parent.prompt();
  const file = parent.session.sessionManager.getSessionFile()!;
  await parent.dispose();
  const count = (await fixture.calls()).length;
  const fork = await openPi(fixture, {
    sessionManager: SessionManager.forkFrom(file, fixture.cwd, sessions),
    sessionStartEvent: { type: 'session_start', reason: 'fork', previousSessionFile: file },
  });
  await fork.prompt();
  assert.equal((await fixture.calls()).length, count);
  assert.equal(fork.session.getActiveToolNames().includes('memcue_search'), false);
  assert.doesNotMatch(currentPrompt(fork.requests.at(-1)!), /PI-SDK-FACT/);
  await fork.dispose();
  const worker = await openPi(fixture, {
    sessionManager: SessionManager.forkFrom(file, fixture.cwd, sessions), agent: 'worker',
    sessionStartEvent: { type: 'session_start', reason: 'fork', previousSessionFile: file },
  });
  t.after(() => worker.dispose());
  await worker.prompt();
  assert.equal(worker.session.getActiveToolNames().includes('memcue_search'), true);
  assert.match(currentPrompt(worker.requests.at(-1)!), /PI-SDK-FACT/);
});

test('session-local off does not disable a fresh ordinary session', async t => {
  const fixture = await piFixture();
  t.after(() => fixture.dispose());
  const old = await openPi(fixture);
  await old.session.prompt('/memcue off');
  await old.prompt();
  assert.deepEqual(await fixture.calls(), []);
  await old.dispose();
  const fresh = await openPi(fixture, { sessionStartEvent: { type: 'session_start', reason: 'new' } });
  t.after(() => fresh.dispose());
  await fresh.prompt();
  assert.match(currentPrompt(fresh.requests.at(-1)!), /PI-SDK-FACT/);
});

test('native compaction refreshes the current snapshot', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture, { compactionKeepRecentTokens: 16 });
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt('Earlier discussion. '.repeat(300));
  await pi.prompt('Later discussion. '.repeat(300));
  await fixture.snapshot('POST-COMPACTION-PITFALL');
  await pi.session.compact();
  await pi.prompt('Continue after compaction.');
  const last = (await fixture.calls()).filter(call => call.args[0] === 'session-card').at(-1)!;
  assert.equal(JSON.parse(last.input).source, 'compact');
  assert.match(currentPrompt(pi.requests.at(-1)!), /POST-COMPACTION-PITFALL/);
  assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/);
});
