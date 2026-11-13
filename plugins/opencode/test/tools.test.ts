import assert from 'node:assert/strict';
import { access, readFile, stat } from 'node:fs/promises';
import { test } from 'node:test';
import { memoryTools, type RunMemcue } from '../src/tools.ts';

const cwd = '/repo with spaces';
const authorized = async () => ({ cwd });
const read = (stdout = '{"facts":[]}') => ({ stdout, stderr: '', exitCode: 0, outcome: 'read' as const });
const committed = (stdout = 'fact-1\n') => ({ stdout, stderr: '', exitCode: 0, outcome: 'committed' as const });
const context = { sessionID: 'session-123' };

function tool(name: string, run: RunMemcue, authorize = authorized) {
  const found = memoryTools(run, authorize).find((item) => item.name === name);
  assert.ok(found);
  return found;
}

test('search passes exact argv and preserves facts', async () => {
  const answer = {
    facts: [{ id: 'fact-1', version: 3, content: 'Привет', created: '2026-11-11', origin: 'docs', scope: 'global' }],
    hints: ['Check the research shelf'], widened: true,
  };
  const signal = new AbortController().signal;
  const search = tool('memcue_search', async (args, options) => {
    assert.deepEqual(args, ['search', '-json', '-n', '7', '-cwd', cwd, '-session', 'session-123',
      '-in', 'shelf:research', '-kind', 'rule,decision', '-since', '2026-11-01', '-min-score', '0',
      '-expired', '-all-words', '--', '-query; $(no shell)']);
    assert.equal(options.cwd, cwd);
    assert.equal(options.signal, signal);
    assert.equal(options.mutation, undefined);
    return read(JSON.stringify(answer));
  });
  const result = await search.execute({
    query: '-query; $(no shell)', limit: 7, in: 'shelf:research', kind: 'rule,decision', since: '2026-11-01',
    minScore: 0, expired: true, allWords: true,
  }, { ...context, signal });
  assert.deepEqual(JSON.parse((result.content[0] as { text: string }).text), answer);
});

test('search defaults to five and rejects malformed native output', async () => {
  await tool('memcue_search', async (args) => {
    assert.deepEqual(args, ['search', '-json', '-n', '5', '-cwd', cwd, '-session', 'session-123', '--', 'cache']);
    return read();
  }).execute({ query: 'cache' }, context);
  for (const bad of ['not JSON', '{}', '{"facts":null}', '{"facts":[{}]}']) {
    await assert.rejects(tool('memcue_search', async () => read(bad)).execute({ query: 'cache' }, context),
      /search.*invalid|search.*malformed/i);
  }
});

test('add is a mutation, preserves literal text and native diagnostics', async () => {
  const add = tool('memcue_add', async (args, options) => {
    assert.deepEqual(args, ['add', '-cwd', cwd, '-source', 'tool', '-kind', 'pitfall', '-scope', 'repo',
      '-origin', 'a named source', '-expires', '2026-12-01', '--', '--literal "text"']);
    assert.equal(options.mutation, true);
    return { ...committed('fact-2\n'), stderr: 'native warning' };
  });
  const result = await add.execute({ text: '--literal "text"', source: 'tool', kind: 'pitfall', scope: 'repo',
    origin: 'a named source', expires: '2026-12-01' }, context);
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
    await assert.rejects(tool(name, never).execute(args, context), /cannot|exclusive/i);
  }
});

test('corrections and retirements carry the caller-observed version', async () => {
  await tool('memcue_correct', async (args, options) => {
    assert.deepEqual(args, ['correct', '-expected-version', '6', '--', '-fact-id', 'Corrected fact.']);
    assert.equal(options.mutation, true);
    return committed('id v7\n');
  }).execute({ id: '-fact-id', text: 'Corrected fact.', expectedVersion: 6 }, context);
  await tool('memcue_correct', async (args) => {
    assert.deepEqual(args, ['correct', '-expected-version', '6', '-no-expires', '--', 'id', 'Fact.']);
    return committed();
  }).execute({ id: 'id', text: 'Fact.', expectedVersion: 6, noExpires: true }, context);
  await tool('memcue_retire', async (args, options) => {
    assert.deepEqual(args, ['retire', '-expected-version', '2', '--', 'id']);
    assert.equal(options.mutation, true);
    return committed('id v3 retired\n');
  }).execute({ id: 'id', expectedVersion: 2 }, context);
});

test('every tool checks policy before any CLI invocation', async () => {
  const deny = async () => { throw new Error('memory disabled'); };
  const never: RunMemcue = async () => { assert.fail('disabled tool invoked CLI'); };
  for (const item of memoryTools(never, deny)) {
    await assert.rejects(item.execute({}, { sessionID: 'session-123' }), /memory disabled/);
  }
});

test('dream prepare returns the full packet and is treated as a mutation', async () => {
  const packet = '# Full instructions\n\n{"packet":"p-123"}\n';
  const result = await tool('memcue_dream_prepare', async (args, options) => {
    assert.deepEqual(args, ['dream', '-prepare', '-all']);
    assert.equal(options.mutation, true);
    assert.equal(options.timeoutMs, 60_000);
    return committed(packet);
  }).execute({ all: true }, context);
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
    const result = apply.execute({ answer }, context);
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
    await assert.rejects(add.execute({ text: 'A fact', source: 'agent' }, context),
      (error: Error) => error.message.includes(outcome) && error.message.includes('native diagnostic') &&
        (outcome !== 'unknown' || /never.*retry|do not.*retry/i.test(error.message)));
    assert.equal(calls, 1);
  }
});

test('schemas carry the native constraints the model must respect', () => {
  const noop: RunMemcue = async () => read();
  const schema = (name: string) => tool(name, noop).input as {
    required?: string[]; additionalProperties?: boolean;
    properties: Record<string, { enum?: string[]; minimum?: number; minItems?: number; type?: string }>;
  };
  assert.deepEqual(schema('memcue_search').required, ['query']);
  assert.equal(schema('memcue_search').additionalProperties, false);
  assert.equal(schema('memcue_search').properties.limit.minimum, 1);
  assert.deepEqual(schema('memcue_add').required, ['text', 'source']);
  assert.deepEqual(schema('memcue_add').properties.source.enum, ['user', 'user_confirmed', 'tool', 'agent']);
  assert.deepEqual(schema('memcue_retire').required, ['id', 'expectedVersion']);
  assert.equal(schema('memcue_retire').properties.expectedVersion.minimum, 1);
  assert.deepEqual(schema('memcue_correct').required, ['id', 'text', 'expectedVersion']);
  assert.equal(schema('memcue_dream_apply').required?.includes('answer'), true);
  const answer = schema('memcue_dream_apply').properties.answer as unknown as {
    properties: Record<string, { items?: { properties: Record<string, { enum?: string[] }> } }>; required?: string[];
  };
  assert.deepEqual(answer.required, ['packet', 'splits', 'actions', 'scopes']);
  assert.deepEqual(answer.properties.actions.items?.properties.op.enum, ['retire', 'correct']);
});
