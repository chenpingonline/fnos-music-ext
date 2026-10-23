import { computed, ref } from 'vue'
import { errorMessage, request } from './api.ts'

export type DownloadTask = {
  id: string
  user: string
  scope?: string
  track: {title: string; artists: string[]; source?: string}
  state: string
  purpose: string
  mode?: string
  percent: number
  bytes: number
  error?: string
  warning?: string
  path?: string
  created_at?: string
  updated_at?: string
}
export type TaskFilter = 'all' | 'active' | 'attention' | 'completed' | 'cancelled'

export function taskGroup(task: DownloadTask): Exclude<TaskFilter, 'all'> {
  if (['running', 'queued'].includes(task.state)) return 'active'
  if (task.state === 'cancelled') return 'cancelled'
  if (task.state !== 'completed' || task.warning) return 'attention'
  return 'completed'
}
export function canRetry(task: DownloadTask) {
  return ['failed', 'paused', 'cancelled'].includes(task.state)
    || task.state === 'completed' && task.purpose === 'bind' && !!task.warning
}
export function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KiB`
  if (bytes < 1073741824) return `${(bytes / 1048576).toFixed(1)} MiB`
  return `${(bytes / 1073741824).toFixed(2)} GiB`
}

export function useDownloadTasks() {
  const jobs = ref<DownloadTask[]>([]), loading = ref(false), loaded = ref(false)
  const error = ref(''), notice = ref(''), ffmpeg = ref(false), updated = ref<Date>()
  const pending = ref<Record<string, string>>({})
  const filter = ref<TaskFilter>('all'), query = ref(''), page = ref(1)
  const counts = computed(() => {
    const result = {all: jobs.value.length, active: 0, attention: 0, completed: 0, cancelled: 0}
    for (const job of jobs.value) result[taskGroup(job)]++
    return result
  })
  const filtered = computed(() => {
    const keyword = query.value.trim().toLocaleLowerCase()
    const rank = {active: 0, attention: 1, completed: 2, cancelled: 3}
    return jobs.value.filter(job =>
      (filter.value === 'all' || taskGroup(job) === filter.value)
      && (!keyword || [job.track.title, ...(job.track.artists ?? []), job.user].join(' ').toLocaleLowerCase().includes(keyword))
    ).sort((a, b) => rank[taskGroup(a)] - rank[taskGroup(b)] || (b.created_at ?? '').localeCompare(a.created_at ?? ''))
  })
  const pageCount = computed(() => Math.max(1, Math.ceil(filtered.value.length / 20)))
  const currentPage = computed(() => Math.min(Math.max(1, page.value), pageCount.value))
  const visible = computed(() => filtered.value.slice((currentPage.value - 1) * 20, currentPage.value * 20))
  let controller: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false, revision = 0
  const actions = new Set<AbortController>()

  function schedule() {
    clearTimeout(timer)
    if (!stopped && !document.hidden) timer = setTimeout(() => void refresh(), counts.value.active ? 3000 : 10000)
  }
  async function refresh() {
    if (stopped) return
    clearTimeout(timer)
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal, current = ++revision
    loading.value = true
    try {
      const result = await request<{list: DownloadTask[] | null; ffmpeg: boolean}>('/tasks', 'GET', undefined, signal)
      if (stopped || current !== revision) return
      jobs.value = result.list ?? []
      ffmpeg.value = result.ffmpeg
      loaded.value = true
      updated.value = new Date()
      error.value = ''
    } catch (reason) {
      if (!stopped && !signal.aborted && current === revision) error.value = errorMessage(reason)
    } finally {
      if (!stopped && current === revision) {
        loading.value = false
        schedule()
      }
    }
  }
  async function action(job: DownloadTask, kind: 'retry' | 'cancel') {
    if (stopped || pending.value[job.id]) return
    const abort = new AbortController()
    actions.add(abort)
    pending.value[job.id] = kind
    notice.value = ''
    error.value = ''
    try {
      await request('/tasks/' + encodeURIComponent(job.id) + '/' + kind, 'POST', {}, abort.signal)
      if (stopped) return
      notice.value = kind === 'cancel' ? '任务已取消。' : job.state === 'completed' ? '已重新提交收藏绑定。' : '任务已提交重试。'
      await refresh()
    } catch (reason) {
      if (!stopped && !abort.signal.aborted) error.value = errorMessage(reason)
    } finally {
      delete pending.value[job.id]
      actions.delete(abort)
    }
  }
  function visibility() {
    if (document.hidden) {
      clearTimeout(timer)
      controller?.abort()
      revision++
      loading.value = false
    } else void refresh()
  }
  function start() {
    stopped = false
    document.addEventListener('visibilitychange', visibility)
    void refresh()
  }
  function stop() {
    stopped = true
    revision++
    clearTimeout(timer)
    controller?.abort()
    for (const action of actions) action.abort()
    document.removeEventListener('visibilitychange', visibility)
  }
  return {jobs, loading, loaded, error, notice, ffmpeg, updated, pending, filter, query, page, counts, filtered, pageCount, currentPage, visible, refresh, action, start, stop}
}
