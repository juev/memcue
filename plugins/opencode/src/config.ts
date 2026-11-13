/**
 * The `options` object of the plugin entry in `opencode.json(c)`:
 *
 *   "plugins": [{ "package": "memcue-opencode", "options": { "facts": "~/memory/facts" } }]
 *
 * Only this namespace is read; ambient `MEMCUE_*` variables still reach the CLI, and a field here wins over them.
 * Every field is optional, and an unknown key is an error rather than a silently ignored typo.
 */
import { homedir } from 'node:os';
import { isAbsolute, join } from 'node:path';

export interface MemcueConfig {
  enabled: boolean;
  /** A configured binary is used as it is; without one the plugin installs its release. */
  binary?: string;
  facts?: string;
  stateDir?: string;
  host?: string;
  subagents: {
    defaultEnabled: boolean;
    agents: Record<string, boolean>;
  };
}

const ROOT_KEYS = ['enabled', 'binary', 'facts', 'stateDir', 'host', 'subagents'];

function object(value: unknown, field: string): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${field} must be an object`);
  }
  return value as Record<string, unknown>;
}

function checkKeys(value: Record<string, unknown>, allowed: string[], field: string): void {
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) throw new Error(`Unknown setting: ${field}.${key}`);
  }
}

function boolean(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') throw new Error(`${field} must be a boolean`);
  return value;
}

function text(value: unknown, field: string): string {
  if (typeof value !== 'string' || !value.trim() || value.includes('\0')) {
    throw new Error(`${field} must be a nonempty string without NUL characters`);
  }
  return value;
}

function path(value: unknown, field: string, allowCommand = false): string {
  const raw = text(value, field);
  const expanded = raw.startsWith('~/') ? join(homedir(), raw.slice(2)) : raw;
  const bareCommand = allowCommand && !expanded.includes('/') && expanded !== '.' && expanded !== '..';
  if (!isAbsolute(expanded) && !bareCommand) {
    throw new Error(`${field} must be an absolute path${allowCommand ? ' or a command name' : ''}; ~/ is supported`);
  }
  return expanded;
}

/** Read the plugin options; absent options mean the defaults. */
export function readConfig(options: unknown): MemcueConfig {
  const value = options === undefined || options === null ? {} : object(options, 'options');
  checkKeys(value, ROOT_KEYS, 'options');
  const child = Object.hasOwn(value, 'subagents') ? object(value.subagents, 'options.subagents') : {};
  checkKeys(child, ['defaultEnabled', 'agents'], 'options.subagents');
  const rawAgents = Object.hasOwn(child, 'agents') ? object(child.agents, 'options.subagents.agents') : {};
  const agents: Record<string, boolean> = Object.create(null);
  for (const [name, permission] of Object.entries(rawAgents)) {
    if (!name.trim() || name !== name.trim()) {
      throw new Error('options.subagents.agents contains an invalid agent name');
    }
    agents[name] = boolean(permission, `options.subagents.agents.${name}`);
  }

  const config: MemcueConfig = {
    enabled: Object.hasOwn(value, 'enabled') ? boolean(value.enabled, 'options.enabled') : true,
    subagents: {
      defaultEnabled: Object.hasOwn(child, 'defaultEnabled')
        ? boolean(child.defaultEnabled, 'options.subagents.defaultEnabled') : false,
      agents,
    },
  };
  if (Object.hasOwn(value, 'binary')) config.binary = path(value.binary, 'options.binary', true);
  if (Object.hasOwn(value, 'facts')) config.facts = path(value.facts, 'options.facts');
  if (Object.hasOwn(value, 'stateDir')) config.stateDir = path(value.stateDir, 'options.stateDir');
  if (Object.hasOwn(value, 'host')) config.host = text(value.host, 'options.host');
  return config;
}

/**
 * Whether this session gets memory. A child session (a subagent or a fork) gets none unless the agent it runs is
 * named in `subagents.agents`; a session switched off with `/memcue off` stays off.
 */
export function isEnabled(
  config: MemcueConfig,
  options: { agent?: string; isChild: boolean; sessionDisabled?: boolean },
): boolean {
  if (!config.enabled || options.sessionDisabled) return false;
  if (options.agent === undefined) return !options.isChild;
  if (!options.agent.trim()) return false;
  const { agents, defaultEnabled } = config.subagents;
  return Object.hasOwn(agents, options.agent) ? agents[options.agent] : defaultEnabled;
}
