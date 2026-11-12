/** Offline tests for the install of the memcue binary: a local HTTP server stands in for the GitHub release. */
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, symlinkSync, writeFileSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { after, before, beforeEach, test } from 'node:test';
import { install, installedPath, pluginVersion } from '../src/install.ts';
import { openPi, piFixture } from './pi-harness.ts';

const os = ({ darwin: 'darwin', linux: 'linux' } as Record<string, string>)[process.platform];
const arch = ({ x64: 'amd64', arm64: 'arm64' } as Record<string, string>)[process.arch];
const skip = !os || !arch;

let version = '';
let server: Server;
let files: Map<string, Buffer | string>;
let hits = 0;
let root = '';
const saved: Record<string, string | undefined> = {};

before(async () => {
  version = await pluginVersion();
  server = createServer((req, res) => {
    hits += 1;
    const body = files.get(req.url ?? '');
    if (!body) { res.writeHead(404).end(); return; }
    res.writeHead(200).end(body);
  });
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
  for (const key of Object.keys(process.env)) if (key.startsWith('MEMCUE_')) saved[key] = process.env[key];
  for (const key of ['HOME', 'XDG_STATE_HOME', 'PATH']) saved[key] = process.env[key];
});

after(() => {
  server.close();
  for (const [key, value] of Object.entries(saved)) {
    if (value === undefined) delete process.env[key]; else process.env[key] = value;
  }
});

/** Pack a release of this plugin's version whose binary is the given script. */
function release(script: string) {
  const build = mkdtempSync(join(root, 'build-'));
  writeFileSync(join(build, 'memcue'), script, { mode: 0o755 });
  const archive = `memcue_${version}_${os}_${arch}.tar.gz`;
  const tar = spawnSync('tar', ['-czf', join(root, archive), '-C', build, 'memcue']);
  assert.equal(tar.status, 0, String(tar.stderr));
  const data = readFileSync(join(root, archive));
  const sum = createHash('sha256').update(data).digest('hex');
  files = new Map<string, Buffer | string>([
    [`/v${version}/${archive}`, data],
    [`/v${version}/checksums.txt`, `${sum}  ${archive}\n`],
  ]);
}

// Each test gets its own home and state directory, so nothing reaches the real ~/.local/bin.
beforeEach(() => {
  root = mkdtempSync(join(tmpdir(), 'memcue-pi-install-'));
  release(`#!/bin/sh\nprintf '{"version":"${version}"}\\n'\n`);
  hits = 0;
  for (const key of Object.keys(process.env)) if (key.startsWith('MEMCUE_')) delete process.env[key];
  process.env.HOME = join(root, 'home');
  process.env.XDG_STATE_HOME = join(root, 'state');
  process.env.MEMCUE_RELEASE_URL = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  process.env.PATH = '/usr/bin:/bin';
});

test('install downloads the release, checks it and puts it in the machine binary', { skip }, async () => {
  const path = await install(version);
  assert.equal(path, join(process.env.HOME!, '.local', 'bin', 'memcue'));
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
  assert.equal(existsSync(join(process.env.XDG_STATE_HOME!, 'memcue', 'install.lock')), false);
});

test('MEMCUE_AUTO_INSTALL=0 downloads nothing', { skip }, async () => {
  process.env.MEMCUE_AUTO_INSTALL = '0';
  await assert.rejects(install(version), /MEMCUE_AUTO_INSTALL=0/);
  assert.equal(hits, 0);
});

/** Put a memcue at the machine's path that passes `version -minimum` or not. */
function machineBinary(passes: boolean) {
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
  const own = join(process.env.HOME!, 'own-memcue');
  mkdirSync(dirname(installedPath()), { recursive: true });
  writeFileSync(own, '#!/bin/sh\nexit 1\n', { mode: 0o755 });
  symlinkSync(own, installedPath());
  await assert.rejects(install(version), /not a plain file/);
  assert.ok(lstatSync(installedPath()).isSymbolicLink());
  assert.equal(hits, 0);
});

/** Put on PATH a memcue that passes `version -minimum` or not. */
function pathBinary(passes: boolean) {
  const bin = mkdtempSync(join(root, 'path-'));
  writeFileSync(join(bin, 'memcue'), `#!/bin/sh\nexit ${passes ? 0 : 1}\n`);
  chmodSync(join(bin, 'memcue'), 0o755);
  process.env.PATH = `${bin}:/usr/bin:/bin`;
}

test('without a configured binary the plugin installs its release and uses it', { skip }, async () => {
  const fixture = await piFixture();
  // The release is the fixture CLI, so the session works on the installed binary.
  release(readFileSync(fixture.binary, 'utf8'));
  pathBinary(false);
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture, { autoBinary: true });
    await pi.prompt();
    assert.ok(hits > 0, 'an older memcue on PATH must be replaced by the release');
    assert.ok(existsSync(installedPath()));
    assert.ok((await fixture.calls()).some(call => call.args[0] === 'session-card'));
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('a memcue on PATH of this release or newer is used, and nothing is downloaded', { skip }, async () => {
  const fixture = await piFixture();
  const bin = mkdtempSync(join(root, 'path-'));
  writeFileSync(join(bin, 'memcue'), readFileSync(fixture.binary, 'utf8'), { mode: 0o755 });
  process.env.PATH = `${bin}:/usr/bin:/bin`;
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture, { autoBinary: true });
    await pi.prompt();
    assert.equal(hits, 0);
    assert.equal(existsSync(installedPath()), false);
    assert.ok((await fixture.calls()).some(call => call.args[0] === 'session-card'));
  } finally { await pi?.dispose(); await fixture.dispose(); }
});

test('a configured binary is used as it is, and nothing is downloaded', { skip }, async () => {
  const fixture = await piFixture();
  let pi: Awaited<ReturnType<typeof openPi>> | undefined;
  try {
    pi = await openPi(fixture);
    await pi.prompt();
    assert.equal(hits, 0);
    assert.equal(existsSync(installedPath()), false);
  } finally { await pi?.dispose(); await fixture.dispose(); }
});
