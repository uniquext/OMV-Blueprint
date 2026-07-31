<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{
  text: string
}>()

const viewport = ref<HTMLElement | null>(null)
const content = ref<HTMLElement | null>(null)
const overflowDistance = ref(0)
const active = ref(false)
let resizeObserver: ResizeObserver | null = null
let touchPointerActive = false

const overflowing = computed(() => overflowDistance.value > 0)
const marqueeStyle = computed<Record<string, string>>(() => ({
  '--overflow-marquee-distance': `${overflowDistance.value}px`,
  '--overflow-marquee-duration': `${Math.max(3, overflowDistance.value / 24).toFixed(2)}s`
}))

function measure(): void {
  const viewportElement = viewport.value
  const contentElement = content.value
  if (!viewportElement || !contentElement) return
  overflowDistance.value = Math.max(0, contentElement.scrollWidth - viewportElement.clientWidth)
  if (!overflowDistance.value) active.value = false
}

function start(): void {
  if (overflowing.value) active.value = true
}

function stop(): void {
  active.value = false
}

function handleFocus(): void {
  if (!touchPointerActive) start()
}

function handlePointerDown(event: PointerEvent): void {
  if (event.pointerType !== 'mouse') touchPointerActive = true
}

function handlePointerUp(event: PointerEvent): void {
  if (event.pointerType !== 'mouse' && overflowing.value) active.value = !active.value
  touchPointerActive = false
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Escape') return
  stop()
  viewport.value?.blur()
}

onMounted(() => {
  void nextTick(measure)
  resizeObserver = new ResizeObserver(measure)
  if (viewport.value) resizeObserver.observe(viewport.value)
  if (content.value) resizeObserver.observe(content.value)
})

watch(
  () => props.text,
  () => void nextTick(measure)
)

onBeforeUnmount(() => resizeObserver?.disconnect())
</script>

<template>
  <span
    ref="viewport"
    class="overflow-marquee"
    :class="{
      'overflow-marquee--overflowing': overflowing,
      'overflow-marquee--active': active
    }"
    :style="marqueeStyle"
    :tabindex="overflowing ? 0 : -1"
    :title="text"
    @mouseenter="start"
    @mouseleave="stop"
    @focus="handleFocus"
    @blur="stop"
    @pointerdown="handlePointerDown"
    @pointerup="handlePointerUp"
    @pointercancel="touchPointerActive = false"
    @keydown="handleKeydown"
  >
    <span ref="content" class="overflow-marquee__content">{{ text }}</span>
  </span>
</template>
