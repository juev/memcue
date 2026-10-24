/**
 * Offline tests for the dsh plugin: a stub binary stands in for memcue and a plain object for
 * the Cordis context, so nothing here needs a model, a base, or a running harness.
 *
 * Run from a checkout that can resolve @deepseek-ai/dsh-llm and @deepseek-ai/dsh-tools:
 *   node --test plugins/deepseek-harness/test/*.test.mjs
 */
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { chmod, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { homedir, tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { after, before, test } from 'node:test'

import { apply, memcueSessionId } from '../src/index.mjs'

const FACT = 'The harness base is STUB-FACT-7743.'

let dir
let binary
let log

/** A stub memcue: records each call, answers `session-card` with an empty card and `search`, accepts `add`. */
const STUB = `#!/bin/sh
printf 'arg: %s\\n' "$*" >> "$MEMCUE_STUB_LOG"
printf 'env: %s|%s|%s\\n' "$MEMCUE_MODEL" "$MEMCUE_API_URL" "$MEMCUE_API_KEY" >> "$MEMCUE_STUB_LOG"
printf 'facts: %s\\n' "$MEMCUE_FACTS" >> "$MEMCUE_STUB_LOG"
case "$1" in
  version)
    printf '{"version":"0.4.0"}\\n'
    ;;
  session-card)
    payload=$(cat)
    printf 'card-input: %s\\n' "$payload" >> "$MEMCUE_STUB_LOG"
    printf '{"hookSpecificOutput":{"hookEventName":"SessionCard","additionalContext":""}}\\n'
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

/** The messages a real Session.deriveMessages() would return for a fixture event list. */
function derivedMessages(events) {
	const messages = []
	for (const event of events) {
		if (event.type === 'user/message') messages.push(event.data)
		else if (event.type === 'assistant/message' || event.type === 'tool/result') messages.push(event.data.message)
	}
	return messages
}

/** A fake session with the supported derived-message and surface API, without deprecated eventAt. */
function fakeSession(id, events, extra = {}) {
	const messageEvents = events.filter((event) => event.type === 'user/message'
		|| event.type === 'assistant/message'
		|| event.type === 'tool/result')
	return {
		id,
		seq: events.length,
		deriveMessages: () => derivedMessages(messageEvents),
		surface: { nodes: messageEvents.map((event) => events.indexOf(event)) },
		...extra,
	}
}

/** Run this plugin's pre-step listener over one proposed batch. */
async function preStep(listeners, messages, session = { id: 's1', header: { cwd: dir } }) {
	const [listener] = listeners.get('agent/pre-step')
	const payload = {
		agent: { session },
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

/** Wait until a file contains a needle. */
async function waitForText(path, needle, timeoutMs = 5000) {
	const deadline = Date.now() + timeoutMs
	while (Date.now() < deadline) {
		try {
			const text = await readFile(path, 'utf8')
			if (text.includes(needle)) return text
		} catch {}
		await new Promise((resolve) => setTimeout(resolve, 50))
	}
	throw new Error(`${path} never held ${needle}`)
}

/** Wait until a state file parses and satisfies a predicate. */
async function waitForState(path, predicate, timeoutMs = 5000) {
	const deadline = Date.now() + timeoutMs
	let last
	while (Date.now() < deadline) {
		try {
			last = JSON.parse(await readFile(path, 'utf8'))
			if (predicate(last)) return last
		} catch {}
		await new Promise((resolve) => setTimeout(resolve, 50))
	}
	throw new Error(`${path} never matched; last=${JSON.stringify(last)}`)
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

test('no fact comes with a prompt: the model searches itself (#361)', async () => {
	await resetLog()
	const { listeners } = mount()
	const decision = await preStep(listeners, [userMessage('what is the harness base?')])
	assert.equal(decision.kind, 'enter')
	assert.equal(decision.messages.length, 1)
	const text = await waitForLog('card-input')
	assert.equal(text.includes('arg: hook'), false)
})

test('the card comes once and again after compaction', async () => {
	const cardLog = join(dir, 'card-85.log')
	const cardBinary = join(dir, 'memcue-card-85.sh')
	await writeFile(cardBinary, `#!/bin/sh
case "$1" in
  version)
    printf '{"version":"0.4.0"}\\n'
    ;;
  session-card)
    payload=$(cat)
    printf 'card-input: %s\\n' "$payload" >> "${cardLog}"
    printf '{"hookSpecificOutput":{"hookEventName":"SessionCard","additionalContext":"memcue: rules card\\\\n"}}\\n'
    ;;
