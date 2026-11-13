/**
 * The memcue CLI transport every native harness plugin shares (#754).
 *
 * One spawn per memcue call, a process-group kill, a hard timeout, the 4 MiB combined output budget and the
 * `MEMCUE_OUTCOME_V1` receipt check: nonce, byte counts and SHA-256 over stdout and over the diagnostics before
 * the marker. A mutation whose receipt cannot be proved is `unknown` and is never retried. Plain ESM on purpose:
 * the DeepSeek Harness plugin loads from `node_modules`, where Node refuses to strip TypeScript types, and
 * OpenCode runs plugins inside Bun.
 *
 * @module memcue-transport/transport
 */
import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';

/**
 * @typedef {object} CliConfig
 * @property {string} binary - executable path or name; never empty, never with a NUL.
 * @property {string} [facts] - MEMCUE_FACTS override; omitted keeps the ambient value.
 * @property {string} [stateDir] - MEMCUE_STATE_DIR override.
 * @property {string} [host] - MEMCUE_HOST override.
 * @property {string} [caller] - MEMCUE_CALLER, the host's name; omitted leaves the ambient value.
 * @property {Record<string, string>} [env] - further environment overrides, applied last.
 */

/**
 * @typedef {object} CliResult
 * @property {string} stdout
 * @property {string} stderr - for a mutation, the diagnostics before the receipt marker, plus transport notes.
 * @property {number | null} exitCode - null when the process did not exit on its own.
 * @property {'read' | 'committed' | 'refused' | 'unknown' | 'not-started'} outcome
 */

/**
 * @typedef {object} CliRunOptions
 * @property {string} cwd - working directory of the memcue call.
 * @property {string} [stdin] - text written to the command's stdin.
 * @property {boolean} [mutation] - the call writes facts: it gets a nonce and its receipt is checked.
 * @property {AbortSignal} [signal] - cancellation; a started mutation becomes unknown, never retried.
 * @property {number} [timeoutMs] - hard deadline, default 15000, at most 2147483647.
 */

const OUTPUT_LIMIT = 4 * 1024 * 1024;
const RECEIPT_MARKER = Buffer.from('\nMEMCUE_OUTCOME_V1 ');
const UNKNOWN = 'UNKNOWN OUTCOME: the mutation may have been written. Inspect durable facts and memcue history before deciding what to do next; do not automatically replay or retry it.';

/** @param {Buffer} bytes */
function digest(bytes) {
  return createHash('sha256').update(bytes).digest('hex');
}

/**
 * @param {Buffer} stdout
 * @param {Buffer} stderr
 * @param {string} nonce
 * @param {number | null} exitCode
 * @returns {{outcome: 'committed' | 'refused' | 'unknown', diagnostics: Buffer}}
 */
function checkReceipt(stdout, stderr, nonce, exitCode) {
  const unknown = { outcome: /** @type {const} */ ('unknown'), diagnostics: stderr };
  const at = stderr.lastIndexOf(RECEIPT_MARKER);
  if (at < 0 || stderr.at(-1) !== 10) return unknown;
  let value;
  try { value = JSON.parse(stderr.subarray(at + RECEIPT_MARKER.length, -1).toString('utf8')); }
  catch { return unknown; }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return unknown;
  const frame = /** @type {Record<string, unknown>} */ (value);
  const diagnostics = stderr.subarray(0, at);
  if (frame.version !== 1 || frame.nonce !== nonce
    || frame.bytes !== stdout.length || frame.sha256 !== digest(stdout)
    || frame.error_bytes !== diagnostics.length || frame.error_sha256 !== digest(diagnostics)
    || !['success', 'refused', 'unknown'].includes(String(frame.status))) return unknown;
  if (frame.status === 'success' && exitCode === 0) return { outcome: 'committed', diagnostics };
  if (frame.status === 'refused' && exitCode !== null && exitCode > 0) return { outcome: 'refused', diagnostics };
  return { outcome: 'unknown', diagnostics };
}

/** @param {string} text @param {string} message */
function diagnostic(text, message) {
  return text + (text && !text.endsWith('\n') ? '\n' : '') + message;
}

/** Local-only memcue transport. It never installs a binary, calls a model or retries. */
export class MemcueCli {
  /** @param {CliConfig} config */
  constructor(config) {
    if (typeof config.binary !== 'string' || !config.binary.trim() || config.binary.includes('\0')) {
      throw new Error('memcue: binary must be a nonempty executable path or name');
    }
    this.config = { ...config };
  }

  /**
   * @param {string[]} args
   * @param {CliRunOptions} options
   * @returns {Promise<CliResult>}
   */
  async run(args, options) {
    const timeoutMs = options.timeoutMs ?? 15_000;
    /** @param {string} stderr @returns {CliResult} */
    const notStarted = (stderr) => ({ stdout: '', stderr, exitCode: null, outcome: 'not-started' });
    if (options.signal?.aborted) return notStarted('memcue: cancelled before the executable started');
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 2_147_483_647) return notStarted('memcue: timeoutMs must be greater than zero and at most 2147483647');
    if (typeof options.cwd !== 'string' || !options.cwd || options.cwd.includes('\0')) return notStarted('memcue: cwd must be a nonempty directory path');
    if (!Array.isArray(args) || args.some(arg => typeof arg !== 'string' || arg.includes('\0'))) return notStarted('memcue: arguments must be strings without NUL characters');

