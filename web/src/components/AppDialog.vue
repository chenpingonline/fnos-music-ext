<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
const props = defineProps<{title: string; busy?: boolean}>()
const emit = defineEmits<{close: []}>()
const dialog = ref<HTMLDialogElement>()
const scrolling = ref(false)
const titleID = useId()
let previous: HTMLElement | null = null
let timer: ReturnType<typeof setTimeout> | undefined
function onScroll() { scrolling.value = true; clearTimeout(timer); timer = setTimeout(() => scrolling.value = false, 800) }
function dismiss(event?: Event) {
  event?.preventDefault()
  if (!props.busy) emit('close')
}
onMounted(() => { previous = document.activeElement as HTMLElement; dialog.value?.showModal() })
onBeforeUnmount(() => { clearTimeout(timer); dialog.value?.close(); if (previous?.isConnected) previous.focus() })
</script>
<template>
  <dialog ref="dialog" class="app-dialog" :class="{'is-scrolling':scrolling}" :aria-labelledby="titleID" @cancel="dismiss" @scroll.capture="onScroll">
    <div class="dialog-header"><h2 :id="titleID">{{title}}</h2><button class="icon-button" aria-label="关闭对话框" :disabled="busy" @click="dismiss()"><AppIcon name="close" /></button></div>
    <slot />
  </dialog>
</template>