esac
exit 0
`)
	await chmod(cardBinary, 0o755)
	const { listeners } = mount({ binary: cardBinary })
	const session = { id: 's85', header: { cwd: dir } }

	const first = await preStep(listeners, [userMessage('question one')], session)
	assert.equal(first.messages.length, 2)
	assert.equal(first.messages[1].source.form, 'notice')

	const second = await preStep(listeners, [userMessage('question two')], session)
	assert.equal(second.messages.length, 1)
	let cardLines = (await readFile(cardLog, 'utf8')).split('\n').filter((line) => line.startsWith('card-input: '))
	assert.equal(cardLines.length, 1)

	for (const listener of listeners.get('session/event') ?? []) {
		listener(session, { type: 'compaction/end', data: {} })
	}
	const third = await preStep(listeners, [userMessage('question three')], session)
	assert.equal(third.messages.length, 2)
	cardLines = (await readFile(cardLog, 'utf8')).split('\n').filter((line) => line.startsWith('card-input: '))
	assert.equal(cardLines.length, 2)
	const sources = cardLines.map((line) => JSON.parse(line.slice('card-input: '.length)).source)
	assert.deepEqual(sources, ['startup', 'compact'])
})

test('the card is asked for the session and its cwd', async () => {
	await resetLog()
	const { listeners } = mount()
	await preStep(listeners, [
		userMessage('what is the harness base?'),
		userMessage('Current runtime context. Snapshot 42.', { kind: 'runtime-context', form: 'snapshot', sections: [] }),
	], { id: 's-card-input', header: { cwd: dir } })
	const text = await waitForLog('card-input')
	const input = JSON.parse(lastPayload(text, 'card-input: '))
	assert.equal(input.session_id, 's-card-input')
	assert.equal(input.cwd, dir)
	assert.equal(input.source, 'startup')
})

test('the session header cwd wins over the process cwd', async () => {
	await resetLog()
	const { listeners } = mount()
	const workspace = join(dir, 'session-workspace')
	await preStep(listeners, [userMessage('what is the harness base?')], {
		id: 's-workspace',
		header: { cwd: workspace },
	})
	const text = await waitForLog('card-input')
	assert.equal(JSON.parse(lastPayload(text, 'card-input: ')).cwd, workspace)
})

test('memcueSessionId keeps a valid id and hashes anything else', () => {
	assert.equal(memcueSessionId('s1'), 's1')
	assert.equal(memcueSessionId('a.b_c-d'), 'a.b_c-d')
	assert.equal(memcueSessionId('a'.repeat(128)), 'a'.repeat(128))

	// Hermes' rule: anything outside [A-Za-z0-9._-], ".", "..", or over 128 chars is a sha256 prefix.
	const hashed = (id) => createHash('sha256').update(id).digest('hex').slice(0, 32)
	for (const invalid of ['session:129', 'a'.repeat(129), '', '.', '..']) {
		const scope = memcueSessionId(invalid)
		assert.match(scope, /^[A-Za-z0-9._-]{1,128}$/)
		assert.equal(scope, hashed(invalid))
		assert.equal(memcueSessionId(invalid), scope) // deterministic across prompts
	}
})

test('a colon-containing session id reaches memcue sanitized', async () => {
	await resetLog()
	const { listeners } = mount()
	const session = { id: 'session:129 with spaces', header: { cwd: dir } }
	const scope = memcueSessionId(session.id)
	assert.match(scope, /^[A-Za-z0-9._-]{1,128}$/)
	assert.notEqual(scope, session.id)
	await preStep(listeners, [userMessage('what is the harness base?')], session)
	const text = await waitForLog('card-input')
	assert.equal(JSON.parse(lastPayload(text, 'card-input: ')).session_id, scope)
})

test('a session id over 128 characters reaches the card and extraction sanitized', async () => {
	await resetLog()
	const stateDir = join(dir, 'state-129-long')
	const { listeners } = mount({ stateDir, minNewChars: 1 })
	const raw = `long-${'x'.repeat(200)}`
	const scope = memcueSessionId(raw)
	assert.match(scope, /^[A-Za-z0-9._-]{1,128}$/)

	const decision = await preStep(listeners, [userMessage('what is the harness base?')], {
		id: raw,
		header: { cwd: dir },
	})
	assert.equal(decision.messages.length, 1) // the default stub's card is empty
	const cardText = await waitForLog('card-input')
	assert.equal(JSON.parse(lastPayload(cardText, 'card-input: ')).session_id, scope)

	const events = [
		{ type: 'user/message', data: userMessage('remember the long session id') },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession(raw, events)
	const [turnEnd] = listeners.get('session/event')
	turnEnd(session, events.at(-1))
	const extractText = await waitForLog(`arg: extract-messages -session ${scope} `)
	assert.equal(extractText.includes(`-session ${raw}`), false)
	await waitForState(join(stateDir, `${scope}.json`),
		(state) => state.pending === null && Object.keys(state.seen).length === 1)
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
	const session = fakeSession('s2', events, { header: { cwd: dir } })
	const [listener] = listeners.get('session/event')
	listener(session, events[events.length - 1])
	const text = await waitForLog('extract-input')
	assert.ok(text.includes(`arg: extract-messages -session s2 -event turn-end -cwd ${dir} -min-chars 1`), text)
	const sent = JSON.parse(lastPayload(text, 'extract-input: '))
	assert.deepEqual(sent, [
		{ role: 'user', content: 'remember the base port' },
		{ role: 'assistant', content: 'noted' },
		{ role: 'tool', content: 'PORT=55432\nsecond line' },
	])
})

test('a short tail is flushed on session/disposed', async () => {
	await resetLog()
	const stateDir = join(dir, 'state-88')
	const { listeners } = mount({ stateDir, minNewChars: 1000 })
	const events = [{ type: 'user/message', data: userMessage('short tail') }]
	const session = fakeSession('s88', events)

	const [turnEnd] = listeners.get('session/event')
	turnEnd(session, { type: 'turn/end', data: { turn: 1, reason: 'stop' } })
	await new Promise((resolve) => setTimeout(resolve, 150))
	assert.equal((await readFile(log, 'utf8')).includes('extract-input'), false)

	const [disposed] = listeners.get('session/disposed')
	disposed(session)
	const text = await waitForLog('extract-input')
	assert.deepEqual(JSON.parse(lastPayload(text, 'extract-input: ')), [{ role: 'user', content: 'short tail' }])
	await waitForState(join(stateDir, 's88.json'),
		(state) => state.pending === null && Object.keys(state.seen).length === 1)
})

test('the extraction checkpoint is private', { skip: process.platform === 'win32' }, async () => {
	await resetLog()
	const stateDir = join(dir, 'state-modes')
	const { listeners } = mount({ stateDir, minNewChars: 1 })
	const events = [{ type: 'user/message', data: userMessage('private batch') }]
	const session = fakeSession('smodes', events)

	const [turnEnd] = listeners.get('session/event')
	turnEnd(session, { type: 'turn/end', data: { turn: 1, reason: 'stop' } })
	const path = join(stateDir, 'smodes.json')
	await waitForState(path, (state) => state.pending === null && Object.keys(state.seen).length === 1)

	const file = await stat(path)
	assert.equal(file.mode & 0o777, 0o600)
	const folder = await stat(stateDir)
	assert.equal(folder.mode & 0o777, 0o700)
})

test('tool calls travel with the assistant message', async () => {
	await resetLog()
	const { listeners } = mount()
	const events = [
		{ type: 'user/message', data: userMessage('run the check') },
		{
			type: 'assistant/message',
			data: {
				turn: 1,
				step: 1,
				message: {
					role: 'assistant',
					content: [
						{ type: 'text', text: 'checking' },
						{ type: 'tool-call', id: 'call-1', name: 'bash', arguments: '{"cmd":"ls"}' },
					],
				},
			},
		},
		{ type: 'tool/result', data: { turn: 1, step: 1, message: { role: 'tool', content: [{ type: 'text', text: 'a.txt' }] } } },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession('s87', events)
	const [listener] = listeners.get('session/event')
	listener(session, events.at(-1))
	const text = await waitForLog('extract-input')
	const sent = JSON.parse(lastPayload(text, 'extract-input: '))
	assert.equal(sent[1].role, 'assistant')
	assert.equal(sent[1].content, 'checking')
	assert.equal(sent[1].tool_calls[0].function.name, 'bash')
	assert.equal(sent[1].tool_calls[0].function.arguments, '{"cmd":"ls"}')
	assert.deepEqual(sent[2], { role: 'tool', content: 'a.txt' })
})

test('a tool-only assistant message is kept', async () => {
	await resetLog()
	const { listeners } = mount()
	const events = [
		{ type: 'user/message', data: userMessage('look') },
		{
			type: 'assistant/message',
			data: {
				turn: 1,
				step: 1,
				message: {
					role: 'assistant',
					content: [{ type: 'tool-call', id: 'call-2', name: 'read', arguments: '{"path":"a.txt"}' }],
				},
			},
		},
		{ type: 'tool/result', data: { turn: 1, step: 1, message: { role: 'tool', content: [{ type: 'text', text: 'contents' }] } } },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession('s87b', events)
	const [listener] = listeners.get('session/event')
	listener(session, events.at(-1))
	const text = await waitForLog('extract-input')
	const sent = JSON.parse(lastPayload(text, 'extract-input: '))
	assert.equal(sent[1].content, '')
	assert.equal(sent[1].tool_calls[0].function.name, 'read')
})

test('a failed extraction keeps the batch and retries it', async () => {
	const stateDir = join(dir, 'state-81-fail')
	const failLog = join(dir, 'fail-81.log')
	const count = join(dir, 'fail-81.count')
	const failBinary = join(dir, 'memcue-fail-81.sh')
	await writeFile(failBinary, `#!/bin/sh
