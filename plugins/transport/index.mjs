/**
 * The memcue CLI transport and binary install shared by the native harness plugins (#754).
 * @module memcue-transport
 */
export { MemcueCli } from './transport.mjs';
export { install, installedPath, current, executable } from './install.mjs';
