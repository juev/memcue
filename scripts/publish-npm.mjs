import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

const registry = 'https://registry.npmjs.org'
const packages = new Set(['memcue-dsh', 'memcue-pi', 'memcue-opencode'])
const releaseTag = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/

export async function publishPackage(name, tag, directory, {
  lookup = fetch,
  publish = archive => execFileSync('npm', ['publish', archive, '--access', 'public', '--ignore-scripts', '--registry', registry], {
    stdio: 'inherit', timeout: 120_000,
  }),
  wait = delay,
} = {}) {
  if (!packages.has(name) || !releaseTag.test(tag) || !directory) throw new Error('expected a memcue npm package, a stable vX.Y.Z tag and a tarball directory')
  const version = tag.slice(1)
  const spec = `${name}@${version}`
  const archive = resolve(directory, `${name}-${version}.tgz`)
  const integrity = `sha512-${createHash('sha512').update(await readFile(archive)).digest('base64')}`

  async function available() {
    const response = await lookup(`${registry}/${name}/${version}`, {
      headers: { 'cache-control': 'no-cache' }, redirect: 'error', signal: AbortSignal.timeout(10_000),
    })
    if (response.status === 404) return false
    if (!response.ok) throw new Error(`npm registry lookup for ${spec} returned HTTP ${response.status}`)
    const published = await response.json()
    if (published.name !== name || published.version !== version || published.dist?.integrity !== integrity) {
      throw new Error(`${spec} already exists with different metadata or bytes; refusing to replace it`)
    }
    return true
  }

  if (await available()) return 'already published; integrity matches'
  publish(archive)
  for (let attempt = 0; attempt < 12; attempt += 1) {
    if (await available()) return 'published and integrity verified'
    await wait(5_000)
  }
  throw new Error(`npm publish succeeded but ${spec} is not yet visible in the registry; rerun the failed npm job`)
}

if (import.meta.main) {
  const [name, tag, directory] = process.argv.slice(2)
  console.log(`${name} ${tag}: ${await publishPackage(name, tag, directory)}`)
}
