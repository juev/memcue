/**
 * The TUI part reads one contract: `status` for the indicator and `notice` for the toasts (#757). These tests run
 * the server part against a mock OpenCode context and read the same RPC the TUI part calls; the TUI itself is
 * verified in a real session, which is not part of CI.
 */
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test, { type TestContext } from 'node:test';
import plugin, { logFile } from '../src/index.ts';
import { asNotice, asProblem, asStatus, indicatorText, offNotice, problemNotice, statusText } from '../src/status.ts';
import { fakeMemcue, harness, type Harness } from './harness.ts';

interface Fixture {
  root: string;
  plugin: Harness;
  status: (sessionID?: string) => Promise<Record<string, unknown>>;
  log: () => Promise<string>;
}

async function setup(
  t: TestContext,
  options: Record<string, unknown> = {},
  environment: { version?: string; sessions?: Record<string, { parentID?: string; agent?: string }> } = {},
  env: Record<string, string> = {},
  storage?: Map<string, unknown>,
): Promise<Fixture> {
  const root = await mkdtemp(join(tmpdir(), 'memcue-opencode-tui-'));
  // A real machine's settings may export MEMCUE_SERVER or MEMCUE_FACTS; the fixture names its own base instead.
  const names = ['XDG_STATE_HOME', 'FAKE_VERSION_FAIL', 'MEMCUE_SERVER', 'MEMCUE_FACTS', 'MEMCUE_STATE_DIR'];
  const previous = new Map(names.map(name => [name, process.env[name]]));
  t.after(async () => {
    for (const [name, value] of previous) {
      if (value === undefined) delete process.env[name]; else process.env[name] = value;
    }
    await rm(root, { recursive: true, force: true });
  });
  for (const name of ['MEMCUE_SERVER', 'MEMCUE_FACTS', 'MEMCUE_STATE_DIR']) delete process.env[name];
  process.env.XDG_STATE_HOME = join(root, 'state');
  for (const [name, value] of Object.entries(env)) process.env[name] = value;
  const binary = await fakeMemcue(root);
  const h = harness({
    options: { binary, facts: join(root, 'facts'), stateDir: join(root, 'state-memcue'), ...options },
    version: environment.version,
    directory: root,
    sessions: environment.sessions,
    storage,
  });
  const cleanup = await plugin.setup(h.ctx as never);
  t.after(() => cleanup?.());
  return {
    root,
    plugin: h,
    status: async (sessionID = 'session-1') => h.rpc.status(sessionID),
    log: async () => {
      try { return await readFile(logFile(), 'utf8'); } catch { return ''; }
    },
  };
}

test('the status method answers what /memcue status prints', async (t) => {
  const fixture = await setup(t);
  const status = asStatus(await fixture.status());
  assert.ok(status, 'the answer has the shape the TUI part reads');
  assert.equal(status.on, true);
  assert.equal(status.role, 'main');
  assert.equal(status.directory, fixture.root);
  assert.equal(status.base, join(fixture.root, 'facts'));
  assert.equal(status.binary, join(fixture.root, 'memcue'));
  assert.equal(status.problem, undefined);
  assert.equal(asProblem(await fixture.plugin.rpc.health()), undefined, 'a healthy plugin says nothing at startup');

  await fixture.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'status' } });
  assert.equal(fixture.plugin.notices.at(-1)?.text, statusText(status));
});

test('/memcue off is announced to the TUI part and the indicator follows it', async (t) => {
  const fixture = await setup(t);
  await fixture.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'off' } });

  const notice = asNotice(fixture.plugin.rpc.emitted.at(-1)?.data);
  assert.equal(fixture.plugin.rpc.emitted.at(-1)?.name, 'notice');
  assert.equal(notice?.sessionID, 'session-1');
  assert.equal(notice?.variant, 'info');
  assert.match(notice?.message ?? '', /off for this session/);

  const status = asStatus(await fixture.status());
  assert.equal(status?.on, false);
  assert.equal(indicatorText(status ?? assert.fail('no status')), 'memcue off');
  assert.equal(problemNotice(status ?? assert.fail('no status')), undefined);
});

test('another plugin instance answers status from the state /memcue off wrote', async (t) => {
  // OpenCode loads the plugin once per location, and the instance that answers `status` need not be the one whose
  // command ran: the flag lives in the plugin storage they share.
  const storage = new Map<string, unknown>();
  const first = await setup(t, {}, {}, {}, storage);
  const second = await setup(t, {}, {}, {}, storage);

  await first.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'off' } });
  assert.equal(asStatus(await second.status())?.on, false);

  // The tools and the instruction read the same stored flag, whichever instance serves them.
  const event = { sessionID: 'session-1', system: [] as Array<{ type: string; text: string }> };
  await second.plugin.hooks.context?.(event);
  assert.equal(event.system.length, 0, 'no instruction reaches a session another instance turned off');
  await assert.rejects(
    second.plugin.tools.get('memcue_search')?.execute({ query: 'x' }, { sessionID: 'session-1' }) ?? Promise.resolve(),
    /off for this session/,
  );

  await first.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'on' } });
  assert.equal(asStatus(await second.status())?.on, true);
  assert.equal(storage.has('session/session-1/disabled'), false);
});

