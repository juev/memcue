/**
 * Live tests: the real memcue binary, a throwaway base and a throwaway repository. No model and no OpenCode binary
 * are involved; MEMCUE_TEST_BINARY (or MEMCUE_BIN) names an absolute executable, and without it these tests skip.
 */
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { isAbsolute, join } from 'node:path';
import test, { type TestContext } from 'node:test';
import plugin from '../src/index.ts';
import { harness } from './harness.ts';

const binary = process.env.MEMCUE_TEST_BINARY || process.env.MEMCUE_BIN;
const live = { skip: !binary, timeout: 120_000 };

interface Live {
  root: string;
  repo: string;
  fixture: ReturnType<typeof harness>;
  add: (input: Record<string, unknown>) => Promise<string>;
  search: (query: string) => Promise<{ facts: Array<{ id: string; content: string; version: number }> }>;
}

async function fixture(t: TestContext): Promise<Live> {
  assert.ok(binary && isAbsolute(binary), 'MEMCUE_TEST_BINARY must name an absolute executable path');
  // A live test must never reach the memory base of the machine that runs it.
  const saved = new Map<string, string>();
  for (const name of Object.keys(process.env)) {
    if (name === 'MEMCUE_SERVER' || name === 'MEMCUE_POSTGRES_DSN' || name.startsWith('MEMCUE_TAILSCALE_')) {
      saved.set(name, process.env[name] as string);
      delete process.env[name];
    }
  }
  t.after(() => { for (const [name, value] of saved) process.env[name] = value; });

  const root = await mkdtemp(join(tmpdir(), 'memcue-opencode-live-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const repo = join(root, 'repo');
  await mkdir(repo);
  execFileSync('git', ['init', '-q', repo], { stdio: 'pipe' });
  execFileSync('git', ['-C', repo, 'remote', 'add', 'origin', 'https://example.com/test/memcue-opencode.git'],
    { stdio: 'pipe' });

  const fixture = harness({
    options: { binary, facts: join(root, 'facts'), stateDir: join(root, 'state'), host: 'opencode-live-test' },
    directory: repo,
  });
  const cleanup = await plugin.setup(fixture.ctx as never);
  t.after(() => cleanup?.());

  const call = async (name: string, input: Record<string, unknown>) => {
    const tool = fixture.tools.get(name);
    assert.ok(tool, `tool ${name} is registered`);
    const result = await tool.execute(input, { sessionID: 'live-session' });
    return (result as { content: Array<{ text: string }> }).content[0].text;
  };
  return {
    root,
    repo,
    fixture,
    add: input => call('memcue_add', input),
    search: async (query) => JSON.parse(await call('memcue_search', { query })),
  };
}

test('the real CLI stores, finds, corrects and retires a fact through the tools', live, async (t) => {
  const live = await fixture(t);
  const id = (await live.add({ text: 'Live cobalt PROJ-9001 listens on port 6432.', source: 'agent' })).trim();
  assert.match(id, /^[0-9A-Z]+$/, `the add reported ${id}`);

  const found = await live.search('cobalt PROJ-9001');
  const fact = found.facts.find(item => item.id === id);
  assert.ok(fact, 'the written fact is searchable at once');
  assert.equal(fact.content, 'Live cobalt PROJ-9001 listens on port 6432.');

  const corrected = await (async () => {
    const tool = live.fixture.tools.get('memcue_correct');
    assert.ok(tool);
    const result = await tool.execute(
      { id, text: 'Live cobalt PROJ-9001 listens on port 7432.', expectedVersion: fact.version },
      { sessionID: 'live-session' },
    );
    return (result as { content: Array<{ text: string }> }).content[0].text;
  })();
  assert.match(corrected, new RegExp(id));

  const after = await live.search('cobalt PROJ-9001');
  assert.equal(after.facts.find(item => item.id === id)?.content, 'Live cobalt PROJ-9001 listens on port 7432.');

  const retire = live.fixture.tools.get('memcue_retire');
  assert.ok(retire);
  const version = after.facts.find(item => item.id === id)?.version ?? 2;
  await retire.execute({ id, expectedVersion: version }, { sessionID: 'live-session' });
  const gone = await live.search('cobalt PROJ-9001');
  assert.equal(gone.facts.find(item => item.id === id), undefined, 'a retired fact leaves ordinary search');
});

test('the repository-pitfall snapshot comes from the real base and is refreshed after a compaction', live, async (t) => {
  const live = await fixture(t);
  await live.add({
    text: 'In this repository the proxy port is 9091, not 8080.',
    source: 'agent',
    kind: 'pitfall',
    scope: 'repo',
  });
  const first = { sessionID: 'live-session', system: [] as Array<{ type: string; text: string }> };
  await live.fixture.hooks.context?.(first);
  const instruction = first.system.find(part => part.text.includes('Long-term memory is memcue'));
  assert.ok(instruction, 'the memory instruction is part of the request');
  const snapshot = first.system.find(part => part.text.includes('repository-pitfall snapshot'));
  assert.ok(snapshot, 'a snapshot is part of the request');
  assert.match(snapshot.text, /proxy port is 9091/);

  await live.fixture.hooks.compaction?.({ sessionID: 'live-session' });
  const second = { sessionID: 'live-session', system: [] as Array<{ type: string; text: string }> };
  await live.fixture.hooks.context?.(second);
  assert.match(second.system.find(part => part.text.includes('repository-pitfall snapshot'))?.text ?? '', /9091/);
});
