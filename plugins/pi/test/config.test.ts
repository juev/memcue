import assert from 'node:assert/strict';
import { homedir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { isEnabled, readConfig } from '../src/config.ts';

test('defaults enable the main session and deny child sessions', () => {
  const config = readConfig({});
  assert.equal(config.enabled, true);
  assert.equal(config.binary, undefined, 'without a binary the plugin installs its release');
  assert.equal(config.facts, undefined);
  assert.equal(config.stateDir, undefined);
  assert.equal(config.host, undefined);
  assert.equal(config.subagents.defaultEnabled, false);
  assert.equal(isEnabled(config, { isChild: false }), true);
  assert.equal(isEnabled(config, { isChild: true }), false);
  assert.equal(isEnabled(config, { isChild: true, agent: 'worker' }), false);
});

test('an explicit role overrides the child default', () => {
  const config = readConfig({
    memcue: {
      subagents: {
        defaultEnabled: true,
        agents: { 'maintainability-reviewer': false, worker: true },
      },
    },
  });
  assert.equal(isEnabled(config, { isChild: true, agent: 'worker' }), true);
  assert.equal(isEnabled(config, { isChild: true, agent: 'maintainability-reviewer' }), false);
  assert.equal(isEnabled(config, { isChild: true, agent: 'scout' }), true);
  // A main entrypoint must never become active just because children are enabled.
  assert.equal(isEnabled(config, { isChild: true }), false);
});

test('master and session disables cannot be overridden by an allowed role', () => {
  const config = readConfig({ memcue: { subagents: { agents: { worker: true } } } });
  assert.equal(isEnabled(config, { isChild: false, sessionDisabled: true }), false);
  assert.equal(isEnabled(config, { isChild: true, agent: 'worker', sessionDisabled: true }), false);
  config.enabled = false;
  assert.equal(isEnabled(config, { isChild: false }), false);
  assert.equal(isEnabled(config, { isChild: true, agent: 'worker' }), false);
});

test('configuration expands home paths but accepts only unambiguous paths', () => {
  const config = readConfig({
    unrelated: true,
    memcue: { binary: '~/bin/memcue', facts: '~/memory/facts', stateDir: '~/memory/state', host: 'dev-mac' },
  });
  assert.equal(config.binary, join(homedir(), 'bin/memcue'));
  assert.equal(config.facts, join(homedir(), 'memory/facts'));
  assert.equal(config.stateDir, join(homedir(), 'memory/state'));
  assert.equal(config.host, 'dev-mac');
  assert.equal(readConfig({ memcue: { binary: '/opt/bin/memcue' } }).binary, '/opt/bin/memcue');
  for (const binary of ['./memcue', '../memcue', 'bin/memcue', '~someone/memcue']) {
    assert.throws(() => readConfig({ memcue: { binary } }), /memcue\.binary/);
  }
  assert.throws(() => readConfig({ memcue: { facts: 'facts.json' } }), /memcue\.facts/);
  assert.throws(() => readConfig({ memcue: { stateDir: './state' } }), /memcue\.stateDir/);
});

test('invalid and unknown settings report their field rather than silently enabling memory', () => {
  const invalid: Array<[unknown, RegExp]> = [
    [{ memcue: null }, /memcue/],
    [{ memcue: [] }, /memcue/],
    [{ memcue: { enabled: 'false' } }, /memcue\.enabled/],
    [{ memcue: { enabled: null } }, /memcue\.enabled/],
    [{ memcue: { binary: '' } }, /memcue\.binary/],
    [{ memcue: { binary: 'memcue\0' } }, /memcue\.binary/],
    [{ memcue: { host: 7 } }, /memcue\.host/],
    [{ memcue: { facts: null } }, /memcue\.facts/],
    [{ memcue: { enable: false } }, /memcue\.enable/],
    [{ memcue: { subagents: null } }, /memcue\.subagents/],
    [{ memcue: { subagents: { defaultEnabled: 'false' } } }, /memcue\.subagents\.defaultEnabled/],
    [{ memcue: { subagents: { useMemcue: false } } }, /memcue\.subagents\.useMemcue/],
    [{ memcue: { subagents: { agents: { worker: 'true' } } } }, /memcue\.subagents\.agents\.worker/],
    [{ memcue: { subagents: { agents: { '': true } } } }, /agent name/],
  ];
  for (const [settings, diagnostic] of invalid) {
    assert.throws(() => readConfig(settings), diagnostic);
  }
});

test('role lookup never inherits Object.prototype permissions', () => {
  const config = readConfig({ memcue: { subagents: { agents: { worker: true } } } });
  for (const agent of ['toString', 'constructor', '__proto__', 'unknown']) {
    assert.equal(isEnabled(config, { isChild: true, agent }), false);
  }
  const explicit = readConfig(JSON.parse('{"memcue":{"subagents":{"agents":{"__proto__":true}}}}'));
  assert.equal(isEnabled(explicit, { isChild: true, agent: '__proto__' }), true);
  assert.equal(isEnabled(explicit, { isChild: true, agent: 'constructor' }), false);
});

test('inherited config properties cannot alter the policy', () => {
  const settings = Object.create({ memcue: { enabled: false } });
  assert.equal(readConfig(settings).enabled, true);
  const agents = Object.create({ worker: true });
  const config = readConfig({ memcue: { subagents: { agents } } });
  assert.equal(isEnabled(config, { isChild: true, agent: 'worker' }), false);
});
