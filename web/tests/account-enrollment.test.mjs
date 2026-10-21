import {test} from 'node:test'
import assert from 'node:assert/strict'
import {useAccountEnrollment} from '../src/useAccountEnrollment.ts'
const reply=(data,status=200)=>new Response(JSON.stringify(data),{status})
const settle=()=>new Promise(resolve=>setImmediate(resolve))
function setup(t,handler){
 t.mock.method(globalThis,'fetch',(url,opts)=>url.endsWith('/csrf')?Promise.resolve(reply({token:'t'.repeat(43)})):handler(url,opts))
 const flow=useAccountEnrollment();t.after(async()=>{flow.close();await settle()});return flow
}
test('draft creation and pending/scanned login never report a saved account',async t=>{
 const paths=[];let state='pending'
 const flow=setup(t,(url)=>{paths.push(url);return Promise.resolve(reply(url.endsWith('/account-enrollments')?{id:'draft',qr_url:'https://music.163.com/login?codekey=test-only'}:{state,logged_in:false}))})
 await flow.start('Family');assert.equal(flow.saved.value,false);assert.match(flow.qrImage.value,/^data:image\/png;base64,/)
 await flow.check();state='scanned';await flow.check();assert.equal(flow.saved.value,false);assert.match(flow.status.value,/手机/)
 assert.ok(paths.every(url=>url.includes('/account-enrollments')))
})
test('logged-in response alone is insufficient until server persistence succeeds',async t=>{
 let saved=false
 const flow=setup(t,url=>Promise.resolve(reply(url.endsWith('/account-enrollments')?{id:'draft',qr_url:'https://music.163.com/login?codekey=test-only'}:{state:'success',logged_in:true,saved})))
 await flow.start('Family');await flow.check();assert.equal(flow.saved.value,false)
 saved=true;await flow.check();assert.equal(flow.saved.value,true)
})
test('cancelling aborts polling, discards the draft and ignores a late success',async t=>{
 let finish;const paths=[]
 const flow=setup(t,(url)=>{paths.push(url);if(url.endsWith('/check'))return new Promise(resolve=>finish=resolve);return Promise.resolve(reply(url.endsWith('/account-enrollments')?{id:'draft',qr_url:'https://music.163.com/login?codekey=test-only'}:{}))})
 await flow.start('Family');const pending=flow.check();await settle();flow.close();finish(reply({state:'success',logged_in:true,saved:true}));await pending;await settle()
 assert.equal(flow.saved.value,false);assert.equal(flow.id.value,'');assert.equal(flow.qrImage.value,'');assert.ok(paths.some(url=>url.endsWith('/account-enrollments/draft')))
})
test('failed QR generation keeps the form retryable and never saves an account',async t=>{
 const flow=setup(t,()=>Promise.resolve(reply({error:'temporary failure'},502)))
 await flow.start('Family');assert.equal(flow.id.value,'');assert.equal(flow.saved.value,false);assert.equal(flow.busy.value,false);assert.ok(flow.error.value)
})

test('expired draft clears QR and allows a fresh attempt without reporting success',async t=>{
 const flow=setup(t,url=>Promise.resolve(url.endsWith('/account-enrollments')?reply({id:'draft',qr_url:'https://music.163.com/login?codekey=test-only'}):reply({},410)))
 await flow.start('Family');await flow.check()
 assert.equal(flow.id.value,'');assert.equal(flow.qrImage.value,'');assert.equal(flow.saved.value,false);assert.equal(flow.state.value,'expired');assert.equal(flow.busy.value,false)
})
