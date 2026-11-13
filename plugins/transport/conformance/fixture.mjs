/**
 * The fake memcue binary of the transport conformance suite (#754): the fixture the Pi plugin's cli tests measured,
 * lifted here so every host adapter answers the same questions. It emits `MEMCUE_OUTCOME_V1` receipts with controlled
 * defects, overflows the output budget, waits to be killed and signals itself.
 */
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export const fixtureSource = String.raw`
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { writeFileSync } from 'node:fs';
const [command, ...rest] = process.argv.slice(2);
// The DSH adapter detects a mutation by the command name, so the mutation commands delegate to a behaviour named
// by their first argument: ['add', 'wait', path] waits, ['correct', 'invalid', kind] emits a broken receipt;
// ['add', '--', text] commits.
const behaviours = ['success', 'split-unicode', 'refusal', 'invalid', 'read-error', 'wait', 'overflow', 'signal', 'signal-with-pipe', 'echo', 'nonce'];
const mutation = ['add', 'correct', 'retire', 'reopen'].includes(command);
const named = mutation && behaviours.includes(rest[0]);
const mode = mutation ? (named ? rest[0] : 'success') : command;
const args = named ? rest.slice(1) : rest;
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

/** Every malformed-receipt variant the contract must report as an unknown mutation. */
export const invalidReceipts = ['missing', 'malformed', 'nonce', 'version', 'stdout_hash', 'stdout_bytes',
  'stderr_hash', 'stderr_bytes', 'status', 'unknown', 'refused_zero', 'success_nonzero', 'truncated'];

/** A fake memcue binary in a temporary directory, removed when the test ends. */
export async function fixture(t) {
  const dir = await mkdtemp(join(tmpdir(), 'memcue-conformance-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const binary = join(dir, 'fake-memcue.mjs');
  await writeFile(binary, `#!${process.execPath}\n${fixtureSource}`, { mode: 0o700 });
  return { dir, binary };
}

export async function waitForFile(path) {
  const deadline = Date.now() + 3_000;
  while (Date.now() < deadline) {
    try { return await readFile(path, 'utf8'); } catch { /* child has not started */ }
    await new Promise(resolve => setTimeout(resolve, 10));
  }
  throw new Error('fixture process did not start');
}
