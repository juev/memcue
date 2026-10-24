import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const script = fileURLToPath(new URL('../scripts/memcue.sh', import.meta.url))
const manifest = JSON.parse(readFileSync(new URL('../.claude-plugin/plugin.json', import.meta.url), 'utf8'))
const version = manifest.version
const os = { darwin: 'darwin', linux: 'linux' }[process.platform]
const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch]
const archive = `memcue_${version}_${os}_${arch}.tar.gz`

// fakeBinary is a memcue that answers `version -minimum` with its own verdict and echoes any other command.
function fakeBinary(path, { current = true, name = 'fake' } = {}) {
	writeFileSync(path, `#!/bin/sh
if [ "$1" = version ]; then ${current ? 'exit 0' : 'exit 1'}; fi
echo "${name} $*"
cat
`)
	chmodSync(path, 0o755)
}

// release builds a release directory with the archive and checksums.txt, as goreleaser publishes them.
function release(root, { badChecksum = false } = {}) {
	const build = join(root, 'build')
	mkdirSync(build, { recursive: true })
	fakeBinary(join(build, 'memcue'), { name: 'installed' })
	const dir = join(root, 'release', `v${version}`)
	mkdirSync(dir, { recursive: true })
	const tar = spawnSync('tar', ['-czf', join(dir, archive), '-C', build, 'memcue'])
	assert.equal(tar.status, 0, String(tar.stderr))
	const sum = createHash('sha256').update(readFileSync(join(dir, archive))).digest('hex')
	writeFileSync(join(dir, 'checksums.txt'), `${badChecksum ? '0'.repeat(64) : sum}  ${archive}\n`)
	return `file://${join(root, 'release')}`
}

function setup(opts = {}) {
	const root = mkdtempSync(join(tmpdir(), 'memcue-install-'))
	const bin = join(root, 'bin')
	mkdirSync(bin)
	const env = {
		HOME: root,
		PATH: `${bin}:/usr/bin:/bin`,
		XDG_DATA_HOME: join(root, 'data'),
		XDG_STATE_HOME: join(root, 'state'),
		CLAUDE_ENV_FILE: join(root, 'env'),
		MEMCUE_RELEASE_URL: release(root, opts),
	}
	return { root, bin, env, installed: join(root, 'data', 'memcue', version, 'memcue') }
}

function run(env, args, input = '') {
	const r = spawnSync('sh', [script, ...args], { env, input, encoding: 'utf8' })
	assert.equal(r.status, 0, r.stderr)
	return r.stdout
}

test('session-start installs the release of the plugin when no binary is on PATH', { skip: !os || !arch }, () => {
	const { env, installed, root } = setup()
	const out = run(env, ['session-start'], '{}')
	assert.ok(existsSync(installed), 'the binary was not installed')
	assert.match(out, /^installed session-start/)
	assert.match(readFileSync(join(root, 'env'), 'utf8'), new RegExp(`export PATH="${join(root, 'data', 'memcue', version)}:\\$PATH"`))
	assert.match(run(env, ['hook-extract'], 'payload'), /^installed hook-extract\npayload/, 'the next hook must use the installed copy')
	assert.match(readFileSync(join(root, 'state', 'memcue', 'install.log'), 'utf8'), /installed/)
})

test('a checksum that does not match installs nothing and says so', { skip: !os || !arch }, () => {
	const { env, installed, root } = setup({ badChecksum: true })
	const out = run(env, ['session-start'])
	assert.equal(existsSync(installed), false)
	assert.match(out, /the binary is not installed/)
	assert.match(readFileSync(join(root, 'state', 'memcue', 'install.log'), 'utf8'), /checksum/)
})

test('a failed install is not retried for an hour', { skip: !os || !arch }, () => {
	const { env, installed, root } = setup({ badChecksum: true })
	run(env, ['session-start'])
	release(root) // the release is fixed, but the next session must not try again yet
	run(env, ['session-start'])
	assert.equal(existsSync(installed), false, 'a second attempt ran within the hour')
	const failed = join(root, 'data', 'memcue', version, '.failed')
	spawnSync('touch', ['-t', '200001010000', failed])
	assert.match(run(env, ['session-start']), /^installed session-start/, 'an old failure must not block the install')
	assert.equal(existsSync(failed), false)
})

test('MEMCUE_AUTO_INSTALL=0 downloads nothing', { skip: !os || !arch }, () => {
	const { env, installed } = setup()
	const out = run({ ...env, MEMCUE_AUTO_INSTALL: '0' }, ['session-start'])
	assert.equal(existsSync(installed), false)
	assert.match(out, /MEMCUE_AUTO_INSTALL=0/)
})

test('a current binary on PATH wins and nothing is downloaded', { skip: !os || !arch }, () => {
	const { env, bin, installed } = setup()
	fakeBinary(join(bin, 'memcue'), { name: 'path' })
	assert.match(run(env, ['session-start']), /^path session-start/)
	assert.equal(existsSync(installed), false)
})

test('an older binary on PATH is replaced by the installed release', { skip: !os || !arch }, () => {
	const { env, bin, installed } = setup()
	fakeBinary(join(bin, 'memcue'), { current: false, name: 'old' })
	assert.match(run(env, ['session-start']), /^installed session-start/)
	assert.ok(existsSync(installed))
	assert.match(run(env, ['hook-extract']), /^installed hook-extract/)
})

test('an older binary stays in use when the release cannot be installed', { skip: !os || !arch }, () => {
	const { env, bin } = setup({ badChecksum: true })
	fakeBinary(join(bin, 'memcue'), { current: false, name: 'old' })
	assert.match(run(env, ['session-start']), /older than/)
	assert.match(run(env, ['hook-extract']), /^old hook-extract/, 'the hooks must keep working on the older binary')
})

test('MEMCUE_BIN wins over everything', { skip: !os || !arch }, () => {
	const { env, root } = setup()
	fakeBinary(join(root, 'pinned'), { name: 'pinned' })
	assert.match(run({ ...env, MEMCUE_BIN: join(root, 'pinned') }, ['hook-extract']), /^pinned hook-extract/)
})

test('a hook without a binary does nothing', { skip: !os || !arch }, () => {
	const { env } = setup()
	assert.equal(run(env, ['hook-extract'], '{"session_id":"x"}'), '')
})