test('a subagent session shows its role and stays off without the agent option', async (t) => {
  const fixture = await setup(t, {}, { sessions: { child: { parentID: 'parent', agent: 'worker' } } });
  const status = asStatus(await fixture.status('child'));
  assert.equal(status?.on, false);
  assert.equal(status?.role, 'subagent');
  assert.equal(status?.agent, 'worker');
  assert.equal(indicatorText(status ?? assert.fail('no status')), 'memcue off · subagent worker');
});

test('a configuration error is reported through status, not only the log', async (t) => {
  const fixture = await setup(t, { fact: '/tmp/typo' });
  assert.equal(fixture.plugin.tools.size, 0);
  const status = asStatus(await fixture.status());
  assert.equal(status?.on, false);
  assert.match(status?.problem ?? '', /configuration error: Unknown setting: options\.fact/);
  assert.match(problemNotice(status ?? assert.fail('no status')) ?? '', /^Memcue is off: /);
  // The TUI part asks `health` before any session exists, which is where a toast still renders.
  const health = asProblem(await fixture.plugin.rpc.health());
  assert.match(health ?? '', /configuration error: Unknown setting: options\.fact/);
  assert.equal(offNotice(health ?? ''), `Memcue is off: ${health ?? ''}`);
  assert.match(await fixture.log(), /Unknown setting/);
});

test('an older OpenCode is reported through status, not only the log', async (t) => {
  const fixture = await setup(t, {}, { version: '1.19.5' });
  const status = asStatus(await fixture.status());
  assert.equal(status?.on, false);
  assert.match(status?.problem ?? '', /OpenCode 1\.19\.5 is older than 2\.0\.0/);
  assert.equal(indicatorText(status ?? assert.fail('no status')),
    'memcue off · OpenCode 1.19.5 is older than 2.0.0');
});

test('a failed binary probe reaches status as a problem', async (t) => {
  const fixture = await setup(t, {}, {}, { FAKE_VERSION_FAIL: '1' });
  const event = { sessionID: 'session-1', system: [] as Array<{ type: string; text: string }> };
  await fixture.plugin.hooks.context?.(event);
  assert.match(event.system[1]?.text ?? '', /Unavailable: memcue .* is unavailable/);

  const status = asStatus(await fixture.status());
  assert.equal(status?.on, true, 'the session still gets memory; the binary is the problem');
  assert.match(status?.problem ?? '', /is unavailable or older than 0\.36\.1: memcue 0\.1\.0: too old/);
  assert.match(problemNotice(status ?? assert.fail('no status')) ?? '', /^Memcue: memcue /);
});

test('the indicator cuts a long reason with an explicit ellipsis', async (t) => {
  const reason = 'configuration error: ' + 'x'.repeat(120);
  const text = indicatorText({ on: false, role: 'main', directory: '/repo', base: 'default', binary: 'memcue', problem: reason });
  assert.ok(text.length < 110, text);
  assert.match(text, /…$/);
  assert.ok(text.startsWith('memcue off · configuration error: '));
});

test('an answer of an unexpected shape is dropped instead of breaking the TUI', () => {
  assert.equal(asStatus(undefined), undefined);
  assert.equal(asStatus({ on: 'yes', role: 'main', directory: '/r', base: 'b', binary: 'm' }), undefined);
  assert.equal(asStatus({ on: true, role: 'other', directory: '/r', base: 'b', binary: 'm' }), undefined);
  assert.deepEqual(asStatus({ on: true, role: 'main', directory: '/r', base: 'b', binary: 'm' }),
    { on: true, role: 'main', directory: '/r', base: 'b', binary: 'm' });
});

test('a notice payload of an unexpected shape is dropped and an unknown variant falls back to info', () => {
  assert.equal(asNotice(undefined), undefined);
  assert.equal(asNotice({ sessionID: 1, message: 'x' }), undefined);
  assert.deepEqual(asNotice({ sessionID: 's', message: 'm', variant: 'loud' }),
    { sessionID: 's', message: 'm', variant: 'info' });
  assert.deepEqual(asNotice({ sessionID: 's', message: 'm', variant: 'warning' }),
    { sessionID: 's', message: 'm', variant: 'warning' });
});
