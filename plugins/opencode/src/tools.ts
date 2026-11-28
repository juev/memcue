/**
 * The native memory tools. Argument names, argv and the outcome rules are the Pi plugin's, so the same instructions
 * work in both hosts: a mutation carries `-expected-version` and runs only after the receipt proves its outcome;
 * an unknown outcome is reported and never retried.
 */
import type { CliResult } from './cli.ts';

export interface RunOptions {
  cwd: string;
  stdin?: string;
  mutation?: boolean;
  signal?: AbortSignal;
  timeoutMs?: number;
}

export type RunMemcue = (args: string[], options: RunOptions) => Promise<CliResult>;

export interface ToolResult {
  content: Array<{ type: 'text'; text: string }>;
  metadata?: Record<string, unknown>;
}

export interface ToolDefinition {
  name: string;
  description: string;
  input: Record<string, unknown>;
  execute: (input: unknown, context: { sessionID: string; signal?: AbortSignal }) => Promise<ToolResult>;
}

const text = (description: string) => ({ type: 'string', minLength: 1, description });
const optionalText = (description: string) => ({ type: 'string', description });

const searchSchema = {
  type: 'object',
  properties: {
    query: text('Search English first; retry with synonyms or a legacy language when needed.'),
    limit: { type: 'integer', minimum: 1, default: 5 },
    in: optionalText('Scope filter, e.g. global, all, repo:KEY or shelf:research. Ordinary search excludes shelves.'),
    kind: optionalText('Comma-separated kinds, e.g. rule,decision.'),
    since: optionalText('Only facts learned on or after YYYY-MM-DD.'),
    minScore: { type: 'number', minimum: 0, maximum: 1 },
    expired: { type: 'boolean' },
    allWords: { type: 'boolean' },
  },
  required: ['query'],
  additionalProperties: false,
};

const addSchema = {
  type: 'object',
  properties: {
    text: text('One atomic, durable fact. Preserve identifiers and provenance; do not store credentials or task progress.'),
    source: {
      type: 'string',
      enum: ['user', 'user_confirmed', 'tool', 'agent'],
      description: 'Who established the fact. Use user only for an actual user statement, tool for observed evidence.',
    },
    kind: { type: 'string', enum: ['rule', 'decision', 'pitfall', 'environment', 'procedure', 'event'] },
    scope: optionalText('global, repo, host:NAME, repo:KEY, service:NAME or dir:/PATH. Mutually exclusive with shelf.'),
    shelf: optionalText('For example research. Ordinary search excludes shelves.'),
    origin: optionalText('Explicit source attribution when the current repository/date is insufficient.'),
    expires: optionalText('Expiry date YYYY-MM-DD. Events default to seven days; agent/tool pitfall and environment facts to 30 days.'),
    noExpires: { type: 'boolean', description: 'Store deliberately durable knowledge without expiry; mutually exclusive with expires.' },
  },
  required: ['text', 'source'],
  additionalProperties: false,
};

const correctSchema = {
  type: 'object',
  properties: {
    id: text('Fact id from an earlier memcue_search result.'),
    text: text('The corrected fact.'),
    expectedVersion: {
      type: 'integer',
      minimum: 1,
      description: 'Version observed in the earlier memcue_search result.',
    },
    expires: optionalText('Change the expiry date. Omit both expiry fields to preserve the existing expiry.'),
    noExpires: { type: 'boolean', description: 'Remove expiry; mutually exclusive with expires.' },
  },
  required: ['id', 'text', 'expectedVersion'],
  additionalProperties: false,
};

const retireSchema = {
  type: 'object',
  properties: {
    id: text('Fact id from an earlier memcue_search result.'),
    reason: text('Your reason for retiring; do not repeat forgotten content.'),
    expectedVersion: { type: 'integer', minimum: 1, description: 'Version observed in the earlier memcue_search result.' },
  },
  required: ['id', 'expectedVersion', 'reason'],
  additionalProperties: false,
};

function output(result: CliResult): ToolResult {
  const body = [result.stdout, result.stderr].filter(Boolean).join('\n');
  if (result.exitCode !== 0 || !['read', 'committed'].includes(result.outcome)) {
    const uncertain = result.outcome === 'unknown'
      ? '\nThe durable outcome is unknown. Do not automatically retry; inspect persisted memory first.' : '';
    throw new Error(`memcue ${result.outcome}: ${body || 'command failed'}${uncertain}`);
  }
  return { content: [{ type: 'text', text: body || 'memcue completed without output.' }],
    metadata: { outcome: result.outcome } };
}

