<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { Check, ChevronDown, ChevronRight, Folder, FolderOpen, Pencil, Plus, Trash2 } from '@lucide/vue'
import { t } from '../i18n'
import type { MediaDirectory, MediaDirectoryListing } from '../lib/types'

type ArrayRule = 'media-root' | 'extension' | 'exclude-dir' | 'exclude-pattern' | 'codec'

interface DirectoryTreeRow {
  kind: 'directory' | 'status'
  key: string
  depth: number
  directory?: MediaDirectory
  parent?: string
  status?: 'loading' | 'error' | 'empty'
  message?: string
}

const props = defineProps<{
  label: string
  hint: string
  values: string[]
  testId: string
  rule: ArrayRule
  help?: string
  directoryLoader?: (parent: string) => Promise<MediaDirectoryListing>
}>()

const emit = defineEmits<{
  'update:values': [values: string[]]
}>()

const dialogOpen = ref(false)
const workingValues = ref<string[]>([])
const pickerOpen = ref(false)
const editingIndex = ref(-1)
const selectedPath = ref('')
const expandedPaths = ref<Set<string>>(new Set())
const loadingPaths = ref<Set<string>>(new Set())
const directoryCache = ref<Record<string, MediaDirectory[]>>({})
const directoryErrors = ref<Record<string, string>>({})

const isMediaRoot = computed(() => props.rule === 'media-root')
const summary = computed(() => props.values.length ? props.values.join('； ') : t('settingsArrayNotConfigured'))
const countText = computed(() => t('settingsArrayCount').replace('{count}', String(props.values.length)))
const required = computed(() => ['media-root', 'extension', 'codec'].includes(props.rule))
const listError = computed(() => required.value && workingValues.value.length === 0 ? t('settingsRequiredList') : '')
const rowErrors = computed(() => workingValues.value.map((_, index) => validateValue(index)))
const selectedDuplicate = computed(() => isDuplicatePath(selectedPath.value))
const pickerConfirmDisabled = computed(() => !selectedPath.value || selectedDuplicate.value)
const treeRows = computed<DirectoryTreeRow[]>(() => {
  const rows: DirectoryTreeRow[] = []
  const append = (directory: MediaDirectory, depth: number): void => {
    rows.push({ kind: 'directory', key: directory.path, depth, directory })
    if (!expandedPaths.value.has(directory.path)) return
    if (loadingPaths.value.has(directory.path)) {
      rows.push({ kind: 'status', key: `${directory.path}:loading`, depth: depth + 1, parent: directory.path, status: 'loading' })
      return
    }
    const error = directoryErrors.value[directory.path]
    if (error) {
      rows.push({ kind: 'status', key: `${directory.path}:error`, depth: depth + 1, parent: directory.path, status: 'error', message: error })
      return
    }
    const children = directoryCache.value[directory.path]
    if (children?.length) {
      children.forEach((child) => append(child, depth + 1))
    } else if (children) {
      rows.push({ kind: 'status', key: `${directory.path}:empty`, depth: depth + 1, parent: directory.path, status: 'empty' })
    }
  }
  append({ name: '/media', path: '/media', has_children: true }, 0)
  return rows
})

function normalizedValue(value: string): string {
  const trimmed = value.trim()
  return props.rule === 'extension' || props.rule === 'codec' ? trimmed.toLowerCase() : trimmed
}

function validateValue(index: number): string {
  const value = workingValues.value[index].trim()
  if (!value) return isMediaRoot.value ? t('settingsSelectMediaRoot') : t('settingsRequiredValue')
  if (props.rule === 'media-root' && !(value === '/media' || value.startsWith('/media/'))) return t('settingsMediaRootError')
  if (props.rule === 'extension' && !value.startsWith('.')) return t('settingsExtensionError')
  if (props.rule === 'exclude-dir' && /[\/]/.test(value)) return t('settingsExcludeDirError')
  if (props.rule === 'exclude-pattern') {
    const opens = (value.match(/\[/g) ?? []).length
    const closes = (value.match(/\]/g) ?? []).length
    if (opens !== closes) return t('settingsPatternError')
  }
  const duplicate = workingValues.value.findIndex((item, itemIndex) => itemIndex !== index && normalizedValue(item) === normalizedValue(value))
  return duplicate >= 0 ? t('settingsArrayDuplicate').replace('{index}', String(duplicate + 1)) : ''
}

