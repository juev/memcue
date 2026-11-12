import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test, { type TestContext } from 'node:test';
import { MemcueCli } from '../src/cli.ts';

const fixtureSource = String.raw`
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { writeFileSync } from 'node:fs';
const [mode, ...args] = process.argv.slice(2);
const hash = value => createHash('sha256').update(value).digest('hex');
const receipt = (out, err, status = 'success', changes = {}) => ({
  version: 1, nonce: process.env.MEMCUE_OUTCOME_NONCE,
  status, bytes: Buffer.byteLength(out), sha256: hash(out),
  error_bytes: Buffer.byteLength(err), error_sha256: hash(err), ...changes,
});
const emit = (out, err, status = 'success', changes = {}, suffix = '\n') => {
  process.stdout.write(out);
  process.stderr.write(err + '\nMEMCUE_OUTCOME_V1 ' + JSON.stringify(receipt(out, err, status, changes)) + suffix);
};
if (mode === 'echo' || mode === 'nonce') {
  let stdin = '';
  for await (const chunk of process.stdin) stdin += chunk;
  const out = JSON.stringify({ args, stdin, cwd: process.cwd(), env: Object.fromEntries(Object.entries(process.env).filter(([k]) => k.startsWith('MEMCUE_'))) });
  if (mode === 'nonce') emit(out, ''); else process.stdout.write(out);
} else if (mode === 'success') {
  emit('01TEST café 日本語 🧠\n', 'note: déjà vu\n');
} else if (mode === 'split-unicode') {
  const out = 'café 🧠\n';
  const bytes = Buffer.from(out);
  process.stdout.write(bytes.subarray(0, 4));
  setTimeout(() => {
    process.stdout.write(bytes.subarray(4));
    process.stderr.write('\nMEMCUE_OUTCOME_V1 ' + JSON.stringify(receipt(out, '')) + '\n');
  }, 20);
} else if (mode === 'refusal') {
  emit('', 'memcue: fact changed since it was read\n', 'refused');
  process.exitCode = 1;
} else if (mode === 'invalid') {
  const kind = args[0];
  const out = '01TEST\n';
  const err = 'diagnostic\n';
  const changes = {
    nonce: { nonce: '0'.repeat(32) }, version: { version: 2 },
    stdout_hash: { sha256: '0'.repeat(64) }, stdout_bytes: { bytes: 1 },
    stderr_hash: { error_sha256: '0'.repeat(64) }, stderr_bytes: { error_bytes: 1 },
    status: { status: 'unexpected' }, unknown: { status: 'unknown' },
    refused_zero: { status: 'refused' },
  }[kind] || {};
  if (kind === 'missing') process.stdout.write(out);
  else if (kind === 'malformed') process.stderr.write('\nMEMCUE_OUTCOME_V1 {broken}\n');
  else emit(out, err, 'success', changes, kind === 'truncated' ? '' : '\n');
  if (kind === 'success_nonzero') process.exitCode = 1;
} else if (mode === 'read-error') {
  process.stderr.write('memcue: unavailable\n');
  process.exitCode = 7;
} else if (mode === 'wait') {
  writeFileSync(args[0], String(process.pid));
  setInterval(() => {}, 1000);
} else if (mode === 'overflow') {
  process[args[0]].write(Buffer.alloc(4 * 1024 * 1024 + 1, 65));
  setInterval(() => {}, 1000);
} else if (mode === 'signal') {
  process.kill(process.pid, 'SIGKILL');
} else if (mode === 'signal-with-pipe') {
  const child = spawn(process.execPath, ['-e', 'setTimeout(() => {}, 1500)'], { stdio: 'inherit' });
  writeFileSync(args[0], String(child.pid));
  child.unref();
  process.kill(process.pid, 'SIGKILL');
}
`;

