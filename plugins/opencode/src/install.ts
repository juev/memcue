/**
 * The binary install of the shared transport (#754): into the machine's `~/.local/bin/memcue`, the one binary every
 * plugin of the machine shares, checked against the release's `checksums.txt`. A machine binary of this release or
 * newer stays, and so does a symlink or anything else that is not a plain file. `MEMCUE_AUTO_INSTALL=0` turns it off.
 * The version installed is this plugin's release, read from its own manifest.
 */
import { readFile } from 'node:fs/promises';

export { install, installedPath, current } from 'memcue-transport';

/** The version of this plugin, which is the release of the binary it installs. */
export async function pluginVersion(): Promise<string> {
  const manifest = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'));
  return String(manifest.version);
}
