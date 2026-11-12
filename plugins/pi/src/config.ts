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

/** Read only this adapter's namespace; unrelated Pi settings are left untouched. */
export function readConfig(settings: unknown): MemcueConfig {
  const root = object(settings, 'settings');
  const value = Object.hasOwn(root, 'memcue') ? object(root.memcue, 'memcue') : {};
  checkKeys(value, ['enabled', 'binary', 'facts', 'stateDir', 'host', 'subagents'], 'memcue');
  const child = Object.hasOwn(value, 'subagents') ? object(value.subagents, 'memcue.subagents') : {};
  checkKeys(child, ['defaultEnabled', 'agents'], 'memcue.subagents');
  const rawAgents = Object.hasOwn(child, 'agents') ? object(child.agents, 'memcue.subagents.agents') : {};
  const agents: Record<string, boolean> = Object.create(null);
  for (const [name, permission] of Object.entries(rawAgents)) {
    if (!name.trim() || name !== name.trim()) {
      throw new Error('memcue.subagents.agents contains an invalid agent name');
    }
    agents[name] = boolean(permission, `memcue.subagents.agents.${name}`);
  }

  const config: MemcueConfig = {
    enabled: Object.hasOwn(value, 'enabled') ? boolean(value.enabled, 'memcue.enabled') : true,
    subagents: {
      defaultEnabled: Object.hasOwn(child, 'defaultEnabled')
        ? boolean(child.defaultEnabled, 'memcue.subagents.defaultEnabled') : false,
      agents,
    },
  };
  if (Object.hasOwn(value, 'binary')) config.binary = path(value.binary, 'memcue.binary', true);
  if (Object.hasOwn(value, 'facts')) config.facts = path(value.facts, 'memcue.facts');
  if (Object.hasOwn(value, 'stateDir')) config.stateDir = path(value.stateDir, 'memcue.stateDir');
  if (Object.hasOwn(value, 'host')) config.host = text(value.host, 'memcue.host');
  return config;
}

/** A bound child entrypoint is required even when the default child policy allows memory. */
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
