import assert from 'node:assert/strict'
import {test,mock} from 'node:test'
import {spawn} from 'node:child_process'
import {mkdtempSync,writeFileSync,chmodSync,existsSync,readFileSync,rmSync} from 'node:fs'
import {join} from 'node:path'
import {tmpdir} from 'node:os'
const owned=[]
mock.module('node:child_process',{namedExports:{spawn:(...args)=>{const p=spawn(...args);if(args[2]?.env?.MEMCUE_OUTCOME_NONCE)owned.push(p);return p}}})
const {apply}=await import('../src/index.mjs')
const A=tmpdir()
test('DSH timeout stops invocation-owned descendant and leaves unrelated sentinel',async()=>{
 const d=mkdtempSync(join(A,'dsh-lifecycle-'));const exe=join(d,'finite'),pid=join(d,'pid'),ran=join(d,'ran')
 // Lifecycle only: finite Python fixture is not an application response or DB oracle.
 writeFileSync(exe,`#!/usr/bin/env python3\nimport os,time,pathlib\nif os.fork()==0:\n pathlib.Path(${JSON.stringify(pid)}).write_text(str(os.getpid()));time.sleep(16);pathlib.Path(${JSON.stringify(ran)}).write_text('survived');os._exit(0)\ntime.sleep(15.5)\n`);chmodSync(exe,0o700)
 const sentinel=spawn(process.execPath,['-e','setTimeout(()=>{},20000)'])
 try{
  const tools=new Map();apply({on:()=>()=>{},tools:{register:t=>{tools.set(t.name,t);return()=>{}}},systemPrompt:{section:()=>()=>{}}},{binary:exe})
  const start=performance.now();const answer=await tools.get('memcue_add').execute({text:'Synthetic lifecycle control.',kind:'rule',source:'user'},{agent:{session:{header:{cwd:d}}}})
  assert.ok(performance.now()-start<15500);assert.equal(answer.outcome,'unknown');assert.equal(answer.stored,null)
  assert.ok(existsSync(pid));const stat='/proc/'+readFileSync(pid,'utf8')+'/stat'
  const running=existsSync(stat)&&!['Z','X'].includes(readFileSync(stat,'utf8').split(') ')[1][0])
  assert.equal(running,false,'owned descendant still running at return')
  // The shared transport (#754) spawns the executable directly: one owned spawn, its pipes destroyed at return.
  assert.equal(owned.length,1)
  for(const s of [owned[0].stdin,owned[0].stdout,owned[0].stderr]){assert.equal(s.destroyed,true)}
  assert.equal(sentinel.exitCode,null);assert.equal(sentinel.signalCode,null)
 }finally{sentinel.kill();await new Promise(r=>sentinel.once('close',r));await new Promise(r=>setTimeout(r,1500));rmSync(d,{recursive:true,force:true})}
})
