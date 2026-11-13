/**
 * The memcue CLI transport and binary install shared by the native harness plugins (#754).
 * @module memcue-transport
 */

export interface CliConfig {
  /** Executable path or name; never empty, never with a NUL. */
  binary: string;
  /** MEMCUE_FACTS override; omitted keeps the ambient value. */
  facts?: string;
  /** MEMCUE_STATE_DIR override. */
  stateDir?: string;
  /** MEMCUE_HOST override. */
  host?: string;
  /** MEMCUE_CALLER, the host's name; omitted leaves the ambient value. */
  caller?: string;
  /** Further environment overrides, applied last. */
  env?: Record<string, string>;
}

export interface CliResult {
  stdout: string;
  /** For a mutation, the diagnostics before the receipt marker, plus transport notes. */
  stderr: string;
  /** Null when the process did not exit on its own. */
  exitCode: number | null;
  outcome: 'read' | 'committed' | 'refused' | 'unknown' | 'not-started';
}

export interface CliRunOptions {
  /** Working directory of the memcue call. */
  cwd: string;
  /** Text written to the command's stdin. */
  stdin?: string;
  /** The call writes facts: it gets a nonce and its receipt is checked. */
  mutation?: boolean;
  /** Cancellation; a started mutation becomes unknown, never retried. */
  signal?: AbortSignal;
  /** Hard deadline, default 15000, at most 2147483647. */
  timeoutMs?: number;
}

/** Local-only memcue transport. It never installs a binary, calls a model or retries. */
export class MemcueCli {
  constructor(config: CliConfig);
  run(args: string[], options: CliRunOptions): Promise<CliResult>;
}

/** Path of the machine's binary, whether or not it exists. */
export function installedPath(): string;

/** Whether path is an executable file. */
export function executable(path: string): Promise<boolean>;

/** Whether the binary at path is the given release or newer, by its own `version -minimum`. */
export function current(path: string, version: string): Promise<boolean>;

/**
 * Install the binary of a release unless the machine's binary is that release or newer. The archive must match its
 * line in the release's checksums.txt; it is unpacked next to the target and renamed over it, so a reader never sees
 * half a binary and a memcue that is running keeps its file.
 * @param version - release version without the leading v.
 * @returns the path of the installed binary.
 * @throws when auto-install is off, the platform has no release, the machine's binary is not a plain file, or the
 *   download or the checksum fails.
 */
export function install(version: string): Promise<string>;

/** One fact of a parsed `memcue search -json` answer; only `id` and `content` are required to render it. */
export interface SearchRenderFact {
  id?: unknown;
  version?: unknown;
  content?: unknown;
  /** `global`, `repo:KEY`, `host:NAME`, `service:NAME` or `shelf:SLUG`. */
  scope?: unknown;
  /** Served under the score floor; the Hermes provider sets it and the line says so. */
  below_floor?: unknown;
}

export interface SearchRenderInput {
  facts?: readonly SearchRenderFact[];
  /** The lines the binary prints after its facts, kept word for word. */
  hints?: readonly unknown[];
  widened?: unknown;
}

export interface SearchRenderOptions {
  /** The most content characters of one fact, clipped with an ellipsis; default 600. */
  contentChars?: number;
  /** The budget of the fact lines; default 4000. The trailer line is added on top. */
  totalChars?: number;
  /** The most fact lines; default 10. */
  maxFacts?: number;
}

/** What a host shows when a search found nothing, the wording the CLI itself prints. */
export const NO_FACTS: string;

/** The default bound of a rendered answer. */
export const SEARCH_RENDER_DEFAULTS: Readonly<{ contentChars: number; totalChars: number; maxFacts: number }>;

/**
 * The text a native host gives the model for a search answer (#756): one line per fact with `<id>@<version>`, the
 * scope and the content, then one trailer line with the count, the widening and the binary's hints. A cut answer
 * says how many facts it left out.
 * @param answer - what `memcue search -json` printed, parsed.
 * @param options - the bounds of the rendering.
 */
export function renderSearch(answer: SearchRenderInput | null | undefined, options?: SearchRenderOptions): string;
