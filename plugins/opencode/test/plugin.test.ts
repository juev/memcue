import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test, { type TestContext } from 'node:test';
import plugin, { atLeast, logFile, MIN_BINARY } from '../src/index.ts';
import { NOTE } from '../src/instructions.ts';
import { fakeMemcue, harness, type Harness } from './harness.ts';

interface Calls {
  args: string[];
  cwd: string;
  nonce: string | null;
  stdin: string | null;
}

interface Fixture {
  root: string;
  calls: () => Promise<Calls[]>;
  log: () => Promise<string>;
  plugin: Harness;
}

async function setup(
  t: TestContext,
  options: Record<string, unknown> = {},
  environment: { version?: string; sessions?: Record<string, { parentID?: string; agent?: string }> } = {},
  env: Record<string, string> = {},
): Promise<Fixture> {
  const root = await mkdtemp(join(tmpdir(), 'memcue-opencode-'));
  const previous = { state: process.env.XDG_STATE_HOME, calls: process.env.FAKE_MEMCUE_CALLS, fails: process.env.FAKE_SNAPSHOT_FAIL, pitfalls: process.env.FAKE_PITFALLS };
  t.after(async () => {
    process.env.XDG_STATE_HOME = previous.state;
    if (previous.calls === undefined) delete process.env.FAKE_MEMCUE_CALLS; else process.env.FAKE_MEMCUE_CALLS = previous.calls;
    if (previous.fails === undefined) delete process.env.FAKE_SNAPSHOT_FAIL; else process.env.FAKE_SNAPSHOT_FAIL = previous.fails;
    if (previous.pitfalls === undefined) delete process.env.FAKE_PITFALLS; else process.env.FAKE_PITFALLS = previous.pitfalls;
    await rm(root, { recursive: true, force: true });
  });
  process.env.XDG_STATE_HOME = join(root, 'state');
  process.env.FAKE_MEMCUE_CALLS = join(root, 'calls.jsonl');
  for (const [name, value] of Object.entries(env)) process.env[name] = value;
  const binary = await fakeMemcue(root);
  const h = harness({
    options: { binary, facts: join(root, 'facts'), stateDir: join(root, 'state-memcue'), ...options },
    version: environment.version,
    directory: root,
    sessions: environment.sessions,
  });
  const cleanup = await plugin.setup(h.ctx as never);
  t.after(() => cleanup?.());
  return {
    root,
    calls: async () => {
      try {
        return (await readFile(process.env.FAKE_MEMCUE_CALLS ?? '', 'utf8'))
          .split('\n').filter(Boolean).map(line => JSON.parse(line) as Calls);
      } catch { return []; }
    },
    log: async () => {
      try { return await readFile(logFile(), 'utf8'); } catch { return ''; }
    },
    plugin: h,
  };
}

test('setup registers the six tools and the command', async (t) => {
  const fixture = await setup(t);
  assert.deepEqual([...fixture.plugin.tools.keys()].sort(),
    ['memcue_add', 'memcue_correct', 'memcue_dream_apply', 'memcue_dream_prepare', 'memcue_retire', 'memcue_search']);
  assert.ok(fixture.plugin.commands.has('memcue'));
  assert.match(await fixture.log(), /loaded for OpenCode 2\.2\.7/);
  for (const tool of fixture.plugin.tools.values()) {
    assert.equal(typeof tool.execute, 'function');
    assert.equal(tool.input.type, 'object');
  }
});

test('an older OpenCode keeps memory off and says so in the log', async (t) => {
  const fixture = await setup(t, {}, { version: '1.19.5' });
  assert.equal(fixture.plugin.tools.size, 0);
  assert.match(await fixture.log(), /older than 2\.0\.0/);
});

test('a configuration error turns memory off instead of breaking the session', async (t) => {
  const fixture = await setup(t, { fact: '/tmp/typo' });
  assert.equal(fixture.plugin.tools.size, 0);
  assert.match(await fixture.log(), /Unknown setting: options\.fact/);
});

test('the instruction and the snapshot reach the context once per session', async (t) => {
  const fixture = await setup(t, {}, {}, { FAKE_PITFALLS: JSON.stringify(['old pitfall']) });
  const first = { sessionID: 'session-1', system: [] as Array<{ type: string; text: string }> };
  await fixture.plugin.hooks.context?.(first);
  assert.equal(first.system.length, 2);
  assert.ok(first.system[0].text.startsWith(NOTE), 'the shared instruction comes first');
  assert.match(first.system[1].text, /old pitfall/);
  assert.match(first.system[1].text, /replaces all earlier automatic snapshots/);

  const second = { sessionID: 'session-1', system: [] as Array<{ type: string; text: string }> };
  await fixture.plugin.hooks.context?.(second);
  assert.equal(second.system.length, 2);
  const snapshots = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(snapshots.length, 1);
  assert.equal(JSON.parse(snapshots[0].stdin ?? '{}').source, 'startup');
});

