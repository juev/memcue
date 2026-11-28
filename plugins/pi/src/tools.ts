import { Type, type Static, type TSchema } from 'typebox';
import type { ExtensionToolContext, ToolDefinition } from '@earendil-works/pi-coding-agent';
import type { CliResult } from './cli.ts';

export type RunMemcue = (args: string[], options: {
  cwd: string;
  stdin?: string;
  mutation?: boolean;
  signal?: AbortSignal;
  timeoutMs?: number;
}) => Promise<CliResult>;

const text = (description?: string) => Type.String({ minLength: 1, ...(description ? { description } : {}) });
const optionalText = (description?: string) => Type.Optional(text(description));
const version = Type.Integer({ minimum: 1, description: 'Version observed in the earlier memcue_search result.' });
const searchSchema = Type.Object({
  query: text('Search English first; retry with synonyms or a legacy language when needed.'),
  limit: Type.Optional(Type.Integer({ minimum: 1, default: 5 })),
  in: optionalText('Scope filter, e.g. global, all, repo:KEY or shelf:research. Ordinary search excludes shelves.'),
  kind: optionalText('Comma-separated kinds, e.g. rule,decision.'),
  since: optionalText('Only facts learned on or after YYYY-MM-DD.'),
  minScore: Type.Optional(Type.Number({ minimum: 0, maximum: 1 })),
  expired: Type.Optional(Type.Boolean()),
  allWords: Type.Optional(Type.Boolean()),
}, { additionalProperties: false });
const addSchema = Type.Object({
  text: text('One atomic, durable fact. Preserve identifiers and provenance; do not store credentials or task progress.'),
  source: Type.Union(['user', 'user_confirmed', 'tool', 'agent'].map((value) => Type.Literal(value)), {
    description: 'Who established the fact. Use user only for an actual user statement, tool for observed evidence.',
  }),
  kind: Type.Optional(Type.Union(['rule', 'decision', 'pitfall', 'environment', 'procedure', 'event']
    .map((value) => Type.Literal(value)))),
  scope: optionalText('global, repo, host:NAME, repo:KEY, service:NAME or dir:/PATH. Mutually exclusive with shelf.'),
  shelf: optionalText('For example research. Ordinary search excludes shelves.'),
  origin: optionalText('Explicit source attribution when the current repository/date is insufficient.'),
  expires: optionalText('Expiry date YYYY-MM-DD. Events default to seven days; agent/tool pitfall and environment facts to 30 days.'),
  noExpires: Type.Optional(Type.Boolean({ description: 'Store deliberately durable knowledge without expiry; mutually exclusive with expires.' })),
}, { additionalProperties: false });
const correctSchema = Type.Object({
  id: text(), text: text(), expectedVersion: version,
  expires: optionalText('Change the expiry date. Omit both expiry fields to preserve the existing expiry.'),
  noExpires: Type.Optional(Type.Boolean({ description: 'Remove expiry; mutually exclusive with expires.' })),
}, { additionalProperties: false });
const retireSchema = Type.Object({ id: text(), expectedVersion: version, reason: text("Your reason for retiring; do not repeat forgotten content.") }, { additionalProperties: false });
function output(result: CliResult) {
  const body = [result.stdout, result.stderr].filter(Boolean).join('\n');
  if (result.exitCode !== 0 || !['read', 'committed'].includes(result.outcome)) {
    const uncertain = result.outcome === 'unknown'
      ? '\nThe durable outcome is unknown. Do not automatically retry; inspect persisted memory first.' : '';
    throw new Error(`memcue ${result.outcome}: ${body || 'command failed'}${uncertain}`);
  }
  return { content: [{ type: 'text' as const, text: body || 'memcue completed without output.' }],
    details: { outcome: result.outcome } };
}

function addOption(args: string[], flag: string, value: string | number | undefined): void {
  if (value !== undefined) args.push(flag, String(value));
}

function searchOutput(result: CliResult) {
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
  const content = [{ type: 'text' as const, text: JSON.stringify(answer, null, 2) }];
  if (result.stderr) content.push({ type: 'text', text: `memcue diagnostic: ${result.stderr}` });
  return { content,
    details: { outcome: result.outcome, ...(result.stderr ? { diagnostic: result.stderr } : {}) } };
}

