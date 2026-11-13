/**
 * The install contract of #754 against the canonical package: a local HTTP server stands in for the GitHub release,
 * as in the Pi plugin's measured install tests. The shell and Python hosts run the same decisions in their own files.
 */
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, symlinkSync, utimesSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { after, before, beforeEach, test } from 'node:test';
import { install, installedPath } from '../index.mjs';

const os = ({ darwin: 'darwin', linux: 'linux' })[process.platform];
const arch = ({ x64: 'amd64', arm64: 'arm64' })[process.arch];
const skip = !os || !arch;
const version = '9.9.9';

let server;
let files;
let hits = 0;
let root = '';
const saved = {};

before(async () => {
  server = createServer((req, res) => {
    hits += 1;
    const body = files.get(req.url ?? '');
    if (!body) { res.writeHead(404).end(); return; }
    res.writeHead(200).end(body);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  for (const key of Object.keys(process.env)) if (key.startsWith('MEMCUE_')) saved[key] = process.env[key];
  for (const key of ['HOME', 'XDG_STATE_HOME', 'PATH']) saved[key] = process.env[key];
});

after(() => {
  server.close();
  for (const [key, value] of Object.entries(saved)) {
    if (value === undefined) delete process.env[key]; else process.env[key] = value;
  }
});

/** Pack a release whose binary is the given script. */
function release(script) {
  const build = mkdtempSync(join(root, 'build-'));
  writeFileSync(join(build, 'memcue'), script, { mode: 0o755 });
  const archive = `memcue_${version}_${os}_${arch}.tar.gz`;
  const tar = spawnSync('tar', ['-czf', join(root, archive), '-C', build, 'memcue']);
  assert.equal(tar.status, 0, String(tar.stderr));
  const data = readFileSync(join(root, archive));
  const sum = createHash('sha256').update(data).digest('hex');
  files = new Map([
    [`/v${version}/${archive}`, data],
    [`/v${version}/checksums.txt`, `${sum}  ${archive}\n`],
  ]);
}

// Each test gets its own home and state directory, so nothing reaches the real ~/.local/bin.
beforeEach(() => {
  root = mkdtempSync(join(tmpdir(), 'memcue-conformance-install-'));
  release(`#!/bin/sh\nprintf '{"version":"${version}"}\\n'\n`);
  hits = 0;
  for (const key of Object.keys(process.env)) if (key.startsWith('MEMCUE_')) delete process.env[key];
  process.env.HOME = join(root, 'home');
  process.env.XDG_STATE_HOME = join(root, 'state');
  process.env.MEMCUE_RELEASE_URL = `http://127.0.0.1:${server.address().port}`;
  process.env.PATH = '/usr/bin:/bin';
});

test('install downloads the release, checks it and puts it in the machine binary', { skip }, async () => {
  const path = await install(version);
  assert.equal(path, join(process.env.HOME, '.local', 'bin', 'memcue'));
  assert.match(spawnSync(path, ['version', '-json'], { encoding: 'utf8' }).stdout, new RegExp(version));
  hits = 0;
  assert.equal(await install(version), path);
  assert.equal(hits, 0, 'an installed release must not be downloaded again');
});

test('a checksum that does not match installs nothing and releases the lock', { skip }, async () => {
  const key = `/v${version}/checksums.txt`;
  files.set(key, String(files.get(key)).replace(/^[0-9a-f]+/, '0'.repeat(64)));
  await assert.rejects(install(version), /checksum/);
  assert.equal(existsSync(installedPath()), false);
  assert.equal(existsSync(join(process.env.XDG_STATE_HOME, 'memcue', 'install.lock')), false);
});

test('MEMCUE_AUTO_INSTALL=0 downloads nothing', { skip }, async () => {
  process.env.MEMCUE_AUTO_INSTALL = '0';
  await assert.rejects(install(version), /MEMCUE_AUTO_INSTALL=0/);
  assert.equal(hits, 0);
});

/** Put a memcue at the machine's path that passes `version -minimum` or not. */
function machineBinary(passes) {
  mkdirSync(dirname(installedPath()), { recursive: true });
  writeFileSync(installedPath(), `#!/bin/sh\n# machine\nexit ${passes ? 0 : 1}\n`, { mode: 0o755 });
}

test('a machine binary of this release or newer stays and nothing is downloaded', { skip }, async () => {
  machineBinary(true);
  assert.equal(await install(version), installedPath());
  assert.equal(hits, 0);
  assert.match(readFileSync(installedPath(), 'utf8'), /machine/);
});

test('an older machine binary is replaced by the release', { skip }, async () => {
  machineBinary(false);
  assert.equal(await install(version), installedPath());
  assert.ok(hits > 0);
  assert.match(spawnSync(installedPath(), ['version', '-json'], { encoding: 'utf8' }).stdout, new RegExp(version));
});

test('an older machine binary that is a symlink is left to the user', { skip }, async () => {
  const own = join(process.env.HOME, 'own-memcue');
  mkdirSync(dirname(installedPath()), { recursive: true });
  writeFileSync(own, '#!/bin/sh\nexit 1\n', { mode: 0o755 });
  symlinkSync(own, installedPath());
  await assert.rejects(install(version), /not a plain file/);
  assert.ok(lstatSync(installedPath()).isSymbolicLink());
  assert.equal(hits, 0);
});

test('a fresh lock held by another install blocks this one', { skip }, async () => {
  const lock = join(process.env.XDG_STATE_HOME, 'memcue', 'install.lock');
  mkdirSync(lock, { recursive: true });
  await assert.rejects(install(version), /another install/);
  assert.equal(hits, 0);
  assert.ok(existsSync(lock), 'the other install keeps its lock');
});

test('a lock left by a crash ten minutes ago is taken over', { skip }, async () => {
  const lock = join(process.env.XDG_STATE_HOME, 'memcue', 'install.lock');
  mkdirSync(lock, { recursive: true });
  const old = new Date(Date.now() - 11 * 60_000);
  utimesSync(lock, old, old);
  assert.equal(await install(version), installedPath());
  assert.ok(hits > 0);
});

test('the rename leaves a running memcue its own file', { skip }, async () => {
  const started = join(root, 'started'), marker = join(root, 'ran');
  release(`#!/bin/sh\nif [ "$1" = version ]; then exit 1; fi\nprintf started > ${JSON.stringify(started)}\nsleep 1\nprintf ran > ${JSON.stringify(marker)}\n`);
  const path = await install(version);
  const running = spawn(path, [], { stdio: 'ignore' });
  // Replace only once the old script is really executing: a process that has not exec'd yet picks up the new file,
  // and that is correct. The guarantee is about the running one.
  const deadline = Date.now() + 5_000;
  while (!existsSync(started) && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 20));
  assert.ok(existsSync(started), 'the first binary never started');
  release(`#!/bin/sh\nprintf '{"version":"${version}"}\\n'\n`);
  assert.equal(await install(version), path);
  await new Promise(resolve => running.once('close', resolve));
  assert.ok(existsSync(marker), 'the running binary lost its file');
  assert.match(spawnSync(path, ['version', '-json'], { encoding: 'utf8' }).stdout, new RegExp(version));
});