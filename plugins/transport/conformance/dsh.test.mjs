/**
 * The DeepSeek Harness adapter of the shared transport (#754): the plugin maps the transport result to the shape
 * its tools read, and the conformance suite holds that mapping to the same contract. The plugin imports the
 * package through the memcue-dsh/transport self-reference, so the identity check proves it is the same code.
 */
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import test from 'node:test';
import { MemcueCli } from '../index.mjs';
import { fixture, invalidReceipts } from './fixture.mjs';

let runMemcue;
try {
  const probe = await import('../../deepseek-harness/test/conformance-probe.mjs');
  runMemcue = probe.runMemcue;
  assert.equal(probe.MemcueCli, MemcueCli, 'DSH must import the shared transport, not a copy');
} catch (error) {
  if (error?.code === 'ERR_MODULE_NOT_FOUND' && !process.env.MEMCUE_CONFORMANCE_REQUIRE_ADAPTERS) {
    test('dsh adapter', { skip: `dependencies are not installed: ${error.message.split('\n')[0]}` }, () => {});
    runMemcue = null;
  } else {
    throw error;
  }
}

const settingsOf = (binary, extra = {}) => ({ binary, facts: '', model: '', apiUrl: '', apiKey: '', ...extra });

if (runMemcue) {
  test('dsh: a committed mutation keeps the native diagnostics', async t => {
    const { dir, binary } = await fixture(t);
    const result = await runMemcue(settingsOf(binary), ['add', '--', 'a fact'], {});
    assert.equal(result.code, 0);
    assert.equal(result.out, '01TEST café 日本語 🧠\n');
    assert.equal(result.err, 'note: déjà vu\n');
    assert.equal(result.outcome, undefined);
  });

  test('dsh: a complete refusal is distinct from an uncertain mutation', async t => {
    const { dir, binary } = await fixture(t);
    const result = await runMemcue(settingsOf(binary), ['correct', 'refusal', '--', '01TEST', 'text'], {});
    assert.equal(result.code, 1);
    assert.equal(result.outcome, undefined);
    assert.match(result.err, /fact changed since it was read/);
  });

  for (const reason of invalidReceipts) {
    test(`dsh: an unproved mutation is unknown for ${reason} receipt and names the fact`, async t => {
      const { dir, binary } = await fixture(t);
      const id = '01J00000000000000000000000';
      const result = await runMemcue(settingsOf(binary), ['correct', 'invalid', reason, '--', id, 'text'], {});
      assert.equal(result.code, -1);
      assert.equal(result.outcome, 'unknown');
      assert.match(result.err, /UNKNOWN OUTCOME/);
      assert.ok(result.err.includes(id), 'the unknown message names the fact');
      assert.match(result.err, /history/i);
      assert.match(result.err, /not.*replay|do not.*retry/i);
    });
  }

  test('dsh: a native read error keeps its exit code and diagnostic', async t => {
    const { dir, binary } = await fixture(t);
    const result = await runMemcue(settingsOf(binary), ['read-error'], {});
    assert.equal(result.code, 7);
    assert.equal(result.out, '');
    assert.equal(result.err, 'memcue: unavailable\n');
  });

  test('dsh: a missing executable reports code -1 without an outcome', async t => {
    const { dir } = await fixture(t);
    const result = await runMemcue(settingsOf(join(dir, 'absent')), ['add', '--', 'a fact'], {});
    assert.equal(result.code, -1);
    assert.equal(result.outcome, undefined);
    assert.match(result.err, /start|ENOENT/);
  });

  test('dsh: a timeout is unknown, bounded and kills the process group', async t => {
    const { dir, binary } = await fixture(t);
    const start = Date.now();
    const result = await runMemcue(settingsOf(binary), ['add', 'wait', join(dir, 'started')], { timeoutMs: 250 });
    assert.equal(result.code, -1);
    assert.equal(result.outcome, 'unknown');
    assert.ok(Date.now() - start < 2_000);
    const pid = Number(await readFile(join(dir, 'started'), 'utf8'));
    assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
  });

  for (const stream of ['stdout', 'stderr']) {
    test(`dsh: the output budget bounds ${stream} at 4 MiB and reports unknown`, async t => {
      const { dir, binary } = await fixture(t);
      const result = await runMemcue(settingsOf(binary), ['add', 'overflow', stream], {});
      assert.equal(result.code, -1);
      assert.equal(result.outcome, 'unknown');
      assert.ok(Buffer.byteLength(result.out) <= 4 * 1024 * 1024);
    });
  }

  test('dsh: the configured model settings reach the CLI and no caller is invented', async t => {
    const { dir, binary } = await fixture(t);
    const saved = Object.fromEntries(Object.entries(process.env).filter(([key]) => key.startsWith('MEMCUE_')));
    for (const key of Object.keys(saved)) delete process.env[key];
    t.after(() => Object.assign(process.env, saved));
    const result = await runMemcue(settingsOf(binary, {
      facts: join(dir, 'facts.db'), model: 'synthetic-model', apiUrl: 'http://127.0.0.1:1', apiKey: 'synthetic-key',
    }), ['echo'], {});
    assert.equal(result.code, 0);
    const env = JSON.parse(result.out).env;
    assert.equal(env.MEMCUE_FACTS, join(dir, 'facts.db'));
    assert.equal(env.MEMCUE_MODEL, 'synthetic-model');
    assert.equal(env.MEMCUE_API_URL, 'http://127.0.0.1:1');
    assert.equal(env.MEMCUE_API_KEY, 'synthetic-key');
    assert.equal(env.MEMCUE_CALLER, undefined);
    assert.equal(env.MEMCUE_OUTCOME_NONCE, undefined, 'a read gets no nonce');
  });
}