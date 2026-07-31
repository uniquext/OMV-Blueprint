<script setup lang="ts">
import { EllipsisVertical } from '@lucide/vue'
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  label: string
  width: number
  testId?: string
}>()

const emit = defineEmits<{
  toggle: []
  close: []
}>()

const root = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)
const popover = ref<HTMLElement | null>(null)
const popoverStyle = ref<Record<string, string>>({})
const positioned = ref(false)

function updatePosition(): void {
  const triggerElement = trigger.value
  const popoverElement = popover.value
  if (!triggerElement || !popoverElement) return

  const rect = triggerElement.getBoundingClientRect()
  const edge = 8
  const gap = 6
  const left = Math.min(window.innerWidth - props.width - edge, Math.max(edge, rect.right - props.width))
  const placeBelow = window.innerHeight - rect.bottom >= popoverElement.offsetHeight + gap
  const top = placeBelow ? rect.bottom + gap : Math.max(edge, rect.top - popoverElement.offsetHeight - gap)
  popoverStyle.value = {
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
    width: `${props.width}px`
  }
  positioned.value = true
}

function handleDocumentPointerDown(event: PointerEvent): void {
  const target = event.target
  if (!(target instanceof Node)) return
  if (root.value?.contains(target) || popover.value?.contains(target)) return
  emit('close')
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Escape') return
  event.stopPropagation()
  emit('close')
  void nextTick(() => trigger.value?.focus())
}

function addListeners(): void {
  document.addEventListener('pointerdown', handleDocumentPointerDown)
  document.addEventListener('keydown', handleKeydown)
  window.addEventListener('resize', updatePosition)
  window.addEventListener('scroll', updatePosition, true)
}

function removeListeners(): void {
  document.removeEventListener('pointerdown', handleDocumentPointerDown)
  document.removeEventListener('keydown', handleKeydown)
  window.removeEventListener('resize', updatePosition)
  window.removeEventListener('scroll', updatePosition, true)
}

watch(
  () => props.open,
  async (open) => {
    removeListeners()
    positioned.value = false
    if (!open) return
    await nextTick()
    updatePosition()
    addListeners()
    const firstAction = popover.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')
    if (firstAction) firstAction.focus()
    else popover.value?.focus()
  },
  { flush: 'post' }
)

onBeforeUnmount(removeListeners)
</script>

<template>
  <div ref="root" class="row-action-menu">
    <button
      ref="trigger"
      class="button row-action-trigger"
      type="button"
      :aria-label="label"
      aria-haspopup="menu"
      :aria-expanded="open"
      :data-testid="testId"
      :title="label"
      @click.stop="emit('toggle')"
    >
      <EllipsisVertical :size="18" aria-hidden="true" />
    </button>

    <Teleport to="body">
      <div
        v-if="open"
        ref="popover"
        class="row-action-popover"
        :class="{ 'row-action-popover--positioned': positioned }"
        role="menu"
        tabindex="-1"
        :aria-label="label"
        :style="popoverStyle"
      >
        <slot />
      </div>
    </Teleport>
  </div>
</template>