case "$1" in
  extract-messages)
    n=$(cat "${count}" 2>/dev/null || echo 0)
    n=$((n+1)); printf '%s' "$n" > "${count}"
    cat >> "${failLog}"; printf '\\n' >> "${failLog}"
    if [ "$n" -eq 1 ]; then exit 7; fi
    ;;
esac
exit 0
`)
	await chmod(failBinary, 0o755)

	const { listeners } = mount({ binary: failBinary, stateDir, minNewChars: 1 })
	const events = [
		{ type: 'user/message', data: userMessage('remember the durable fact') },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession('s81', events)
	const [listener] = listeners.get('session/event')

	listener(session, events.at(-1))
	await waitForText(failLog, 'remember the durable fact')
	const pending = JSON.parse(await readFile(join(stateDir, 's81.json'), 'utf8'))
	assert.deepEqual(pending.seen, {})
	assert.equal(pending.pending.messages[0].content, 'remember the durable fact')
	assert.equal(pending.pending.fingerprints.length, 1)

	listener(session, events.at(-1))
	await waitForState(join(stateDir, 's81.json'),
		(state) => state.pending === null && Object.keys(state.seen).length === 1)
	const calls = (await readFile(failLog, 'utf8')).trim().split('\n')
	assert.equal(calls.length, 2)
	assert.deepEqual(JSON.parse(calls[0]), JSON.parse(calls[1]))
})

test('a pending batch is retried when the plugin starts', async () => {
	await resetLog()
	const stateDir = join(dir, 'state-retry-161')
	await mkdir(stateDir, { recursive: true })
	const path = join(stateDir, 's-retry.json')
	await writeFile(path, JSON.stringify({
		seen: {},
		pending: {
			messages: [{ role: 'user', content: 'a fact from the disposed session' }],
			fingerprints: ['fp-retry'],
		},
		cwd: dir,
	}))
	mount({ stateDir, minNewChars: 1 })
	const text = await waitForLog('extract-input')
	assert.ok(text.includes(`arg: extract-messages -session s-retry -event retry -cwd ${dir} -min-chars 1`), text)
	await waitForState(path, (state) => state.pending === null && Object.keys(state.seen).length === 1)
})

test('a legacy seq state is migrated without re-extracting the history', async () => {
	await resetLog()
	const stateDir = join(dir, 'state-81-old')
	await mkdir(stateDir, { recursive: true })
	await writeFile(join(stateDir, 's81old.json'), JSON.stringify({ seq: 1 }))

	const { listeners } = mount({ stateDir, minNewChars: 1 })
	const events = [
		{ type: 'user/message', data: userMessage('old message') },
		{ type: 'user/message', data: userMessage('new message') },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession('s81old', events)
	const [listener] = listeners.get('session/event')
	listener(session, events.at(-1))
	await waitForState(join(stateDir, 's81old.json'),
		(state) => state.pending === null && Object.keys(state.seen).length === 2)
	assert.equal((await readFile(log, 'utf8')).includes('extract-input'), false)

	const nextEvents = [...events, { type: 'user/message', data: userMessage('third message') },
		{ type: 'turn/end', data: { turn: 2, reason: 'stop' } }]
	const next = fakeSession('s81old', nextEvents)
	listener(next, nextEvents.at(-1))
	const text = await waitForLog('extract-input')
	assert.deepEqual(JSON.parse(lastPayload(text, 'extract-input: ')), [{ role: 'user', content: 'third message' }])
})

test('extraction sees only direct user messages', async () => {
	await resetLog()
	const { listeners } = mount()
	const events = [
		{ type: 'user/message', data: userMessage('the real prompt') },
		{ type: 'user/message', data: userMessage('compacted summary body', { kind: 'compact-checkpoint', compactionId: 'c1' }) },
		{ type: 'user/message', data: userMessage('scheduled reminder body', { kind: 'schedule' }) },
		{ type: 'user/message', data: userMessage('goal round body', { kind: 'goal', goalId: 'g1' }) },
		{ type: 'user/message', data: userMessage('hook context body', { kind: 'hooks-claude-code' }) },
		{ type: 'user/message', data: userMessage('memcue recall body', { kind: 'memcue', form: 'recall' }) },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession('s82', events)
	const [listener] = listeners.get('session/event')
	listener(session, events.at(-1))

	const text = await waitForLog('extract-input')
	assert.deepEqual(JSON.parse(lastPayload(text, 'extract-input: ')), [{ role: 'user', content: 'the real prompt' }])
})

test('promptOf ignores goal, schedule and checkpoint messages', async () => {
	await resetLog()
	const { listeners } = mount()
	const decision = await preStep(listeners, [
		userMessage('the real human question'),
		userMessage('goal round body', { kind: 'goal', goalId: 'g1' }),
		userMessage('scheduled reminder body', { kind: 'schedule' }),
		userMessage('compacted summary body', { kind: 'compact-checkpoint', compactionId: 'c1' }),
	], { id: 's-prompt-of', header: { cwd: dir } })
	await waitForLog('card-input') // the real question lets the card through
	assert.equal(decision.messages.length, 4) // the four inputs; the default stub's card is empty
})

test('a batch of only injected messages does not ask for the card', async () => {
	await resetLog()
	const { listeners } = mount()
	const decision = await preStep(listeners, [
		userMessage('goal round body', { kind: 'goal', goalId: 'g1' }),
		userMessage('scheduled reminder body', { kind: 'schedule' }),
	])
	assert.equal(decision.messages.length, 2)
	await new Promise((resolve) => setTimeout(resolve, 150))
	assert.equal((await readFile(log, 'utf8')).includes('card-input'), false)
})

test('a subagent session is not extracted', async () => {
	await resetLog()
	const stateDir = join(dir, 'state-82-subagent')
	const { listeners } = mount({ stateDir })
	const events = [
		{ type: 'user/message', data: userMessage('subagent work') },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession('s82sub', events, { header: { origin: 'subagent' } })
	const [listener] = listeners.get('session/event')
	listener(session, events.at(-1))
	await new Promise((resolve) => setTimeout(resolve, 150))
	await assert.rejects(readFile(join(stateDir, 's82sub.json'), 'utf8'), /ENOENT/)
})

test('a forked session starts at the inherited prefix', async () => {
	await resetLog()
	const stateDir = join(dir, 'state-82-fork')
	const { listeners } = mount({ stateDir })
	const events = [
		{ type: 'user/message', data: userMessage('parent message') },
		{ type: 'user/message', data: userMessage('fork message') },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession('s82fork', events, { inheritedEventCount: 1 })
	const [listener] = listeners.get('session/event')
	listener(session, events.at(-1))
	const text = await waitForLog('extract-input')
	assert.deepEqual(JSON.parse(lastPayload(text, 'extract-input: ')), [{ role: 'user', content: 'fork message' }])
})

test('tool output sent to extraction is capped', async () => {
	await resetLog()
	const { listeners } = mount({ maxToolResultChars: 10 })
	const events = [
		{ type: 'user/message', data: userMessage('look at the port') },
		{ type: 'tool/result', data: { turn: 1, step: 1, message: { role: 'tool', content: [{ type: 'text', text: 'x'.repeat(500) }] } } },
		{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
	]
	const session = fakeSession('s3', events)
	const [listener] = listeners.get('session/event')
	listener(session, events[events.length - 1])
	const text = await waitForLog('extract-input')
	const sent = JSON.parse(lastPayload(text, 'extract-input: '))
	assert.equal(sent[1].role, 'tool')
	assert.equal(sent[1].content.length, 10)
})

test('a ~ in facts is expanded and a relative facts path is rejected', async () => {
	await resetLog()
	const { listeners } = mount({ facts: '~/memcue/facts.db' })
	await preStep(listeners, [userMessage('hello there')])
	const text = await waitForLog('facts: ')
	assert.ok(text.includes(`facts: ${join(homedir(), 'memcue', 'facts.db')}`), text)
	assert.throws(() => mount({ facts: 'relative/facts.db' }), /facts must be an absolute path/)
})

test('a checkpoint is not reused for another fact base', async () => {
	const state = join(dir, 'state-187')
	const first = join(dir, 'base-a.db')
	const second = join(dir, 'base-b.db')
	const statePath = join(state, 's187.json')
	const extract = async (facts) => {
		const { listeners } = mount({ facts, stateDir: state })
		const events = [
			{ type: 'user/message', data: userMessage('remember the base port') },
			{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
		]
		const session = fakeSession('s187', events, { header: { cwd: dir } })
		const [listener] = listeners.get('session/event')
		listener(session, events[events.length - 1])
	}
	await resetLog()
	await extract(first)
	// The first write holds the pending batch; wait for the one after the extraction, or the second instance starts
	// while the first still writes the checkpoint (#261).
	await waitForText(statePath, '"pending":null')
	const before = ((await readFile(log, 'utf8')).match(/extract-input:/g) ?? []).length
	assert.equal(before, 1)
	// The same session against another base must send its messages again: the fingerprints belonged to the first base.
	await extract(second)
	const deadline = Date.now() + 5000
	let after = before
	while (Date.now() < deadline && after === before) {
		after = ((await readFile(log, 'utf8')).match(/extract-input:/g) ?? []).length
		await new Promise((resolve) => setTimeout(resolve, 50))
	}
	assert.equal(after, before + 1)
	const stateText = await readFile(statePath, 'utf8')
	assert.ok(stateText.includes(resolve(second)), stateText)
})

test('an older binary turns the card and extraction off with one warning', async () => {
	const oldBinary = join(dir, 'memcue-old.sh')
	await writeFile(oldBinary, `#!/bin/sh
