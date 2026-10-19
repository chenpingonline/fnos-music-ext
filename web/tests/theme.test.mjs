import { test } from 'node:test'
import assert from 'node:assert/strict'
import { connectTheme } from '../src/theme.ts'
const deferred = () => { let resolve, reject; const promise = new Promise((r,j) => {resolve=r;reject=j}); return {promise,resolve,reject} }
function fixture(overrides={}) {
  const applied=[], callbacks=new Set(), mediaCallbacks=new Set()
  const media={matches:false,addEventListener:(_,cb)=>mediaCallbacks.add(cb),removeEventListener:(_,cb)=>mediaCallbacks.delete(cb)}
  const host={isWeb:true,isStandaloneWeb:false,getPlatformConfig:async()=>({theme:'dark'}),$on:async(_,cb)=>callbacks.add(cb),$off:(_,cb)=>callbacks.delete(cb),...overrides}
  const connection=connectTheme(host,media,theme=>applied.push(theme))
  return {host,media,connection,applied,callbacks,mediaCallbacks,emit(theme){for(const cb of callbacks)cb(theme)},browser(dark){media.matches=dark;for(const cb of mediaCallbacks)cb()}}
}
test('host initial theme and continuous events override browser changes',async()=>{
  const f=fixture();await f.connection.ready;assert.deepEqual(f.applied,['light','dark']);
  f.emit('light');f.browser(true);assert.equal(f.applied.at(-1),'light');f.emit('dark');assert.equal(f.applied.at(-1),'dark');
  f.connection.dispose();assert.equal(f.callbacks.size,0);assert.equal(f.mediaCallbacks.size,0)
})
test('standalone browser uses change events without contacting host',async()=>{
  const f=fixture({isStandaloneWeb:true,getPlatformConfig:()=>{throw Error('must not call')}});await f.connection.ready;
  assert.equal(f.callbacks.size,0);f.browser(true);assert.equal(f.applied.at(-1),'dark');f.connection.dispose()
})
test('failed initialization keeps browser fallback',async()=>{
  const f=fixture({ready:async()=>{throw Error('old host')}});await f.connection.ready;f.browser(true);assert.equal(f.applied.at(-1),'dark');f.connection.dispose()
})
test('config failure and invalid theme leave fallback operational',async()=>{
  for(const getPlatformConfig of [async()=>{throw Error('offline')},async()=>({theme:'invalid'})]){
    const f=fixture({getPlatformConfig});await f.connection.ready;f.emit({theme:'dark'});f.browser(true);assert.equal(f.applied.at(-1),'dark');f.connection.dispose()
  }
})
test('mobile reads initial config without web event subscription',async()=>{
  const f=fixture({isWeb:false});await f.connection.ready;assert.equal(f.callbacks.size,0);assert.equal(f.applied.at(-1),'dark');f.connection.dispose()
})
test('new theme event cannot be overwritten by an old initial response',async()=>{
  const config=deferred();const f=fixture({getPlatformConfig:()=>config.promise});await new Promise(r=>setImmediate(r));
  f.emit('dark');config.resolve({theme:'light'});await f.connection.ready;assert.equal(f.applied.at(-1),'dark');f.connection.dispose()
})
test('unmount while subscription is pending immediately removes eventual listener',async()=>{
  const pending=deferred();let removed=0;let registered;
  const f=fixture({$on:async(_,cb)=>{registered=cb;await pending.promise},$off:(_,cb)=>{assert.equal(cb,registered);removed++}});
  await new Promise(r=>setImmediate(r));f.connection.dispose();pending.resolve();await f.connection.ready;
  assert.equal(removed,1);assert.deepEqual(f.applied,['light']);assert.equal(f.mediaCallbacks.size,0)
})
test('unmount during initial request prevents later DOM update',async()=>{
  const pending=deferred();const f=fixture({getPlatformConfig:()=>pending.promise});await new Promise(r=>setImmediate(r));
  f.connection.dispose();pending.resolve({theme:'dark'});await f.connection.ready;assert.deepEqual(f.applied,['light']);assert.equal(f.callbacks.size,0)
})
