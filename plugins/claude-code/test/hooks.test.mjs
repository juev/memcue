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

test('the hooks cover the session start only: no recall with a prompt (#361), no extraction (#494)', async () => {
	const config = await json('hooks/hooks.json')
	assert.deepEqual(Object.keys(config.hooks), ['SessionStart'])
})

test('the SessionStart hook can install the binary and warns about an older one', async () => {
	const config = await json('hooks/hooks.json')
	const [hook] = config.hooks.SessionStart[0].hooks
	assert.ok(hook.timeout >= 60, 'the first session downloads the release inside SessionStart')
	const script = await readFile(new URL('scripts/memcue.sh', pluginRoot), 'utf8')
	assert.match(script, /version -minimum "\$version"/, 'the script must check the binary version')
	assert.match(script, /older than \$version/, 'the warning must name the required version')
})

test('every hook runs the binary through the plugin script, with a timeout', async () => {
	const config = await json('hooks/hooks.json')
	// The card comes in three parts, one SessionStart hook each: Claude Code hides a hook's text of 10 000 characters
	// or more, and the limit holds per hook (#480).
	const expected = {
		SessionStart: ['session-start -part 1', 'session-start -part 2', 'session-start -part 3'],
	}
	for (const [event, commands] of Object.entries(expected)) {
		const groups = config.hooks[event]
		assert.equal(groups.length, commands.length, `${event} needs ${commands.length} matcher groups`)
		commands.forEach((command, i) => {
			const [hook] = groups[i].hooks
			assert.equal(hook.type, 'command', `${event} must run a command`)
			assert.equal(typeof hook.timeout, 'number')
			assert.ok(hook.timeout > 0, `${event} needs a positive timeout`)
			assert.equal(hook.command, `sh "\${CLAUDE_PLUGIN_ROOT}/scripts/memcue.sh" ${command} || true`,
				`${event} must run the plugin script and never block the session`)
		})
	}
})
