// Full facts are read once per lifecycle event, independently of search/receipts.
const pendingStart = new Map()
const pendingTask = new Map()
const pendingTool = new Map()
const activeSessions = new Set()
const deliveredCwds = new Map()
// The snapshot last delivered to a session, by content: a prompt or a cwd change re-sends the same facts only when they
// changed, so a 30 KB snapshot is not repeated with every message (#712). A failed read forgets it, so the next
// boundary delivers again; a session start, /clear or compaction always delivers.
const deliveredSnapshots = new Map()
const fingerprint = data => JSON.stringify(texts(data))
const unchanged = (id, data) => !data.failed && deliveredSnapshots.get(id) === fingerprint(data)
function remember(id, data) {
  if (data.failed) deliveredSnapshots.delete(id)
  else deliveredSnapshots.set(id, fingerprint(data))
}
const unavailable = 'Repository pitfalls unavailable: native transport failed or truncated. Earlier snapshots do not establish current applicability.'
async function snapshot($, cwd) {
  try {
    const binary = await $.env.get('MEMCUE_BIN') || 'memcue'
    const reply = await $.process.run([binary, 'session-card', '-pitfalls-json'], {cwd, stdin:JSON.stringify({cwd}), timeoutMs:10000})
    if (reply.exitCode !== 0 || reply.isStdoutTruncated || reply.isStderrTruncated) throw new Error('native transport')
    const data = JSON.parse(reply.stdout)
    if (!Array.isArray(data.facts) || !data.facts.every(f => typeof f === 'string')) throw new Error('invalid snapshot')
    return {context:data.context || `Repository pitfalls for ${data.repository || 'no current repository'}: this snapshot supersedes earlier snapshots and applies only to this repository.`, facts:data.facts}
  } catch (_) { return {context:unavailable, facts:[], failed:true} }
}
const large = data => data.context.length > 10000 || data.facts.some(f => f.length > 10000)
const texts = data => [data.context, ...data.facts]
const smallTexts = data => texts(data).filter(t=>t.length <= 10000)
const largeTexts = data => [data.context, ...data.facts.filter(f=>f.length > 10000)]
const hardLimit = 'Automatic repository pitfall delivery UNSUPPORTED: whole facts exceed this additive context transport (100000 per string, 200000 together). No subset delivered; earlier snapshots do not establish applicability.'
const exceeds = all => all.some(t=>t.length > 100000) || all.reduce((n,t)=>n+t.length,0)>200000
function additiveContext(existing, data) {
  const all = [...existing, ...texts(data)]
  if (exceeds(all)) return [...existing, hardLimit]
  return all
}
export async function deliver($, e, next) {
  const result = await next(e)
  if (result.block || result.preventContinuation) return result
  const data = await snapshot($, e.cwd)
  activeSessions.add(e.session_id)
  if (data.failed) deliveredCwds.delete(e.session_id)
  else deliveredCwds.set(e.session_id, e.cwd)
  if (e.hook_event_name === 'SessionStart') { pendingTask.delete(e.session_id); pendingStart.set(e.session_id, data) }
  else if (unchanged(e.session_id, data)) { pendingTask.delete(e.session_id); return result }
  else {
    data.unsupported = large(data) && exceeds(largeTexts(data))
    pendingTask.set(e.session_id, data)
    if (data.unsupported) return {...result, additionalContext:[...(result.additionalContext || []), hardLimit]}
  }
  remember(e.session_id, data)
  // One captured snapshot hands off only its large fields; small facts have no aggregate budget.
  return {...result, additionalContext:[...(result.additionalContext || []), ...smallTexts(data)]}
}
export function register(on) {
  on('classic.SessionStart', deliver)
  on('classic.UserPromptSubmit', deliver)
  on('classic.PostToolUse', async ($, e, next) => {
    const result = await next(e)
    if (!activeSessions.has(e.session_id) || result.block || result.preventContinuation) return result
    const cwd = await $.session.cwd()
    if (deliveredCwds.get(e.session_id) === cwd) return result
    const data = await snapshot($, cwd)
    if (data.failed) deliveredCwds.delete(e.session_id)
    else deliveredCwds.set(e.session_id, cwd)
    if (unchanged(e.session_id, data)) return result
    remember(e.session_id, data)
    data.unsupported = large(data) && exceeds(largeTexts(data))
    pendingTool.set(`${e.session_id}:${e.tool_use_id}`, data)
    return {...result, additionalContext:[...(result.additionalContext || []), ...(data.unsupported ? [hardLimit] : smallTexts(data))]}
  })
  on('tool.call', async ($, e, next) => {
    const result = await next(e)
    const id = await $.session.id()
    if (!activeSessions.has(id) || result.deny) return result
    const key = `${id}:${e.tool_use_id}`
    const captured = pendingTool.get(key)
    pendingTool.delete(key)
    if (captured) {
      if (!large(captured) || captured.unsupported) return result
      const context = [...(result.context || []), ...largeTexts(captured)]
      return {...result, context:exceeds(context) ? [...(result.context || []), hardLimit] : context}
    }
    const cwd = await $.session.cwd()
    if (deliveredCwds.get(id) === cwd) return result
    const data = await snapshot($, cwd)
    if (data.failed) deliveredCwds.delete(id)
    else deliveredCwds.set(id, cwd)
    if (unchanged(id, data)) return result
    remember(id, data)
    return {...result, context:additiveContext(result.context || [], data)}
  })
  on('prompt.submit', async ($, e, next) => {
    const id = await $.session.id()
    const data = pendingTask.get(id)
    pendingTask.delete(id)
    if (!data || !large(data)) return next(e)
    if (data.unsupported) return next({...e, context:[...(e.context || []), hardLimit]})
    const additions = largeTexts(data)
    if (exceeds([...(e.context || []), ...additions])) return {drop:hardLimit}
    return next({...e, context:[...(e.context || []), ...additions]})
  })
  on('prompt.context', async ($, e, next) => {
    const result = await next(e)
    const id = await $.session.id()
    const captured = pendingStart.get(id)
    const data = captured || await snapshot($, await $.session.cwd())
    pendingStart.delete(id)
    if (!large(data)) return result
    return {...result, blocks:[...result.blocks, ...(captured ? largeTexts(data) : texts(data)).map((text,i)=>({name:`memcue.repository-pitfalls.${i}`,text}))]}
  })
}
