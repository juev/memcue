import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { test } from 'node:test'

const root = new URL('../', import.meta.url)
const manifest = path => JSON.parse(readFileSync(new URL(path, root), 'utf8'))
const transport = manifest('plugins/transport/package.json')
const packages = [
  { path: '', transportPath: 'plugins/transport', module: 'memcue-dsh/transport' },
  { path: 'plugins/pi/', transportPath: 'vendor/transport', module: './node_modules/memcue-pi/index.ts' },
  { path: 'plugins/opencode/', transportPath: 'vendor/transport', module: 'memcue-opencode' },
]

const directory = resolve(process.argv[2] ?? 'dist/npm')
for (const pkg of packages) {
  const source = manifest(`${pkg.path}package.json`)
  test(`${source.name} installs with the transport from this checkout`, { timeout: 120_000 }, t => {
    const archive = join(directory, `${source.name}-${source.version}.tgz`)
    const packed = JSON.parse(execFileSync('tar', ['-xOf', archive, 'package/package.json'], { encoding: 'utf8' }))
    assert.equal(packed.name, source.name)
    assert.equal(packed.version, source.version)
    for (const field of ['dependencies', 'optionalDependencies', 'peerDependencies']) {
      assert.ok(!Object.hasOwn(packed[field] ?? {}, 'memcue-transport'), `${packed.name} must not resolve transport from npm`)
    }
    const install = mkdtempSync(join(tmpdir(), 'memcue-packed-'))
    t.after(() => rmSync(install, { recursive: true, force: true }))
    // The plugin tarball is the only memcue package installed. npm installs any host SDK peers normally.
    execFileSync('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', archive], {
      cwd: install, timeout: 90_000, stdio: 'pipe',
    })
    for (const file of transport.files) {
      assert.deepEqual(readFileSync(join(install, 'node_modules', source.name, pkg.transportPath, file)),
        readFileSync(new URL(`plugins/transport/${file}`, root)), `${source.name} did not ship the checkout's ${file}`)
    }
    // Pi's SDK and OpenCode's Bun load distributed TypeScript. For this smoke
    // check on Node, strip types in place without rewriting the installed files.
    execFileSync(process.execPath, ['--input-type=module', '-e', `
      import { readFileSync } from 'node:fs';
      import { registerHooks, stripTypeScriptTypes } from 'node:module';
      registerHooks({ load(url, context, next) {
        if (url.startsWith('file:') && url.endsWith('.ts')) {
          const source = stripTypeScriptTypes(readFileSync(new URL(url), 'utf8'));
          return { format: 'module', source, shortCircuit: true };
        }
        return next(url, context);
      }});
      await import(${JSON.stringify(pkg.module)});
    `], {
      cwd: install, timeout: 15_000, stdio: 'pipe',
    })
  })
}