test('a compaction makes the next request read the snapshot again', async (t) => {
  const fixture = await setup(t);
  await fixture.plugin.hooks.context?.({ sessionID: 'session-1', system: [] });
  await fixture.plugin.hooks.compaction?.({ sessionID: 'session-1' });
  await fixture.plugin.hooks.context?.({ sessionID: 'session-1', system: [] });
  const snapshots = (await fixture.calls()).filter(call => call.args[0] === 'session-card');
  assert.equal(snapshots.length, 2);
  assert.equal(JSON.parse(snapshots[1].stdin ?? '{}').source, 'compact');
});

test('without pitfalls only the instruction is added', async (t) => {
  const fixture = await setup(t);
  const event = { sessionID: 'session-1', system: [] as Array<{ type: string; text: string }> };
  await fixture.plugin.hooks.context?.(event);
  assert.equal(event.system.length, 1);
});

test('an unavailable snapshot says so instead of reusing an older one', async (t) => {
  const fixture = await setup(t, {}, {}, { FAKE_SNAPSHOT_FAIL: '1' });
  const event = { sessionID: 'session-1', system: [] as Array<{ type: string; text: string }> };
  await fixture.plugin.hooks.context?.(event);
  assert.equal(event.system.length, 2);
  assert.match(event.system[1].text, /Unavailable: .*no fact base/);
  assert.match(event.system[1].text, /Earlier snapshots do not establish current applicability/);
  assert.match(await fixture.log(), /snapshot failed/);
});

test('/memcue off stops the instruction in this session and refuses tools', async (t) => {
  const fixture = await setup(t);
  await fixture.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'off' } });
  assert.match(fixture.plugin.notices.at(-1)?.text ?? '', /off for this session/);
  const event = { sessionID: 'session-1', system: [] as Array<{ type: string; text: string }> };
  await fixture.plugin.hooks.context?.(event);
  assert.equal(event.system.length, 0);
  await assert.rejects(
    fixture.plugin.tools.get('memcue_search')?.execute({ query: 'x' }, { sessionID: 'session-1' }) ?? Promise.resolve(),
    /off for this session/,
  );
  await fixture.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'on' } });
  assert.match(fixture.plugin.notices.at(-1)?.text ?? '', /on for this session/);
});

test('/memcue status and dream do not need the model to answer status', async (t) => {
  const fixture = await setup(t);
  await fixture.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'status' } });
  assert.match(fixture.plugin.notices.at(-1)?.text ?? '', /Memcue: on; role: main/);
  assert.equal(fixture.plugin.notices.at(-1)?.resume, false);
  await fixture.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'dream' } });
  assert.match(fixture.plugin.prompts.at(-1)?.text ?? '', /memcue_dream_prepare/);
  await fixture.plugin.commands.get('memcue')?.execute({ sessionID: 'session-1', prompt: { text: 'nonsense' } });
  assert.match(fixture.plugin.notices.at(-1)?.text ?? '', /Usage: \/memcue/);
});

test('a subagent session gets memory only when its agent role is enabled', async (t) => {
  const sessions = { child: { parentID: 'parent', agent: 'worker' } };
  const off = await setup(t, {}, { sessions });
  const quiet = { sessionID: 'child', system: [] as Array<{ type: string; text: string }> };
  await off.plugin.hooks.context?.(quiet);
  assert.equal(quiet.system.length, 0);
  await assert.rejects(off.plugin.tools.get('memcue_search')?.execute({ query: 'x' }, { sessionID: 'child' }) ?? Promise.resolve(),
    /subagent session/);

  const enabled = await setup(t, { subagents: { agents: { worker: true } } }, { sessions });
  const loud = { sessionID: 'child', system: [] as Array<{ type: string; text: string }> };
  await enabled.plugin.hooks.context?.(loud);
  assert.equal(loud.system.length, 1);
  await enabled.plugin.commands.get('memcue')?.execute({ sessionID: 'child', prompt: { text: 'status' } });
  assert.match(enabled.plugin.notices.at(-1)?.text ?? '', /role: subagent \(worker\)/);
});

test('a tool that writes goes through the CLI with the receipt and reports its id', async (t) => {
  const fixture = await setup(t);
  const result = await fixture.plugin.tools.get('memcue_add')?.execute(
    { text: 'A fact about the host', source: 'agent' },
    { sessionID: 'session-1' },
  );
  assert.match((result as { content: Array<{ text: string }> }).content[0].text, /fact-id/);
  const calls = await fixture.calls();
  const add = calls.find(call => call.args[0] === 'add');
  assert.ok(add);
  assert.match(add.nonce ?? '', /^[0-9a-f]{32}$/);
  assert.ok(add.args.includes('--'));
  assert.deepEqual(add.args.slice(-2), ['--', 'A fact about the host']);
});

test('version comparison follows dotted numbers', () => {
  assert.equal(atLeast('2.2.7', '2.0.0'), true);
  assert.equal(atLeast('2.0.0', '2.0.0'), true);
  assert.equal(atLeast('1.19.5', '2.0.0'), false);
  assert.equal(atLeast('2.10.1', '2.9.9'), true);
  assert.equal(atLeast('2.0.0-rc.1', '2.0.0'), true);
  assert.equal(atLeast('dev', MIN_BINARY), false);
});
