import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const root = new URL('../', import.meta.url);
const manifest = async path => JSON.parse(await readFile(new URL(path, root), 'utf8'));

test('the standalone OpenCode package carries the release version and the server entrypoint', async () => {
  const pkg = await manifest('package.json');
  const rootPackage = JSON.parse(await readFile(new URL('../../package.json', root), 'utf8'));
  assert.equal(pkg.version, rootPackage.version);
  assert.equal(pkg.private, undefined);
  assert.equal(pkg.publishConfig?.access, 'public');
  assert.equal(pkg.type, 'module');
  assert.deepEqual(pkg.exports, { '.': './src/index.ts' });
  assert.ok(pkg.dependencies?.['@opencode/plugin'], 'the plugin SDK is a runtime dependency');
  assert.equal(pkg.dependencies?.['@opencode/plugin'].startsWith('^2.'), true);
});

test('the distributed package includes every OpenCode runtime module', () => {
  const packed = JSON.parse(execFileSync('npm', ['pack', '--dry-run', '--json', '--ignore-scripts'], {
    cwd: fileURLToPath(root), encoding: 'utf8', timeout: 60_000,
  }));
  const files = new Set(packed[0].files.map(file => file.path));
  for (const path of ['src/index.ts', 'src/cli.ts', 'src/config.ts', 'src/install.ts', 'src/instructions.ts',
    'src/tools.ts', 'package.json', 'README.md', 'LICENSE']) {
    assert.ok(files.has(path), `missing packaged runtime: ${path}`);
  }
  for (const path of files) {
    assert.ok(!path.startsWith('test/'), `the package ships a test: ${path}`);
  }
});