function editorSelector(index: number): string {
  if (index < 0) return `[data-testid="${props.testId}-add"]`
  return isMediaRoot.value
    ? `[data-testid="${props.testId}-row-edit-${index}"]`
    : `[data-testid="${props.testId}-dialog-${index}"]`
}

function focusEditor(index: number): void {
  document.querySelector<HTMLElement>(editorSelector(index))?.focus()
}

async function openEditor(): Promise<void> {
  workingValues.value = [...props.values]
  dialogOpen.value = true
  await nextTick()
  focusEditor(workingValues.value.length ? 0 : -1)
}

async function add(): Promise<void> {
  workingValues.value.push('')
  await nextTick()
  focusEditor(workingValues.value.length - 1)
}

async function remove(index: number): Promise<void> {
  workingValues.value.splice(index, 1)
  await nextTick()
  focusEditor(workingValues.value.length ? Math.min(index, workingValues.value.length - 1) : -1)
}

function cancel(): void {
  closePicker()
  dialogOpen.value = false
  workingValues.value = []
}

async function apply(): Promise<void> {
  const firstError = rowErrors.value.findIndex(Boolean)
  if (listError.value || firstError >= 0) {
    await nextTick()
    focusEditor(firstError)
    return
  }
  emit('update:values', workingValues.value.map(normalizedValue))
  cancel()
}

async function openPicker(index: number): Promise<void> {
  editingIndex.value = index
  selectedPath.value = workingValues.value[index]
  expandedPaths.value = new Set()
  directoryErrors.value = {}
  pickerOpen.value = true
  await nextTick()
  document.querySelector<HTMLElement>(`[data-testid="${props.testId}-tree-toggle-/media"]`)?.focus()
}

function closePicker(): void {
  pickerOpen.value = false
  editingIndex.value = -1
  selectedPath.value = ''
  expandedPaths.value = new Set()
  loadingPaths.value = new Set()
  directoryErrors.value = {}
}

function isDuplicatePath(path: string): boolean {
  return Boolean(path) && workingValues.value.some((value, index) => index !== editingIndex.value && value === path)
}

function selectDirectory(directory: MediaDirectory): void {
  if (!isDuplicatePath(directory.path)) selectedPath.value = directory.path
}

async function toggleDirectory(directory: MediaDirectory): Promise<void> {
  if (!directory.has_children) return
  const next = new Set(expandedPaths.value)
  if (next.has(directory.path)) {
    next.delete(directory.path)
    expandedPaths.value = next
    return
  }
  next.add(directory.path)
  expandedPaths.value = next
  if (!(directory.path in directoryCache.value)) await loadDirectory(directory.path)
}

async function loadDirectory(parent: string): Promise<void> {
  const loading = new Set(loadingPaths.value)
  loading.add(parent)
  loadingPaths.value = loading
  const errors = { ...directoryErrors.value }
  delete errors[parent]
  directoryErrors.value = errors
  try {
    if (!props.directoryLoader) throw new Error(t('settingsDirectoryLoadUnavailable'))
    const listing = await props.directoryLoader(parent)
    if (listing.parent !== parent) throw new Error(t('settingsDirectoryResponseMismatch'))
    directoryCache.value = { ...directoryCache.value, [parent]: listing.directories }
  } catch (caught) {
    directoryErrors.value = { ...directoryErrors.value, [parent]: caught instanceof Error ? caught.message : String(caught) }
  } finally {
    const nextLoading = new Set(loadingPaths.value)
    nextLoading.delete(parent)
    loadingPaths.value = nextLoading
  }
}

async function retryDirectory(parent: string): Promise<void> {
  await loadDirectory(parent)
}

async function confirmPicker(): Promise<void> {
  if (pickerConfirmDisabled.value) return
  const index = editingIndex.value
  workingValues.value[index] = selectedPath.value
  closePicker()
  await nextTick()
  focusEditor(index)
}
</script>

