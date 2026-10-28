/**
 * Offline tests for the dsh plugin: a stub binary stands in for memcue and a plain object for
 * the Cordis context, so nothing here needs a model, a base, or a running harness.
 *
 * Run from a checkout that can resolve @deepseek-ai/dsh-llm and @deepseek-ai/dsh-tools:
 *   node --test plugins/deepseek-harness/test/*.test.mjs
 */
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { chmod, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
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
    printf '{"version":"0.8.0"}\\n'
    ;;
  session-card)
    payload=$(cat)
    printf 'card-input: %s\\n' "$payload" >> "$MEMCUE_STUB_LOG"
    printf '{"hookSpecificOutput":{"hookEventName":"SessionCard","additionalContext":""}}\\n'
    ;;
  search)
    case "$*" in
      *shelved*) printf '{"facts":[],"hints":["1 fact on the shelf matches: -in shelf:research"]}\\n' ;;
      *) printf '[{"id":"f1","content":"${FACT}","origin":"test 2026-10-20"}]\\n' ;;
    esac
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
		...overrides,
	})
	return { listeners, tools, sections }
}

/** A user message as the harness shapes it. */
function userMessage(text, source = { kind: 'user' }) {
	return { id: `m-${text.length}`, role: 'user', content: [{ type: 'text', text }], source }
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

test('a session id over 128 characters reaches the card sanitized', async () => {
	await resetLog()
	const { listeners } = mount()
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

test('a ~ in facts is expanded and a relative facts path is rejected', async () => {
	await resetLog()
	const { listeners } = mount({ facts: '~/memcue/facts.db' })
	await preStep(listeners, [userMessage('hello there')])
	const text = await waitForLog('facts: ')
	assert.ok(text.includes(`facts: ${join(homedir(), 'memcue', 'facts.db')}`), text)
	assert.throws(() => mount({ facts: 'relative/facts.db' }), /facts must be an absolute path/)
})

test('an older binary turns the card off with one warning', async () => {
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
		const { listeners } = mount({ binary: oldBinary })
		const deadline = Date.now() + 5000
		while (!warnings.some((warning) => warning.includes('older than 0.4.0')) && Date.now() < deadline) {
			await new Promise((resolve) => setTimeout(resolve, 50))
		}
		const decision = await preStep(listeners, [userMessage('hello there')])
		assert.equal(decision.messages.length, 1, 'the card must not be delivered')
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
	// Nothing is extracted any more: the note tells the model to store what it learns itself (#494).
	assert.match(sections[0].text, /You keep this memory yourself/)
	assert.doesNotMatch(sections[0].text, /extracted from the session automatically/)
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

test('memcue_search hands over the hints of the binary (#453)', async () => {
	const { tools } = mount()
	const search = tools.get('memcue_search')
	const result = await search.execute({ query: 'shelved words' }, {})
	assert.deepEqual(result.facts, [])
	assert.equal(result.hint, '1 fact on the shelf matches: -in shelf:research')
	const [part] = search.output.render({}, result)
	assert.match(part.text, /-in shelf:research/)
})

test('memcue_search hints on a Chinese query (#410)', async () => {
	const { tools } = mount()
	const search = tools.get('memcue_search')
	const result = await search.execute({ query: '如何 harness base' }, {})
	assert.match(result.hint, /Russian and English/)
	assert.equal((await search.execute({ query: 'harness base' }, {})).hint, undefined)
	const [part] = search.output.render({}, result)
	assert.match(part.text, /Russian and English/)
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

test('memcue_search passes the search controls (#409)', async () => {
	const log = join(dir, 'search-014.log')
	const binary = join(dir, 'memcue-014.sh')
	await writeFile(binary, `#!/bin/sh
case "$1" in
  version) printf '{"version":"0.14.0"}\\n' ;;
  search) printf '%s\\n' "$*" >> "${log}"; printf '[]\\n' ;;
esac
exit 0
`)
	await chmod(binary, 0o755)
	const { tools } = mount({ binary })
	await tools.get('memcue_search').execute({ query: 'harness base', in: 'project,host', kind: 'rule,decision', since: '2026-10-01', expired: true, words: 'all', min_score: 0 }, {})
	assert.equal((await readFile(log, 'utf8')).trim(), `search -json -n 5 -cwd ${process.cwd()} -in project,host -kind rule,decision -since 2026-10-01 -min-score 0 -expired -all-words -- harness base`)
	await assert.rejects(tools.get('memcue_search').execute({ query: 'x', words: 'some' }, {}), /words must be all or any/)
})

test('memcue_search refuses the controls on a binary older than 0.14.0', async () => {
	const { tools } = mount()
	await assert.rejects(tools.get('memcue_search').execute({ query: 'x', kind: 'rule' }, {}), /need memcue 0\.14\.0/)
})

test('memcue_search gives a binary older than 0.8.0 only the flags it knows (#368)', async () => {
	const oldLog = join(dir, 'search-070.log')
	const oldBinary = join(dir, 'memcue-070.sh')
	await writeFile(oldBinary, `#!/bin/sh
case "$1" in
  version) printf '{"version":"0.7.0"}\\n' ;;
  search) printf '%s\\n' "$*" >> "${oldLog}"; printf '[]\\n' ;;
esac
exit 0
`)
	await chmod(oldBinary, 0o755)
	const { tools } = mount({ binary: oldBinary })
	const exec = { agent: { session: { id: 's-old', header: { cwd: '/repo/demo' } } } }
	await tools.get('memcue_search').execute({ query: 'harness base' }, exec)
	assert.equal((await readFile(oldLog, 'utf8')).trim(), 'search -json -n 5 -- harness base')
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
	// A rule is the user's words by default: only that source reaches the session card (#494).
	assert.match(text, /arg: add -source user -cwd \/repo\/demo -keywords new, fact -expires 2026-10-21 -kind rule -scope global -- A new fact\./)
})

test('memcue_add passes the source it is given and refuses an unknown one (#494)', async () => {
	await resetLog()
	const { tools } = mount()
	await tools.get('memcue_add').execute({ text: 'The demo container logs to /var/log/demo.', kind: 'environment' }, {})
	assert.match(await waitForLog('arg: add '), /arg: add -source agent /)
	await resetLog()
	await tools.get('memcue_add').execute({ text: 'The demo container logs to /var/log/demo.', source: 'tool' }, {})
	assert.match(await waitForLog('arg: add '), /arg: add -source tool /)
	const refused = await tools.get('memcue_add').execute({ text: 'x', source: 'boss' }, {})
	assert.equal(refused.stored, false)
	assert.match(refused.message, /source must be one of/)
})

test('memcue_add puts a fact on a shelf, and the tools name the shelf (#545)', async () => {
	await resetLog()
	const { tools } = mount()
	const exec = { agent: { session: { header: { cwd: '/repo/demo' } } } }
	await tools.get('memcue_add').execute({ text: 'Quarrel was archived in 2025.', shelf: 'research' }, exec)
	assert.match(await waitForLog('arg: add '), /arg: add -source agent -cwd \/repo\/demo -shelf research -- Quarrel was archived in 2025\./)
	assert.match(tools.get('memcue_add').parameters.properties.shelf.description, /in shelf:<name>/)
	assert.match(tools.get('memcue_search').parameters.properties.in.description, /shelf \(facts read from web pages and documents/)
	assert.match(tools.get('memcue_search').parameters.properties.in.description, /shelf:research/)
	assert.match(tools.get('memcue_search').description, /leaves out the shelf/)
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
