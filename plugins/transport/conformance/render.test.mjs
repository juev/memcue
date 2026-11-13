/**
 * The one rendering of a search answer (#756) against the hosts that share it: the vectors pin the exact text for
 * the JavaScript renderer here, and conformance/hermes_contract.py runs the same file against the Python renderer
 * of the Hermes provider, so the two cannot drift apart. The DeepSeek Harness plugin asserts the same text through
 * its own package subpath in deepseek-harness/test/plugin.test.mjs.
 */
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { NO_FACTS, SEARCH_RENDER_DEFAULTS, renderSearch } from '../index.mjs';

const vectors = JSON.parse(await readFile(new URL('./render-vectors.json', import.meta.url), 'utf8'));

test('the bounds of a rendered answer are the ones the hosts document', () => {
  assert.deepEqual({ ...SEARCH_RENDER_DEFAULTS }, vectors.defaults);
  assert.equal(NO_FACTS, 'No stored facts match.');
});

for (const vector of vectors.cases) {
  test(`render: ${vector.name}`, () => {
    assert.equal(renderSearch(vector.answer, vector.options), vector.expected);
  });
}
