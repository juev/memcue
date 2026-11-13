import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isEnabled, readConfig } from '../src/config.ts';

test('defaults apply when no options are given', () => {
  const config = readConfig(undefined);
  assert.equal(config.enabled, true);
  assert.equal(config.binary, undefined);
  assert.equal(config.facts, undefined);
  assert.equal(config.stateDir, undefined);
  assert.equal(config.host, undefined);
  assert.deepEqual({ ...config.subagents.agents }, {});
  assert.equal(config.subagents.defaultEnabled, false);
});

test('every documented field is read and ~/ is expanded', () => {
  const config = readConfig({
    enabled: false,
    binary: 'memcue',
    facts: '~/memory/facts',
    stateDir: '/var/tmp/state',
    host: 'laptop',
    subagents: { defaultEnabled: true, agents: { worker: true, scout: false } },
  });
  assert.equal(config.enabled, false);
  assert.equal(config.binary, 'memcue');
  assert.match(config.facts ?? '', /\/memory\/facts$/);
  assert.equal(config.stateDir, '/var/tmp/state');
  assert.equal(config.host, 'laptop');
  assert.deepEqual({ ...config.subagents.agents }, { worker: true, scout: false });
  assert.equal(config.subagents.defaultEnabled, true);
});

test('unknown keys are errors rather than silently ignored typos', () => {
  assert.throws(() => readConfig({ facts: '/tmp/f', fact: '/tmp/g' }), /Unknown setting: options\.fact/);
  assert.throws(() => readConfig({ subagents: { enabled: true } }), /Unknown setting: options\.subagents\.enabled/);
  assert.throws(() => readConfig({ subagents: { agents: { ' worker': true } } }), /invalid agent name/);
});

test('wrong types and relative paths are rejected', () => {
  assert.throws(() => readConfig({ enabled: 'yes' }), /options\.enabled must be a boolean/);
  assert.throws(() => readConfig({ facts: 'memory/facts' }), /options\.facts must be an absolute path/);
  assert.throws(() => readConfig({ host: '' }), /options\.host must be a nonempty string/);
  assert.throws(() => readConfig({ subagents: { agents: { worker: 'yes' } } }), /must be a boolean/);
  assert.throws(() => readConfig({ facts: '/tmp/f\0' }), /NUL/);
  assert.throws(() => readConfig([] as unknown), /options must be an object/);
});

test('a binary may be a bare command name, nothing else may', () => {
  assert.equal(readConfig({ binary: 'memcue' }).binary, 'memcue');
  assert.equal(readConfig({ binary: '/usr/local/bin/memcue' }).binary, '/usr/local/bin/memcue');
  assert.throws(() => readConfig({ binary: './memcue' }), /must be an absolute path or a command name/);
});

test('a primary session is on unless switched off or disabled globally', () => {
  const config = readConfig({});
  assert.equal(isEnabled(config, { isChild: false }), true);
  assert.equal(isEnabled(config, { isChild: false, sessionDisabled: true }), false);
  assert.equal(isEnabled(readConfig({ enabled: false }), { isChild: false }), false);
});

test('a child session is on only for an enabled agent role', () => {
  const off = readConfig({});
  assert.equal(isEnabled(off, { isChild: true }), false);
  assert.equal(isEnabled(off, { isChild: true, agent: 'worker' }), false);
  assert.equal(isEnabled(readConfig({ subagents: { defaultEnabled: true } }), { isChild: true, agent: 'worker' }), true);
  assert.equal(isEnabled(readConfig({ subagents: { agents: { worker: true } } }), { isChild: true, agent: 'worker' }), true);
  assert.equal(isEnabled(readConfig({ subagents: { agents: { worker: false } } }), { isChild: true, agent: 'worker' }), false);
});
