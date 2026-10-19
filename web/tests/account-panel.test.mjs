import { test } from 'node:test'
import assert from 'node:assert/strict'
import { useAccountPanel } from '../src/useAccountPanel.ts'
const account = id => ({id,name:id,bridge_url:'native://netease/'+id})
const response = (data,status=200) => new Response(JSON.stringify(data),{status,headers:{'Content-Type':'application/json'}})
const mockFetch=(t,handler)=>t.mock.method(globalThis,'fetch',(url,options)=>url.endsWith('/csrf')?Promise.resolve(response({token:'t'.repeat(43)})):handler(url,options))
const settle = () => new Promise(resolve=>setImmediate(resolve))

test('switching accounts rejects a late login response from the previous account',async t=>{
  let finishOld
  mockFetch(t,(url)=> url.includes('/first/') ? new Promise(resolve=>finishOld=resolve) : Promise.resolve(response({state:'pending',logged_in:false})))
  const panel=useAccountPanel();t.after(()=>panel.close())
  panel.open(account('first'));panel.open(account('second'));await settle()
  finishOld(response({state:'success',logged_in:true}));await settle()
  assert.equal(panel.account.value.id,'second');assert.equal(panel.auth.value.logged_in,false);assert.equal(panel.statusText.value,'未登录')
})

test('closing a panel aborts its request and ignores any late result',async t=>{
  let finish,signal
  mockFetch(t,(_,options)=>{signal=options.signal;return new Promise(resolve=>finish=resolve)})
  const panel=useAccountPanel();panel.open(account('test'));panel.close()
  assert.equal(signal.aborted,true)
  finish(response({state:'success',logged_in:true}));await settle()
  assert.equal(panel.auth.value,null);assert.equal(panel.account.value,null);assert.equal(panel.error.value,'')
})

test('all returned tracks remain accessible through pagination',async t=>{
  const tracks=Array.from({length:151},(_,index)=>({id:String(index),title:'track '+index,artists:[]}))
  mockFetch(t,(url)=>Promise.resolve(response(url.endsWith('/tracks')?tracks:{state:'success',logged_in:true})))
  const panel=useAccountPanel();t.after(()=>panel.close());panel.open(account('test'));await settle()
  await panel.loadTracks({id:'playlist',title:'playlist',track_count:151})
  assert.equal(panel.pageCount.value,4);assert.equal(panel.visibleTracks.value.length,50)
  panel.page.value=4;assert.equal(panel.visibleTracks.value[0].id,'150')
})

test('login links generate a local QR image and expired challenges are cleared',async t=>{
  mockFetch(t,url=>Promise.resolve(url.endsWith('/auth/qr')?response({qr_url:'https://music.163.com/login?codekey=test-only'}):url.endsWith('/auth/check')?response({},410):response({state:'pending',logged_in:false})))
  const panel=useAccountPanel();t.after(()=>panel.close());panel.open(account('test'));await settle()
  await panel.startLogin();assert.match(panel.qrImage.value,/^data:image\/png;base64,/);assert.equal(panel.statusText.value,'等待扫码')
  await panel.checkLogin();assert.equal(panel.qrImage.value,'');assert.equal(panel.qrURL.value,'');assert.equal(panel.expired.value,true);assert.match(panel.error.value,/过期/)
})

test('unexpected login destinations cannot become a displayed QR code or link',async t=>{
  mockFetch(t,url=>Promise.resolve(response(url.endsWith('/auth/qr')?{qr_url:'javascript:alert(1)'}:{state:'pending',logged_in:false})))
  const panel=useAccountPanel();t.after(()=>panel.close());panel.open(account('test'));await settle()
  await panel.startLogin();assert.equal(panel.qrImage.value,'');assert.match(panel.error.value,/链接无效/)
})

test('a successful status check removes the active QR challenge',async t=>{
  let loggedIn=false
  mockFetch(t,url=>Promise.resolve(response(url.endsWith('/auth/qr')?{qr_url:'https://music.163.com/login?codekey=test-only'}:{state:loggedIn?'success':'pending',logged_in:loggedIn})))
  const panel=useAccountPanel();t.after(()=>panel.close());panel.open(account('test'));await settle()
  assert.equal(panel.statusText.value,'未登录')
  await panel.startLogin();loggedIn=true;await panel.checkStatus()
  assert.equal(panel.statusText.value,'已登录');assert.equal(panel.qrURL.value,'');assert.equal(panel.qrImage.value,'')
})

test('QR results are polled automatically until authorized and stop after success',async t=>{
  t.mock.timers.enable({apis:['setTimeout']})
  let checks=0
  mockFetch(t,url=>Promise.resolve(response(url.endsWith('/auth/qr')?{qr_url:'https://music.163.com/login?codekey=test-only'}:url.endsWith('/auth/check')?{state:++checks===1?'scanned':'success',logged_in:checks>1}:{state:'pending',logged_in:false})))
  const panel=useAccountPanel();t.after(()=>panel.close());panel.open(account('test'));await settle();await panel.startLogin()
  t.mock.timers.tick(2500);await settle();assert.equal(panel.statusText.value,'已扫码，请在手机上确认')
  t.mock.timers.tick(2500);await settle();assert.equal(panel.statusText.value,'已登录');assert.equal(panel.qrURL.value,'')
  t.mock.timers.tick(10000);await settle();assert.equal(checks,2)
})

test('status button checks an active QR challenge and closing cancels automatic checks',async t=>{
  t.mock.timers.enable({apis:['setTimeout']})
  let checks=0
  mockFetch(t,url=>Promise.resolve(response(url.endsWith('/auth/qr')?{qr_url:'https://music.163.com/login?codekey=test-only'}:url.endsWith('/auth/check')?(checks++,{state:'scanned',logged_in:false}):{state:'pending',logged_in:false})))
  const panel=useAccountPanel();panel.open(account('test'));await settle();await panel.startLogin();await panel.checkStatus()
  assert.equal(checks,1);assert.equal(panel.statusText.value,'已扫码，请在手机上确认')
  panel.close();t.mock.timers.tick(10000);await settle();assert.equal(checks,1)
})
