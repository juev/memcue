// Native candidate coverage: fixture Cordis registrations, real memcue storage.
// No provider or authenticated model is involved.
import assert from 'node:assert/strict'
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { test } from 'node:test'
import { apply } from '../src/index.mjs'

// The real binary must write only the throwaway base: an inherited server or PostgreSQL backend wins over
// MEMCUE_FACTS, and a run with the developer's MEMCUE_SERVER wrote this fixture into the live base.
for (const key of Object.keys(process.env)) {
 if (key === 'MEMCUE_SERVER' || key === 'MEMCUE_POSTGRES_DSN' || key.startsWith('MEMCUE_TAILSCALE_')) delete process.env[key]
}

test('native candidate: DSH content-only add/search/correct/retire and obsolete refusal', async () => {
 const binary = process.env.MEMCUE_BIN
 assert.ok(binary, 'MEMCUE_BIN must name the candidate build; native coverage cannot skip')
 const root = await mkdtemp(join(tmpdir(), 'memcue-dsh-native-'))
 try {
  const tools = new Map()
  const ctx = { on: () => () => {}, tools: { register: tool => { tools.set(tool.name, tool); return () => {} } }, systemPrompt: { section: () => () => {} } }
  apply(ctx, { binary: resolve(binary), facts: join(root, 'facts') })
  const exec = { agent: { session: { header: { cwd: root } } } }
  const call = (name, args) => tools.get(name).execute(args, exec)
  const original = 'Обсерватория PROJ-1234 использует порт 6432.'
  assert.equal((await call('memcue_add', { text: original, kind: 'environment', scope: 'global', source: 'tool' })).stored, true)
  let result = await call('memcue_search', { query: 'PROJ-1234', min_score: 0 })
  assert.equal(result.facts.length, 1)
  assert.equal(result.facts[0].content, original)
  const id = result.facts[0].id
  for (const name of ['memcue_add', 'memcue_correct']) {
   for (const field of ['keywords', 'queries']) {
    assert.equal(Object.hasOwn(tools.get(name).parameters.properties, field), false)
    assert.equal((await call(name, { id, expected_version: result.facts[0].version, text: 'Unwanted saffron marker.', [field]: ['saffron'] })).stored, false)
   }
  }
  assert.equal((await call('memcue_search', { query: 'saffron', min_score: 0 })).facts.length, 0)
  const corrected = 'Обсерватория PROJ-1234 использует порт 7432.'
  assert.equal((await call('memcue_correct', { id, expected_version: result.facts[0].version, text: corrected })).stored, true)
  result = await call('memcue_search', { query: 'PROJ-1234', min_score: 0 })
  assert.equal(result.facts[0].content, corrected)
  assert.equal((await call('memcue_retire', { id, expected_version: result.facts[0].version })).stored, true)
  assert.equal((await call('memcue_search', { query: 'PROJ-1234', min_score: 0 })).facts.length, 0)
  const history = []
  for (const entry of await readdir(join(root, 'facts'), { withFileTypes: true })) {
   if (!entry.isDirectory() || entry.name.startsWith('.')) continue
   for (const file of await readdir(join(root, 'facts', entry.name))) {
    if (file.endsWith('.jsonl')) history.push(...(await readFile(join(root, 'facts', entry.name, file), 'utf8')).trim().split('\n').map(JSON.parse))
   }
  }
  assert.deepEqual(history.map(row => row.content), [original, corrected, corrected])
  assert.deepEqual(history.map(row => row.version), [1, 2, 3])
  assert.equal(history.at(-1).status, 'retired')
  for (const row of history) {
   assert.equal(row.source, 'tool')
   assert.equal(Object.hasOwn(row, 'keywords'), false)
   assert.equal(Object.hasOwn(row, 'queries'), false)
  }
 } finally { await rm(root, { recursive: true, force: true }) }
})
