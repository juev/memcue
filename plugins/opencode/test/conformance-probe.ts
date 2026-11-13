/**
 * The conformance probe of #754: the shared transport as the OpenCode plugin wires it, imported through the
 * plugin's own resolution so the suite proves the plugin uses the package, not a copy. Loaded by
 * plugins/transport/conformance/transport.test.mjs with --experimental-strip-types.
 */
export { MemcueCli, transportConfig } from '../src/cli.ts';