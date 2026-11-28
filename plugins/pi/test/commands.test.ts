import assert from 'node:assert/strict';
import { access, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import test from 'node:test';
import { SessionManager } from '@earendil-works/pi-coding-agent';
import { currentPrompt, openPi, piFixture, requestText, waitForCliCall } from './pi-harness.ts';

const mutation = [{ type: 'toolCall' as const, id: 'delayed-add', name: 'memcue_add',
  arguments: { text: 'A synthetic durable preference.', source: 'user', kind: 'rule', scope: 'repo' } }];

test('off survives a native reload until on explicitly enables this session again', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt();
  await pi.session.prompt('/memcue off');
  const calls = await fixture.calls();
  await pi.session.reload();
  assert.equal(pi.session.getActiveToolNames().includes('memcue_search'), false);
  await pi.prompt('Stay disabled after reload.');
  assert.deepEqual(await fixture.calls(), calls);
  assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT|<memcue>/);
  await pi.session.prompt('/memcue on');
  await pi.session.reload();
  await pi.prompt('Remain explicitly enabled across another reload.');
  assert.equal(pi.session.getActiveToolNames().includes('memcue_search'), true);
  assert.match(requestText(pi.requests.at(-1)!), /PI-SDK-FACT/);
  assert.doesNotMatch(requestText(pi.requests.at(-1)!), /memcue-session-state/,
    'control metadata must not enter model context');
});

test('resuming resets an earlier off choice according to settings, including a later reload', async t => {
  const fixture = await piFixture();
  t.after(() => fixture.dispose());
  const sessions = join(fixture.root, 'sessions');
  const first = await openPi(fixture, { sessionManager: SessionManager.create(fixture.cwd, sessions) });
  await first.prompt();
  await first.session.prompt('/memcue off');
  const file = first.session.sessionManager.getSessionFile()!;
  await first.dispose();
  const resumed = await openPi(fixture, {
    sessionManager: SessionManager.open(file, sessions),
    sessionStartEvent: { type: 'session_start', reason: 'resume', previousSessionFile: file },
  });
  t.after(() => resumed.dispose());
  await resumed.session.reload();
  await resumed.prompt();
  assert.equal(resumed.session.getActiveToolNames().includes('memcue_search'), true);
  assert.match(requestText(resumed.requests.at(-1)!), /PI-SDK-FACT/);
});

test('on during an active mutation does not disconnect its later off cancellation', async t => {
  const fixture = await piFixture({ mutationDelayMs: 5_000 });
  const pi = await openPi(fixture, { tools: mutation });
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  const pending = pi.session.prompt('Start a delayed synthetic mutation.');
  await waitForCliCall(fixture, 'add');
  await pi.session.prompt('/memcue on');
  await pi.session.prompt('/memcue off');
  await pending;
  await assert.rejects(access(fixture.mutationFile), { code: 'ENOENT' }, 'off must stop the child before its durable effect');
  assert.equal((await fixture.calls()).filter(call => call.args[0] === 'add').length, 1, 'never replay the mutation');
  const result = pi.requests.at(-1)!.messages.find(message => message.role === 'toolResult' && message.toolCallId === 'delayed-add');
  assert.ok(result && result.role === 'toolResult' && result.isError);
  assert.match(requestText([result]), /unknown|UNKNOWN OUTCOME/);
  assert.equal(pi.session.getActiveToolNames().includes('memcue_add'), false);
  assert.deepEqual(pi.errors, []);
});

test('on alone is idempotent and does not cancel an already enabled mutation', async t => {
  const fixture = await piFixture({ mutationDelayMs: 200 });
  const pi = await openPi(fixture, { tools: mutation });
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  const pending = pi.session.prompt('Run a synthetic mutation while already enabled.');
  await waitForCliCall(fixture, 'add');
  await pi.session.prompt('/memcue on');
  await pending;
  assert.equal((await readFile(fixture.mutationFile, 'utf8')).trim().split('\n').length, 1);
  const result = pi.requests.at(-1)!.messages.find(message => message.role === 'toolResult' && message.toolCallId === 'delayed-add');
  assert.ok(result && result.role === 'toolResult');
  assert.equal(result.isError, false);
});
