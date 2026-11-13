/**
 * The transport contract of #754, run against the canonical package and against the Pi and OpenCode adapters as
 * those plugins wire it. The DeepSeek Harness adapter has its own result shape and its own file; the shell and
 * Python contracts run beside this suite. A host that resolves a copy instead of the package fails the identity
 * check, and a wiring that drifts fails the cases.
 */
import assert from 'node:assert/strict';
import { readFile, realpath } from 'node:fs/promises';
import { join } from 'node:path';
import test from 'node:test';
import { MemcueCli } from '../index.mjs';
import { fixture, invalidReceipts, waitForFile } from './fixture.mjs';

/** The adapters: the canonical package, and each Node plugin's transport as the plugin configures it. */
async function adapters() {
  const list = [{
    name: 'canonical',
    create: (binary, dir) => new MemcueCli({ binary, facts: join(dir, 'facts.db'), stateDir: join(dir, 'state'), host: 'laptop' }),
    callerEnv: {},
  }];
  for (const [name, path, caller] of [
    ['pi', '../../pi/test/conformance-probe.ts', 'pi'],
    ['opencode', '../../opencode/test/conformance-probe.ts', 'opencode'],
  ]) {
    try {
      const probe = await import(path);
      assert.equal(probe.MemcueCli, MemcueCli, `${name} must import the shared transport, not a copy`);
      list.push({
        name,
        create: (binary, dir) => new probe.MemcueCli(probe.transportConfig({ facts: join(dir, 'facts.db'), stateDir: join(dir, 'state'), host: 'laptop' }, binary)),
        callerEnv: { MEMCUE_CALLER: caller, MEMCUE_DREAM_BACKGROUND: '0' },
      });
    } catch (error) {
      if (error?.code === 'ERR_MODULE_NOT_FOUND' && !process.env.MEMCUE_CONFORMANCE_REQUIRE_ADAPTERS) {
        test(`${name} adapter`, { skip: `dependencies are not installed: ${error.message.split('\n')[0]}` }, () => {});
        continue;
      }
      throw error;
    }
  }
  return list;
}

const hosts = await adapters();

