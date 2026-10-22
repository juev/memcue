/**
 * Installs the memcue binary of this plugin's release when none is configured, the way the Claude Code plugin does:
 * into `${XDG_DATA_HOME:-~/.local/share}/memcue/<version>/memcue`, a directory the two plugins share, checked
 * against the release's `checksums.txt`. `MEMCUE_AUTO_INSTALL=0` turns it off.
 *
 * @module memcue-dsh/install
 */
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { access, constants, chmod, mkdir, mkdtemp, readFile, rename, rm, rmdir, stat, writeFile } from 'node:fs/promises'
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
 * Path of the installed binary of a release.
 * @param version - release version without the leading v.
 * @returns the absolute path, whether or not it exists.
 */
export function installedPath(version) {
	const data = process.env.XDG_DATA_HOME || join(homedir(), '.local', 'share')
	return join(data, 'memcue', version, 'memcue')
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
 * Install the binary of a release unless it is there already. The archive must match its line in the release's
 * checksums.txt; it is unpacked next to the target and moved into place, so a reader never sees half a binary. A lock
 * directory, the same one the Claude Code plugin takes, keeps two installs from racing.
 * @param version - release version without the leading v.
 * @returns the path of the installed binary.
 * @throws when auto-install is off, the platform has no release, or the download or the checksum fails.
 */
export async function install(version) {
	const path = installedPath(version)
	if (await executable(path)) return path
	if (process.env.MEMCUE_AUTO_INSTALL === '0') throw new Error('MEMCUE_AUTO_INSTALL=0')
	const archive = `memcue_${version}_${target()}.tar.gz`
	const dir = join(path, '..')
	await mkdir(dir, { recursive: true })
	const lock = join(dir, '.lock')
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
	const tmp = await mkdtemp(join(dir, '.tmp.'))
	try {
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
		await rm(tmp, { recursive: true, force: true })
		await rmdir(lock).catch(() => {})
	}
}