case "$1" in
  version) printf '{"version":"0.3.0"}\\n' ;;
  session-card) printf '{"hookSpecificOutput":{"hookEventName":"SessionCard","additionalContext":"card"}}\\n' ;;
esac
exit 0
`)
	await chmod(oldBinary, 0o755)
	const warnings = []
	const original = console.warn
	console.warn = (...args) => warnings.push(args.join(' '))
	try {
		await resetLog()
		const { listeners } = mount({ binary: oldBinary, minNewChars: 1 })
		const deadline = Date.now() + 5000
		while (!warnings.some((warning) => warning.includes('older than 0.4.0')) && Date.now() < deadline) {
			await new Promise((resolve) => setTimeout(resolve, 50))
		}
		const decision = await preStep(listeners, [userMessage('hello there')])
		assert.equal(decision.messages.length, 1, 'the card must not be delivered')
		const events = [
			{ type: 'user/message', data: userMessage('remember the port') },
			{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
		]
		const session = fakeSession('s186', events, { header: { cwd: dir } })
		const [listener] = listeners.get('session/event')
		listener(session, events[events.length - 1])
		await new Promise((resolve) => setTimeout(resolve, 200))
		const logText = await readFile(log, 'utf8').catch(() => '')
		assert.ok(!logText.includes('extract-input'), 'extraction must stay off')
		assert.ok(warnings.some((warning) => warning.includes('older than 0.4.0')), warnings.join('\n'))
	} finally {
		console.warn = original
	}
})

test('a failed card is reported once and the step still runs', async () => {
	const failBinary = join(dir, 'card-fail.sh')
	await writeFile(failBinary, `#!/bin/sh
