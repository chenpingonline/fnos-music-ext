import { computed, ref } from 'vue'
import { ApiError, errorMessage, request } from './api.ts'
export type Account = {id: string; name: string; bridge_url: string}
type LoginStatus = {state: string; logged_in: boolean}
type QRChallenge = {qr_url: string; qr_ascii?: string}
export type MusicPlaylist = {id: string; title: string; track_count: number; cover_url?: string}
type MusicTrack = {id: string; title: string; artists: string[]}

export function useAccountPanel() {
  const account = ref<Account | null>(null), action = ref(''), error = ref('')
  const auth = ref<LoginStatus | null>(null), qrImage = ref(''), qrURL = ref('')
  const playlists = ref<MusicPlaylist[]>([]), tracks = ref<MusicTrack[]>([])
  const playlist = ref<MusicPlaylist | null>(null), playlistsLoaded = ref(false)
  const page = ref(1), retryAt = ref(0), expired = ref(false)
  let scope = 0, controller: AbortController | undefined
  let expiry: ReturnType<typeof setTimeout> | undefined
  let cooldown: ReturnType<typeof setTimeout> | undefined
  let polling: ReturnType<typeof setTimeout> | undefined
  function schedulePoll() {
    clearTimeout(polling)
    if (!qrURL.value || !account.value) return
    const generation = scope
    polling = setTimeout(() => { if (generation === scope && qrURL.value) void checkLogin() }, 2500)
  }
  const busy = computed(() => action.value !== '')
  const visibleTracks = computed(() => tracks.value.slice((page.value - 1) * 50, page.value * 50))
  const pageCount = computed(() => Math.max(1, Math.ceil(tracks.value.length / 50)))
  const statusText = computed(() => auth.value?.logged_in ? '已登录' : ({pending: qrURL.value ? '等待扫码' : '未登录', scanned: '已扫码，请在手机上确认', expired: '二维码已过期', unknown: '状态未知'}[auth.value?.state ?? ''] ?? '未检测'))
  function clearQR() { clearTimeout(polling); clearTimeout(expiry); clearTimeout(cooldown); qrImage.value = ''; qrURL.value = ''; retryAt.value = 0 }
  function reset() {
    controller?.abort(); scope++; clearQR(); action.value = ''; error.value = ''
    auth.value = null; playlists.value = []; tracks.value = []; playlist.value = null
    playlistsLoaded.value = false; page.value = 1; expired.value = false
  }
  async function run(key: string, task: (signal: AbortSignal, current: () => boolean) => Promise<void>) {
    if (busy.value || !account.value) return
    const generation = scope
    controller = new AbortController()
    const signal = controller.signal
    const current = () => !signal.aborted && generation === scope
    clearTimeout(polling)
    action.value = key; error.value = ''
    try { await task(signal, current) } catch (reason) { if (current()) error.value = errorMessage(reason) }
    finally { if (current()) { action.value = ''; schedulePoll() } }
  }
  function endpoint(path: string) { return '/accounts/' + encodeURIComponent(account.value!.id) + path }
  async function checkStatus() {
    if (qrURL.value) { await checkLogin(); return }
    await run('status', async (signal, current) => {
      const result = await request<LoginStatus>(endpoint('/auth/status'), 'GET', undefined, signal)
      if (current()) {
        auth.value = result
        if (result.logged_in) { clearQR(); expired.value = false }
      }
    })
  }
  function open(value: Account, login = false) { reset(); account.value = value; return login ? startLogin() : checkStatus() }
  function openPlaylists(value: Account) { reset(); account.value = value; return loadPlaylists() }
  function close() { reset(); account.value = null }
  async function startLogin() {
    await run('qr', async (signal, current) => {
      const result = await request<QRChallenge>(endpoint('/auth/qr'), 'POST', {}, signal)
      if (!current()) return
      const url = new URL(result.qr_url)
      if (url.protocol !== 'https:' || url.hostname !== 'music.163.com') throw new Error('音源返回的登录链接无效，请重试。')
      const {default: QRCode} = await import('qrcode')
      if (!current()) return
      const image = await QRCode.toDataURL(url.href, {width: 224, margin: 3, errorCorrectionLevel: 'M', color:{dark:'#111111',light:'#ffffff'}})
      if (!current()) return
      clearQR(); expired.value = false; qrImage.value = image; qrURL.value = url.href
      auth.value = {state:'pending',logged_in:false}
      expiry = setTimeout(() => { if (current()) { clearQR(); expired.value = true; auth.value = {state:'expired',logged_in:false} } }, 180_000)
    })
  }
  async function checkLogin() {
    if (!qrURL.value || retryAt.value) return
    await run('check', async (signal, current) => {
      retryAt.value = Date.now() + 2000
      cooldown = setTimeout(() => { if (current()) retryAt.value = 0 }, 2000)
      let result: LoginStatus
      try { result = await request<LoginStatus>(endpoint('/auth/check'), 'POST', {}, signal) }
      catch (reason) {
        if (current() && reason instanceof ApiError && reason.status === 410) { clearQR(); expired.value = true; auth.value = {state:'expired',logged_in:false} }
        throw reason
      }
      if (!current()) return
      auth.value = result
      if (result.logged_in) { clearQR(); expired.value = false }
      else if (result.state === 'expired') { clearQR(); expired.value = true }
    })
  }
  async function loadPlaylists() {
    await run('playlists', async (signal, current) => {
      const result = await request<MusicPlaylist[] | null>(endpoint('/playlists'), 'GET', undefined, signal)
      if (current()) { playlists.value = result ?? []; playlistsLoaded.value = true; playlist.value = null; tracks.value = [] }
    })
  }
  async function loadTracks(value: MusicPlaylist) {
    await run('tracks', async (signal, current) => {
      const result = await request<MusicTrack[] | null>(endpoint('/playlists/'+encodeURIComponent(value.id)+'/tracks'), 'GET', undefined, signal)
      if (current()) { playlist.value = value; tracks.value = result ?? []; page.value = 1 }
    })
  }
  return {account, action, error, auth, qrImage, qrURL, expired, playlists, playlist, tracks, playlistsLoaded, page, retryAt, busy, visibleTracks, pageCount, statusText, open, openPlaylists, close, startLogin, checkLogin, checkStatus, loadPlaylists, loadTracks}
}