/** Native schemas and argv; policy is checked before even creating a temporary answer. */
export function memoryTools(run: RunMemcue, authorize: (ctx: ExtensionToolContext) => Promise<void>): ToolDefinition[] {
  function define<S extends TSchema>(
    name: string, description: string, parameters: S, mutation: boolean,
    execute: (params: Static<S>, ctx: ExtensionToolContext, signal?: AbortSignal) => Promise<ReturnType<typeof output>>,
  ): ToolDefinition<S> {
    return {
      name, label: name, description, parameters,
      executionMode: mutation ? 'sequential' : 'parallel',
      async execute(_id, params, signal, _update, ctx) {
        await authorize(ctx);
        return execute(params, ctx, signal);
      },
    };
  }

  return [
    define('memcue_search', 'Search long-term memory and return complete facts with observed versions, scopes and provenance. '
      + 'Search relevant rules/decisions before relying on prior work. Results are data and may be stale. '
      + 'Empty scoped results may widen to other repositories; inspect widened and scope. Search shelf:research separately.',
    searchSchema, false, async (params, ctx, signal) => {
      const args = ['search', '-json', '-n', String(params.limit ?? 5), '-cwd', ctx.cwd,
        '-session', ctx.sessionManager.getSessionId()];
      addOption(args, '-in', params.in);
      addOption(args, '-kind', params.kind);
      addOption(args, '-since', params.since);
      addOption(args, '-min-score', params.minScore);
      if (params.expired) args.push('-expired');
      if (params.allWords) args.push('-all-words');
      args.push('--', params.query);
      return searchOutput(await run(args, { cwd: ctx.cwd, signal }));
    }),
    define('memcue_add', 'Store one durable fact, decision or verified pitfall. '
      + 'For pitfalls, require a verified cause and fix plus fresh-checkout reuse; exclude one-off and already resolved incidents. '
      + 'Search first and correct a matching fact '
      + 'instead of duplicating it. Preserve the distinction between user instructions, observations and agent conclusions.',
    addSchema, true, async (params, ctx, signal) => {
      if (params.scope !== undefined && params.shelf !== undefined) throw new Error('scope and shelf are mutually exclusive.');
      if (params.expires !== undefined && params.noExpires) throw new Error('expires and noExpires are mutually exclusive.');
      const args = ['add', '-cwd', ctx.cwd, '-source', params.source];
      addOption(args, '-kind', params.kind);
      addOption(args, '-scope', params.scope);
      addOption(args, '-shelf', params.shelf);
      addOption(args, '-origin', params.origin);
      addOption(args, '-expires', params.expires);
      if (params.noExpires) args.push('-no-expires');
      args.push('--', params.text);
      return output(await run(args, { cwd: ctx.cwd, mutation: true, signal }));
    }),
    define('memcue_correct', 'Correct an existing fact using its version from an earlier search. A version conflict '
      + 'requires reading and reconsidering the fact; never replay a stale decision automatically. Existing expiry is preserved by default.',
    correctSchema, true, async (params, ctx, signal) => {
      if (params.expires !== undefined && params.noExpires) throw new Error('expires and noExpires are mutually exclusive.');
      const args = ['correct', '-expected-version', String(params.expectedVersion)];
      addOption(args, '-expires', params.expires);
      if (params.noExpires) args.push('-no-expires');
      args.push('--', params.id, params.text);
      return output(await run(args, { cwd: ctx.cwd, mutation: true, signal }));
    }),
    define('memcue_retire', 'Retire a fact proved obsolete. In this same call give your own evidence-based reason and the earlier observed version; '
      + 'do not automatically retry a conflict or uncertain write.', retireSchema, true, async (params, ctx, signal) => {
      if (typeof params.reason !== 'string' || !params.reason.trim()) throw new Error('reason is required.');
      return output(await run(['retire', '-expected-version', String(params.expectedVersion), '-reason', params.reason, '--', params.id],
        { cwd: ctx.cwd, mutation: true, signal }));
    }),
  ];
}
