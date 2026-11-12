import assert from 'node:assert/strict';
import { join } from 'node:path';
import { test } from 'node:test';
import { currentPrompt, openPi, piFixture, PI_PACKAGE_ROOT, requestText, waitForCliCall } from './pi-harness.ts';

test('real Pi loader exposes Memcue tools and sends memory in system-prompt sections once per prompt', async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture, { tools: [{ type: 'toolCall', id: 'offline-search', name: 'memcue_search', arguments: { query: 'generated schema' } }] });
    assert.ok(pi.session.getActiveToolNames().includes('memcue_search'));
    await pi.prompt();
    assert.match(currentPrompt(pi.requests[0]!), /<memcue>\nLong-term memory is memcue/);
    assert.match(currentPrompt(pi.requests[0]!), /<memcue_pitfalls>[\s\S]*PI-SDK-FACT/);
    assert.match(requestText(pi.requests.at(-1)!), /Search result for generated schema/);
    assert.ok(pi.requests.length >= 2, 'the tool call needs a second model request');
    const calls = await fixture.calls();
    assert.equal(calls.filter(call => call.args[0] === 'session-card').length, 1,
      'the snapshot is read once per prompt, not before every model request of the tool loop');
    assert.ok(calls.some(call => call.args[0] === 'version'));
    const snapshot = calls.find(call => call.args[0] === 'session-card');
    assert.ok(snapshot);
    assert.deepEqual(snapshot.args, ['session-card', '-pitfalls-json']);
    assert.equal(JSON.parse(snapshot.input).cwd, fixture.cwd);
    assert.equal(snapshot.facts, fixture.facts);
    assert.equal(snapshot.stateDir, fixture.stateDir);
    assert.equal(snapshot.host, 'pi-test-host');
    assert.ok(calls.some(call => call.args[0] === 'search' && call.args.at(-1) === 'generated schema'));
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('real Pi child session disables the main entrypoint before every subprocess', async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture, { child: true });
    await pi.prompt();
    assert.equal(pi.session.getActiveToolNames().some(name => name.startsWith('memcue_')), false);
    assert.doesNotMatch(requestText(pi.requests[0]!), /PI-SDK-FACT/);
    assert.deepEqual(await fixture.calls(), [], 'disabled children must not even probe the Memcue version');
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('detached child marker disables the main entrypoint even without a parent header', async () => {
  const previous = process.env.PI_SUBAGENT_CHILD;
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    process.env.PI_SUBAGENT_CHILD = '1';
    pi = await openPi(fixture);
    await pi.prompt();
    assert.equal(pi.session.getActiveToolNames().some(name => name.startsWith('memcue_')), false);
    assert.doesNotMatch(requestText(pi.requests[0]!), /PI-SDK-FACT/);
    assert.deepEqual(await fixture.calls(), []);
  } finally {
    await pi?.dispose(); await fixture.dispose();
    if (previous === undefined) delete process.env.PI_SUBAGENT_CHILD; else process.env.PI_SUBAGENT_CHILD = previous;
  }
});

test('explicit worker child entrypoint enables its allowlisted role in a real Pi child', async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture, { child: true, entry: join(PI_PACKAGE_ROOT, 'children/worker.ts') });
    await pi.prompt();
    assert.ok(pi.session.getActiveToolNames().includes('memcue_search'));
    assert.match(requestText(pi.requests[0]!), /PI-SDK-FACT/);
    assert.ok((await fixture.calls()).some(call => call.args[0] === 'session-card'));
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('a detached runner can load ambient main and explicit worker entrypoints without duplicate tools', async () => {
  const previous = process.env.PI_SUBAGENT_CHILD;
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    process.env.PI_SUBAGENT_CHILD = '1';
    pi = await openPi(fixture, { child: true,
      entries: [join(PI_PACKAGE_ROOT, 'index.ts'), join(PI_PACKAGE_ROOT, 'children/worker.ts')] });
    await pi.prompt();
    assert.ok(pi.session.getActiveToolNames().includes('memcue_search'));
    assert.match(requestText(pi.requests[0]!), /PI-SDK-FACT/);
    assert.equal((await fixture.calls()).filter(call => call.args[0] === 'session-card').length, 1);
  } finally {
    await pi?.dispose(); await fixture.dispose();
    if (previous === undefined) delete process.env.PI_SUBAGENT_CHILD; else process.env.PI_SUBAGENT_CHILD = previous;
  }
});

