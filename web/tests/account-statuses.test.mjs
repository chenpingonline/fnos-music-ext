import { test } from 'node:test'
import assert from 'node:assert/strict'
import { useAccountStatuses } from '../src/useAccountStatuses.ts'

const account = id => ({id, name: id, bridge_url: 'native://netease/' + id})
const response = (data, status = 200) => new Response(JSON.stringify(data), {status, headers: {'Content-Type': 'application/json'}})
const settle = () => new Promise(resolve => setImmediate(resolve))

test('checks each account once and distinguishes expired credentials from failed or unknown checks', async t => {
  const calls = []
  t.mock.method(globalThis, 'fetch', async url => {
    calls.push(url)
    if (url.includes('/valid/')) return response({state: 'success', logged_in: true})
    if (url.includes('/expired/')) return response({state: 'expired', logged_in: false})
    if (url.includes('/pending/')) return response({state: 'pending', logged_in: false})
    if (url.includes('/unknown/')) return response({state: 'unknown', logged_in: false})
    return response({}, 502)
  })
  const statuses = useAccountStatuses()
  const accounts = ['valid', 'expired', 'pending', 'unknown', 'failure'].map(account)
  const checking = statuses.check(accounts)
  assert.equal(statuses.get('valid').state, 'checking')
  await checking
  assert.equal(statuses.get('valid').state, 'valid')
  assert.equal(statuses.get('expired').state, 'expired')
  assert.equal(statuses.get('pending').state, 'expired')
  assert.equal(statuses.get('unknown').state, 'unknown')
  assert.equal(statuses.get('failure').state, 'error')
  assert.match(statuses.get('failure').detail, /音源暂时无法响应/)
  assert.equal(calls.length, accounts.length)
  assert.equal(new Set(calls).size, accounts.length)
  assert.ok(calls.every(url => url.endsWith('/auth/status')))
})

test('returning to the page cancels the previous check and ignores its late result', async t => {
  let finishOld, oldSignal
  let calls = 0
  t.mock.method(globalThis, 'fetch', (url, options) => {
    if (++calls === 1) {
      oldSignal = options.signal
      return new Promise(resolve => { finishOld = resolve })
    }
    return Promise.resolve(response({state: 'expired', logged_in: false}))
  })
  const statuses = useAccountStatuses()
  const old = statuses.check([account('one')])
  await statuses.check([account('one')])
  assert.equal(oldSignal.aborted, true)
  finishOld(response({state: 'success', logged_in: true}))
  await old
  assert.equal(statuses.get('one').state, 'expired')
  assert.equal(calls, 2)
})

test('leaving the page stops queued checks and late responses cannot change status', async t => {
  const pending = []
  t.mock.method(globalThis, 'fetch', (url, options) => new Promise(resolve => pending.push({resolve, signal: options.signal})))
  const statuses = useAccountStatuses()
  const checking = statuses.check(['a', 'b', 'c', 'd', 'e'].map(account))
  assert.equal(pending.length, 3)
  statuses.cancel()
  assert.ok(pending.every(p => p.signal.aborted))
  for (const p of pending) p.resolve(response({state: 'success', logged_in: true}))
  await checking
  await settle()
  assert.equal(pending.length, 3)
  assert.equal(statuses.get('a').state, 'checking')
})
