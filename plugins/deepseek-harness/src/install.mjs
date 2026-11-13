/**
 * The binary install of the shared transport (#754): into the machine's `~/.local/bin/memcue`, the one binary every
 * plugin of the machine shares (#718), checked against the release's `checksums.txt`. A machine binary of this
 * release or newer stays, and so does a symlink or anything else that is not a plain file. `MEMCUE_AUTO_INSTALL=0`
 * turns it off.
 *
 * The version installed is this plugin's release, read from the repository-root package: since #754 the npm package
 * and the GitHub bundle are one package, so the manifest sits at the root in the checkout and in the tarball alike.
 *
 * @module memcue-dsh/install
 */
import { readFile } from 'node:fs/promises'

export { install, installedPath, current, executable } from 'memcue-dsh/transport'

/**
 * The version of this plugin, which is the release of the binary it installs.
 * @returns the version from the root package.json.
 */
export async function pluginVersion() {
	const manifest = JSON.parse(await readFile(new URL('../../../package.json', import.meta.url), 'utf8'))
	return String(manifest.version)
}
