import { test } from 'node:test'
import assert from 'node:assert/strict'
import { request } from '../src/api.ts'
test('administrator errors and disconnected service produce actionable messages',async t=>{
 t.mock.method(globalThis,'fetch',async()=>new Response('forbidden',{status:403}))
 await assert.rejects(request('/accounts'),/管理员/)
 t.mock.method(globalThis,'fetch',async()=>{throw new TypeError('Failed to fetch')})
 await assert.rejects(request('/accounts'),/检查应用是否正在运行/)
})
test('writes retain the management headers and same-origin credentials',async t=>{
 let options
 t.mock.method(globalThis,'fetch',async(url,value)=>{if(url.endsWith('/csrf'))return new Response(JSON.stringify({token:'t'.repeat(43)}));options=value;return new Response('{"ok":true}',{status:200})})
 await request('/accounts','POST',{id:'test'})
 assert.equal(options.headers['X-FnMusic-CSRF'],'t'.repeat(43));assert.equal(options.credentials,'same-origin');assert.equal(options.headers['X-FnMusic-Admin'],'1');assert.equal(options.body,'{"id":"test"}')
})
test('a stalled connection times out instead of leaving controls blocked',async t=>{
 t.mock.timers.enable({apis:['setTimeout']})
 t.mock.method(globalThis,'fetch',(_,options)=>new Promise((_,reject)=>options.signal.addEventListener('abort',()=>reject(new Error('aborted')))))
 const pending=request('/status')
 const assertion=assert.rejects(pending,/请求超时/)
 t.mock.timers.tick(20000)
 await assertion
})

test('a rejected write is distinguished from missing administrator identity',async t=>{
 t.mock.method(globalThis,'fetch',async url=>new Response(JSON.stringify(url.endsWith('/csrf')?{token:'t'.repeat(43)}:{code:'management_request_rejected'}),{status:url.endsWith('/csrf')?200:403}))
 await assert.rejects(request('/accounts','POST',{id:'test'}),/写入请求校验失败/)
})
test('writes refresh their token after service restart',async t=>{
 let issued=0;const sent=[]
 t.mock.method(globalThis,'fetch',async(url,options)=>{
  if(url.endsWith('/csrf'))return new Response(JSON.stringify({token:(++issued===1?'a':'b').repeat(43)}))
  sent.push(options.headers['X-FnMusic-CSRF']);return new Response('{}')
 })
 await request('/accounts','POST',{});await request('/settings','PUT',{});assert.deepEqual(sent,['a'.repeat(43),'b'.repeat(43)])
})
test('a failed token request cannot reach the mutation',async t=>{
 let writes=0;t.mock.method(globalThis,'fetch',async(url)=>{if(!url.endsWith('/csrf'))writes++;return new Response('{"code":"admin_required"}',{status:403})})
 await assert.rejects(request('/accounts','POST',{}),/管理员/);assert.equal(writes,0)
})

test('unexpected QR codes are shown without echoing upstream response content',async t=>{
 t.mock.method(globalThis,'fetch',async()=>new Response(JSON.stringify({code:'qr_state_unexpected',upstream_code:200,error:'sensitive upstream content'}),{status:502}))
 await assert.rejects(request('/accounts/test/auth/check'),error=>/状态码 200/.test(error.message)&&!error.message.includes('sensitive'))
})
