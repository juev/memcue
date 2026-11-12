/** Offline integration harness: real Pi session/extension loader, deterministic provider and CLI. */
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import {
  createAgentSession, DefaultResourceLoader, initTheme, ModelRuntime,
  SessionManager, SettingsManager,
  type ExtensionError,
} from '@earendil-works/pi-coding-agent';
import {
  createAssistantMessageEventStream, getCurrentSystemPrompt,
  type AssistantMessage, type ToolCall,
} from '@earendil-works/pi-ai';

export const PI_PACKAGE_ROOT = dirname(dirname(fileURLToPath(import.meta.url)));
export const PITFALL = 'PI-SDK-FACT: rebuild the generated schema before compiling the fixture.';

export type CliCall = { args: string[]; input: string; facts?: string; stateDir?: string; host?: string };

export async function piFixture(options: { versionDelayMs?: number; snapshotDelayMs?: number } = {}) {
  const versionDelayMs = options.versionDelayMs ?? 0;
  const snapshotDelayMs = options.snapshotDelayMs ?? 0;
  assert.ok([versionDelayMs, snapshotDelayMs].every(value => Number.isFinite(value) && value >= 0 && value <= 200));
  const root = await mkdtemp(join(tmpdir(), 'memcue-pi-sdk-'));
  const cwd = join(root, 'project');
  const agentDir = join(root, 'agent');
  await Promise.all([mkdir(cwd), mkdir(agentDir)]);
  const callsFile = join(root, 'calls.jsonl');
  const snapshotFile = join(root, 'snapshot.json');
  const binary = join(root, 'memcue-fixture.cjs');
  const facts = join(root, 'facts');
  const stateDir = join(root, 'state');
  await writeFile(snapshotFile, JSON.stringify({ facts: [PITFALL], context: 'Repository pitfalls for fixture:' }));
  await writeFile(binary, `#!${process.execPath}
const fs = require('node:fs');
const {createHash} = require('node:crypto');
const args = process.argv.slice(2);
const input = fs.readFileSync(0, 'utf8');
fs.appendFileSync(${JSON.stringify(callsFile)}, JSON.stringify({args,input,facts:process.env.MEMCUE_FACTS,stateDir:process.env.MEMCUE_STATE_DIR,host:process.env.MEMCUE_HOST})+'\\n');
const delayMs = args[0] === 'version' ? ${versionDelayMs} : args[0] === 'session-card' ? ${snapshotDelayMs} : 0;
if (delayMs) Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, delayMs);
let output = '', error = '', status = 0;
switch (args[0]) {
case 'version': output = JSON.stringify({version:'0.36.1'})+'\\n'; break;
case 'session-card':
  if (args.length !== 2 || args[1] !== '-pitfalls-json') { status=2; error='unexpected session-card arguments'; }
  else output = fs.readFileSync(${JSON.stringify(snapshotFile)}, 'utf8')+'\\n';
  break;
case 'search': output = JSON.stringify({facts:[{id:'pi-fixture-fact',content:'Search result for '+args.at(-1),origin:'offline fixture',version:1}]})+'\\n'; break;
default: status=2; error='unexpected memcue command: '+args.join(' ');
}
process.stdout.write(output);
process.stderr.write(error);
if (process.env.MEMCUE_OUTCOME_NONCE) {
  const hash = text => createHash('sha256').update(text).digest('hex');
  process.stderr.write('\\nMEMCUE_OUTCOME_V1 '+JSON.stringify({version:1,nonce:process.env.MEMCUE_OUTCOME_NONCE,status:status===0?'success':'refused',bytes:Buffer.byteLength(output),sha256:hash(output),error_bytes:Buffer.byteLength(error),error_sha256:hash(error)})+'\\n');
}
process.exit(status);
`, { mode: 0o755 });
  return {
    root, cwd, agentDir, binary, facts, stateDir,
    async calls(): Promise<CliCall[]> {
      try { return (await readFile(callsFile, 'utf8')).trim().split('\n').filter(Boolean).map(line => JSON.parse(line)); }
      catch (error) { if ((error as NodeJS.ErrnoException).code === 'ENOENT') return []; throw error; }
    },
    async snapshot(fact: string) {
      await writeFile(snapshotFile, JSON.stringify({ facts: [fact], context: 'Repository pitfalls for fixture:' }));
    },
    async empty() {
      await writeFile(snapshotFile, JSON.stringify({ facts: [], context: '' }));
    },
    async dispose() { await rm(root, { recursive: true, force: true }); },
  };
}

export type PiFixture = Awaited<ReturnType<typeof piFixture>>;

/** Observe the CLI handshake so cancellation tests interrupt work, rather than racing startup. */
export async function waitForCliCall(fixture: PiFixture, command: string): Promise<void> {
  const deadline = Date.now() + 2_000;
  while (Date.now() < deadline) {
    if ((await fixture.calls()).some(call => call.args[0] === command)) return;
    await delay(5);
  }
  assert.fail(`Timed out waiting for the ${command} subprocess to start`);
}

