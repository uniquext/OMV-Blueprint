<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{
  open: boolean
  title: string
  message: string
  confirmText: string
  cancelText: string
}>()

const emit = defineEmits<{
  confirm: []
  cancel: []
}>()

const dialog = ref<HTMLDialogElement | null>(null)

watch(
  () => props.open,
  (open) => {
    const element = dialog.value
    if (!element) {
      return
    }
    if (open && !element.open) {
      element.showModal()
    }
    if (!open && element.open) {
      element.close()
    }
  },
  { immediate: true, flush: 'post' }
)
</script>

<template>
  <dialog ref="dialog" class="confirm-dialog" @cancel.prevent="emit('cancel')">
    <form method="dialog" class="confirm-dialog__content" @submit.prevent="emit('confirm')">
      <h2 class="confirm-dialog__title">{{ title }}</h2>
      <p class="confirm-dialog__message">{{ message }}</p>
      <div class="confirm-dialog__actions">
        <button class="button" type="button" @click="emit('cancel')">{{ cancelText }}</button>
        <button class="button primary" type="submit">{{ confirmText }}</button>
      </div>
    </form>
  </dialog>
</template>
