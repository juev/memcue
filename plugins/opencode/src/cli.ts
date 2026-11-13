/**
 * The OpenCode wiring of the shared memcue transport (#754). The contract — one spawn per call, the process-group
 * kill, the timeout, the 4 MiB output budget and the outcome receipt — lives in memcue-transport; this file names
 * the host and keeps the plugin's import path stable. The conformance suite runs the contract through this wiring.
 */
import { MemcueCli, type CliConfig } from 'memcue-transport';

export { MemcueCli };
export type { CliConfig, CliResult, CliRunOptions } from 'memcue-transport';

/** The transport configuration every OpenCode memcue call runs with: ambient settings reach the CLI, these win. */
export function transportConfig(config: { facts?: string; stateDir?: string; host?: string }, binary: string): CliConfig {
  return { ...config, binary, caller: 'opencode', env: { MEMCUE_DREAM_BACKGROUND: '0' } };
}