export async function openPi(
  fixture: PiFixture,
  options: { entry?: string; entries?: string[]; agent?: string; child?: boolean; enabled?: boolean; tools?: ToolCall[];
    /** Leave memcue.binary out of the settings, so the plugin finds or installs its binary itself. */
    autoBinary?: boolean } = {},
) {
  const requests: unknown[] = [];
  const errors: ExtensionError[] = [];
  let pendingTools = options.tools;
  const runtime = await ModelRuntime.create({
    authPath: join(fixture.agentDir, 'auth.json'),
    modelsPath: null,
    modelsStorePath: join(fixture.agentDir, 'models-cache.json'),
    allowModelNetwork: false,
    refreshOnCreate: false,
  });
  runtime.registerProvider('memcue-offline-test', {
    api: 'openai-completions',
    apiKey: 'offline-fixture-not-a-real-key',
    baseUrl: 'http://127.0.0.1:1/must-never-be-contacted',
    models: [{ id: 'deterministic', name: 'Offline test model', reasoning: false, input: ['text'],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 100_000, maxTokens: 1024 }],
    streamSimple(model, context) {
      // The whole request: the system prompt carries the memcue sections, the messages carry the rest.
      requests.push(structuredClone(context));
      const calls = pendingTools;
      pendingTools = undefined;
      const message: AssistantMessage = {
        role: 'assistant', api: model.api, provider: model.provider, model: model.id,
        content: calls ?? [{ type: 'text', text: 'Offline test complete.' }],
        stopReason: calls ? 'toolUse' : 'stop', timestamp: Date.now(),
        usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2,
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
      };
      const stream = createAssistantMessageEventStream();
      stream.push({ type: 'start', partial: message });
      stream.push({ type: 'done', reason: calls ? 'toolUse' : 'stop', message });
      stream.end();
      return stream;
    },
  });
  const model = runtime.getModel('memcue-offline-test', 'deterministic');
  assert.ok(model, 'offline model must be registered');
  const settingsManager = SettingsManager.inMemory({
    compaction: { enabled: false }, retry: { enabled: false },
    memcue: { enabled: options.enabled ?? true, ...(options.autoBinary ? {} : { binary: fixture.binary }), facts: fixture.facts,
      stateDir: fixture.stateDir, host: 'pi-test-host',
      subagents: { defaultEnabled: false, agents: { worker: true, 'maintainability-reviewer': false } } },
  } as Parameters<typeof SettingsManager.inMemory>[0]);
  let entry = options.entry ?? join(PI_PACKAGE_ROOT, 'index.ts');
  if (options.agent) {
    entry = join(fixture.root, 'role-extension.ts');
    await writeFile(entry,
      `import { createMemcueExtension } from ${JSON.stringify(join(PI_PACKAGE_ROOT, 'src/extension.ts'))};\nexport default createMemcueExtension({ agent: ${JSON.stringify(options.agent)} });\n`);
  }
  initTheme('dark');
  const loader = new DefaultResourceLoader({
    cwd: fixture.cwd, agentDir: fixture.agentDir, settingsManager,
    noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true,
    systemPrompt: 'You are the isolated offline SDK test.',
    additionalExtensionPaths: options.entries ?? [entry],
  });
  await loader.reload();
  assert.deepEqual(loader.getExtensions().errors, [], 'native loader must load the Memcue entrypoint');
  const sessionManager = SessionManager.inMemory(fixture.cwd,
    options.child ? { parentSession: join(fixture.root, 'parent.jsonl') } : undefined);
  const { session } = await createAgentSession({
    cwd: fixture.cwd, agentDir: fixture.agentDir, modelRuntime: runtime, model,
    resourceLoader: loader, settingsManager, sessionManager, noTools: 'builtin',
  });
  await session.bindExtensions({ mode: 'print', onError: error => errors.push(error) });
  return {
    session, requests, errors,
    async prompt(text = 'Review the offline fixture.') {
      await session.prompt(text);
      assert.deepEqual(errors, [], 'extension lifecycle must not report failures');
      assert.ok(requests.length > 0, 'offline provider must receive a request');
    },
    async dispose() {
      await session.extensionRunner.emit({ type: 'session_shutdown', reason: 'quit' });
      session.dispose();
      await runtime.settleCredentialWrites();
    },
  };
}

export function requestText(request: unknown): string {
  return JSON.stringify(request);
}

/** The system prompt the model has at this request: the first one with every later section patch applied. */
export function currentPrompt(request: unknown): string {
  return getCurrentSystemPrompt((request as { messages: Parameters<typeof getCurrentSystemPrompt>[0] }).messages);
}
