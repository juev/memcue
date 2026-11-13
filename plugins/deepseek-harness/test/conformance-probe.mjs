/**
 * The conformance probe of #754: the shared transport as the DSH plugin imports it (the memcue-dsh/transport
 * self-reference) and the adapter its tools run. Loaded by plugins/transport/conformance/dsh.test.mjs; the probe
 * lives in the plugin so the self-reference resolves in the plugin's own package scope.
 */
export { MemcueCli } from 'memcue-dsh/transport';
export { runMemcue } from '../src/index.mjs';