import assert from 'node:assert/strict';
import { access, readFile, stat } from 'node:fs/promises';
import { test } from 'node:test';
import type { ExtensionToolContext } from '@earendil-works/pi-coding-agent';
import { Check } from 'typebox/value';
import { memoryTools, type RunMemcue } from '../src/tools.ts';

const ctx = {
  cwd: '/repo with spaces',
  sessionManager: { getSessionId: () => 'session-123' },
} as unknown as ExtensionToolContext;
const read = (stdout = '{"facts":[]}') => ({ stdout, stderr: '', exitCode: 0, outcome: 'read' as const });
const committed = (stdout = 'fact-1\n') => ({ stdout, stderr: '', exitCode: 0, outcome: 'committed' as const });
const allowed = async () => {};

function tool(name: string, run: RunMemcue, authorize = allowed) {
  const found = memoryTools(run, authorize).find((item) => item.name === name);
  assert.ok(found);
  return found;
}

test('search passes exact argv and preserves facts, hints and widening', async () => {
  const answer = {
    facts: [{ id: 'fact-1', version: 3, content: 'Привет', created: '2026-11-11', origin: 'docs', scope: 'global' }],
    hints: ['Check the research shelf'], widened: true,
  };
  const signal = new AbortController().signal;
  const search = tool('memcue_search', async (args, options) => {
    assert.deepEqual(args, ['search', '-json', '-n', '7', '-cwd', ctx.cwd, '-session', 'session-123',
      '-in', 'shelf:research', '-kind', 'rule,decision', '-since', '2026-11-01', '-min-score', '0',
      '-expired', '-all-words', '--', '-query; $(no shell)']);
    assert.equal(options.cwd, ctx.cwd);
    assert.equal(options.signal, signal);
    assert.equal(options.mutation, undefined);
    return read(JSON.stringify(answer));
  });
  const result = await search.execute('call', {
    query: '-query; $(no shell)', limit: 7, in: 'shelf:research', kind: 'rule,decision', since: '2026-11-01',
    minScore: 0, expired: true, allWords: true,
  }, signal, undefined, ctx);
  assert.deepEqual(JSON.parse((result.content[0] as { text: string }).text), answer);
});

test('search defaults to five and rejects malformed native output', async () => {
  await tool('memcue_search', async (args) => {
    assert.deepEqual(args, ['search', '-json', '-n', '5', '-cwd', ctx.cwd, '-session', 'session-123', '--', 'cache']);
    return read();
  }).execute('call', { query: 'cache' }, undefined, undefined, ctx);
  for (const bad of ['not JSON', '{}', '{"facts":null}', '{"facts":[{}]}']) {
    await assert.rejects(tool('memcue_search', async () => read(bad)).execute('call', { query: 'cache' },
      undefined, undefined, ctx), /search.*invalid|search.*malformed/i);
  }
});

test('add is a mutation, preserves literal text and native diagnostics', async () => {
  const add = tool('memcue_add', async (args, options) => {
    assert.deepEqual(args, ['add', '-cwd', ctx.cwd, '-source', 'tool', '-kind', 'pitfall', '-scope', 'repo',
      '-origin', 'a named source', '-expires', '2026-12-01', '--', '--literal "text"']);
    assert.equal(options.mutation, true);
    return { ...committed('fact-2\n'), stderr: 'native warning' };
  });
  assert.equal(add.executionMode, 'sequential');
  const result = await add.execute('call', { text: '--literal "text"', source: 'tool', kind: 'pitfall', scope: 'repo',
    origin: 'a named source', expires: '2026-12-01' }, undefined, undefined, ctx);
  assert.match((result.content[0] as { text: string }).text, /fact-2/);
  assert.match((result.content[0] as { text: string }).text, /native warning/);
});

test('conflicting metadata and dream ranges fail without invoking the CLI', async () => {
  const never: RunMemcue = async () => { assert.fail('CLI must not run'); };
  for (const [name, args] of [
    ['memcue_add', { text: 'fact', source: 'agent', scope: 'repo', shelf: 'research' }],
    ['memcue_correct', { id: 'id', text: 'fact', expectedVersion: 2, expires: '2027-01-01', noExpires: true }],
    ['memcue_dream_prepare', { all: true, since: '2026-01-01' }],
  ] as const) {
    await assert.rejects(tool(name, never).execute('call', args, undefined, undefined, ctx), /cannot|exclusive/i);
  }
});

