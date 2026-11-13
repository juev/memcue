/**
 * The compact text of a `memcue search -json` answer (#756), the one rendering every native host shares.
 *
 * A native host hands the model the text it composes itself. The whole pretty-printed answer put braces, repeated
 * field names and the `created`/`origin` pair into every search, and every later request carried them again. One
 * line per fact keeps what a later `memcue_correct` or `memcue_retire` needs beside the fact: `<id>@<version>`, the
 * scope that says where it holds, and the content; one trailer line carries the count, the widening and the hints
 * the binary printed, word for word. The form is the same in every host, and `conformance/render-vectors.json`
 * pins it for this renderer and for the Python one of the Hermes provider.
 *
 * @module memcue-transport/render
 */

/**
 * @typedef {object} SearchRenderFact
 * @property {unknown} [id]
 * @property {unknown} [version]
 * @property {unknown} [content]
 * @property {unknown} [scope]
 * @property {unknown} [below_floor] - served under the score floor; the Hermes provider sets it.
 */

/**
 * @typedef {object} SearchRenderInput
 * @property {readonly SearchRenderFact[]} [facts]
 * @property {readonly unknown[]} [hints] - the lines the binary prints after its facts, kept word for word.
 * @property {unknown} [widened]
 */

/**
 * @typedef {object} SearchRenderOptions
 * @property {number} [contentChars] - the most content characters of one fact, clipped with an ellipsis.
 * @property {number} [totalChars] - the budget of the fact lines; the trailer line is added on top.
 * @property {number} [maxFacts] - the most fact lines.
 */

/** @typedef {{contentChars: number, totalChars: number, maxFacts: number}} Limits */

/**
 * What a host shows when a search found nothing, the wording the CLI itself prints.
 * @type {string}
 */
export const NO_FACTS = 'No stored facts match.';

/**
 * The bound of a rendered answer: a fact is one durable statement, usually 150–400 characters, and five of them
 * fit far inside the budget; the numbers bound a pathological answer, they do not trim ordinary ones.
 * @type {Readonly<Limits>}
 */
export const SEARCH_RENDER_DEFAULTS = Object.freeze({ contentChars: 600, totalChars: 4000, maxFacts: 10 });

/** The characters of text, counted by code point so a clip never splits a surrogate pair. */
function size(text) {
  return Array.from(text).length;
}

/** The first `limit` characters of the fact, with an ellipsis when it was cut; a fact never costs two lines. */
function clip(text, limit) {
  const flat = text.replace(/\s+/gu, ' ').trim();
  const points = Array.from(flat);
  return points.length > limit ? `${points.slice(0, limit).join('')}…` : flat;
}

/** A positive integer option, or the default. */
function limitOf(value, fallback) {
  return typeof value === 'number' && Number.isInteger(value) && value > 0 ? value : fallback;
}

/** One fact line, or an empty string for an entry that cannot name a fact at all. */
function factLine(fact, limits) {
  if (fact === null || typeof fact !== 'object') return '';
  const id = typeof fact.id === 'string' ? fact.id.trim() : '';
  const content = typeof fact.content === 'string' ? fact.content : '';
  if (!id || content.trim() === '') return '';
  const version = Number.isInteger(fact.version) && fact.version > 0 ? `@${fact.version}` : '';
  const scope = typeof fact.scope === 'string' ? fact.scope.trim() : '';
  const below = fact.below_floor === true ? ' (below the floor)' : '';
  return [id + version, scope, clip(content, limits.contentChars) + below].filter(Boolean).join(' ');
}

/**
 * The text a native host gives the model for a search answer.
 * @param {SearchRenderInput | null | undefined} answer - what `memcue search -json` printed, parsed.
 * @param {SearchRenderOptions} [options]
 * @returns {string}
 */
export function renderSearch(answer, options = {}) {
  const limits = /** @type {Limits} */ ({
    contentChars: limitOf(options?.contentChars, SEARCH_RENDER_DEFAULTS.contentChars),
    totalChars: limitOf(options?.totalChars, SEARCH_RENDER_DEFAULTS.totalChars),
    maxFacts: limitOf(options?.maxFacts, SEARCH_RENDER_DEFAULTS.maxFacts),
  });
  const facts = Array.isArray(answer?.facts) ? answer.facts : [];
  const hints = (Array.isArray(answer?.hints) ? answer.hints : [])
    .filter((hint) => typeof hint === 'string' && hint.trim() !== '')
    .map((hint) => hint.trim());
  const widened = answer?.widened === true;

  const lines = [];
  let budget = limits.totalChars;
  let dropped = 0;
  let full = false;
  for (const fact of facts) {
    if (full) {
      dropped += 1;
      continue;
    }
    const line = factLine(fact, limits);
    if (!line) continue;
    const cost = size(line) + 1;
    // Past the fact count or the budget the rest is left out by name, never silently: a cut answer must say so.
    if (lines.length >= limits.maxFacts || cost > budget) {
      full = true;
      dropped += 1;
      continue;
    }
    budget -= cost;
    lines.push(line);
  }

  if (lines.length === 0) {
    // Nothing to show for two different reasons, and they must not be confused: an answer that held no fact, and
    // one whose facts did not fit the caller's bound. The second one says how many it left out.
    if (dropped > 0) return [`… ${dropped} fact${dropped === 1 ? '' : 's'} not shown`, ...(widened ? ['widened'] : []), ...hints].join(' | ');
    return hints.length > 0 ? `${NO_FACTS} | ${hints.join(' | ')}` : NO_FACTS;
  }
  const trailer = [`${lines.length} fact${lines.length === 1 ? '' : 's'}`];
  if (widened) trailer.push('widened');
  trailer.push(...hints);
  if (dropped > 0) trailer.push(`… ${dropped} fact${dropped === 1 ? '' : 's'} not shown`);
  lines.push(trailer.join(' | '));
  return lines.join('\n');
}
