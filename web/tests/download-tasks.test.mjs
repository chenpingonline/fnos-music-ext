import { test } from 'node:test'
import assert from 'node:assert/strict'
import { useDownloadTasks, canRetry, taskGroup } from '../src/useDownloadTasks.ts'
const response=(data,status=200)=>new Response(JSON.stringify(data),{status,headers:{'Content-Type':'application/json'}})
const job=(id,state='completed',extras={})=>({id,user:'alice',track:{title:id,artists:['Artist']},state,purpose:'download',percent:100,bytes:1024,...extras})
const settle=()=>new Promise(resolve=>setImmediate(resolve))
function fixture(t){
 const listeners=new Map()
 const previous=globalThis.document
 globalThis.document={hidden:false,addEventListener:(key,fn)=>listeners.set(key,fn),removeEventListener:key=>listeners.delete(key)}
 const tasks=useDownloadTasks()
 t.after(()=>{tasks.stop();if(previous===undefined)delete globalThis.document;else globalThis.document=previous})
 return {tasks,listeners}
}
test('filters and paginates tasks, keeping active work ahead of history',t=>{
 const {tasks}=fixture(t)
 tasks.jobs.value=[...Array.from({length:42},(_,i)=>job('history '+i)),job('active','running'),job('broken','failed'),job('warning','completed',{warning:'cover unavailable'}),job('cancelled','cancelled')]
 assert.deepEqual(tasks.counts.value,{all:46,active:1,attention:2,completed:42,cancelled:1})
 assert.equal(tasks.visible.value[0].id,'active');assert.equal(tasks.visible.value.length,20)
 tasks.page.value=3;assert.equal(tasks.visible.value.length,6)
 tasks.filter.value='attention';assert.equal(tasks.currentPage.value,1);assert.equal(tasks.visible.value.length,2)
 tasks.query.value='BROKEN';assert.equal(tasks.visible.value[0].id,'broken')
 tasks.query.value='absent';assert.equal(tasks.visible.value.length,0)
})
test('completed downloads have no retry-binding action; failed jobs and binding warnings can retry',()=>{
 assert.equal(canRetry(job('download')),false)
 assert.equal(canRetry(job('save','completed',{purpose:'save',warning:'lyrics unavailable'})),false)
 assert.equal(canRetry(job('bind','completed',{purpose:'bind',warning:'binding timeout'})),true)
 assert.equal(canRetry(job('failed','failed')),true)
 assert.equal(taskGroup(job('save','completed',{warning:'lyrics unavailable'})),'attention')
})
test('refresh errors retain the last successful list and a late response cannot replace newer data',async t=>{
 const {tasks}=fixture(t);let finish
 t.mock.method(globalThis,'fetch',()=>Promise.resolve(response({list:[job('original')],ffmpeg:true})))
 await tasks.refresh();assert.equal(tasks.loaded.value,true)
 t.mock.method(globalThis,'fetch',()=>Promise.resolve(response({},502)))
 await tasks.refresh();assert.equal(tasks.jobs.value[0].id,'original');assert.ok(tasks.error.value)
 t.mock.method(globalThis,'fetch',()=>new Promise(resolve=>{finish=resolve}))
 const old=tasks.refresh()
 t.mock.method(globalThis,'fetch',()=>Promise.resolve(response({list:[job('new')],ffmpeg:true})))
 await tasks.refresh();finish(response({list:[job('old')],ffmpeg:false}));await old
 assert.equal(tasks.jobs.value[0].id,'new');assert.equal(tasks.error.value,'')
})
test('stopping the page aborts work and ignores late polling results',async t=>{
 const {tasks,listeners}=fixture(t);let finish,signal
 t.mock.method(globalThis,'fetch',(_url,options)=>{signal=options.signal;return new Promise(resolve=>{finish=resolve})})
 tasks.start();assert.equal(listeners.size,1);tasks.stop();assert.equal(signal.aborted,true);assert.equal(listeners.size,0)
 finish(response({list:[job('late')],ffmpeg:true}));await settle();assert.equal(tasks.jobs.value.length,0)
})
test('hiding the page cancels polling and becoming visible refreshes once',async t=>{
 const {tasks,listeners}=fixture(t);let calls=0,signal,finish
 t.mock.method(globalThis,'fetch',(_url,options)=>{calls++;signal=options.signal;return new Promise(resolve=>{finish=resolve})})
 tasks.start();globalThis.document.hidden=true;listeners.get('visibilitychange')();assert.equal(signal.aborted,true)
 finish(response({list:[job('hidden')],ffmpeg:true}));await settle();assert.equal(tasks.jobs.value.length,0)
 t.mock.method(globalThis,'fetch',()=>{calls++;return Promise.resolve(response({list:[job('visible')],ffmpeg:true}))})
 globalThis.document.hidden=false;listeners.get('visibilitychange')();await settle()
 assert.equal(calls,2);assert.equal(tasks.jobs.value[0].id,'visible')
})
test('repeated clicks submit one mutation per task and refresh after success',async t=>{
 const {tasks}=fixture(t);const target=job('retry','failed');let finish;let writes=0
 t.mock.method(globalThis,'fetch',(url,options)=>{
  if(url.endsWith('/csrf'))return Promise.resolve(response({token:'a'.repeat(32)}))
  if(options.method==='POST'){writes++;return new Promise(resolve=>{finish=resolve})}
  return Promise.resolve(response({list:[job('retry','queued')],ffmpeg:true}))
 })
 const pending=tasks.action(target,'retry');await settle();await tasks.action(target,'retry');assert.equal(writes,1)
 finish(response({ok:true}));await pending
 assert.equal(tasks.jobs.value[0].state,'queued');assert.equal(tasks.pending.value.retry,undefined)
})
