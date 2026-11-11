/**
 * Offline tests for the install of the memcue binary: a local HTTP server stands in for the GitHub release.
 */
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, symlinkSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { after, before, beforeEach, test } from 'node:test'

import { apply } from '../src/index.mjs'
import { install, installedPath, pluginVersion } from '../src/install.mjs'

const os = { darwin: 'darwin', linux: 'linux' }[process.platform]
const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch]
const skip = !os || !arch

let version
let server
let files // url path -> body
let hits
const saved = {}

before(async () => {
	version = await pluginVersion()
	server = createServer((req, res) => {
		hits += 1
		const body = files.get(req.url)
		if (!body) {
			res.writeHead(404).end()
			return
		}
		res.writeHead(200).end(body)
	})
	await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
	for (const key of ['HOME', 'XDG_STATE_HOME', 'MEMCUE_RELEASE_URL', 'MEMCUE_AUTO_INSTALL', 'MEMCUE_BIN', 'PATH']) saved[key] = process.env[key]
})

after(() => {
	server.close()
	for (const [key, value] of Object.entries(saved)) {
		if (value === undefined) delete process.env[key]
		else process.env[key] = value
	}
})

// Each test gets its own home and state directory, so nothing reaches the real ~/.local/bin, and a release whose binary answers `version -json` with the plugin version.
beforeEach(() => {
	const root = mkdtempSync(join(tmpdir(), 'memcue-dsh-install-'))
	const build = join(root, 'build')
	mkdirSync(build)
	writeFileSync(join(build, 'memcue'), `#!/bin/sh\nprintf '{"version":"${version}"}\\n'\n`)
	chmodSync(join(build, 'memcue'), 0o755)
	const archive = `memcue_${version}_${os}_${arch}.tar.gz`
	const tar = spawnSync('tar', ['-czf', join(root, archive), '-C', build, 'memcue'])
	assert.equal(tar.status, 0, String(tar.stderr))
	const data = readFileSync(join(root, archive))
	const sum = createHash('sha256').update(data).digest('hex')
	files = new Map([
		[`/v${version}/${archive}`, data],
		[`/v${version}/checksums.txt`, `${sum}  ${archive}\n`],
	])
	hits = 0
	process.env.HOME = join(root, 'home')
	process.env.XDG_STATE_HOME = join(root, 'state')
	process.env.MEMCUE_RELEASE_URL = `http://127.0.0.1:${server.address().port}`
	process.env.PATH = '/usr/bin:/bin'
	delete process.env.MEMCUE_AUTO_INSTALL
	delete process.env.MEMCUE_BIN
})

test('install downloads the release, checks it and puts it in the machine binary', { skip }, async () => {
	const path = await install(version)
	assert.equal(path, installedPath())
	assert.equal(path, join(process.env.HOME, '.local', 'bin', 'memcue'))
	assert.match(spawnSync(path, ['version', '-json'], { encoding: 'utf8' }).stdout, new RegExp(version))
	hits = 0
	assert.equal(await install(version), path)
	assert.equal(hits, 0, 'an installed release must not be downloaded again')
})

test('a checksum that does not match installs nothing', { skip }, async () => {
	const key = `/v${version}/checksums.txt`
	files.set(key, files.get(key).toString().replace(/^[0-9a-f]+/, '0'.repeat(64)))
	await assert.rejects(install(version), /checksum/)
	assert.equal(existsSync(installedPath()), false)
	assert.equal(existsSync(join(process.env.XDG_STATE_HOME, 'memcue', 'install.lock')), false, 'the lock must be released')
})

test('MEMCUE_AUTO_INSTALL=0 downloads nothing', { skip }, async () => {
	process.env.MEMCUE_AUTO_INSTALL = '0'
	await assert.rejects(install(version), /MEMCUE_AUTO_INSTALL=0/)
	assert.equal(hits, 0)
})

function context() {
	return { on() {}, tools: { register() {} }, systemPrompt: { section() {} } }
}

async function waitFor(check, ms = 5_000) {
	const end = Date.now() + ms
	while (Date.now() < end) {
		if (check()) return true
		await new Promise((resolve) => setTimeout(resolve, 20))
	}
	return false
}

test('the plugin installs its release when no binary is configured or on PATH', { skip }, async () => {
	apply(context(), {})
	assert.ok(await waitFor(() => existsSync(installedPath())), 'the plugin did not install the binary')
})

// pathBinary puts on PATH a memcue that answers `version -json` with the given version.
function pathBinary(reported) {
	const bin = mkdtempSync(join(tmpdir(), 'memcue-dsh-path-'))
	writeFileSync(join(bin, 'memcue'), `#!/bin/sh\nprintf '{"version":"${reported}"}\\n'\n`)
	chmodSync(join(bin, 'memcue'), 0o755)
	process.env.PATH = `${bin}:/usr/bin:/bin`
}

// A release older than the plugin's on PATH is replaced, even when it has every command the plugin uses: it may not
// open the base the plugin's release writes (#272).
test('an older release on PATH is replaced by the plugin release', { skip }, async () => {
	pathBinary('0.4.0')
	apply(context(), {})
	assert.ok(await waitFor(() => existsSync(installedPath())), 'the plugin kept the older binary')
	assert.ok(hits > 0)
})

test('a binary on PATH as new as the plugin is kept, and nothing is downloaded', { skip }, async () => {
	pathBinary(version)
	apply(context(), {})
	await new Promise((resolve) => setTimeout(resolve, 300))
	assert.equal(hits, 0)
	assert.equal(existsSync(installedPath()), false)
})

test('a configured binary is used as it is, and nothing is downloaded', { skip }, async () => {
	const warn = console.warn
	console.warn = () => {}
	try {
		apply(context(), { binary: join(tmpdir(), 'no-such-memcue') })
		await new Promise((resolve) => setTimeout(resolve, 300))
	} finally {
		console.warn = warn
	}
	assert.equal(hits, 0)
	assert.equal(existsSync(installedPath()), false)
})

// machineBinary puts a memcue at the machine's path that passes `version -minimum` or not, as current says.
function machineBinary(current) {
	mkdirSync(join(installedPath(), '..'), { recursive: true })
	writeFileSync(installedPath(), `#!/bin/sh\n# machine\nexit ${current ? 0 : 1}\n`)
	chmodSync(installedPath(), 0o755)
}

// #718: the machine has one binary, which every plugin shares.
test('a machine binary of this release or newer stays and nothing is downloaded', { skip }, async () => {
	machineBinary(true)
	assert.equal(await install(version), installedPath())
	assert.equal(hits, 0)
	assert.match(readFileSync(installedPath(), 'utf8'), /machine/)
})

test('an older machine binary is replaced by the release', { skip }, async () => {
	machineBinary(false)
	assert.equal(await install(version), installedPath())
	assert.ok(hits > 0)
	assert.match(spawnSync(installedPath(), ['version', '-json'], { encoding: 'utf8' }).stdout, new RegExp(version))
})

test('an older machine binary that is a symlink is left to the user', { skip }, async () => {
	const own = join(process.env.HOME, 'own-memcue')
	mkdirSync(join(installedPath(), '..'), { recursive: true })
	writeFileSync(own, '#!/bin/sh\nexit 1\n')
	chmodSync(own, 0o755)
	symlinkSync(own, installedPath())
	await assert.rejects(install(version), /not a plain file/)
	assert.ok(lstatSync(installedPath()).isSymbolicLink())
	assert.equal(hits, 0)
})