test('explicitly denied reviewer cannot activate memory through a child entrypoint', async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture, { child: true, agent: 'maintainability-reviewer',
      tools: [{ type: 'toolCall', id: 'forbidden-memory-read', name: 'memcue_search', arguments: { query: 'parent decisions' } }] });
    await pi.prompt();
    assert.equal(pi.session.getActiveToolNames().some(name => name.startsWith('memcue_')), false);
    assert.doesNotMatch(requestText(pi.requests[0]!), /PI-SDK-FACT/);
    await pi.session.prompt('/memcue on');
    await pi.prompt('Review again without prior memory.');
    assert.equal(pi.session.getActiveToolNames().some(name => name.startsWith('memcue_')), false);
    assert.deepEqual(await fixture.calls(), []);
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('a disabled child in the same process cannot disable its live parent memory', async () => {
  const fixture = await piFixture();
  let parent: Awaited<ReturnType<typeof openPi>> | undefined;
  let child: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    parent = await openPi(fixture);
    await parent.prompt('First parent request.');
    const callsBeforeChild = await fixture.calls();
    child = await openPi(fixture, { child: true });
    await child.prompt();
    assert.deepEqual(await fixture.calls(), callsBeforeChild, 'the child must remain inert while its parent stays enabled');
    assert.doesNotMatch(requestText(child.requests[0]!), /PI-SDK-FACT/);
    await parent.prompt('Second parent request.');
    assert.ok(parent.session.getActiveToolNames().includes('memcue_search'));
    assert.match(requestText(parent.requests.at(-1)!), /PI-SDK-FACT/);
  } finally { await child?.dispose(); await parent?.dispose(); await fixture.dispose(); }
});

test('a later Pi request receives the current snapshot without retaining an earlier automatic fact', async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture);
    await pi.prompt('First request.');
    assert.match(requestText(pi.requests[0]!), /PI-SDK-FACT/);
    await fixture.snapshot('CURRENT-SNAPSHOT-ONLY: the schema issue has been repaired.');
    await pi.prompt('Second request.');
    const current = currentPrompt(pi.requests.at(-1)!);
    assert.match(current, /CURRENT-SNAPSHOT-ONLY/);
    assert.doesNotMatch(current, /PI-SDK-FACT/);
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('native memcue off and on commands remove and restore current memory access', async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture);
    await pi.prompt();
    const callsBeforeOff = await fixture.calls();
    await pi.session.prompt('/memcue off');
    await pi.prompt('Run with memory switched off.');
    assert.equal(pi.session.getActiveToolNames().some(name => name.startsWith('memcue_')), false);
    assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT|<memcue>/);
    assert.deepEqual(await fixture.calls(), callsBeforeOff);
    await pi.session.prompt('/memcue on');
    await pi.prompt('Run with memory switched on.');
    assert.ok(pi.session.getActiveToolNames().includes('memcue_search'));
    assert.match(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/);
    assert.ok((await fixture.calls()).length > callsBeforeOff.length);
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('global disable keeps an opted-in child free of tools, context and subprocesses', async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture, { child: true, agent: 'worker', enabled: false });
    await pi.prompt();
    assert.equal(pi.session.getActiveToolNames().some(name => name.startsWith('memcue_')), false);
    assert.doesNotMatch(requestText(pi.requests[0]!), /PI-SDK-FACT/);
    assert.deepEqual(await fixture.calls(), []);
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('a prompt cancelled during the version probe does not poison later Pi requests', async () => {
  const fixture = await piFixture({ versionDelayMs: 200 });
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture);
    const first = pi.session.prompt('This request will be cancelled.').catch(error => error);
    await waitForCliCall(fixture, 'version');
    await pi.session.abort();
    await first;
    assert.equal(pi.requests.length, 0, 'cancellation must occur before the first model request');
    await pi.prompt('This later request should recover without a manual reset.');
    assert.match(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/);
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('switching memory off during a snapshot prevents late memory injection', async () => {
  const fixture = await piFixture({ snapshotDelayMs: 200 });
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture);
    const pending = pi.session.prompt('Snapshot collection will be interrupted.');
    await waitForCliCall(fixture, 'session-card');
    await pi.session.prompt('/memcue off');
    await pending;
    assert.deepEqual(pi.errors, []);
    assert.ok(pi.requests.length > 0, 'the ordinary request should continue without memory');
    assert.doesNotMatch(requestText(pi.requests.at(-1)!), /PI-SDK-FACT|Current repository-pitfall snapshot|Long-term memory is memcue/);
    assert.equal(pi.session.getActiveToolNames().some(name => name.startsWith('memcue_')), false);
    const callsAfterOff = await fixture.calls();
    await pi.prompt('Remain disabled on the next request.');
    assert.deepEqual(await fixture.calls(), callsAfterOff);
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('an unchanged snapshot adds no system-prompt patch, and an empty one removes the pitfall section', async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture);
    await pi.prompt('First request.');
    await pi.prompt('Second request with the same snapshot.');
    const patches = (request: unknown) => (request as { messages: { role: string }[] }).messages
      .slice(1).filter(message => message.role === 'system').length;
    assert.equal(patches(pi.requests.at(-1)), 0, 'an unchanged section must keep the cached prompt');
    await fixture.empty();
    await pi.prompt('Third request without pitfalls.');
    const current = currentPrompt(pi.requests.at(-1)!);
    assert.doesNotMatch(current, /memcue_pitfalls|PI-SDK-FACT/);
    assert.match(current, /<memcue>/);
  } finally { await pi?.dispose(); await fixture.dispose(); }
});
