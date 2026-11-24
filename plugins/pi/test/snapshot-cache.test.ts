import assert from 'node:assert/strict';
import test from 'node:test';
import { currentPrompt, openPi, piFixture } from './pi-harness.ts';

const addCall = { type: 'toolCall' as const, id: 'add-1', name: 'memcue_add',
  arguments: { text: 'A committed fact for the fixture.', source: 'tool' } };

test('an unchanged snapshot is read once and stays for the session', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt('First request.');
  await pi.prompt('Second request.');
  await pi.prompt('Third request.');
  const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(reads.length, 1, 'a warm snapshot cache must not re-run the CLI');
  for (const request of pi.requests) assert.match(currentPrompt(request), /PI-SDK-FACT/);
});

for (const [name, args] of [
  ['memcue_add', { text: 'A committed fact for the fixture.', source: 'tool' }],
  ['memcue_correct', { id: 'pi-fixture-fact', text: 'A corrected fact.', expectedVersion: 1 }],
  ['memcue_retire', { id: 'pi-fixture-fact', expectedVersion: 1 }],
] as const) {
  test(`${name} re-reads the snapshot on the next agent start`, async t => {
    const fixture = await piFixture();
    const pi = await openPi(fixture);
    t.after(async () => { await pi.dispose(); await fixture.dispose(); });
    await pi.prompt('First request.');
    pi.enqueueTools([{ type: 'toolCall', id: `${name}-1`, name, arguments: args }]);
    await pi.prompt('Run the memory operation.');
    await pi.prompt('Next request.');
    const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
    assert.equal(reads.length, 2, `one committed ${name} invalidates the cached snapshot once`);
  });
}

test('a mutation with an unknown outcome re-reads the snapshot: it may have been written', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  // The fixture exits nonzero without an outcome receipt: the transport classifies the durable outcome as unknown
  // (the fact may already be written), the tool reports an error, and the cache must still be invalidated.
  await fixture.configure({ mutationExitCode: 1, dropReceipt: true });
  await pi.prompt('First request.');
  pi.enqueueTools([addCall]);
  await pi.prompt('Store the fact; the outcome is unknown.');
  await pi.prompt('Next request.');
  const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(reads.length, 2, 'an unknown-outcome mutation must invalidate the cached snapshot');
});

test('a search does not re-read the snapshot', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt('First request.');
  pi.enqueueTools([{ type: 'toolCall', id: 'search-1', name: 'memcue_search', arguments: { query: 'fixture' } }]);
  await pi.prompt('Search memory.');
  await pi.prompt('Next request.');
  const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(reads.length, 1, 'a read-only search leaves the snapshot cache warm');
});

test('dream apply re-reads the snapshot; dream prepare does not', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt('First request.');
  pi.enqueueTools([{ type: 'toolCall', id: 'dream-prepare', name: 'memcue_dream_prepare', arguments: { all: true } }]);
  await pi.prompt('Prepare the maintenance packet.');
  pi.enqueueTools([{ type: 'toolCall', id: 'dream-apply', name: 'memcue_dream_apply',
    arguments: { answer: { packet: 'P1', splits: [], actions: [], scopes: [] } } }]);
  await pi.prompt('Apply the empty answer.');
  await pi.prompt('Next request.');
  const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(reads.length, 2, 'prepare changes no facts; apply does');
});

for (const [name, response] of [
  ['CLI failure', { snapshotExitCode: 1 }],
  ['malformed JSON', { snapshotRaw: '{broken' }],
  ['invalid shape', { snapshot: { facts: [42], context: 'Invalid.' } }],
] as const) {
  test(`${name} keeps the last successful snapshot until a good read replaces it`, async t => {
    const fixture = await piFixture();
    const pi = await openPi(fixture);
    t.after(async () => { await pi.dispose(); await fixture.dispose(); });
    await pi.prompt('First request.');
    assert.match(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/);
    await fixture.configure(response);
    pi.enqueueTools([addCall]);
    await pi.prompt('Store the fact.');
    assert.match(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/,
      'a failed read must not replace the last good snapshot');
    await pi.prompt('Still failing.');
    assert.match(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/,
      'a persistent failure keeps serving the stable block');
    await fixture.snapshot('RECOVERED-PITFALL');
    await pi.prompt('Recovered.');
    assert.match(currentPrompt(pi.requests.at(-1)!), /RECOVERED-PITFALL/);
    await pi.prompt('Stable again.');
    assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/);
  });
}

test('a failed read after a successful empty snapshot keeps the section absent', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await fixture.empty();
  await pi.prompt('First request, no pitfalls.');
  assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /memcue_pitfalls|Unavailable/);
  await fixture.configure({ snapshotExitCode: 1 });
  pi.enqueueTools([addCall]);
  await pi.prompt('Store the fact.');
  await pi.prompt('The re-read then fails.');
  assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /memcue_pitfalls|Unavailable/,
    'a failed read after a successful empty one must not conjure a section');
  await fixture.snapshot('LATE-PITFALL');
  await pi.prompt('Recovered.');
  assert.match(currentPrompt(pi.requests.at(-1)!), /LATE-PITFALL/);
});

test('facts cleared mid-session drop the section after the next read', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt('First request.');
  assert.match(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/);
  await fixture.empty();
  pi.enqueueTools([addCall]);
  await pi.prompt('Store the fact.');
  await pi.prompt('The re-read then finds no pitfalls.');
  const current = currentPrompt(pi.requests.at(-1)!);
  assert.doesNotMatch(current, /memcue_pitfalls|PI-SDK-FACT/);
  assert.match(current, /<memcue>/);
});
