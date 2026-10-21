import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const pluginRoot = new URL('../', import.meta.url)

async function json(path) {
	return JSON.parse(await readFile(new URL(path, pluginRoot), 'utf8'))
}

test('the Claude Code plugin manifest names the plugin', async () => {
	const manifest = await json('.claude-plugin/plugin.json')
	assert.equal(manifest.name, 'memcue')
	assert.equal(typeof manifest.version, 'string')
	assert.equal(manifest.license, 'MIT')
})

test('the marketplace points at the Claude Code plugin', async () => {
	const marketplace = JSON.parse(await readFile(
		new URL('../../../.claude-plugin/marketplace.json', import.meta.url), 'utf8'))
	const plugin = marketplace.plugins.find((entry) => entry.name === 'memcue')
	assert.ok(plugin, 'memcue is missing from the marketplace')
	assert.equal(plugin.source, './plugins/claude-code')
})

test('the hooks cover the four lifecycle events', async () => {
	const config = await json('hooks/hooks.json')
	assert.deepEqual(Object.keys(config.hooks).sort(),
		['PreCompact', 'SessionEnd', 'SessionStart', 'UserPromptSubmit'])
})

test('the SessionStart hook warns about a binary older than the plugin', async () => {
	const config = await json('hooks/hooks.json')
	const [hook] = config.hooks.SessionStart[0].hooks
	assert.match(hook.command, /memcue version -minimum 0\.4\.0/, 'the hook must check the binary version')
	assert.match(hook.command, /older than 0\.4\.0/, 'the warning must name the required version')
})

test('every hook is a guarded command with a timeout', async () => {
	const config = await json('hooks/hooks.json')
	const expected = {
		SessionStart: /memcue session-start/,
		UserPromptSubmit: /memcue hook/,
		PreCompact: /memcue hook-extract/,
		SessionEnd: /memcue hook-extract/,
	}
	for (const [event, pattern] of Object.entries(expected)) {
		const groups = config.hooks[event]
		assert.equal(groups.length, 1, `${event} needs one matcher group`)
		const [hook] = groups[0].hooks
		assert.equal(hook.type, 'command', `${event} must run a command`)
		assert.equal(typeof hook.timeout, 'number')
		assert.ok(hook.timeout > 0, `${event} needs a positive timeout`)
		assert.match(hook.command, pattern, `${event} names the wrong command`)
		assert.match(hook.command, /command -v memcue/, `${event} must tolerate a missing binary`)
		assert.match(hook.command, /\|\| true/, `${event} must not block the session`)
	}
})