for (const host of hosts) {
  test(`${host.name}: passes literal argv, stdin and cwd, inherits ambient memcue settings and lets the configured ones win`, async t => {
    const { dir, binary } = await fixture(t);
    // The developer's own MEMCUE_* settings would reach the fixture too; the test sees only its own.
    const saved = Object.fromEntries(Object.entries(process.env).filter(([key]) => key.startsWith('MEMCUE_')));
    for (const key of Object.keys(saved)) delete process.env[key];
    const ambient = ['MEMCUE_SERVER', 'MEMCUE_API_URL', 'MEMCUE_OUTCOME_NONCE', 'MEMCUE_FACTS', 'MEMCUE_DREAM_BACKGROUND'];
    for (const key of ambient) process.env[key] = 'ambient';
    t.after(() => { for (const key of ambient) delete process.env[key]; Object.assign(process.env, saved); });
    const cli = host.create(binary, dir);
    const args = ['--', 'a $HOME `command` $(command) ; & "quote"', '--starts-with-flag'];
    const result = await cli.run(['echo', ...args], { cwd: dir, stdin: '{"cwd":"example"}\n' });
    assert.equal(result.outcome, 'read');
    assert.equal(result.exitCode, 0);
    assert.equal(result.stderr, '');
    assert.deepEqual(JSON.parse(result.stdout), {
      args, stdin: '{"cwd":"example"}\n', cwd: await realpath(dir),
      env: { MEMCUE_SERVER: 'ambient', MEMCUE_API_URL: 'ambient',
        MEMCUE_FACTS: join(dir, 'facts.db'), MEMCUE_STATE_DIR: join(dir, 'state'), MEMCUE_HOST: 'laptop',
        MEMCUE_DREAM_BACKGROUND: 'ambient', ...host.callerEnv },
    });
  });

  test(`${host.name}: validates receipts by UTF-8 bytes and retains native diagnostics`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['success'], { cwd: dir, mutation: true });
    assert.deepEqual(result, { stdout: '01TEST café 日本語 🧠\n', stderr: 'note: déjà vu\n', exitCode: 0, outcome: 'committed' });
  });

  test(`${host.name}: decodes Unicode only after collecting split byte chunks`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['split-unicode'], { cwd: dir, mutation: true });
    assert.equal(result.outcome, 'committed');
    assert.equal(result.stdout, 'café 🧠\n');
  });

  test(`${host.name}: a closed stdin does not lose an otherwise complete mutation receipt`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['success'], { cwd: dir, mutation: true, stdin: 'input'.repeat(1024 * 1024) });
    assert.equal(result.outcome, 'committed');
  });

  test(`${host.name}: a complete refusal is distinct from an uncertain mutation`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['refusal'], { cwd: dir, mutation: true });
    assert.deepEqual(result, { stdout: '', stderr: 'memcue: fact changed since it was read\n', exitCode: 1, outcome: 'refused' });
  });

  test(`${host.name}: uses a fresh valid receipt nonce for each mutation`, async t => {
    const { dir, binary } = await fixture(t);
    const cli = host.create(binary, dir);
    const first = await cli.run(['nonce'], { cwd: dir, mutation: true });
    const second = await cli.run(['nonce'], { cwd: dir, mutation: true });
    assert.equal(first.outcome, 'committed');
    assert.equal(second.outcome, 'committed');
    const nonce = JSON.parse(first.stdout).env.MEMCUE_OUTCOME_NONCE;
    assert.match(nonce, /^[a-f0-9]{32}$/);
    assert.notEqual(nonce, JSON.parse(second.stdout).env.MEMCUE_OUTCOME_NONCE);
  });

  for (const reason of invalidReceipts) {
    test(`${host.name}: reports an unknown mutation outcome for ${reason} receipt`, async t => {
      const { dir, binary } = await fixture(t);
      const result = await host.create(binary, dir).run(['invalid', reason], { cwd: dir, mutation: true });
      assert.equal(result.outcome, 'unknown');
      assert.match(result.stderr, /UNKNOWN OUTCOME/);
      assert.match(result.stderr, /history/i);
      assert.match(result.stderr, /not.*replay|do not.*retry/i);
    });
  }

  test(`${host.name}: does not turn a native read error into an empty successful result`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['read-error'], { cwd: dir });
    assert.deepEqual(result, { stdout: '', stderr: 'memcue: unavailable\n', exitCode: 7, outcome: 'read' });
  });

  test(`${host.name}: missing executable is known not to have started`, async t => {
    const { dir } = await fixture(t);
    const result = await host.create(join(dir, 'absent'), dir).run(['add'], { cwd: dir, mutation: true });
    assert.equal(result.outcome, 'not-started');
    assert.equal(result.exitCode, null);
    assert.match(result.stderr, /start|ENOENT/);
  });

  test(`${host.name}: already aborted signal does not spawn the executable`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, signal: AbortSignal.abort() });
    assert.equal(result.outcome, 'not-started');
    await assert.rejects(readFile(join(dir, 'started')), { code: 'ENOENT' });
  });

  test(`${host.name}: immediate cancellation after OS launch does not claim the process never started`, async t => {
    const { dir, binary } = await fixture(t);
    const controller = new AbortController();
    const pending = host.create(binary, dir).run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, signal: controller.signal });
    controller.abort();
    assert.equal((await pending).outcome, 'unknown');
  });

  test(`${host.name}: rejects timer values that Node would silently turn into one millisecond`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, timeoutMs: 2 ** 31 });
    assert.equal(result.outcome, 'not-started');
    assert.match(result.stderr, /timeoutMs/);
  });

  test(`${host.name}: cancellation after process start is unknown and stops the process`, async t => {
    const { dir, binary } = await fixture(t);
    const controller = new AbortController();
    const pending = host.create(binary, dir).run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, signal: controller.signal });
    const pid = Number(await waitForFile(join(dir, 'started')));
    controller.abort();
    const result = await pending;
    assert.equal(result.outcome, 'unknown');
    assert.match(result.stderr, /cancel|abort/i);
    assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
  });

  test(`${host.name}: timeout bounds the call and does not retry a mutation`, async t => {
    const { dir, binary } = await fixture(t);
    const start = Date.now();
    const result = await host.create(binary, dir).run(['wait', join(dir, 'started')], { cwd: dir, mutation: true, timeoutMs: 250 });
    assert.equal(result.outcome, 'unknown');
    assert.match(result.stderr, /timed out/i);
    assert.ok(Date.now() - start < 2_000);
    const pid = Number(await readFile(join(dir, 'started'), 'utf8'));
    assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
  });

  for (const stream of ['stdout', 'stderr']) {
    test(`${host.name}: bounds ${stream} at 4 MiB and reports unknown mutation outcome`, async t => {
      const { dir, binary } = await fixture(t);
      const result = await host.create(binary, dir).run(['overflow', stream], { cwd: dir, mutation: true });
      assert.equal(result.outcome, 'unknown');
      assert.match(result.stderr, /output.*limit/i);
      assert.ok(Buffer.byteLength(result.stdout) <= 4 * 1024 * 1024);
      assert.ok(Buffer.byteLength(result.stderr) < 4 * 1024 * 1024 + 1_024);
    });
  }

  test(`${host.name}: a process signal without a receipt cannot prove a mutation was refused`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['signal'], { cwd: dir, mutation: true });
    assert.equal(result.outcome, 'unknown');
    assert.equal(result.exitCode, null);
  });

  test(`${host.name}: signaled reads carry an actionable transport diagnostic`, async t => {
    const { dir, binary } = await fixture(t);
    const result = await host.create(binary, dir).run(['signal'], { cwd: dir });
    assert.equal(result.outcome, 'read');
    assert.equal(result.exitCode, null);
    assert.match(result.stderr, /SIGKILL/);
  });

  test(`${host.name}: a prior exit signal cannot disable the deadline when another process holds a pipe`, async t => {
    const { dir, binary } = await fixture(t);
    const marker = join(dir, 'descendant');
    t.after(async () => {
      try { process.kill(Number(await readFile(marker, 'utf8')), 'SIGKILL'); } catch { /* exited */ }
    });
    const start = Date.now();
    const result = await host.create(binary, dir).run(['signal-with-pipe', marker], { cwd: dir, timeoutMs: 200 });
    assert.equal(result.exitCode, null);
    assert.match(result.stderr, /SIGKILL/);
    assert.ok(Date.now() - start < 1_000, 'transport deadline must still apply after exit');
  });
}