test('corrections and retirements carry the caller-observed version', async () => {
  await tool('memcue_correct', async (args, options) => {
    assert.deepEqual(args, ['correct', '-expected-version', '6', '--', '-fact-id', 'Corrected fact.']);
    assert.equal(options.mutation, true);
    return committed('id v7\n');
  }).execute('call', { id: '-fact-id', text: 'Corrected fact.', expectedVersion: 6 }, undefined, undefined, ctx);
  await tool('memcue_correct', async (args) => {
    assert.deepEqual(args, ['correct', '-expected-version', '6', '-no-expires', '--', 'id', 'Fact.']);
    return committed();
  }).execute('call', { id: 'id', text: 'Fact.', expectedVersion: 6, noExpires: true }, undefined, undefined, ctx);
  await tool('memcue_retire', async (args, options) => {
    assert.deepEqual(args, ['retire', '-expected-version', '2', '--', 'id']);
    assert.equal(options.mutation, true);
    return committed('id v3 retired\n');
  }).execute('call', { id: 'id', expectedVersion: 2 }, undefined, undefined, ctx);
});

test('every tool checks policy before any CLI invocation', async () => {
  const deny = async () => { throw new Error('memory disabled'); };
  const never: RunMemcue = async () => { assert.fail('disabled tool invoked CLI'); };
  for (const item of memoryTools(never, deny)) {
    await assert.rejects(item.execute('call', {}, undefined, undefined, ctx), /memory disabled/);
  }
});

test('dream prepare returns the full packet and is treated as a mutation', async () => {
  const packet = '# Full instructions\n\n{"packet":"p-123"}\n';
  const result = await tool('memcue_dream_prepare', async (args, options) => {
    assert.deepEqual(args, ['dream', '-prepare', '-all']);
    assert.equal(options.mutation, true);
    assert.equal(options.timeoutMs, 60_000);
    return committed(packet);
  }).execute('call', { all: true }, undefined, undefined, ctx);
  assert.match((result.content[0] as { text: string }).text, /# Full instructions/);
});

test('dream apply creates a private temporary answer and removes it on every outcome', async () => {
  const answer = { packet: 'p-123', splits: [], actions: [], scopes: [] };
  for (const fail of [false, true]) {
    let filename = '';
    const apply = tool('memcue_dream_apply', async (args, options) => {
      assert.deepEqual(args.slice(0, 2), ['dream', '-apply']);
      filename = args[2];
      assert.deepEqual(JSON.parse(await readFile(filename, 'utf8')), answer);
      assert.equal((await stat(filename)).mode & 0o777, 0o600);
      assert.equal(options.mutation, true);
      assert.equal(options.timeoutMs, 60_000);
      if (fail) throw new Error('transport failure');
      return committed('0 changed, 1 refused\n');
    });
    const result = apply.execute('call', { answer }, undefined, undefined, ctx);
    if (fail) await assert.rejects(result, /transport failure/);
    else assert.match(((await result).content[0] as { text: string }).text, /1 refused/);
    assert.notEqual(filename, '');
    await assert.rejects(access(filename), { code: 'ENOENT' });
  }
});

test('native refusal and uncertain writes are errors and never retried', async () => {
  for (const outcome of ['refused', 'unknown', 'not-started'] as const) {
    let calls = 0;
    const add = tool('memcue_add', async () => {
      calls++;
      return { stdout: '', stderr: 'native diagnostic', exitCode: 1, outcome };
    });
    await assert.rejects(add.execute('call', { text: 'A fact', source: 'agent' }, undefined, undefined, ctx),
      (error: Error) => error.message.includes(outcome) && error.message.includes('native diagnostic') &&
        (outcome !== 'unknown' || /never.*retry|do not.*retry/i.test(error.message)));
    assert.equal(calls, 1);
  }
});

test('schemas reject unsafe shapes and require observed versions', () => {
  const noop: RunMemcue = async () => read();
  assert.equal(Check(tool('memcue_search', noop).parameters, { query: 'q', limit: 0 }), false);
  assert.equal(Check(tool('memcue_correct', noop).parameters, { id: 'id', text: 'fact' }), false);
  assert.equal(Check(tool('memcue_retire', noop).parameters, { id: 'id', expectedVersion: 0 }), false);
  assert.equal(Check(tool('memcue_add', noop).parameters, { text: 'fact', source: 'agent', force: true }), false);
  assert.equal(Check(tool('memcue_dream_apply', noop).parameters, {
    answer: { packet: 'p', splits: [], actions: [], scopes: [], translations: [] },
  }), false);
  assert.equal(Check(tool('memcue_dream_apply', noop).parameters, {
    answer: { packet: 'p', splits: [], actions: [{ op: 'retire', id: 'id', kind: 'duplicate', by: 'another', reason: 'same fact' }], scopes: [] },
  }), true);
});
