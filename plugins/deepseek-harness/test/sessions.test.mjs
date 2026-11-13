// The history fallback of the dsh plugin (#630): the real binary reads an invented journal, so the tool answers with
// the command's own contract. No provider or authenticated model is involved. The journal is plaintext, which dsh
// writes when its JSONL encoding is not zstd; the compressed form is covered by internal/history's own tests.
import assert from 'node:assert/strict'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { test } from 'node:test'
import { apply } from '../src/index.mjs'

/** One journal as dsh keeps it: a session header, then the records of the session, one JSON line each. */
function journal(lines) {
	return lines.map((line) => JSON.stringify(line)).join('\n') + '\n'
}

test('memcue_sessions reads the harness journal through the real binary', async () => {
	const binary = process.env.MEMCUE_BIN
	assert.ok(binary, 'MEMCUE_BIN must name the candidate build; native coverage cannot skip')
	const root = await mkdtemp(join(tmpdir(), 'memcue-dsh-sessions-'))
	const saved = new Map()
	const isolate = (key, value) => { saved.set(key, process.env[key]); process.env[key] = value }
	try {
		// The command reads every host's history of this machine: point them all at the fixture.
		isolate('HOME', join(root, 'home'))
		isolate('CLAUDE_CONFIG_DIR', join(root, 'home', 'claude'))
		isolate('XDG_DATA_HOME', join(root, 'home', 'data'))
		isolate('DSH_HOME', join(root, 'dsh'))
		isolate('OPENCODE_DB', '')
		isolate('CLAUDE_CODE_SESSION_ID', '')

		const at = Date.UTC(2026, 10, 10, 12, 0, 0)
		const header = (id, depth) => ({ type: 'session', version: 4, id, createdAt: at, cwd: root,
			isSeeded: false, delegationDepth: depth })
		const record = (seq, type, data, offset = 0) => ({ seq, time: at + offset * 1000, type, data })
		const user = (id, text, kind = 'user') => ({ id, role: 'user', content: [{ type: 'text', text }],
			source: { kind } })
		const write = async (session, lines) => {
			const dir = join(process.env.DSH_HOME, 'sessions', '-fixture', session)
			await mkdir(dir, { recursive: true })
			await writeFile(join(dir, 'session.v4.jsonl'), journal(lines))
		}
		await write('dsh-answer', [
			header('dsh-answer', 0),
			record(1, 'user/message', user('u1', 'Какой порт у обсерватории PROJ-77?')),
			record(2, 'assistant/message', { message: { id: 'a1', role: 'assistant', content: [
				{ type: 'reasoning', text: 'REASONING-CANARY PROJ-77' },
				{ type: 'text', text: 'Обсерватория PROJ-77 слушает порт 6432.' },
				{ type: 'tool-call', name: 'shell' },
			] }, stream: [{ type: 'text-delta', text: 'STREAM-CANARY PROJ-77' }] }, 60),
			record(3, 'tool/result', { message: { content: [{ type: 'text', text: 'TOKEN=TOOL-CANARY PROJ-77' }] } }, 61),
			record(4, 'user/message', user('m1', 'NOTICE-CANARY PROJ-77', 'memcue'), 62),
		])
		await write('dsh-current', [
			header('dsh-current', 0),
			record(1, 'user/message', user('c1', 'CURRENT-CANARY PROJ-77')),
		])
		await write('dsh-sub', [
			header('dsh-sub', 1),
			record(1, 'user/message', user('s1', 'SUBAGENT-CANARY PROJ-77')),
		])

		const tools = new Map()
		const sections = []
		const ctx = {
			on: () => () => {},
			tools: { register: (tool) => { tools.set(tool.name, tool); return () => {} } },
			systemPrompt: { section: (section) => { sections.push(section); return () => {} } },
		}
		apply(ctx, { binary: resolve(binary), facts: join(root, 'facts') })
		const exec = { agent: { session: { id: 'dsh-current', header: { cwd: root } } } }
		const tool = tools.get('memcue_sessions')
		assert.ok(tool, 'the plugin registers memcue_sessions')

		const result = await tool.execute({ query: 'PROJ-77' }, exec)
		assert.match(result.history, /memcue sessions: passages of earlier sessions, newest first/)
		assert.match(result.history, /history, not facts/)
		assert.match(result.history, /Обсерватория PROJ-77 слушает порт 6432\./)
		assert.match(result.history, /Какой порт у обсерватории PROJ-77\?/)
		for (const canary of ['REASONING-CANARY', 'STREAM-CANARY', 'TOOL-CANARY', 'NOTICE-CANARY',
			'CURRENT-CANARY', 'SUBAGENT-CANARY']) {
			assert.ok(!result.history.includes(canary), `the answer holds ${canary}:\n${result.history}`)
		}
		assert.deepEqual(tool.output.render({}, result), [{ type: 'text', text: result.history }])

		// The note tells the model to fall back to the history, and the shared part of it stays the other hosts' text.
		const text = sections.find((section) => section.name === 'memcue').text
		assert.match(text, /When memcue_search finds nothing, search the history of earlier sessions with memcue_sessions/)
		assert.match(text, /history, not a fact/)

		// Nothing holds the words: the command's own empty answer, not an error.
		const empty = await tool.execute({ query: 'сафран' }, exec)
		assert.match(empty.history, /nothing in the history holds/)
	} finally {
		for (const [key, value] of saved) {
			if (value === undefined) delete process.env[key]
			else process.env[key] = value
		}
		await rm(root, { recursive: true, force: true })
	}
})