    // Ambient MEMCUE_* settings reach the CLI, as with every host; the configured ones override them.
    const env = { ...process.env };
    delete env.MEMCUE_OUTCOME_NONCE;
    if (this.config.facts !== undefined) env.MEMCUE_FACTS = this.config.facts;
    if (this.config.stateDir !== undefined) env.MEMCUE_STATE_DIR = this.config.stateDir;
    if (this.config.host !== undefined) env.MEMCUE_HOST = this.config.host;
    if (this.config.caller !== undefined) env.MEMCUE_CALLER = this.config.caller;
    Object.assign(env, this.config.env);
    const nonce = options.mutation ? randomBytes(16).toString('hex') : '';
    if (nonce) env.MEMCUE_OUTCOME_NONCE = nonce;

    return new Promise(resolve => {
      /** @type {import('node:child_process').ChildProcess | undefined} */
      let child;
      /** @type {Buffer[]} */ const stdout = [], stderr = [];
      let bytes = 0, attempted = false, settled = false, stopping = false, exited = false;
      /** @type {number | null} */ let exitCode = null;
      let gotResult = false, spawnError = '', transportError = '';
      /** @type {ReturnType<typeof setTimeout> | undefined} */ let closeDeadline;

      const finish = () => {
        if (settled) return;
        settled = true;
        clearTimeout(deadline);
        if (closeDeadline) clearTimeout(closeDeadline);
        options.signal?.removeEventListener('abort', onAbort);
        const out = Buffer.concat(stdout), err = Buffer.concat(stderr);
        /** @type {CliResult} */
        let result = { stdout: out.toString('utf8'), stderr: err.toString('utf8'), exitCode, outcome: 'read' };
        if (spawnError || !attempted) {
          result = { ...result, outcome: 'not-started', stderr: diagnostic(result.stderr, spawnError || transportError || 'memcue: executable did not start') };
        } else if (options.mutation) {
          const checked = checkReceipt(out, err, nonce, exitCode);
          result.stderr = checked.diagnostics.toString('utf8');
          result.outcome = transportError || !gotResult ? 'unknown' : checked.outcome;
          if (transportError) result.stderr = diagnostic(result.stderr, transportError);
          if (result.outcome === 'unknown') result.stderr = diagnostic(result.stderr, UNKNOWN);
        } else if (transportError || !gotResult) {
          result.stderr = diagnostic(result.stderr, transportError || 'memcue: command response unavailable');
          result.exitCode = null;
        }
        // Destroy local pipe handles even if an abnormal child prevented closure.
        for (const stream of [child?.stdin, child?.stdout, child?.stderr]) stream?.destroy();
        resolve(result);
      };

      const stop = () => {
        if (stopping) return;
        stopping = true;
        // Only signal while the direct child is still ours. After its exit event,
        // do not risk signaling a process group whose ID could have been reused.
        if (child?.pid && !exited) {
          try {
            if (process.platform === 'win32') child.kill('SIGKILL');
            else process.kill(-child.pid, 'SIGKILL');
          } catch { /* already exited; close or the bounded fallback completes */ }
        }
      };
      /** @param {string} reason */
      const interrupt = (reason) => {
        if (settled) return;
        if (!transportError) transportError = reason;
        stop();
        // Normally exit closes the pipes immediately. A detached descendant may
        // retain a pipe after memcue exits, so collection also has a hard bound.
        closeDeadline ??= setTimeout(finish, 100);
      };
      const onAbort = () => interrupt('memcue: command cancelled');
      const deadline = setTimeout(() => interrupt(`memcue: command timed out after ${timeoutMs} ms`), timeoutMs);

      try {
        child = spawn(this.config.binary, args, {
          cwd: options.cwd, env, shell: false, detached: process.platform !== 'win32',
          stdio: ['pipe', 'pipe', 'pipe'],
        });
        attempted = child.pid !== undefined;
      } catch (error) {
        spawnError = `memcue: configured executable could not start: ${error instanceof Error ? error.message : String(error)}`;
        finish();
        return;
      }
      /** @param {Buffer[]} target @param {Buffer} chunk */
      const capture = (target, chunk) => {
        if (settled || transportError) return;
        const room = OUTPUT_LIMIT - bytes;
        if (room > 0) target.push(chunk.subarray(0, room));
        bytes += chunk.length;
        if (bytes > OUTPUT_LIMIT) interrupt(`memcue: combined output exceeded the ${OUTPUT_LIMIT}-byte output limit`);
      };
      child.stdout?.on('data', chunk => capture(stdout, chunk));
      child.stderr?.on('data', chunk => capture(stderr, chunk));
      // A command may exit before consuming stdin. Its receipt, not EPIPE, decides
      // whether a mutation was committed; never leave an unhandled stream error.
      child.stdin?.on('error', () => {});
      child.stdout?.on('error', () => interrupt('memcue: stdout transport unavailable'));
      child.stderr?.on('error', () => interrupt('memcue: stderr transport unavailable'));
      child.on('exit', (code, signal) => {
        exited = true;
        gotResult = true;
        exitCode = code;
        if (signal && !transportError) transportError = `memcue: executable terminated by ${signal}`;
      });
      child.on('error', error => {
        if (!attempted) spawnError = `memcue: configured executable could not start: ${error.message}`;
        else interrupt(`memcue: command transport unavailable: ${error.message}`);
      });
      child.on('close', finish);
      options.signal?.addEventListener('abort', onAbort, { once: true });
      // Account for a signal aborted between the initial guard and registration.
      if (options.signal?.aborted) onAbort();
      child.stdin?.end(options.stdin ?? '');
    });
  }
}