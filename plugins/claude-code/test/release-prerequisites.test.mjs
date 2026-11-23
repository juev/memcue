import assert from 'node:assert/strict'
import { readFile, mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { test } from 'node:test'

const root = new URL('../../../', import.meta.url)

test('one release tag publishes the tested npm tarballs only after the binary', async () => {
	const release = await readFile(new URL('.github/workflows/release.yml', root), 'utf8')
	const ci = await readFile(new URL('.github/workflows/ci.yml', root), 'utf8')
	for (const plugin of ['dsh', 'pi', 'opencode']) {
		await assert.rejects(readFile(new URL(`.github/workflows/publish-${plugin}.yml`, root)), { code: 'ENOENT' })
	}
	await assert.rejects(readFile(new URL('.github/workflows/publish-transport.yml', root)), { code: 'ENOENT' })
	const publish = release.slice(release.indexOf('\n  npm:\n'))
	assert.match(release, /tags:\s+- "v\*"/)
	assert.match(publish, /needs: goreleaser/)
	assert.match(publish, /id-token: write/)
	assert.match(publish, /package: \[memcue-dsh, memcue-pi, memcue-opencode\]/)
	assert.match(publish, /uses: actions\/download-artifact@/)
	assert.match(publish, /node scripts\/publish-npm\.mjs/)
	assert.match(release, /make test-npm-packages[\s\S]+uses: actions\/upload-artifact@[\s\S]+args: release --clean/)
	assert.match(ci, /run: make test-npm-packages/)
})

test('every release make test path installs the CI native client and Node runtime first', async () => {
	const release = await readFile(new URL('.github/workflows/release.yml', root), 'utf8')
	const ci = await readFile(new URL('.github/workflows/ci.yml', root), 'utf8')
	const setup = ci.slice(ci.indexOf('      - uses: actions/setup-node@'), ci.indexOf('      # Builds bin/memcue'))
	assert.ok(setup.includes('node-version: "24"'))
	assert.ok(setup.includes('@anthropic-ai/claude-code@2.1.301'))
	assert.ok(release.includes(setup), 'release must reuse the working CI runtime and pinned installation')
	const gates = [...release.matchAll(/^\s+run: (make test(?:[ 	][^\n]*)?)$/gm)]
	assert.ok(gates.length > 0, 'required release test gate missing')
	for (const gate of gates) {
		assert.equal(gate[1], 'make test CLAUDE_BIN="$RUNNER_TEMP/claude-client/node_modules/.bin/claude"')
		assert.ok(release.indexOf(setup) < gate.index, 'setup must precede every release test gate')
	}
})

test('native validation fails at execution when the declared client is absent', async () => {
	const directory = await mkdtemp(join(tmpdir(), 'release-client-'))
	try {
		const missing = join(directory, 'missing-claude')
		const result = spawnSync('make', ['test-claude-mods', `CLAUDE_BIN=${missing}`], { cwd: root, encoding: 'utf8' })
		assert.ifError(result.error)
		assert.notEqual(result.status, 0)
		assert.match(result.stdout, /plugin validate plugins\/claude-code/)
		assert.ok(result.stderr.includes(missing))
		assert.match(result.stderr, /(?:No such file or directory|not found)/)
		assert.match(result.stderr, /(?:Error 127|Error 2)/)
	} finally {
		await rm(directory, { recursive: true, force: true })
	}
})
