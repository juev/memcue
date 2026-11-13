import assert from 'node:assert/strict'
import {test,mock} from 'node:test'
import {spawn,spawnSync} from 'node:child_process'
import {readFileSync,writeFileSync} from 'node:fs'
const fixture=process.env.MEMCUE_RESPONSE_FIXTURE
if(fixture){
 const cfg=JSON.parse(readFileSync(fixture,'utf8'));Object.assign(process.env,cfg.native_env)
 let fault='';const native=[]
 mock.module('node:child_process',{namedExports:{spawn:(bin,args,options)=>{
  const child=spawn(bin,args,options)
  if(options.env?.MEMCUE_OUTCOME_NONCE&&fault){
   let actual='';const on=child.stderr.on.bind(child.stderr);on('data',b=>{actual+=b})
   child.stderr.on=(event,cb)=>on(event,event==='data'?b=>{if(fault==='partial-error')cb(String(b).slice(0,12))}:cb)
   // The shared transport (#754) spawns the executable directly: observe its own exit, not a supervisor's IPC.
   child.stdout.destroy();child.on('exit',code=>native.push({code,actual}))
  }
  return child
 }}})
 const {apply}=await import('../src/index.mjs')
 test('DSH requires a complete outcome after native diagnostic delivery loss',async(t)=>{
  const tools=new Map();apply({on:()=>()=>{},tools:{register:t=>{tools.set(t.name,t);return()=>{}}},systemPrompt:{section:()=>()=>{}}},{binary:cfg.binary,facts:cfg.binary+'.absent'})
  const exec={agent:{session:{header:{cwd:process.cwd()}}}};const call=(name,args)=>tools.get(name).execute(args,exec)
  const sql=q=>{const r=spawnSync(cfg.psql,['-XAt','-v','ON_ERROR_STOP=1','-c',q],{env:{...process.env,...cfg.psql_env},encoding:'utf8'});assert.equal(r.status,0);return r.stdout.trim()}
  for(const mode of ['missing-error','partial-error']) await t.test(mode,async()=>{
   const added=await call('memcue_add',{text:'Prefer explicit completion receipts.',kind:'rule',source:'user'});assert.equal(added.stored,true)
   const id=added.message.match(/[0-9A-HJKMNP-TV-Z]{26}/)[0]
   fault=mode;const answer=await call('memcue_correct',{id,expected_version:1,text:'Prefer explicit complete outcome receipts.'});fault=''
   assert.equal(sql(`SELECT string_agg(version::text,',' ORDER BY version) FROM memcue.facts WHERE id='${id}'`),'1,2')
   assert.match(native.at(-1).actual,/UNKNOWN OUTCOME/);assert.equal(native.at(-1).code,1)
   writeFileSync(fixture+'.dsh-observation.json',JSON.stringify({native,answer}))
   assert.equal(answer.stored,null,'nonzero without complete refusal evidence must not claim unwritten')
   assert.equal(answer.outcome,'unknown');assert.match(answer.message,/UNKNOWN OUTCOME/);assert.ok(answer.message.includes(id));assert.ok(!answer.message.includes(cfg.password))
  })
 })
}else test('requires isolated native fixture',{skip:true},()=>{})
