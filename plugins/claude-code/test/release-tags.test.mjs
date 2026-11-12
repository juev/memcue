/**
 * #740: the plugin tags (dsh-v*, pi-v*) share the tag namespace with the binary's releases (v*). The build version and
 * the previous release that the release workflow hands to goreleaser must look at the v* tags only.
 */
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

const root = new URL('../../../', import.meta.url)
const makefile = fileURLToPath(new URL('Makefile', root))

// A repository with the history of v0.37.0: a plugin-only tag one commit before the release commit.
async function history(t) {
	const dir = await mkdtemp(join(tmpdir(), 'memcue-release-tags-'))
	t.after(() => rm(dir, { recursive: true, force: true }))
	const env = { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1',
		GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@example.com', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@example.com' }
	const git = (...args) => {
		const result = spawnSync('git', ['-C', dir, ...args], { env, encoding: 'utf8' })
		assert.equal(result.status, 0, result.stderr)
		return result.stdout.trim()
	}
	git('init', '-q')
	const commit = (message, ...tags) => {
		git('commit', '-q', '--allow-empty', '-m', message)
		for (const tag of tags) git('tag', tag)
	}
	commit('release 0.36.2', 'v0.36.2', 'dsh-v0.36.2')
	commit('a fix')
	commit('the pi plugin', 'pi-v0.36.2')
	commit('release 0.37.0', 'v0.37.0', 'dsh-v0.37.0', 'pi-v0.37.0')
	commit('after the release', 'pi-v0.37.1')
	return { dir, env, head: git('rev-parse', '--short', 'HEAD') }
}

function make(dir, env, ...args) {
	return spawnSync('make', ['-f', makefile, '-C', dir, '-s', ...args], { env, encoding: 'utf8' })
}

test('the previous release skips a plugin tag on the commit before the release', async (t) => {
	const { dir, env } = await history(t)
	const result = make(dir, env, 'previous-release', 'TAG=v0.37.0')
	assert.equal(result.status, 0, result.stderr)
	assert.equal(result.stdout.trim(), 'v0.36.2')
})

test('previous-release refuses to run without a tag', async (t) => {
	const { dir, env } = await history(t)
	const result = make(dir, env, 'previous-release')
	assert.notEqual(result.status, 0)
	assert.match(result.stderr, /usage: make previous-release TAG=/)
})

test('a build after a release is versioned from the release tag, not a later plugin tag', async (t) => {
	const { dir, env, head } = await history(t)
	const result = make(dir, env, '-n', 'build')
	assert.equal(result.status, 0, result.stderr)
	assert.ok(result.stdout.includes(`-X main.version=v0.37.0-1-g${head}`), result.stdout)
})

test('the release workflow hands goreleaser the current and the previous v* tag', async () => {
	const release = await readFile(new URL('.github/workflows/release.yml', root), 'utf8')
	assert.match(release, /previous=\$\(make -s previous-release TAG="\$GITHUB_REF_NAME"\)/)
	assert.match(release, /echo "previous=\$previous" >> "\$GITHUB_OUTPUT"/)
	const goreleaser = release.slice(release.indexOf('goreleaser/goreleaser-action@'))
	assert.match(goreleaser, /GORELEASER_CURRENT_TAG: \$\{\{ github\.ref_name \}\}/)
	assert.match(goreleaser, /GORELEASER_PREVIOUS_TAG: \$\{\{ steps\.tags\.outputs\.previous \}\}/)
	assert.ok(release.indexOf('id: tags') < release.indexOf('goreleaser/goreleaser-action@'),
		'the previous tag must be found before goreleaser runs')
})