case "$1" in
  version) printf '{"version":"0.4.0"}\\n' ;;
  session-card) echo 'base is locked' >&2; exit 1 ;;
esac
exit 0
`)
	await chmod(failBinary, 0o755)
	const warnings = []
	const original = console.warn
	console.warn = (...args) => warnings.push(args.join(' '))
	try {
		const { listeners } = mount({ binary: failBinary })
		const first = await preStep(listeners, [userMessage('hello there')])
		const second = await preStep(listeners, [userMessage('again')])
		assert.equal(first.messages.length, 1)
		assert.equal(second.messages.length, 1)
		const cardWarnings = warnings.filter((warning) => warning.includes('session-card failed'))
		assert.equal(cardWarnings.length, 1, warnings.join('\n'))
		assert.match(cardWarnings[0], /base is locked/)
	} finally {
		console.warn = original
	}
})

test('a failed card is retried on the next step', async () => {
	const counter = join(dir, 'card-190.count')
	process.env.MEMCUE_CARD_COUNT = counter
	const cardBinary = join(dir, 'card-retry.sh')
	await writeFile(cardBinary, `#!/bin/sh
case "$1" in
  version) printf '{"version":"0.4.0"}\\n' ;;
  session-card)
    n=$(cat "$MEMCUE_CARD_COUNT" 2>/dev/null || echo 0)
    n=$((n+1))
    echo "$n" > "$MEMCUE_CARD_COUNT"
    if [ "$n" -eq 1 ]; then echo 'transient lock' >&2; exit 1; fi
    printf '{"hookSpecificOutput":{"hookEventName":"SessionCard","additionalContext":"retried card"}}\\n'
    ;;
