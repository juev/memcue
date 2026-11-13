import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const root = fileURLToPath(new URL('../../../', import.meta.url));
const dryRun = target => execFileSync('make', ['--dry-run', target], {
  cwd: root, encoding: 'utf8', timeout: 30_000,
});

test('the OpenCode target builds the CLI and runs typecheck plus the suite', () => {
  const commands = dryRun('test-opencode');
  assert.match(commands, /go build[^\n]+-X main\.version=/);
  assert.match(commands, /npm --prefix plugins\/opencode run typecheck/);
  assert.match(commands, /MEMCUE_TEST_BINARY=.*bin\/memcue/);
  assert.match(commands, /npm --prefix plugins\/opencode test/);
});

test('existing default verification delegates to the OpenCode target', () => {
  const commands = dryRun('test');
  assert.match(commands, /\bmake(?:\[\d+\])? test-opencode/);
  assert.match(commands, /go test -race/);
});