async function fixture(t: TestContext) {
  const dir = await mkdtemp(join(tmpdir(), 'memcue-pi-cli-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const binary = join(dir, 'fake-memcue.mjs');
  await writeFile(binary, `#!${process.execPath}\n${fixtureSource}`, { mode: 0o700 });
  return { dir, binary, cli: new MemcueCli({ binary }) };
}

async function waitForFile(path: string) {
  const deadline = Date.now() + 3_000;
  while (Date.now() < deadline) {
    try { return await readFile(path, 'utf8'); } catch { /* child has not started */ }
    await new Promise(resolve => setTimeout(resolve, 10));
  }
  throw new Error('fixture process did not start');
}

test('passes literal argv, stdin and cwd, inherits ambient memcue settings and lets the configured ones win', async t => {
  const { dir, binary } = await fixture(t);
  // The developer's own MEMCUE_* settings would reach the fixture too; the test sees only its own.
  const saved = Object.fromEntries(Object.entries(process.env).filter(([key]) => key.startsWith('MEMCUE_')));
  for (const key of Object.keys(saved)) delete process.env[key];
  const ambient = ['MEMCUE_SERVER', 'MEMCUE_API_URL', 'MEMCUE_OUTCOME_NONCE', 'MEMCUE_FACTS', 'MEMCUE_DREAM_BACKGROUND'];
  for (const key of ambient) process.env[key] = 'ambient';
  t.after(() => { for (const key of ambient) delete process.env[key]; Object.assign(process.env, saved); });
  const cli = new MemcueCli({ binary, facts: join(dir, 'facts.db'), stateDir: join(dir, 'state'), host: 'laptop' });
  const args = ['--', 'a $HOME `command` $(command) ; & "quote"', '--starts-with-flag'];
  const result = await cli.run(['echo', ...args], { cwd: dir, stdin: '{"cwd":"example"}\n' });
  assert.equal(result.outcome, 'read');
  assert.equal(result.exitCode, 0);
  assert.equal(result.stderr, '');
  assert.deepEqual(JSON.parse(result.stdout), {
    args, stdin: '{"cwd":"example"}\n', cwd: dir,
    env: { MEMCUE_SERVER: 'ambient', MEMCUE_API_URL: 'ambient',
      MEMCUE_FACTS: join(dir, 'facts.db'), MEMCUE_STATE_DIR: join(dir, 'state'), MEMCUE_HOST: 'laptop', MEMCUE_CALLER: 'pi', MEMCUE_DREAM_BACKGROUND: '0' },
  });
});

test('validates receipts by UTF-8 bytes and retains native diagnostics', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['success'], { cwd: dir, mutation: true });
  assert.deepEqual(result, { stdout: '01TEST café 日本語 🧠\n', stderr: 'note: déjà vu\n', exitCode: 0, outcome: 'committed' });
});

test('decodes Unicode only after collecting split byte chunks', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['split-unicode'], { cwd: dir, mutation: true });
  assert.equal(result.outcome, 'committed');
  assert.equal(result.stdout, 'café 🧠\n');
});

test('a closed stdin does not lose an otherwise complete mutation receipt', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['success'], { cwd: dir, mutation: true, stdin: 'input'.repeat(1024 * 1024) });
  assert.equal(result.outcome, 'committed');
});

test('a complete refusal is distinct from an uncertain mutation', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['refusal'], { cwd: dir, mutation: true });
  assert.deepEqual(result, { stdout: '', stderr: 'memcue: fact changed since it was read\n', exitCode: 1, outcome: 'refused' });
});

test('uses a fresh valid receipt nonce for each mutation', async t => {
  const { dir, cli } = await fixture(t);
  const first = await cli.run(['nonce'], { cwd: dir, mutation: true });
  const second = await cli.run(['nonce'], { cwd: dir, mutation: true });
  assert.equal(first.outcome, 'committed');
  assert.equal(second.outcome, 'committed');
  const nonce = JSON.parse(first.stdout).env.MEMCUE_OUTCOME_NONCE;
  assert.match(nonce, /^[a-f0-9]{32}$/);
  assert.notEqual(nonce, JSON.parse(second.stdout).env.MEMCUE_OUTCOME_NONCE);
});

for (const reason of ['missing', 'malformed', 'nonce', 'version', 'stdout_hash', 'stdout_bytes', 'stderr_hash', 'stderr_bytes', 'status', 'unknown', 'refused_zero', 'success_nonzero', 'truncated']) {
  test(`reports an unknown mutation outcome for ${reason} receipt`, async t => {
    const { dir, cli } = await fixture(t);
    const result = await cli.run(['invalid', reason], { cwd: dir, mutation: true });
    assert.equal(result.outcome, 'unknown');
    assert.match(result.stderr, /UNKNOWN OUTCOME/);
    assert.match(result.stderr, /history/i);
    assert.match(result.stderr, /not.*replay|do not.*retry/i);
  });
}

