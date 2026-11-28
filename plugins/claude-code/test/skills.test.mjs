import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const root = new URL('../../../', import.meta.url)

test('the note and the skill send an empty search to memcue sessions (#621)', async () => {
	const skill = await readFile(new URL('plugins/claude-code/skills/memcue/SKILL.md', root), 'utf8')
	const session = await readFile(new URL('cmd/memcue/session.go', root), 'utf8')
	for (const text of [skill, session]) {
		assert.match(text, /memcue sessions/)
		assert.match(text, /history, not a fact/)
	}
})

test('the delivered memory skill specifies English facts and first queries with legacy fallback', async () => {
	const skill = await readFile(new URL('plugins/claude-code/skills/memcue/SKILL.md', root), 'utf8')
	for (const phrase of ['new and corrected facts in English', 'regardless of the chat language',
		'first search query in English', 'Russian or another legacy language',
		'identifiers, commands, paths, versions, exact values and verbatim quotes', "user's reply language"]) {
		assert.ok(skill.includes(phrase), phrase)
	}
	assert.doesNotMatch(skill, /until a dream translates/)
})
