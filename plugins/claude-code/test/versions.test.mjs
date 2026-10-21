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

	assert.equal(rootPackage.version, '0.4.0')
	assert.equal(dsh.version, rootPackage.version)
	assert.equal(claude.version, rootPackage.version)
	assert.equal(hermes, rootPackage.version)
})

test('the README compatibility table names the same release', async () => {
	const readme = await readFile(new URL('README.md', root), 'utf8')
	assert.match(readme, /\| 0\.4\.0 \| 4 \| 0\.4\.0 \| 0\.4\.0 /)
	assert.match(readme, /0\.4\.0 \(`hermes-agent` 0\.15\.2\)/)
})