test('does not turn a native read error into an empty successful result', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['read-error'], { cwd: dir });
  assert.deepEqual(result, { stdout: '', stderr: 'memcue: unavailable\n', exitCode: 7, outcome: 'read' });
});

test('missing executable is known not to have started', async t => {
  const { dir } = await fixture(t);
  const result = await new MemcueCli({ binary: join(dir, 'absent') }).run(['add'], { cwd: dir, mutation: true });
  assert.equal(result.outcome, 'not-started');
  assert.equal(result.exitCode, null);
  assert.match(result.stderr, /start|ENOENT/);
});

test('already aborted signal does not spawn the executable', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, signal: AbortSignal.abort() });
  assert.equal(result.outcome, 'not-started');
  await assert.rejects(readFile(join(dir, 'started')), { code: 'ENOENT' });
});

test('immediate cancellation after OS launch does not claim the process never started', async t => {
  const { dir, cli } = await fixture(t);
  const controller = new AbortController();
  const pending = cli.run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, signal: controller.signal });
  controller.abort();
  assert.equal((await pending).outcome, 'unknown');
});

test('rejects timer values that Node would silently turn into one millisecond', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, timeoutMs: 2 ** 31 });
  assert.equal(result.outcome, 'not-started');
  assert.match(result.stderr, /timeoutMs/);
});

test('cancellation after process start is unknown and stops the process', async t => {
  const { dir, cli } = await fixture(t);
  const controller = new AbortController();
  const pending = cli.run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, signal: controller.signal });
  const pid = Number(await waitForFile(join(dir, 'started')));
  controller.abort();
  const result = await pending;
  assert.equal(result.outcome, 'unknown');
  assert.match(result.stderr, /cancel|abort/i);
  assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
});

test('timeout bounds the call and does not retry a mutation', async t => {
  const { dir, cli } = await fixture(t);
  const start = Date.now();
  const result = await cli.run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, timeoutMs: 250 });
  assert.equal(result.outcome, 'unknown');
  assert.match(result.stderr, /timed out/i);
  assert.ok(Date.now() - start < 2_000);
  const pid = Number(await readFile(join(dir, 'started'), 'utf8'));
  assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
});

for (const stream of ['stdout', 'stderr']) {
  test(`bounds ${stream} at 4 MiB and reports unknown mutation outcome`, async t => {
    const { dir, cli } = await fixture(t);
    const result = await cli.run(['overflow', stream], { cwd: dir, mutation: true });
    assert.equal(result.outcome, 'unknown');
    assert.match(result.stderr, /output.*limit/i);
    assert.ok(Buffer.byteLength(result.stdout) <= 4 * 1024 * 1024);
    assert.ok(Buffer.byteLength(result.stderr) < 4 * 1024 * 1024 + 1_024);
  });
}

test('a process signal without a receipt cannot prove a mutation was refused', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['signal'], { cwd: dir, mutation: true });
  assert.equal(result.outcome, 'unknown');
  assert.equal(result.exitCode, null);
});

test('signaled reads carry an actionable transport diagnostic', async t => {
  const { dir, cli } = await fixture(t);
  const result = await cli.run(['signal'], { cwd: dir });
  assert.equal(result.outcome, 'read');
  assert.equal(result.exitCode, null);
  assert.match(result.stderr, /SIGKILL/);
});

test('a prior exit signal cannot disable the deadline when another process holds a pipe', async t => {
  const { dir, cli } = await fixture(t);
  const marker = join(dir, 'descendant');
  t.after(async () => {
    try { process.kill(Number(await readFile(marker, 'utf8')), 'SIGKILL'); } catch { /* exited */ }
  });
  const start = Date.now();
  const result = await cli.run(['signal-with-pipe', marker], { cwd: dir, timeoutMs: 200 });
  assert.equal(result.exitCode, null);
  assert.match(result.stderr, /SIGKILL/);
  assert.ok(Date.now() - start < 1_000, 'transport deadline must still apply after exit');
});
