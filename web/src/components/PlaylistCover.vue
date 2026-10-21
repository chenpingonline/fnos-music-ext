<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'

const props = defineProps<{src?: string}>()
const failed = ref(false)
const source = computed(() => {
  if (!props.src) return ''
  try {
    const url = new URL(props.src)
    if (!['http:', 'https:'].includes(url.protocol)) return ''
    // NetEase also returns HTTP artwork URLs; use HTTPS on secure NAS pages.
    if (url.protocol === 'http:' && /(^|\.)music\.126\.net$/.test(url.hostname)) url.protocol = 'https:'
    return url.href
  } catch { return '' }
})
watch(source, () => { failed.value = false })
</script>

<template>
  <div class="playlist-cover" aria-hidden="true">
    <img v-if="source && !failed" :key="source" :src="source" alt="" loading="lazy" decoding="async" referrerpolicy="no-referrer" @error="failed = true"/>
    <AppIcon v-else name="music"/>
  </div>
</template>

<style scoped>
.playlist-cover { width: 44px; height: 44px; flex: 0 0 44px; display: grid; place-items: center; overflow: hidden; border-radius: 6px; background: var(--surface); color: var(--muted); }
.playlist-cover img { display: block; width: 100%; height: 100%; object-fit: cover; }
.playlist-cover :deep(svg) { width: 20px; height: 20px; }
</style>
