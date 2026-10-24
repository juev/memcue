import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const root = new URL('../../../', import.meta.url)

test('the dream hint of the session start names a skill the plugin ships', async () => {
	const skill = await readFile(new URL('plugins/claude-code/skills/memcue-dream/SKILL.md', root), 'utf8')
	assert.match(skill, /^---\nname: memcue-dream\n/)
	assert.match(skill, /memcue dream -prepare/)
	assert.match(skill, /memcue dream -apply/)
	const session = await readFile(new URL('cmd/memcue/session.go', root), 'utf8')
	assert.match(session, /memcue-dream skill/)
})
