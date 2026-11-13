/**
 * The shell host of the shared contract (#754): plugins/claude-code/scripts/memcue.sh resolves and installs the
 * machine binary by the same decisions as the Node transport. The Claude Code plugin's own suite keeps its
 * host-specific cases (the failure cooldown, the hook parts, the session PATH file); this file holds the shared ones.
 */
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const script = fileURLToPath(new URL('../../claude-code/scripts/memcue.sh', import.meta.url));
const manifest = JSON.parse(readFileSync(new URL('../../claude-code/.claude-plugin/plugin.json', import.meta.url), 'utf8'));
const version = manifest.version;
const os = ({ darwin: 'darwin', linux: 'linux' })[process.platform];
const arch = ({ x64: 'amd64', arm64: 'arm64' })[process.arch];
const skip = !os || !arch;
const archive = `memcue_${version}_${os}_${arch}.tar.gz`;

function fakeBinary(path, { current = true, name = 'fake' } = {}) {
  writeFileSync(path, `#!/bin/sh
if [ "$1" = version ]; then ${current ? 'exit 0' : 'exit 1'}; fi
echo "${name} $*"
cat
`);
  chmodSync(path, 0o755);
}

function release(root, { badChecksum = false } = {}) {
  const build = join(root, 'build');
  mkdirSync(build, { recursive: true });
  fakeBinary(join(build, 'memcue'), { name: 'installed' });
  const dir = join(root, 'release', `v${version}`);
  mkdirSync(dir, { recursive: true });
  const tar = spawnSync('tar', ['-czf', join(dir, archive), '-C', build, 'memcue']);
  assert.equal(tar.status, 0, String(tar.stderr));
  const sum = createHash('sha256').update(readFileSync(join(dir, archive))).digest('hex');
  writeFileSync(join(dir, 'checksums.txt'), `${badChecksum ? '0'.repeat(64) : sum}  ${archive}\n`);
  return `file://${join(root, 'release')}`;
}

function setup(opts = {}) {
  const root = mkdtempSync(join(tmpdir(), 'memcue-conformance-shell-'));
  const bin = join(root, 'bin');
  mkdirSync(bin);
  const env = {
    HOME: root,
    PATH: `${bin}:/usr/bin:/bin`,
    XDG_STATE_HOME: join(root, 'state'),
    CLAUDE_ENV_FILE: join(root, 'env'),
    MEMCUE_RELEASE_URL: release(root, opts),
  };
  return { root, bin, env, installed: join(root, '.local', 'bin', 'memcue') };
}

function run(env, args, input = '') {
  const r = spawnSync('sh', [script, ...args], { env, input, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  return r.stdout;
}

test('shell: a configured binary wins over everything', { skip }, () => {
  const { env, root } = setup();
  fakeBinary(join(root, 'pinned'), { name: 'pinned' });
  assert.match(run({ ...env, MEMCUE_BIN: join(root, 'pinned') }, ['hook-extract'], 'payload'), /^pinned hook-extract\npayload/);
});

test('shell: a current binary on PATH is used and nothing is downloaded', { skip }, () => {
  const { env, bin, installed } = setup();
  fakeBinary(join(bin, 'memcue'), { name: 'path' });
  assert.match(run(env, ['session-start']), /^path session-start/);
  assert.equal(existsSync(installed), false);
});

test('shell: session-start installs the checksum-checked release when no binary is current', { skip }, () => {
  const { env, installed } = setup();
  assert.match(run(env, ['session-start'], '{}'), /^installed session-start/);
  assert.ok(existsSync(installed));
  assert.match(run(env, ['hook-extract'], 'payload'), /^installed hook-extract\npayload/);
});

test('shell: an older binary on PATH is replaced by the installed release', { skip }, () => {
  const { env, bin, installed } = setup();
  fakeBinary(join(bin, 'memcue'), { current: false, name: 'old' });
  assert.match(run(env, ['session-start']), /^installed session-start/);
  assert.ok(existsSync(installed));
});

test('shell: a checksum that does not match installs nothing', { skip }, () => {
  const { env, installed, root } = setup({ badChecksum: true });
  assert.match(run(env, ['session-start']), /the binary is not installed/);
  assert.equal(existsSync(installed), false);
  assert.match(readFileSync(join(root, 'state', 'memcue', 'install.log'), 'utf8'), /checksum/);
});

test('shell: MEMCUE_AUTO_INSTALL=0 downloads nothing', { skip }, () => {
  const { env, installed } = setup();
  const out = run({ ...env, MEMCUE_AUTO_INSTALL: '0' }, ['session-start']);
  assert.equal(existsSync(installed), false);
  assert.match(out, /MEMCUE_AUTO_INSTALL=0/);
});

test('shell: a machine binary of this release or newer stays', { skip }, () => {
  const { env, installed } = setup();
  mkdirSync(join(installed, '..'), { recursive: true });
  fakeBinary(installed, { name: 'machine' });
  assert.match(run(env, ['session-start']), /^machine session-start/);
  assert.match(readFileSync(installed, 'utf8'), /machine/);
});

test('shell: an older machine binary that is a symlink is left to the user', { skip }, () => {
  const { env, installed, root } = setup();
  mkdirSync(join(installed, '..'), { recursive: true });
  fakeBinary(join(root, 'own'), { current: false, name: 'own' });
  symlinkSync(join(root, 'own'), installed);
  assert.match(run(env, ['session-start']), /older than/);
  assert.ok(lstatSync(installed).isSymbolicLink());
});

test('shell: the shared lock blocks a concurrent install', { skip }, () => {
  const { env, installed, root } = setup();
  mkdirSync(join(root, 'state', 'memcue', 'install.lock'), { recursive: true });
  assert.match(run(env, ['session-start']), /the binary is not installed/);
  assert.equal(existsSync(installed), false);
  assert.match(readFileSync(join(root, 'state', 'memcue', 'install.log'), 'utf8'), /another session is installing/);
});

test('shell: a hook without a binary does nothing, and the exec passes argv, stdin and the exit code', { skip }, () => {
  const { env, bin } = setup();
  assert.equal(run(env, ['hook-extract'], '{"session_id":"x"}'), '');
  writeFileSync(join(bin, 'memcue'), '#!/bin/sh\nprintf \'%s\\n\' "$*"\ncat\nexit 3\n');
  chmodSync(join(bin, 'memcue'), 0o755);
  const r = spawnSync('sh', [script, 'hook-extract', 'a b'], { env, input: 'stdin-text', encoding: 'utf8' });
  assert.equal(r.status, 3, 'the exec must keep the exit code');
  assert.equal(r.stdout, 'hook-extract a b\nstdin-text');
});
