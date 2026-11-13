/**
 * A mock OpenCode plugin context: the domains the plugin touches, with the registered tools, commands and hooks
 * reachable by the test. Nothing here talks to a model.
 */
import { chmod, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

export interface SessionFixture {
  parentID?: string;
  agent?: string;
  directory?: string;
}

export interface HarnessOptions {
  options?: unknown;
  version?: string;
  directory?: string;
  sessions?: Record<string, SessionFixture>;
}

export interface Harness {
  ctx: unknown;
  tools: Map<string, { name: string; description: string; input: Record<string, unknown>;
    execute: (input: unknown, context: { sessionID: string; signal?: AbortSignal }) => Promise<unknown> }>;
  commands: Map<string, { name: string; description?: string; execute: (input: {
    sessionID: string; prompt: { text: string }; delivery?: string }) => Promise<void> }>;
  hooks: Record<string, (event: any) => unknown>;
  notices: Array<{ sessionID: string; text: string; description?: string; resume?: boolean }>;
  prompts: Array<{ sessionID: string; text: string; delivery?: string }>;
}

export function harness(options: HarnessOptions = {}): Harness {
  const directory = options.directory ?? '/repo';
  const sessions = options.sessions ?? {};
  const tools: Harness['tools'] = new Map();
  const commands: Harness['commands'] = new Map();
  const hooks: Harness['hooks'] = {};
  const notices: Harness['notices'] = [];
  const prompts: Harness['prompts'] = [];
  const transform = async (register: (editor: unknown) => void) => {
    register({
      list: () => [...tools.values()],
      get: (name: string) => tools.get(name),
      add: (value: { name: string }) => { tools.set(value.name, value as never); },
      update: () => {},
      remove: () => {},
      namespace: () => {},
    });
    return { dispose: async () => {} };
  };
  const ctx = {
    app: { name: 'opencode', version: options.version ?? '2.2.7', channel: 'latest' },
    location: { directory, project: { id: 'project', directory, canonical: directory } },
    options: options.options,
    tool: { transform },
    command: {
      transform: async (register: (editor: unknown) => void) => {
        register({ add: (value: { name: string }) => { commands.set(value.name, value as never); } });
        return { dispose: async () => {} };
      },
    },
    session: {
      get: async ({ sessionID }: { sessionID: string }) => {
        const fixture = sessions[sessionID];
        if (!fixture) throw new Error(`session ${sessionID} not found`);
        return {
          id: sessionID,
          parentID: fixture.parentID,
          agent: fixture.agent,
          location: { directory: fixture.directory ?? directory },
        };
      },
      hook: async (name: string, callback: (event: any) => unknown) => {
        hooks[name] = callback;
        return { dispose: async () => {} };
      },
      synthetic: async (input: Harness['notices'][number]) => { notices.push(input); return {}; },
      prompt: async (input: Harness['prompts'][number]) => { prompts.push(input); return {}; },
    },
  };
  return { ctx, tools, commands, hooks, notices, prompts };
}

/** A stand-in for the memcue CLI: it answers the reads the plugin makes and reports a receipt for writes. */
export const FAKE_MEMCUE = `#!/usr/bin/env node
const { appendFileSync } = require('node:fs');
const { createHash } = require('node:crypto');

const args = process.argv.slice(2);
const stdin = args[0] === 'session-card' ? require('node:fs').readFileSync(0, 'utf8') : null;
const calls = process.env.FAKE_MEMCUE_CALLS;
if (calls) appendFileSync(calls, JSON.stringify({ args, cwd: process.cwd(), nonce: process.env.MEMCUE_OUTCOME_NONCE ?? null, stdin }) + '\\n');
const sha = (value) => createHash('sha256').update(value).digest('hex');

if (args[0] === 'version') process.exit(0);
if (args[0] === 'session-card') {
  if (process.env.FAKE_SNAPSHOT_FAIL === '1') { process.stderr.write('no fact base\\n'); process.exit(1); }
  const facts = JSON.parse(process.env.FAKE_PITFALLS ?? '[]');
  process.stdout.write(JSON.stringify({ facts, context: 'header' }));
  process.exit(0);
}
if (args[0] === 'search') {
  process.stdout.write(JSON.stringify({ facts: [] }));
  process.exit(0);
}
const out = 'fact-id\\n';
process.stdout.write(out);
const nonce = process.env.MEMCUE_OUTCOME_NONCE;
if (nonce && /^[0-9a-f]{32}$/.test(nonce)) {
  const frame = { version: 1, nonce, status: 'success', bytes: Buffer.byteLength(out), sha256: sha(out),
    error_bytes: 0, error_sha256: sha('') };
  process.stderr.write('\\nMEMCUE_OUTCOME_V1 ' + JSON.stringify(frame) + '\\n');
}
process.exit(0);
`;

export async function fakeMemcue(root: string): Promise<string> {
  const path = join(root, 'memcue');
  await writeFile(path, FAKE_MEMCUE, { mode: 0o755 });
  await chmod(path, 0o755);
  return path;
}