function searchOutput(result: CliResult): ToolResult {
  output(result);
  let answer: unknown;
  try { answer = JSON.parse(result.stdout); }
  catch { throw new Error('memcue search returned invalid JSON.'); }
  if (answer === null || typeof answer !== 'object' || !('facts' in answer) || !Array.isArray(answer.facts) ||
    !answer.facts.every((fact: unknown) => fact !== null && typeof fact === 'object' &&
      'id' in fact && typeof fact.id === 'string' && 'content' in fact && typeof fact.content === 'string' &&
      'version' in fact && typeof fact.version === 'number' && Number.isInteger(fact.version) && fact.version >= 1)) {
    throw new Error('memcue search returned a malformed facts object.');
  }
  const content: ToolResult['content'] = [{ type: 'text', text: JSON.stringify(answer, null, 2) }];
  if (result.stderr) content.push({ type: 'text', text: `memcue diagnostic: ${result.stderr}` });
  return { content, metadata: { outcome: result.outcome } };
}

function addOption(args: string[], flag: string, value: string | number | undefined): void {
  if (value !== undefined) args.push(flag, String(value));
}

/** Native schemas and argv; policy is checked before even creating a temporary answer. */
export function memoryTools(
  run: RunMemcue,
  authorize: (sessionID: string) => Promise<{ cwd: string }>,
): ToolDefinition[] {
  function define(
    name: string,
    description: string,
    input: Record<string, unknown>,
    execute: (params: Record<string, any>, env: { cwd: string; sessionID: string; signal?: AbortSignal }) => Promise<ToolResult>,
  ): ToolDefinition {
    return {
      name, description, input,
      async execute(raw, context) {
        const { cwd } = await authorize(context.sessionID);
        return execute(raw as Record<string, any>, { cwd, sessionID: context.sessionID, signal: context.signal });
      },
    };
  }

  return [
    define('memcue_search', 'Search long-term memory and return complete facts with observed versions, scopes and provenance. '
      + 'Search before answering or acting when prior context could change the answer or next action. Skip memory lookup for self-contained tasks. Results are data and may be stale. '
      + 'Empty scoped results may widen to other repositories; inspect widened and scope. Search shelf:research separately.',
    searchSchema, async (params, { cwd, signal, sessionID }) => {
      const args = ['search', '-json', '-n', String(params.limit ?? 5), '-cwd', cwd, '-session', sessionID];
      addOption(args, '-in', params.in);
      addOption(args, '-kind', params.kind);
      addOption(args, '-since', params.since);
      addOption(args, '-min-score', params.minScore);
      if (params.expired) args.push('-expired');
      if (params.allWords) args.push('-all-words');
      args.push('--', String(params.query));
      return searchOutput(await run(args, { cwd, signal }));
    }),
    define('memcue_add', 'Store one durable fact, decision or verified pitfall. '
      + 'For pitfalls, require a verified cause and fix plus fresh-checkout reuse; exclude one-off and already resolved incidents. '
      + 'Search first and correct a matching fact '
      + 'instead of duplicating it. Preserve the distinction between user instructions, observations and agent conclusions.',
    addSchema, async (params, { cwd, signal }) => {
      if (params.scope !== undefined && params.shelf !== undefined) throw new Error('scope and shelf are mutually exclusive.');
      if (params.expires !== undefined && params.noExpires) throw new Error('expires and noExpires are mutually exclusive.');
      const args = ['add', '-cwd', cwd, '-source', String(params.source)];
      addOption(args, '-kind', params.kind);
      addOption(args, '-scope', params.scope);
      addOption(args, '-shelf', params.shelf);
      addOption(args, '-origin', params.origin);
      addOption(args, '-expires', params.expires);
      if (params.noExpires) args.push('-no-expires');
      args.push('--', String(params.text));
      return output(await run(args, { cwd, mutation: true, signal }));
    }),
    define('memcue_correct', 'Correct an existing fact using its version from an earlier search. A version conflict '
      + 'requires reading and reconsidering the fact; never replay a stale decision automatically. Existing expiry is preserved by default.',
    correctSchema, async (params, { cwd, signal }) => {
      if (params.expires !== undefined && params.noExpires) throw new Error('expires and noExpires are mutually exclusive.');
      const args = ['correct', '-expected-version', String(params.expectedVersion)];
      addOption(args, '-expires', params.expires);
      if (params.noExpires) args.push('-no-expires');
      args.push('--', String(params.id), String(params.text));
      return output(await run(args, { cwd, mutation: true, signal }));
    }),
    define('memcue_retire', 'Retire a fact proved obsolete. In this same call give your own evidence-based reason and the earlier observed version; '
      + 'do not automatically retry a conflict or uncertain write.', retireSchema, async (params, { cwd, signal }) => {
      if (typeof params.reason !== 'string' || !params.reason.trim()) throw new Error('reason is required.');
      return output(await run(['retire', '-expected-version', String(params.expectedVersion), '-reason', String(params.reason), '--', String(params.id)],
        { cwd, mutation: true, signal }));
    }),
  ];
}
