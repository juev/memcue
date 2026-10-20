/**
 * Offline tests for the dsh plugin: a stub binary stands in for memcue and a plain object for
 * the Cordis context, so nothing here needs a model, a base, or a running harness.
 *
 * Run from a checkout that can resolve @deepseek-ai/dsh-llm and @deepseek-ai/dsh-tools:
 *   node --test plugins/deepseek-harness/test/
 */
import assert from 'node:assert/strict'
import { chmod, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { after, before, test } from 'node:test'

import { apply } from '../src/index.ts'

const FACT = 'The harness base is STUB-FACT-7743.'

let dir
let binary
let log

/** A stub memcue: records each call, answers `hook` and `search`, accepts `add`. */
const STUB = `#!/bin/sh
printf 'arg: %s\\n' "$*" >> "$MEMCUE_STUB_LOG"
case "$1" in
  hook)
    payload=$(cat)
    printf 'hook-input: %s\\n' "$payload" >> "$MEMCUE_STUB_LOG"
    case "$payload" in
      *NOTHING-MATCHES*) ;;
      *) printf '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"memcue: stored facts\\\\n- ${FACT} (learnt: test 2026-10-20)\\\\n"}}\\n' ;;
    esac
    ;;
  search)
    printf '[{"id":"f1","content":"${FACT}","origin":"test 2026-10-20"}]\\n'
    ;;
  extract-messages)
    payload=$(cat)
    printf 'extract-input: %s\\n' "$payload" >> "$MEMCUE_STUB_LOG"
    ;;
esac
exit 0
`

before(async () => {
	dir = await mkdtemp(join(tmpdir(), 'memcue-dsh-test-'))
	binary = join(dir, 'memcue-stub.sh')
	log = join(dir, 'calls.log')
	await writeFile(binary, STUB)
	await chmod(binary, 0o755)
	process.env.MEMCUE_STUB_LOG = log
})

after(async () => {
	await rm(dir, { recursive: true, force: true })
	delete process.env.MEMCUE_STUB_LOG
})

/** Minimal Cordis context: records every registration, nothing else. */
function fakeContext() {
	const listeners = new Map()
	const tools = new Map()
	const sections = []
	const ctx = {
		on(event, listener) {
			const list = listeners.get(event) ?? []
			list.push(listener)
			listeners.set(event, list)
			return () => {}
		},
		tools: { register: (definition) => { tools.set(definition.name, definition); return () => {} } },
		systemPrompt: { section: (section) => { sections.push(section); return () => {} } },
	}
	return { ctx, listeners, tools, sections }
}

/** One plugin instance wired to the stub binary. */
function mount(overrides = {}) {
	const { ctx, listeners, tools, sections } = fakeContext()
	apply(ctx, {
		binary,
		facts: join(dir, 'facts.db'),
		stateDir: join(dir, 'state'),
		extract: true,
		minNewChars: 1,
		...overrides,
	})
	return { listeners, tools, sections }
}

/** A user message as the harness shapes it. */
function userMessage(text, source = { kind: 'user' }) {
	return { id: `m-${text.length}`, role: 'user', content: [{ type: 'text', text }], source }
}

/** Run this plugin's pre-step listener over one proposed batch. */
async function preStep(listeners, messages) {
	const [listener] = listeners.get('agent/pre-step')
	const payload = {
		agent: { session: { id: 's1', cwd: dir } },
		messages,
		turn: 1,
		step: 1,
		signal: { aborted: false },
	}
	return listener(payload, async () => ({ kind: 'enter', messages }))
}

/** Poll until the stub log holds the wanted line, or fail. */
async function waitForLog(needle, timeoutMs = 5000) {
	const deadline = Date.now() + timeoutMs
	while (Date.now() < deadline) {
		try {
			const text = await readFile(log, 'utf8')
			if (text.includes(needle)) return text
		} catch {}
		await new Promise((resolve) => setTimeout(resolve, 50))
	}
	throw new Error(`stub log never saw ${needle}`)
}

/** Drop what earlier tests recorded, so a wait cannot match their lines. */
async function resetLog() {
	await writeFile(log, '')
}

/** The payload of the last line the stub recorded with this marker. */
function lastPayload(text, marker) {
	const line = text.split('\n').filter((row) => row.startsWith(marker)).pop()
	if (line === undefined) throw new Error(`no ${marker} line in the stub log`)
	return line.slice(marker.length)
}

test('a matching fact reaches the step as a recall message', async () => {
	const { listeners } = mount()
	const decision = await preStep(listeners, [userMessage('what is the harness base?')])
	assert.equal(decision.kind, 'enter')
	assert.equal(decision.messages.length, 2)
	const recall = decision.messages[1]
	assert.equal(recall.role, 'user')
	assert.equal(recall.source.kind, 'memcue')
	assert.equal(recall.source.form, 'recall')
	assert.match(recall.content[0].text, /STUB-FACT-7743/)
})

test('the binary is asked with the real prompt, not with injected context', async () => {
	await resetLog()
	const { listeners } = mount()
	await preStep(listeners, [
		userMessage('what is the harness base?'),
		userMessage('Current runtime context. Snapshot 42.', { kind: 'runtime-context', form: 'snapshot', sections: [] }),
	])
	const text = await waitForLog('hook-input')
	const input = JSON.parse(lastPayload(text, 'hook-input: '))
	assert.equal(input.prompt, 'what is the harness base?')
	assert.equal(input.session_id, 's1')
	assert.equal(input.cwd, dir)
})

test('no facts mean no message', async () => {
	const { listeners } = mount()
	const decision = await preStep(listeners, [userMessage('NOTHING-MATCHES here')])
	assert.equal(decision.messages.length, 1)
})

test('a turn end sends the new messages to extract-messages', async () => {
	await resetLog()
	const { listeners } = mount()
	const events = [
		{ type: 'turn/start', data: { turn: 1 } },
		{ type: 'user/message', data: userMessage('remember the base port') },
		{ type: 'user/message', data: userMessage('injected', { kind: 'runtime-context', form: 'snapshot', sections: [] }) },
		{ type: 'assistant/message', data: { turn: 1, step: 1, message: { role: 'assistant', content: [{ type: 'text', text: 'noted' }] } } },
		{ type: 'tool/result', data: { turn: 1, step: 1, message: { role: 'tool', content: [{ type: 'text', text: 'PORT=55432\nsecond line' }] } } },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = { id: 's2', seq: events.length, eventAt: (seq) => events[seq] }
	const [listener] = listeners.get('session/event')
	listener(session, events[events.length - 1])
	const text = await waitForLog('extract-input')
	const sent = JSON.parse(lastPayload(text, 'extract-input: '))
	assert.deepEqual(sent, [
		{ role: 'user', content: 'remember the base port' },
		{ role: 'assistant', content: 'noted' },
		{ role: 'tool', content: 'PORT=55432\nsecond line' },
	])
})

test('tool output sent to extraction is capped', async () => {
	await resetLog()
	const { listeners } = mount({ maxToolResultChars: 10 })
	const events = [
		{ type: 'user/message', data: userMessage('look at the port') },
		{ type: 'tool/result', data: { turn: 1, step: 1, message: { role: 'tool', content: [{ type: 'text', text: 'x'.repeat(500) }] } } },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = { id: 's3', seq: events.length, eventAt: (seq) => events[seq] }
	const [listener] = listeners.get('session/event')
	listener(session, events[events.length - 1])
	const text = await waitForLog('extract-input')
	const sent = JSON.parse(lastPayload(text, 'extract-input: '))
	assert.equal(sent[1].role, 'tool')
	assert.equal(sent[1].content.length, 10)
})

test('a missing binary costs the facts, not the step', async () => {
	const { listeners } = mount({ binary: join(dir, 'no-such-memcue') })
	const decision = await preStep(listeners, [userMessage('what is the harness base?')])
	assert.equal(decision.kind, 'enter')
	assert.equal(decision.messages.length, 1)
})

test('the memory note is registered as a system-prompt section', () => {
	const { sections } = mount()
	assert.equal(sections.length, 1)
	assert.equal(sections[0].name, 'memcue')
	assert.match(sections[0].text, /memcue_search/)
})

test('memcue_search returns the facts the binary printed', async () => {
	const { tools } = mount()
	const result = await tools.get('memcue_search').execute({ query: 'harness base' }, {})
	assert.deepEqual(result.facts, [`${FACT} (learnt: test 2026-10-20)`])
})

test('memcue_add reports success and writes nothing itself', async () => {
	await resetLog()
	const { tools } = mount()
	const result = await tools.get('memcue_add').execute({ text: 'A new fact.', keywords: 'new, fact' }, {})
	assert.equal(result.stored, true)
	const text = await waitForLog('arg: add ')
	assert.match(text, /arg: add -origin dsh \d{4}-\d{2}-\d{2} -keywords new, fact A new fact\./)
})
