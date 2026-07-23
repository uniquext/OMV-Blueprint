<script setup lang="ts">
import { ref } from 'vue'
import { CircleHelp, Plus, Trash2 } from '@lucide/vue'
import { t } from '../i18n'

const props = defineProps<{
  label: string
  values: string[]
  testId: string
  errors?: Record<number, string>
  error?: string
  help?: string
}>()

const emit = defineEmits<{
  'update:values': [values: string[]]
}>()

const helpOpen = ref(false)

function update(index: number, value: string): void {
  const next = [...props.values]
  next[index] = value
  emit('update:values', next)
}

function add(): void {
  emit('update:values', [...props.values, ''])
}

function remove(index: number): void {
  emit('update:values', props.values.filter((_, itemIndex) => itemIndex !== index))
}

function paste(index: number, event: ClipboardEvent): void {
  const lines = event.clipboardData?.getData('text').split(/\r?\n/).map((line) => line.trim()).filter(Boolean) ?? []
  if (lines.length < 2) {
    return
  }
  event.preventDefault()
  const next = [...props.values]
  next.splice(index, 1, ...lines)
  emit('update:values', next)
}
</script>

<template>
  <div
    class="settings-list-editor settings-list-editor--inline"
    :data-testid="`${testId}-field`"
    role="group"
    :aria-labelledby="`${testId}-label`"
  >
    <span :id="`${testId}-label`" class="settings-list-editor__label">
      <span>{{ label }}</span>
      <button
        v-if="help"
        type="button"
        class="settings-list-editor__help-toggle"
        :data-testid="`${testId}-help-toggle`"
        :title="helpOpen ? t('settingsHideHelp') : t('settingsShowHelp')"
        :aria-label="`${label} - ${helpOpen ? t('settingsHideHelp') : t('settingsShowHelp')}`"
        :aria-expanded="helpOpen"
        :aria-controls="`${testId}-help`"
        @click="helpOpen = !helpOpen"
      >
        <CircleHelp aria-hidden="true" />
      </button>
    </span>
    <div class="settings-list-editor__controls">
      <div v-for="(value, index) in values" :key="index" class="settings-list-editor__row">
        <input
          :value="value"
          :data-testid="`${testId}-${index}`"
          :aria-invalid="Boolean(errors?.[index])"
          @input="update(index, ($event.target as HTMLInputElement).value)"
          @paste="paste(index, $event)"
        />
        <button type="button" class="icon-button" :title="`${label} - ${index + 1}`" @click="remove(index)">
          <Trash2 aria-hidden="true" />
        </button>
        <span v-if="errors?.[index]" class="field-error">{{ errors[index] }}</span>
      </div>
      <button type="button" class="text-button" @click="add">
        <Plus aria-hidden="true" />
        <span>{{ t('settingsAddItem') }}</span>
      </button>
      <span v-if="error" class="field-error">{{ error }}</span>
    </div>
    <div v-if="help && helpOpen" :id="`${testId}-help`" class="settings-list-editor__help" :data-testid="`${testId}-help`">
      <slot name="help">{{ help }}</slot>
    </div>
  </div>
</template>
