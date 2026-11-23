import { copyFile, mkdir, readFile, rm } from 'node:fs/promises'

const transport = new URL('../plugins/transport/', import.meta.url)
const { files } = JSON.parse(await readFile(new URL('package.json', transport), 'utf8'))
for (const plugin of ['pi', 'opencode']) {
  const destination = new URL(`../plugins/${plugin}/vendor/transport/`, import.meta.url)
  await rm(destination, { recursive: true, force: true })
  await mkdir(destination, { recursive: true })
  // Use the same public file list as the standalone transport; never copy tests or node_modules.
  for (const file of files) {
    await copyFile(new URL(file, transport), new URL(file, destination))
  }
}
