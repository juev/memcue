import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const root = new URL('../../../', import.meta.url)

async function json(path) {
	return JSON.parse(await readFile(new URL(path, root), 'utf8'))
}

test('the three plugin manifests carry the release version', async () => {
	const rootPackage = await json('package.json')
	const dsh = await json('plugins/deepseek-harness/package.json')
	const claude = await json('plugins/claude-code/.claude-plugin/plugin.json')
	const hermesYaml = await readFile(new URL('plugins/hermes/memcue/plugin.yaml', root), 'utf8')
	const hermes = hermesYaml.match(/^version:\s*(\S+)\s*$/m)?.[1]

	assert.equal(rootPackage.version, '0.21.0')
	assert.equal(dsh.version, rootPackage.version)
	assert.equal(claude.version, rootPackage.version)
	assert.equal(hermes, rootPackage.version)
})

test('the README compatibility table names the same release', async () => {
	const readme = await readFile(new URL('README.md', root), 'utf8')
	assert.match(readme, /\| 0\.21\.0 \| 6 \| 0\.21\.0 \| 0\.21\.0 /)
	assert.match(readme, /0\.21\.0 \(`hermes-agent` 0\.15\.2\)/)
})

test('the README describes the per-base state and the Hermes card', async () => {
	const readme = await readFile(new URL('README.md', root), 'utf8')
	assert.match(readme, /MEMCUE_STATE_DIR/)
	assert.match(readme, /per-base subdirectory/)
	assert.match(readme, /in the system prompt, rebuilt after compression/)
	assert.ok(!readme.includes('version 0.1'), 'README still says version 0.1')
	assert.ok(!readme.includes('no separate card'), 'README still says Hermes has no card')

	const hermes = await readFile(new URL('plugins/hermes/README.md', root), 'utf8')
	assert.match(hermes, /session_card/)
	assert.match(hermes, /session card/i)
})

test('the docs and the CLI help name the per-base state directory, not the flat pause path', async () => {
	for (const path of ['docs/models.md', 'docs/rerank.md', 'docs/vectors.md']) {
		const text = await readFile(new URL(path, root), 'utf8')
		assert.ok(!text.includes('$XDG_STATE_HOME/memcue/judge.pause'), `${path} still names the flat judge.pause path`)
		assert.ok(!text.includes('$XDG_STATE_HOME/memcue/embed.pause'), `${path} still names the flat embed.pause path`)
	}
	const main = await readFile(new URL('cmd/memcue/main.go', root), 'utf8')
	assert.ok(!main.includes('$XDG_STATE_HOME/memcue/recall.log'), 'the CLI help still names the flat recall.log path')
	assert.ok(!main.includes('$XDG_STATE_HOME/memcue/dream.log'), 'the CLI help still names the flat dream.log path')
})
