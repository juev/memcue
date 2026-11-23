import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { publishPackage } from './publish-npm.mjs'

const name = 'memcue-pi'
const tag = 'v1.2.3'
const version = '1.2.3'

async function fixture(t) {
  const directory = await mkdtemp(join(tmpdir(), 'memcue-publish-test-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const archive = join(directory, `${name}-${version}.tgz`)
  const bytes = Buffer.from('synthetic archive for publish control flow')
  await writeFile(archive, bytes)
  const integrity = `sha512-${createHash('sha512').update(bytes).digest('base64')}`
  return { directory, archive, reply: (digest = integrity) => Response.json({ name, version, dist: { integrity: digest } }) }
}

const missing = () => new Response(null, { status: 404 })
const noPublish = () => assert.fail('npm publish must not run')

test('publish a missing version once and verify exactly those bytes after registry propagation', async t => {
  const { directory, archive, reply } = await fixture(t)
  const operations = []
  const responses = [missing(), missing(), reply()]
  assert.equal(await publishPackage(name, tag, directory, {
    lookup: url => { assert.equal(url, 'https://registry.npmjs.org/memcue-pi/1.2.3'); operations.push('lookup'); return responses.shift() },
    publish: path => { assert.equal(path, archive); operations.push('publish') },
    wait: () => operations.push('wait'),
  }), 'published and integrity verified')
  assert.deepEqual(operations, ['lookup', 'publish', 'lookup', 'wait', 'lookup'])
})

test('a retried job accepts identical existing bytes but refuses a conflicting version', async t => {
  const { directory, reply } = await fixture(t)
  assert.equal(await publishPackage(name, tag, directory, { lookup: () => reply(), publish: noPublish }),
    'already published; integrity matches')
  await assert.rejects(publishPackage(name, tag, directory, { lookup: () => reply('sha512-other'), publish: noPublish }),
    /different metadata or bytes/)
})

test('a registry error or a non-release tag does not permit publishing', async t => {
  const { directory } = await fixture(t)
  await assert.rejects(publishPackage(name, tag, directory, {
    lookup: () => new Response(null, { status: 503 }), publish: noPublish,
  }), /HTTP 503/)
  await assert.rejects(publishPackage(name, `${tag}-rc.1`, directory, { lookup: noPublish, publish: noPublish }), /stable vX.Y.Z/)
})
