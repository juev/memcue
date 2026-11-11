/**
 * Installs the memcue binary of this plugin's release when none is configured, the way the Claude Code plugin does:
 * into the machine's `~/.local/bin/memcue`, the one binary every plugin of the machine shares (#718), checked against
 * the release's `checksums.txt`. A machine binary of this release or newer stays, and so does a symlink or anything
 * else that is not a plain file. `MEMCUE_AUTO_INSTALL=0` turns it off.
 *
 * @module memcue-dsh/install
 */
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { access, chmod, constants, lstat, mkdir, mkdtemp, readFile, rename, rm, rmdir, stat, writeFile } from 'node:fs/promises'
import { homedir } from 'node:os'
import { join } from 'node:path'

/** Where the releases are downloaded from; MEMCUE_RELEASE_URL replaces it, for a mirror or a test. */
const RELEASES = 'https://github.com/juev/memcue/releases/download'

/** A lock left by a crashed install stops blocking after this long. */
const STALE_LOCK_MS = 10 * 60_000

/**
 * The version of this plugin, which is the release of the binary it installs.
 * @returns the version from package.json.
 */
export async function pluginVersion() {
	const manifest = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'))
	return String(manifest.version)
}

/**
 * Path of the machine's binary.
 * @returns the absolute path, whether or not it exists.
 */
export function installedPath() {
	return join(homedir(), '.local', 'bin', 'memcue')
}

/**
 * The lock both plugins take, in memcue's state directory, so two plugins of one machine do not install at once.
 * @returns the absolute path of the lock directory.
 */
function lockPath() {
	return join(process.env.XDG_STATE_HOME || join(homedir(), '.local', 'state'), 'memcue', 'install.lock')
}

/**
 * Whether path is an executable file.
 * @param path - file to check.
 * @returns true when it can be run.
 */
export async function executable(path) {
	try {
		await access(path, constants.X_OK)
		return true
	} catch {
		return false
	}
}

/**
 * Whether the binary at path is the given release or newer, by its own `version -minimum`.
 * @param path - binary to ask.
 * @param version - release version without the leading v.
 * @returns true when it says so.
 */
export function current(path, version) {
	return new Promise((resolve) => {
		const child = spawn(path, ['version', '-minimum', version], { stdio: 'ignore' })
		child.on('error', () => resolve(false))
		child.on('close', (code) => resolve(code === 0))
	})
}

function target() {
	const os = { darwin: 'darwin', linux: 'linux' }[process.platform]
	const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch]
	if (!os || !arch) throw new Error(`no memcue release for ${process.platform}/${process.arch}`)
	return `${os}_${arch}`
}

async function download(url, timeoutMs) {
	const response = await fetch(url, { signal: AbortSignal.timeout(timeoutMs) })
	if (!response.ok) throw new Error(`${url}: ${response.status} ${response.statusText}`)
	return Buffer.from(await response.arrayBuffer())
}

function untar(archive, dir) {
	return new Promise((resolve, reject) => {
		const child = spawn('tar', ['-xzf', archive, '-C', dir, 'memcue'], { stdio: ['ignore', 'ignore', 'pipe'] })
		let err = ''
		child.stderr.on('data', (chunk) => { err += chunk })
		child.on('error', reject)
		child.on('close', (code) => (code === 0 ? resolve() : reject(new Error(`tar: ${err.trim()}`))))
	})
}

/**
 * Install the binary of a release unless the machine's binary is that release or newer. The archive must match its
 * line in the release's checksums.txt; it is unpacked next to the target and renamed over it, so a reader never sees
 * half a binary and a memcue that is running keeps its file. A symlink or anything else that is not a plain file is
 * the user's and stays.
 * @param version - release version without the leading v.
 * @returns the path of the installed binary.
 * @throws when auto-install is off, the platform has no release, the machine's binary is not a plain file, or the
 *   download or the checksum fails.
 */
export async function install(version) {
	const path = installedPath()
	if (await executable(path) && await current(path, version)) return path
	if (process.env.MEMCUE_AUTO_INSTALL === '0') throw new Error('MEMCUE_AUTO_INSTALL=0')
	const info = await lstat(path).catch(() => null)
	if (info && !info.isFile()) throw new Error(`${path} is not a plain file; leaving it to the user`)
	const archive = `memcue_${version}_${target()}.tar.gz`
	const dir = join(path, '..')
	await mkdir(dir, { recursive: true })
	const lock = lockPath()
	await mkdir(join(lock, '..'), { recursive: true })
	try {
		if (Date.now() - (await stat(lock)).mtimeMs > STALE_LOCK_MS) await rmdir(lock)
	} catch {
		// no lock
	}
	try {
		await mkdir(lock)
	} catch {
		throw new Error('another install of memcue is running')
	}
	let tmp = ''
	try {
		// Another plugin may have put a current binary in place before this one took the lock.
		if (await executable(path) && await current(path, version)) return path
		tmp = await mkdtemp(join(dir, '.memcue-install.'))
		const base = `${process.env.MEMCUE_RELEASE_URL || RELEASES}/v${version}`
		const [data, sums] = await Promise.all([download(`${base}/${archive}`, 45_000),
			download(`${base}/checksums.txt`, 15_000)])
		const want = sums.toString('utf8').split('\n').map((line) => line.trim().split(/\s+/))
			.find(([, file]) => file === archive)?.[0]
		const got = createHash('sha256').update(data).digest('hex')
		if (!want || want !== got) throw new Error(`checksum of ${archive} does not match checksums.txt`)
		await writeFile(join(tmp, archive), data)
		await untar(join(tmp, archive), tmp)
		await chmod(join(tmp, 'memcue'), 0o755)
		await rename(join(tmp, 'memcue'), path)
		return path
	} finally {
		if (tmp) await rm(tmp, { recursive: true, force: true })
		await rmdir(lock).catch(() => {})
	}
}
