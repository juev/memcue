import assert from 'node:assert/strict'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFileSync } from 'node:child_process'
import { test } from 'node:test'
import { apply } from '../src/index.mjs'

// The real binary must write only the throwaway base: an inherited server or PostgreSQL backend wins over
// MEMCUE_FACTS, and a run with the developer's MEMCUE_SERVER wrote this fixture into the live base.
for (const key of Object.keys(process.env)) {
 if (key === 'MEMCUE_SERVER' || key === 'MEMCUE_POSTGRES_DSN' || key.startsWith('MEMCUE_TAILSCALE_')) delete process.env[key]
}

test('repository pitfalls follow tasks, compaction and repository switches through native DSH delivery', async () => {
 const root = await mkdtemp(join(tmpdir(), 'memcue-pitfalls-dsh-'))
 try {
  const binary = process.env.MEMCUE_BIN
  assert.ok(binary, 'native candidate binary is required')
  const env = { ...process.env, MEMCUE_FACTS:join(root,'facts'), MEMCUE_STATE_DIR:join(root,'state'), MEMCUE_DREAM_BACKGROUND:'0', GIT_CONFIG_GLOBAL:'/dev/null',GIT_CONFIG_SYSTEM:'/dev/null',GIT_CONFIG_NOSYSTEM:'1' }
  const repos = ['one','two'].map(name => join(root,name))
  for (const [i,repo] of repos.entries()) {
   execFileSync('git',['init','-q',repo],{env})
   execFileSync('git',['-C',repo,'remote','add','origin',`https://example.invalid/team/${i}.git`],{env})
  }
  const content = 'DSH build failed because its index was stale; rebuild the entire index.'
  const cli = args => execFileSync(binary,args,{env,encoding:'utf8'})
  const id = cli(['add','-cwd',repos[0],'-kind','pitfall','-scope','repo','--',content]).trim()
  const listeners = new Map()
  const ctx = { on: (event, fn) => { listeners.set(event,fn);return ()=>{} }, tools:{register:()=>()=>{}},systemPrompt:{section:()=>()=>{}} }
  apply(ctx,{binary,facts:env.MEMCUE_FACTS})
  const session = {id:'native-pitfalls',header:{cwd:repos[0]}}
  const run = async turn => {
   const messages = [{id:`user-${turn}`,role:'user',content:[{type:'text',text:'Unrelated task'}],source:{kind:'user'}}]
   const result = await listeners.get('agent/pre-step')({agent:{session},turn,step:1,signal:{aborted:false}},async()=>({kind:'enter',messages}))
   return result.messages.slice(1).flatMap(m=>m.content.map(c=>c.text||'')).join('\n')
  }
  assert.ok((await run(1)).includes(content), 'startup pitfall missing')
  cli(['correct',id,'DSH corrected failure cause; regenerate the full index.'])
  assert.ok((await run(2)).includes('DSH corrected failure cause'), 'new task did not rebuild pitfall context')
  listeners.get('session/event')(session,{type:'compaction/end'})
  assert.ok((await run(2)).includes('DSH corrected failure cause'), 'compaction lost pitfalls')
  session.header.cwd = repos[1]
  const switched = await run(2)
  assert.ok(switched.includes('No active repository pitfalls'), 'repository switch did not supersede old snapshot')
  assert.ok(!switched.includes(content) && !switched.includes('DSH corrected failure cause'))
 } finally { await rm(root,{recursive:true,force:true}) }
})
