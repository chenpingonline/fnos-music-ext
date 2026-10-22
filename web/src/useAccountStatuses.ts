import { ref } from 'vue'
import { errorMessage, request } from './api.ts'
import type { Account } from './useAccountPanel.ts'

type State = 'checking' | 'valid' | 'expired' | 'error' | 'unknown'
type AccountStatus = { state: State; label: string; detail: string }
type LoginStatus = { state: string; logged_in: boolean }

const unknown: AccountStatus = { state: 'unknown', label: '未检测', detail: '进入音源账号页面后检查登录状态。' }

export function useAccountStatuses() {
  const statuses = ref<Record<string, AccountStatus>>({})
  let controller: AbortController | undefined
  let generation = 0

  function cancel() {
    generation++
    controller?.abort()
  }

  async function check(accounts: readonly Account[]) {
    cancel()
    const current = generation
    controller = new AbortController()
    const signal = controller.signal
    statuses.value = Object.fromEntries(accounts.map(a => [a.id, { state: 'checking', label: '检测中', detail: '正在向音乐平台确认登录状态。' }]))
    let next = 0
    // Limit upstream requests without delaying rendering the account list.
    await Promise.all(Array.from({ length: Math.min(3, accounts.length) }, async () => {
      while (!signal.aborted && next < accounts.length) {
        const account = accounts[next++]!
        let status: AccountStatus
        try {
          const result = await request<LoginStatus>('/accounts/' + encodeURIComponent(account.id) + '/auth/status', 'GET', undefined, signal)
          if (result?.logged_in === true) {
            status = { state: 'valid', label: '有效', detail: '音乐平台已确认账号登录有效。' }
          } else if (result?.logged_in === false && ['pending', 'expired'].includes(result.state)) {
            status = { state: 'expired', label: '已失效', detail: '账号未登录或登录已失效，请在歌单面板中重新扫码登录。' }
          } else {
            status = { state: 'unknown', label: '状态未知', detail: '音乐平台暂未确认登录状态，请稍后刷新重试。' }
          }
        } catch (reason) {
          status = { state: 'error', label: '检测失败', detail: errorMessage(reason) }
        }
        if (current === generation && !signal.aborted) statuses.value[account.id] = status
      }
    }))
  }

  function get(id: string) { return statuses.value[id] ?? unknown }
  return { check, cancel, get }
}
