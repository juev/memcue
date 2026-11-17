/**
 * The contract between the server part and the TUI part of this plugin (#757). OpenCode resolves a package's
 * `./rpc` entrypoint and both parts import it, so one definition names the method the TUI reads state with and the
 * event the server announces a user-visible outcome with. The TUI part never runs memcue itself: every decision
 * stays in the server part, which answers `status` and emits `notice`.
 */
import { Rpc } from '@opencode/plugin/rpc';

export type NoticeVariant = 'info' | 'success' | 'warning' | 'error';

/** The `sessionID` of a `status` call. The RPC schema types a JSON Schema input as unknown, so it is read here. */
export function sessionIDOf(input: unknown): string | undefined {
  if (input === null || typeof input !== 'object') return undefined;
  const value: unknown = (input as { sessionID?: unknown }).sessionID;
  return typeof value === 'string' && value ? value : undefined;
}

export const memcueRpc = Rpc.define({
  id: 'memcue',
  methods: {
    health: {
      input: { type: 'object', properties: {} },
      output: {
        type: 'object',
        properties: { problem: { type: 'string' } },
      },
    },
    status: {
      input: {
        type: 'object',
        properties: { sessionID: { type: 'string' } },
        required: ['sessionID'],
      },
      output: {
        type: 'object',
        properties: {
          on: { type: 'boolean' },
          role: { type: 'string', enum: ['main', 'subagent'] },
          agent: { type: 'string' },
          directory: { type: 'string' },
          base: { type: 'string' },
          binary: { type: 'string' },
          problem: { type: 'string' },
        },
        required: ['on', 'role', 'directory', 'base', 'binary'],
      },
    },
  },
  events: {
    notice: {
      schema: {
        type: 'object',
        properties: {
          sessionID: { type: 'string' },
          message: { type: 'string' },
          variant: { type: 'string', enum: ['info', 'success', 'warning', 'error'] },
        },
        required: ['sessionID', 'message'],
      },
    },
  },
} as const);
