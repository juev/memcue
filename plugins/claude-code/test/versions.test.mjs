import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const root = new URL('../../../', import.meta.url)

async function json(path) {
	return JSON.parse(await readFile(new URL(path, root), 'utf8'))
}

test('all plugin manifests carry the release version', async () => {
	// The root package is the memcue-dsh plugin itself since #754; the transport package shares the release version.
	const rootPackage = await json('package.json')
	const transport = await json('plugins/transport/package.json')
	const pi = await json('plugins/pi/package.json')
	const opencode = await json('plugins/opencode/package.json')
	const codex = await json('plugins/codex/.codex-plugin/plugin.json')
	const companionYaml = await readFile(new URL('plugins/hermes/memcue-context/plugin.yaml', root), 'utf8')
	const companion = companionYaml.match(/^version:\s*(\S+)\s*$/m)?.[1]
	const claude = await json('plugins/claude-code/.claude-plugin/plugin.json')
	const hermesYaml = await readFile(new URL('plugins/hermes/memcue/plugin.yaml', root), 'utf8')
	const hermes = hermesYaml.match(/^version:\s*(\S+)\s*$/m)?.[1]

	assert.match(rootPackage.version, /^\d+\.\d+\.\d+$/)
	assert.equal(transport.version, rootPackage.version)
	assert.equal(pi.version, rootPackage.version)
	assert.equal(opencode.version, rootPackage.version)
	assert.equal(claude.version, rootPackage.version)
	assert.equal(hermes, rootPackage.version)
	assert.equal(codex.version, rootPackage.version)
	assert.equal(companion, rootPackage.version)
})

test('Pi and OpenCode locks carry the release version without a registry transport', async () => {
	for (const plugin of ['pi', 'opencode']) {
		const manifest = await json(`plugins/${plugin}/package.json`)
		const lock = await json(`plugins/${plugin}/package-lock.json`)
		assert.equal(lock.version, manifest.version)
		assert.equal(lock.packages[''].version, manifest.version)
		assert.ok(!Object.keys(lock.packages).some(path => path.endsWith('/memcue-transport')),
			`plugins/${plugin} must install without memcue-transport from npm`)
	}
})

test('the README compatibility table names the same release', async () => {
	const readme = await readFile(new URL('README.md', root), 'utf8')
	const version = (await json('package.json')).version.replaceAll('.', '\\.')
	assert.match(readme, new RegExp(`\\| ${version} \\| 9 \\| ${version} \\| ${version} `))
	assert.match(readme, new RegExp(`${version} \\(\`hermes-agent\` >=0\\.17\\.0\\)`))
	assert.match(readme, new RegExp(`This release candidate is \`${version}\``))
})

test('the README describes the per-base state, and the Hermes README no card', async () => {
	const readme = await readFile(new URL('README.md', root), 'utf8')
	assert.match(readme, /MEMCUE_STATE_DIR/)
	assert.match(readme, /per-base subdirectory/)
	assert.match(readme, /in the system prompt, rebuilt after compression/)
	assert.ok(!readme.includes('version 0.1'), 'README still says version 0.1')
	assert.ok(!readme.includes('no separate card'), 'README still says Hermes has no card')

	const hermes = await readFile(new URL('plugins/hermes/README.md', root), 'utf8')
	assert.match(hermes, /No card of rules/)
	assert.doesNotMatch(hermes, /MEMCUE_CARD_BUDGET/, 'the Hermes README still names the card budget (#656)')
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