esac
exit 0
`)
	await chmod(cardBinary, 0o755)
	try {
		const { listeners } = mount({ binary: cardBinary })
		const first = await preStep(listeners, [userMessage('hello there')])
		assert.equal(first.messages.length, 1, 'the failed card must not be delivered')
		const second = await preStep(listeners, [userMessage('again')])
		assert.equal(second.messages.length, 2, 'the card must arrive on the retry')
		assert.match(second.messages[1].content[0].text, /retried card/)
	} finally {
		delete process.env.MEMCUE_CARD_COUNT
	}
})

test('MEMCUE_EXTRACT=0 turns extraction off even when the profile asks for it', async () => {
	const previous = process.env.MEMCUE_EXTRACT
	process.env.MEMCUE_EXTRACT = '0'
	try {
		await resetLog()
		const { listeners } = mount({ extract: true })
		const events = [
			{ type: 'user/message', data: userMessage('remember the port') },
			{ type: 'turn/end', data: { turn: 1, reason: 'stop' } },
		]
		const session = fakeSession('s185', events, { header: { cwd: dir } })
		const [listener] = listeners.get('session/event')
		listener(session, events[events.length - 1])
		await new Promise((resolve) => setTimeout(resolve, 200))
		const logText = await readFile(log, 'utf8').catch(() => '')
		assert.ok(!logText.includes('extract-input'), 'the off switch must win over extract: true')
	} finally {
		if (previous === undefined) delete process.env.MEMCUE_EXTRACT
		else process.env.MEMCUE_EXTRACT = previous
	}
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

test('memcue_search returns facts with their ids', async () => {
	const { tools } = mount()
	const result = await tools.get('memcue_search').execute({ query: 'harness base' }, {})
	assert.deepEqual(result.facts, [{ id: 'f1', content: FACT, origin: 'test 2026-10-20' }])
})

test('memcue_search passes a limit and a leading dash safely', async () => {
	await resetLog()
	const { tools } = mount()
	await tools.get('memcue_search').execute({ query: '-dash query', limit: 3 }, {})
	const text = await waitForLog('arg: search ')
	assert.match(text, new RegExp(`arg: search -json -n 3 -cwd ${process.cwd()} -- -dash query`))
})

test('memcue_search passes a leading dash as a query', async () => {
	await resetLog()
	const { tools } = mount()
	await tools.get('memcue_search').execute({ query: '-v' }, {})
	const text = await waitForLog('arg: search ')
	assert.match(text, new RegExp(`arg: search -json -n 5 -cwd ${process.cwd()} -- -v`))
})

test('memcue_search passes the session cwd and id for the tie order and the search log', async () => {
	await resetLog()
	const { tools } = mount()
	const exec = { agent: { session: { id: 'session:7', header: { cwd: '/repo/demo' } } } }
	await tools.get('memcue_search').execute({ query: 'harness base' }, exec)
	const text = await waitForLog('arg: search ')
	assert.ok(text.includes(`arg: search -json -n 5 -cwd /repo/demo -session ${memcueSessionId('session:7')} -- harness base`), text)
})

test('memcue_add sends provenance, expiry, cwd and a separator', async () => {
	await resetLog()
	const { tools } = mount()
	const exec = { agent: { session: { header: { cwd: '/repo/demo' } } } }
	const result = await tools.get('memcue_add').execute({
		text: 'A new fact.',
		keywords: 'new, fact',
		expires: '2026-10-21',
		kind: 'rule',
		scope: 'global',
	}, exec)
	assert.equal(result.stored, true)
	const text = await waitForLog('arg: add ')
	assert.match(text, /arg: add -source agent -cwd \/repo\/demo -keywords new, fact -expires 2026-10-21 -kind rule -scope global -- A new fact\./)
})

test('memcue_add stores a fact that starts with a dash', async () => {
	await resetLog()
	const { tools } = mount()
	await tools.get('memcue_add').execute({ text: '-v prints the version' }, {})
	const text = await waitForLog('arg: add ')
	assert.match(text, /-- -v prints the version/)
})

test('memcue_add takes an explicit origin', async () => {
	await resetLog()
	const { tools } = mount()
	await tools.get('memcue_add').execute({ text: 'A fact.', origin: 'demo 2026-10-21' }, {})
	const text = await waitForLog('arg: add ')
	assert.match(text, /-origin demo 2026-10-21/)
})

test('memcue_add rejects an empty text', async () => {
	const { tools } = mount()
	const result = await tools.get('memcue_add').execute({ text: '  ' }, {})
	assert.equal(result.stored, false)
	assert.match(result.message, /text is required/)
})

test('memcue_correct and memcue_retire pass the id and text', async () => {
	await resetLog()
	const { tools } = mount()
	const corrected = await tools.get('memcue_correct').execute({
		id: 'f1',
		text: 'Corrected fact.',
		keywords: 'fixed',
		expires: '2026-10-21',
	}, {})
	assert.equal(corrected.stored, true)
	let text = await waitForLog('arg: correct ')
	assert.match(text, /arg: correct -keywords fixed -expires 2026-10-21 -- f1 Corrected fact\./)

	const retired = await tools.get('memcue_retire').execute({ id: 'f1' }, {})
	assert.equal(retired.stored, true)
	text = await waitForLog('arg: retire ')
	assert.match(text, /arg: retire -- f1/)
})

test('model, endpoint and key from config reach the memcue child', async () => {
	await resetLog()
	const { listeners } = mount({
		model: 'deepseek-flash',
		apiUrl: 'https://api.deepseek.com',
		apiKey: 'sk-test',
	})
	await preStep(listeners, [userMessage('what is the harness base?')])
	const text = await waitForLog('env: ')
	assert.match(text, /env: deepseek-flash\|https:\/\/api\.deepseek\.com\|sk-test/)
})

test('a configured model wins over the ambient one', async () => {
	const previous = process.env.MEMCUE_MODEL
	process.env.MEMCUE_MODEL = 'ambient-model'
	try {
		await resetLog()
		const { listeners } = mount({ model: 'configured-model' })
		await preStep(listeners, [userMessage('what is the harness base?')])
		const text = await waitForLog('env: ')
		assert.match(text, /env: configured-model\|/)
	} finally {
		if (previous === undefined) delete process.env.MEMCUE_MODEL
		else process.env.MEMCUE_MODEL = previous
	}
})

test('an absent model leaves the ambient one in place', async () => {
	const previous = process.env.MEMCUE_MODEL
	process.env.MEMCUE_MODEL = 'ambient-model'
	try {
		await resetLog()
		const { listeners } = mount()
		await preStep(listeners, [userMessage('what is the harness base?')])
		const text = await waitForLog('env: ')
		assert.match(text, /env: ambient-model\|/)
	} finally {
		if (previous === undefined) delete process.env.MEMCUE_MODEL
		else process.env.MEMCUE_MODEL = previous
	}
})

test('a bad searchLimit names the field instead of falling back', () => {
	assert.throws(() => mount({ searchLimit: 0 }), /searchLimit/)
})

test('a non-boolean extract is rejected', () => {
	assert.throws(() => mount({ extract: 'no' }), /extract/)
})

test('a non-string model is rejected', () => {
	assert.throws(() => mount({ model: 42 }), /model/)
})

test('an incomplete dsh context fails loud', () => {
	assert.throws(() => apply({}, {}), /lacks the event, tool or system-prompt service/)
})

test('the package is a dsh bundle with a loadable entry', async () => {
	const pkg = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'))
	assert.equal(pkg.dsh?.bundle?.patch, './cordis.patch.yml')
	assert.equal(pkg.exports, './src/index.mjs')
	assert.equal(pkg.peerDependencies?.['@deepseek-ai/dsh-llm'], '>=0.2.0-rc.2 <0.3.0')
	assert.equal(pkg.peerDependencies?.['@deepseek-ai/dsh-tools'], '>=0.2.0-rc.2 <0.3.0')
	const patch = await readFile(new URL('../cordis.patch.yml', import.meta.url), 'utf8')
	assert.match(patch, /- id: memcue/)
	assert.match(patch, /name: \.\/src\/index\.mjs/)
	const source = await readFile(new URL('../src/index.mjs', import.meta.url), 'utf8')
	assert.doesNotMatch(source, /\.eventAt\(|snapshotEvents/)
})

test('the repository root is installable without a path parameter', async () => {
	const rootUrl = new URL('../../../package.json', import.meta.url)
	const root = JSON.parse(await readFile(rootUrl, 'utf8'))
	const rootDir = new URL('./', rootUrl)
	assert.equal(root.dsh?.bundle?.patch, 'plugins/deepseek-harness/cordis.patch.yml')
	assert.equal(root.exports, './plugins/deepseek-harness/src/index.mjs')
	assert.equal(root.peerDependencies?.['@deepseek-ai/dsh-llm'], '>=0.2.0-rc.2 <0.3.0')
	assert.equal(root.peerDependencies?.['@deepseek-ai/dsh-tools'], '>=0.2.0-rc.2 <0.3.0')
	await readFile(new URL(root.dsh.bundle.patch, rootDir), 'utf8')
	await readFile(new URL(root.exports, rootDir), 'utf8')
})