<template>
  <div class="settings-array-editor" :data-testid="`${testId}-field`">
    <div class="settings-array-summary" :data-testid="`${testId}-summary-row`">
      <span class="settings-array-summary__label">{{ label }}</span>
      <span class="settings-array-summary__value" :class="{ 'is-empty': !values.length }" :title="summary" :data-testid="`${testId}-summary`">{{ summary }}</span>
      <span class="settings-array-summary__count" :data-testid="`${testId}-count`">{{ countText }}</span>
      <button type="button" class="settings-array-summary__edit" :data-testid="`${testId}-edit`" @click="openEditor">
        <Pencil aria-hidden="true" />{{ t('settingsArrayEdit') }}
      </button>
    </div>

    <div v-if="dialogOpen" class="settings-array-dialog__overlay" role="presentation">
      <section
        class="settings-array-dialog"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="`${testId}-dialog-title`"
        :data-testid="`${testId}-dialog`"
        @keydown.esc.stop.prevent="cancel"
      >
        <header class="settings-array-dialog__header">
          <h3 :id="`${testId}-dialog-title`">{{ t('settingsArrayEditTitle').replace('{label}', label) }}</h3>
          <span class="settings-array-dialog__hint">{{ hint }}</span>
          <button type="button" class="settings-array-dialog__add" :data-testid="`${testId}-add`" @click="add">
            <Plus aria-hidden="true" />{{ t('settingsArrayAdd') }}
          </button>
        </header>

        <div class="settings-array-dialog__body">
          <div class="settings-array-dialog__list" :class="{ 'settings-array-dialog__list--media-root': isMediaRoot }">
            <div v-if="workingValues.length" class="settings-array-dialog__list-head" :class="{ 'settings-array-dialog__list-head--media-root': isMediaRoot }">
              <span>{{ t('settingsArraySequence') }}</span><span>{{ t(isMediaRoot ? 'settingsArrayPath' : 'settingsArrayValue') }}</span><span>{{ t('settingsArrayAction') }}</span>
            </div>
            <div v-for="(value, index) in workingValues" :key="index" class="settings-array-dialog__item" :class="{ 'settings-array-dialog__item--media-root': isMediaRoot }">
              <span class="settings-array-dialog__index">{{ index + 1 }}</span>
              <div v-if="isMediaRoot" class="settings-array-dialog__path-wrap">
                <span class="settings-array-dialog__path" :class="{ 'is-empty': !value }" :title="value" :data-testid="`${testId}-row-value-${index}`">{{ value || t('settingsSelectMediaRoot') }}</span>
                <span v-if="rowErrors[index]" class="settings-array-dialog__error" :data-testid="`${testId}-error-${index}`">{{ rowErrors[index] }}</span>
              </div>
              <label v-else class="settings-array-dialog__input-wrap">
                <span class="sr-only">{{ label }} {{ index + 1 }}</span>
                <input v-model="workingValues[index]" :data-testid="`${testId}-dialog-${index}`" :aria-invalid="Boolean(rowErrors[index])" />
                <span v-if="rowErrors[index]" class="settings-array-dialog__error" :data-testid="`${testId}-error-${index}`">{{ rowErrors[index] }}</span>
              </label>
              <div v-if="isMediaRoot" class="settings-array-dialog__row-actions">
                <button type="button" class="settings-array-dialog__row-edit" :data-testid="`${testId}-row-edit-${index}`" :title="t('settingsArrayEditItem').replace('{index}', String(index + 1))" @click="openPicker(index)">
                  <Pencil aria-hidden="true" />
                </button>
                <button type="button" class="settings-array-dialog__delete" :data-testid="`${testId}-delete-${index}`" :title="t('settingsArrayDelete').replace('{index}', String(index + 1))" @click="remove(index)">
                  <Trash2 aria-hidden="true" />
                </button>
              </div>
              <button v-else type="button" class="settings-array-dialog__delete" :data-testid="`${testId}-delete-${index}`" :title="t('settingsArrayDelete').replace('{index}', String(index + 1))" @click="remove(index)">
                <Trash2 aria-hidden="true" />
              </button>
            </div>
            <div v-if="!workingValues.length" class="settings-array-dialog__empty">{{ t('settingsArrayNotConfigured') }}</div>
          </div>
          <span v-if="listError" class="settings-array-dialog__list-error" :data-testid="`${testId}-list-error`">{{ listError }}</span>
          <div v-if="help || $slots.help" class="settings-array-dialog__help" :data-testid="`${testId}-help`">
            <slot name="help">{{ help }}</slot>
          </div>
        </div>

        <footer class="settings-array-dialog__footer">
          <span>{{ t('settingsArrayDraftOnly') }}</span>
          <div class="settings-array-dialog__actions">
            <button type="button" class="button" :data-testid="`${testId}-cancel`" @click="cancel">{{ t('confirmCancel') }}</button>
            <button type="button" class="button primary" :data-testid="`${testId}-apply`" @click="apply"><Check aria-hidden="true" />{{ t('settingsArrayApplyDraft') }}</button>
          </div>
        </footer>
      </section>
    </div>

    <div v-if="pickerOpen" class="settings-directory-picker__overlay" role="presentation">
      <section
        class="settings-directory-picker"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="`${testId}-picker-title`"
        :data-testid="`${testId}-picker`"
        @keydown.esc.stop.prevent="closePicker"
      >
        <header class="settings-directory-picker__header">
          <h3 :id="`${testId}-picker-title`">{{ t('settingsDirectoryPickerTitle') }}</h3>
          <span>{{ t('settingsDirectoryPickerHint') }}</span>
        </header>
        <div class="settings-directory-picker__selection">
          <span>{{ t('settingsDirectorySelected') }}</span>
          <strong :data-testid="`${testId}-picker-selected`">{{ selectedPath || t('settingsDirectoryNoneSelected') }}</strong>
        </div>
        <div class="settings-directory-picker__body">
          <div class="settings-directory-picker__instructions">{{ t('settingsDirectoryInstructions') }}</div>
          <div class="settings-directory-tree">
            <template v-for="row in treeRows" :key="row.key">
              <div v-if="row.kind === 'directory' && row.directory" class="settings-directory-tree__row" :class="{ 'is-selected': selectedPath === row.directory.path, 'is-disabled': isDuplicatePath(row.directory.path) }" :style="{ '--tree-depth': row.depth }">
                <button
                  v-if="row.directory.has_children"
                  type="button"
                  class="settings-directory-tree__toggle"
                  :data-testid="`${testId}-tree-toggle-${row.directory.path}`"
                  :title="t(expandedPaths.has(row.directory.path) ? 'settingsDirectoryCollapse' : 'settingsDirectoryExpand').replace('{path}', row.directory.path)"
                  @click="toggleDirectory(row.directory)"
                >
                  <ChevronDown v-if="expandedPaths.has(row.directory.path)" aria-hidden="true" />
                  <ChevronRight v-else aria-hidden="true" />
                </button>
                <span v-else class="settings-directory-tree__toggle" aria-hidden="true"></span>
                <FolderOpen v-if="expandedPaths.has(row.directory.path)" aria-hidden="true" />
                <Folder v-else aria-hidden="true" />
                <button type="button" class="settings-directory-tree__name" :data-testid="`${testId}-tree-select-${row.directory.path}`" :disabled="isDuplicatePath(row.directory.path)" @click="selectDirectory(row.directory)">{{ row.directory.name }}</button>
                <span class="settings-directory-tree__badge">{{ isDuplicatePath(row.directory.path) ? t('settingsDirectoryAlreadyAdded') : selectedPath === row.directory.path ? t('settingsDirectorySelected') : '' }}</span>
              </div>
              <div v-else class="settings-directory-tree__status" :style="{ '--tree-depth': row.depth }" :data-testid="row.status === 'error' ? `${testId}-tree-error-${row.parent}` : undefined">
                <span v-if="row.status === 'loading'">{{ t('settingsDirectoryLoading') }}</span>
                <span v-else-if="row.status === 'empty'">{{ t('settingsDirectoryEmpty') }}</span>
                <template v-else>
                  <span>{{ row.message }}</span>
                  <button type="button" :data-testid="`${testId}-tree-retry-${row.parent}`" @click="retryDirectory(row.parent!)">{{ t('settingsDirectoryRetry') }}</button>
                </template>
              </div>
            </template>
          </div>
        </div>
        <footer class="settings-directory-picker__footer">
          <span :class="{ 'is-error': selectedDuplicate }">{{ selectedDuplicate ? t('settingsDirectoryAlreadyAdded') : '' }}</span>
          <div class="settings-directory-picker__actions">
            <button type="button" class="button" :data-testid="`${testId}-picker-cancel`" @click="closePicker">{{ t('confirmCancel') }}</button>
            <button type="button" class="button primary" :data-testid="`${testId}-picker-confirm`" :disabled="pickerConfirmDisabled" @click="confirmPicker">{{ t('confirmConfirm') }}</button>
          </div>
        </footer>
      </section>
    </div>
  </div>
</template>
