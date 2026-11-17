/**
 * The status the server part answers and the TUI part renders (#757). Both readers format one `SessionStatus`, so
 * the `/memcue status` text and the memory indicator cannot disagree. Pure: no I/O, no environment.
 */
import type { NoticeVariant } from './rpc.ts';

export type Role = 'main' | 'subagent';

export interface SessionStatus {
  /** Whether this session gets memory. */
  readonly on: boolean;
  readonly role: Role;
  /** The agent a subagent session runs. */
  readonly agent?: string;
  /** The session directory memcue runs in. */
  readonly directory: string;
  /** The fact base: a path, a server name, or `default`. */
  readonly base: string;
  /** The configured binary, `MEMCUE_BIN`, or `auto-installed`. */
  readonly binary: string;
  /** Why memory is off or unusable: an old OpenCode, a configuration error, a failed binary probe. */
  readonly problem?: string;
}

/** The text of `/memcue status`, as the server part prints it there and hands it to the TUI part. */
export function statusText(status: SessionStatus): string {
  const role = status.role === 'main' ? 'main' : status.agent ? `subagent (${status.agent})` : 'subagent';
  const head = `Memcue: ${status.on ? 'on' : 'off'}; role: ${role}; `
    + `directory: ${status.directory}; base: ${status.base}; binary: ${status.binary}.`;
  if (!status.problem) return `${head} Status does not probe the binary.`;
  const reason = status.problem.endsWith('.') ? status.problem : `${status.problem}.`;
  return `${head} ${reason} Status does not probe the binary.`;
}

/** The compact text of the prompt-footer indicator: the same state, cut to one line. */
export function indicatorText(status: SessionStatus): string {
  const parts = [`memcue ${status.on ? 'on' : 'off'}`];
  if (status.role === 'subagent') parts.push(status.agent ? `subagent ${status.agent}` : 'subagent');
  if (status.problem) parts.push(shorten(status.problem));
  return parts.join(' · ');
}

/** The toast the TUI part shows once for a status with a problem; nothing when there is none. */
export function problemNotice(status: SessionStatus): string | undefined {
  if (!status.problem) return undefined;
  return status.on ? `Memcue: ${status.problem}` : offNotice(status.problem);
}

/** The toast for a reason memory is off before any session exists: a configuration error, an older OpenCode. */
export function offNotice(problem: string): string {
  return `Memcue is off: ${problem}`;
}

/** Read the `health` answer: the reason memory is off for the whole plugin, if any. */
export function asProblem(value: unknown): string | undefined {
  const answer = object(value);
  return answer && typeof answer.problem === 'string' ? answer.problem : undefined;
}

/** The toast title, so several memcue toasts stay distinguishable in the TUI. */
export const TOAST_TITLE = 'memcue';

/** A user-visible outcome the server part announces to the TUI part. */
export interface Notice {
  readonly sessionID: string;
  readonly message: string;
  readonly variant: NoticeVariant;
}

/** The indicator cuts a long reason instead of hiding it: the ellipsis is always explicit. */
const INDICATOR_LIMIT = 80;

function shorten(text: string): string {
  if (text.length <= INDICATOR_LIMIT) return text;
  return `${text.slice(0, INDICATOR_LIMIT - 1).trimEnd()}…`;
}

function object(value: unknown): Record<string, unknown> | undefined {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return undefined;
  return value as Record<string, unknown>;
}

/**
 * Read a `status` answer. The RPC schema types the output as unknown, so the TUI part narrows it here instead of
 * asserting: an answer of an unexpected shape leaves the indicator without state rather than breaking the TUI.
 */
export function asStatus(value: unknown): SessionStatus | undefined {
  const answer = object(value);
  if (!answer) return undefined;
  const { on, role, directory, base, binary } = answer;
  if (typeof on !== 'boolean' || (role !== 'main' && role !== 'subagent')) return undefined;
  if (typeof directory !== 'string' || typeof base !== 'string' || typeof binary !== 'string') return undefined;
  return {
    on, role, directory, base, binary,
    ...(typeof answer.agent === 'string' ? { agent: answer.agent } : {}),
    ...(typeof answer.problem === 'string' ? { problem: answer.problem } : {}),
  };
}

const VARIANTS: readonly NoticeVariant[] = ['info', 'success', 'warning', 'error'];

/** Read a `notice` event payload; a payload of another shape is dropped rather than shown wrong. */
export function asNotice(value: unknown): Notice | undefined {
  const event = object(value);
  if (!event) return undefined;
  const { sessionID, message, variant } = event;
  if (typeof sessionID !== 'string' || typeof message !== 'string') return undefined;
  return {
    sessionID,
    message,
    variant: VARIANTS.find(known => known === variant) ?? 'info',
  };
}
