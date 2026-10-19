export type Theme = 'dark' | 'light'
export interface HostTheme {
  ready?(): Promise<void>
  isWeb: boolean
  isStandaloneWeb: boolean
  getPlatformConfig(): Promise<{theme?: unknown}>
  $on(event: 'os/theme', callback: (theme: unknown) => void): Promise<unknown>
  $off(event: 'os/theme', callback: (theme: unknown) => void): unknown
}

// Dependency injection keeps host lifecycle races testable without a real NAS.
export function connectTheme(host: HostTheme, media: MediaQueryList, apply: (theme: Theme) => void) {
  let disposed = false, hostActive = false, subscribed = false, eventRevision = 0
  const valid = (value: unknown): value is Theme => value === 'light' || value === 'dark'
  const browserTheme = () => { if (!disposed && !hostActive) apply(media.matches ? 'dark' : 'light') }
  const hostTheme = (theme: unknown) => {
    if (disposed || !valid(theme)) return
    eventRevision++
    hostActive = true
    apply(theme)
  }
  const unsubscribe = () => { try { void Promise.resolve(host.$off('os/theme', hostTheme)).catch(() => {}) } catch {} }
  browserTheme()
  media.addEventListener('change', browserTheme)
  const ready = (async () => {
    try { await host.ready?.() } catch { return }
    if (disposed || host.isStandaloneWeb) return
    if (host.isWeb) {
      try {
        await host.$on('os/theme', hostTheme)
        subscribed = true
        if (disposed) { unsubscribe(); return }
      } catch { /* Old hosts can still provide an initial config. */ }
    }
    try {
      const config = await host.getPlatformConfig()
      if (!disposed && eventRevision === 0 && valid(config.theme)) {
        hostActive = true
        apply(config.theme)
      }
    } catch { /* Keep the browser fallback. */ }
  })()
  return {
    ready,
    dispose() {
      disposed = true
      media.removeEventListener('change', browserTheme)
      if (subscribed) { subscribed = false; unsubscribe() }
    },
  }
}
