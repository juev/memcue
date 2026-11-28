// Exercise the actual Claude launcher with the native candidate and isolated data.
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

test('native candidate: Claude launcher stores content only and refuses obsolete writes', async () => {
 assert.ok(process.env.MEMCUE_BIN, 'native candidate binary is required')
 const root = await mkdtemp(join(tmpdir(), 'memcue-claude-native-'))
 const launcher = fileURLToPath(new URL('../scripts/memcue.sh', import.meta.url))
 const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('MEMCUE_')))
 Object.assign(env, { HOME: root, MEMCUE_BIN: resolve(process.env.MEMCUE_BIN), MEMCUE_FACTS: join(root, 'facts'), XDG_STATE_HOME: join(root, 'state'), XDG_DATA_HOME: join(root, 'data'), MEMCUE_AUTO_INSTALL: '0', MEMCUE_DREAM_BACKGROUND: '0', GIT_CONFIG_GLOBAL: '/dev/null' })
 const run = (...args) => spawnSync('sh', [launcher, ...args], { cwd: root, env, encoding: 'utf8' })
 try {
  let r = run('add', '-source', 'user', '--', 'Native cobalt PROJ-1234 port 6432.')
  assert.equal(r.status, 0, r.stderr)
  const id = r.stdout.trim()
  r = run('correct', '-keywords', 'saffron', id, 'Unwanted replacement.')
  assert.notEqual(r.status, 0)
  r = run('correct', '--', id, 'Native cobalt PROJ-1234 port 7432.')
  assert.equal(r.status, 0, r.stderr)
  r = run('search', '-json', '-min-score', '0', '--', 'PROJ-1234')
  assert.equal(r.status, 0, r.stderr)
  const result = JSON.parse(r.stdout)
  const facts = Array.isArray(result) ? result : result.facts
  assert.equal(facts.length, 1)
  assert.equal(facts[0].content, 'Native cobalt PROJ-1234 port 7432.')
  assert.equal(Object.hasOwn(facts[0], 'keywords'), false)
  assert.equal(Object.hasOwn(facts[0], 'queries'), false)
  r = run('retire', '-reason', 'The synthetic rule was superseded.', id)
  assert.equal(r.status, 0, r.stderr)
  r = run('search', '-json', '-min-score', '0', '--', 'PROJ-1234')
  assert.equal(r.status, 0, r.stderr)
  const retired = JSON.parse(r.stdout)
  assert.equal((Array.isArray(retired) ? retired : retired.facts).length, 0)
 } finally { await rm(root, { recursive: true, force: true }) }
})
