import assert from 'node:assert/strict';
import test from 'node:test';
import { currentPrompt, openPi, piFixture } from './pi-harness.ts';

test('every agent start re-reads the snapshot', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt('First request.');
  await pi.prompt('Second request.');
  await pi.prompt('Third request.');
  const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(reads.length, 3, 'each agent start must read the current snapshot');
  for (const request of pi.requests) assert.match(currentPrompt(request), /PI-SDK-FACT/);
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
    await pi.prompt('The next read fails.');
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
    const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
    assert.equal(reads.length, 5, 'failures and recovery must be observed on every agent start');
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
  await pi.prompt('The next read fails.');
  assert.doesNotMatch(currentPrompt(pi.requests.at(-1)!), /memcue_pitfalls|Unavailable/,
    'a failed read after a successful empty one must not conjure a section');
  await fixture.snapshot('LATE-PITFALL');
  await pi.prompt('Recovered.');
  assert.match(currentPrompt(pi.requests.at(-1)!), /LATE-PITFALL/);
  const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(reads.length, 3);
});

test('facts cleared externally drop the section on the next prompt', async t => {
  const fixture = await piFixture();
  const pi = await openPi(fixture);
  t.after(async () => { await pi.dispose(); await fixture.dispose(); });
  await pi.prompt('First request.');
  assert.match(currentPrompt(pi.requests.at(-1)!), /PI-SDK-FACT/);
  await fixture.empty();
  await pi.prompt('The external retirement is visible.');
  const current = currentPrompt(pi.requests.at(-1)!);
  assert.doesNotMatch(current, /memcue_pitfalls|PI-SDK-FACT/);
  assert.match(current, /<memcue>/);
  const reads = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(reads.length, 2);
});
