<script setup lang="ts">
import { computed, getCurrentInstance, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Router } from 'vue-router'
import { ChevronDown, RotateCcw, Save, X } from '@lucide/vue'
import SettingsListEditor from '../components/SettingsListEditor.vue'
import { setLanguage, t } from '../i18n'
import { api } from '../lib/api'
import type {
  AudioCleanerConfig,
  ConfigApplyMode,
  ConfigModuleName,
  SettingsView
} from '../lib/types'

type DraftStatus = 'pristine' | 'dirty' | 'saving' | 'applying' | 'restarting' | 'restart_failed' | 'active' | 'apply_failed' | 'conflict'

class RestartFailedError extends Error {}

interface ConflictSnapshot {
  revision: string
  config: AudioCleanerConfig[ConfigModuleName]
}

const modules: ConfigModuleName[] = ['scan', 'media', 'pipeline', 'audio', 'validation', 'ui']
const pipelineFields = ['workers', 'max_retries', 'retry_delay_seconds', 'stat_quiet_seconds', 'job_timeout_minutes'] as const
const validationFields = ['max_size_ratio', 'max_size_increase_mb', 'duration_tolerance_seconds'] as const
const loadedView = ref<SettingsView | null>(null)
const defaultView = ref<SettingsView | null>(null)
const loaded = ref<AudioCleanerConfig | null>(null)
const drafts = ref<AudioCleanerConfig | null>(null)
const revisions = ref<Record<ConfigModuleName, string> | null>(null)
const statuses = ref<Record<ConfigModuleName, DraftStatus>>({
  media: 'pristine', audio: 'pristine', pipeline: 'pristine', validation: 'pristine', scan: 'pristine', ui: 'pristine'
})
const activeModule = ref<ConfigModuleName>('media')
const loading = ref(true)
const pageError = ref('')
const confirmOpen = ref(false)
const languageOpen = ref(false)
const conflicts = ref<Partial<Record<ConfigModuleName, ConflictSnapshot>>>({})
const instance = getCurrentInstance()
let removeRouteGuard: (() => void) | undefined

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function moduleLabel(module: ConfigModuleName): string {
  return module === 'ui' ? 'UI' : module[0].toUpperCase() + module.slice(1)
}

async function loadConfig(): Promise<void> {
  loading.value = true
  pageError.value = ''
  try {
    const [view, defaults] = await Promise.all([api.config(), api.configDefaults()])
    loadedView.value = view
    defaultView.value = defaults
    loaded.value = clone(view.modules)
    drafts.value = clone(view.modules)
    revisions.value = Object.fromEntries(modules.map((module) => [module, view.metadata[module].revision])) as Record<ConfigModuleName, string>
  } catch (caught) {
    pageError.value = errorMessage(caught)
  } finally {
    loading.value = false
  }
}

function isDirty(module: ConfigModuleName): boolean {
  return Boolean(loaded.value && drafts.value && JSON.stringify(loaded.value[module]) !== JSON.stringify(drafts.value[module]))
}

function moduleStatus(module: ConfigModuleName): DraftStatus {
  const current = statuses.value[module]
  if (['saving', 'applying', 'restarting', 'restart_failed', 'apply_failed', 'conflict'].includes(current)) {
    return current
  }
  if (current === 'active' && !isDirty(module)) return 'active'
  return isDirty(module) ? 'dirty' : 'pristine'
}

function activateModule(module: ConfigModuleName, focus = false): void {
  activeModule.value = module
  languageOpen.value = false
  if (focus) {
    requestAnimationFrame(() => document.querySelector<HTMLButtonElement>(`[data-module-tab="${module}"]`)?.focus())
  }
}

function handleTabKey(event: KeyboardEvent, index: number): void {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') {
    return
  }
  event.preventDefault()
  const offset = event.key === 'ArrowRight' ? 1 : -1
  activateModule(modules[(index + offset + modules.length) % modules.length], true)
}

function validationErrors(module: ConfigModuleName): Record<string, string> {
  const config = drafts.value!
  const errors: Record<string, string> = {}
  if (module === 'media') {
    if (!config.media.roots.length) errors.roots = t('settingsRequiredList')
    config.media.roots.forEach((value, index) => {
      if (!value.trim() || !(value === '/media' || value.startsWith('/media/'))) errors[`roots.${index}`] = t('settingsMediaRootError')
    })
    if (!config.media.extensions.length) errors.extensions = t('settingsRequiredList')
    config.media.extensions.forEach((value, index) => {
      if (!value.trim().startsWith('.')) errors[`extensions.${index}`] = t('settingsExtensionError')
    })
    config.media.exclude_dirs.forEach((value, index) => {
      if (!value.trim() || /[\\/]/.test(value)) errors[`exclude_dirs.${index}`] = t('settingsExcludeDirError')
    })
    config.media.exclude_patterns.forEach((value, index) => {
      const opens = (value.match(/\[/g) ?? []).length
      const closes = (value.match(/\]/g) ?? []).length
      if (!value.trim() || opens !== closes) errors[`exclude_patterns.${index}`] = t('settingsPatternError')
    })
  }
  if (module === 'audio') {
    if (!config.audio.incompatible_codecs.length) errors.incompatible_codecs = t('settingsRequiredList')
    config.audio.incompatible_codecs.forEach((value, index) => {
      if (!value.trim()) errors[`incompatible_codecs.${index}`] = t('settingsRequiredValue')
    })
  }
  if (module === 'pipeline') {
    validateRange(errors, 'workers', config.pipeline.workers, 1, 64)
    validateRange(errors, 'max_retries', config.pipeline.max_retries, 0, 20)
    validateRange(errors, 'retry_delay_seconds', config.pipeline.retry_delay_seconds, 0, 86400)
    validateRange(errors, 'stat_quiet_seconds', config.pipeline.stat_quiet_seconds, 1, 3600)
    validateRange(errors, 'job_timeout_minutes', config.pipeline.job_timeout_minutes, 1, 10080)
  }
  if (module === 'validation') {
    validateRange(errors, 'max_size_ratio', config.validation.max_size_ratio, 1, 10)
    validateRange(errors, 'max_size_increase_mb', config.validation.max_size_increase_mb, 0, 1048576)
    validateRange(errors, 'duration_tolerance_seconds', config.validation.duration_tolerance_seconds, 0, 3600)
  }
	if (module === 'scan') {
		validateRange(errors, 'reconciliation_interval_minutes', config.scan.reconciliation_interval_minutes, 1, 10080)
	}
  return errors
}

function validateRange(errors: Record<string, string>, field: string, value: number, minimum: number, maximum: number): void {
  if (!Number.isFinite(value) || value < minimum || value > maximum) {
    errors[field] = t('settingsRangeError').replace('{min}', String(minimum)).replace('{max}', String(maximum))
  }
}

const activeErrors = computed(() => validationErrors(activeModule.value))
const canSave = computed(() => isDirty(activeModule.value) && Object.keys(activeErrors.value).length === 0 && !['saving', 'applying', 'restarting'].includes(statuses.value[activeModule.value]))
const activeApplyMode = computed<ConfigApplyMode>(() => loadedView.value!.metadata[activeModule.value].apply_mode)
const changedFields = computed(() => {
  const before = loaded.value![activeModule.value] as unknown as Record<string, unknown>
  const after = drafts.value![activeModule.value] as unknown as Record<string, unknown>
  return Object.keys(after).filter((field) => JSON.stringify(before[field]) !== JSON.stringify(after[field]))
})
const statusText = computed(() => {
  const status = moduleStatus(activeModule.value)
  const key: Record<DraftStatus, string> = {
    pristine: 'settingsStatusPristine', dirty: 'settingsStatusDirty', saving: 'settingsStatusSaving', applying: 'settingsStatusApplying',
    restarting: 'settingsStatusRestarting', restart_failed: 'settingsStatusRestartFailed', active: 'settingsStatusActive', apply_failed: 'settingsStatusApplyFailed', conflict: 'settingsStatusConflict'
  }
  return t(key[status])
})
const saveText = computed(() => activeApplyMode.value === 'service_restart' ? t('settingsSaveRestart') : activeApplyMode.value === 'module_reload' ? t('settingsSaveReload') : t('settingsSaveApply'))

function requestSave(): void {
  if (canSave.value) confirmOpen.value = true
}

async function confirmSave(): Promise<void> {
  const module = activeModule.value
  const currentDrafts = drafts.value!
  const currentLoaded = loaded.value!
  const currentRevisions = revisions.value!
  confirmOpen.value = false
  statuses.value[module] = 'saving'
  try {
    const result = await api.patchConfig(module, currentRevisions[module], clone(currentDrafts[module]))
    statuses.value[module] = result.status === 'restarting' ? 'restarting' : 'applying'
    if (result.status === 'restarting') {
      const refreshed = await waitForRestart(result.revision)
      loadedView.value = refreshed
      assignModule(currentLoaded, module, refreshed.modules[module])
      assignModule(currentDrafts, module, refreshed.modules[module])
      revisions.value = Object.fromEntries(modules.map((name) => [name, refreshed.metadata[name].revision])) as Record<ConfigModuleName, string>
    } else {
      assignModule(currentLoaded, module, result.config)
      assignModule(currentDrafts, module, result.config)
      currentRevisions[module] = result.revision
    }
    if (module === 'ui') setLanguage(currentDrafts.ui.language)
    statuses.value[module] = 'active'
  } catch (caught) {
    const conflict = errorCode(caught) === 'config_changed' ? conflictSnapshot(caught) : undefined
    if (conflict) conflicts.value[module] = conflict
    statuses.value[module] = caught instanceof RestartFailedError ? 'restart_failed' : conflict ? 'conflict' : 'apply_failed'
    pageError.value = errorMessage(caught)
  }
}

function assignModule(target: AudioCleanerConfig, module: ConfigModuleName, value: AudioCleanerConfig[ConfigModuleName]): void {
  ;(target as unknown as Record<ConfigModuleName, unknown>)[module] = clone(value)
}

async function waitForRestart(expectedRevision: string): Promise<SettingsView> {
  let consecutiveRunning = 0
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      const status = await api.status()
      if (status.status === 'restart_failed') throw new RestartFailedError(t('settingsStatusRestartFailed'))
      if (status.status === 'running') {
        consecutiveRunning += 1
        if (consecutiveRunning >= 2) {
          const refreshed = await api.config()
          if (refreshed.metadata.scan.revision === expectedRevision) return refreshed
        }
      } else {
        consecutiveRunning = 0
      }
    } catch (caught) {
      if (caught instanceof RestartFailedError) throw caught
    }
    await new Promise((resolve) => window.setTimeout(resolve, 250))
  }
  throw new Error(t('settingsRestartTimeout'))
}

function discard(): void {
  assignModule(drafts.value!, activeModule.value, loaded.value![activeModule.value])
  statuses.value[activeModule.value] = 'pristine'
  pageError.value = ''
}

function loadConflictBaseline(): void {
  const module = activeModule.value
  const conflict = conflicts.value[module]!
  assignModule(loaded.value!, module, conflict.config)
  revisions.value![module] = conflict.revision
  delete conflicts.value[module]
  statuses.value[module] = 'dirty'
  pageError.value = ''
}

function restoreDefault(): void {
  if (activeModule.value === 'audio') {
    drafts.value!.audio.incompatible_codecs = clone(defaultView.value!.modules.audio.incompatible_codecs)
    statuses.value.audio = 'dirty'
    return
  }
  assignModule(drafts.value!, activeModule.value, defaultView.value!.modules[activeModule.value])
  statuses.value[activeModule.value] = 'dirty'
}

function selectLanguage(language: 'zh-CN' | 'en-US'): void {
  if (drafts.value) drafts.value.ui.language = language
  languageOpen.value = false
}

function errorMessage(caught: unknown): string {
  return caught instanceof Error ? caught.message : String(caught)
}

function errorCode(caught: unknown): string | undefined {
  return typeof caught === 'object' && caught !== null && 'errorCode' in caught ? String(caught.errorCode) : undefined
}

function conflictSnapshot(caught: unknown): ConflictSnapshot | undefined {
  if (typeof caught !== 'object' || caught === null || !('data' in caught)) return undefined
  const data = caught.data
  if (typeof data !== 'object' || data === null || !('revision' in data) || !('config' in data)) return undefined
  return { revision: String(data.revision), config: clone(data.config as AudioCleanerConfig[ConfigModuleName]) }
}

function beforeUnload(event: BeforeUnloadEvent): void {
  if (modules.some(isDirty)) event.preventDefault()
}

function outsideClick(event: MouseEvent): void {
  if (!(event.target as Element).closest?.('.settings-language')) languageOpen.value = false
}

onMounted(() => {
  void loadConfig()
  window.addEventListener('beforeunload', beforeUnload)
  document.addEventListener('click', outsideClick)
  const router = instance?.appContext.config.globalProperties.$router as Router | undefined
  if (router) {
    removeRouteGuard = router.beforeEach((_to, from) => {
      if (from.name === 'Settings' && modules.some(isDirty) && !window.confirm(t('settingsLeaveWarning'))) return false
      return true
    })
  }
})
onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', beforeUnload)
  document.removeEventListener('click', outsideClick)
  removeRouteGuard?.()
})
</script>

<template>
  <div class="settings-page">
    <div v-if="pageError" class="alert error"><strong>{{ t('settingsErrorLabel') }}</strong><span>{{ pageError }}</span></div>
    <div v-if="loading" class="panel"><div class="panel-body muted">{{ t('settingsLoading') }}</div></div>

    <template v-if="drafts && loadedView">
      <div class="settings-tabs" role="tablist" :aria-label="t('pageSettingsTitle')">
        <button
          v-for="(module, index) in modules"
          :id="`settings-tab-${module}`"
          :key="module"
          type="button"
          role="tab"
          :aria-selected="activeModule === module"
          :aria-controls="`settings-panel-${module}`"
          :tabindex="activeModule === module ? 0 : -1"
          :data-module-tab="module"
          :data-dirty="isDirty(module)"
          :title="isDirty(module) ? t('settingsUnsavedTooltip') : module"
          @click="activateModule(module)"
          @keydown="handleTabKey($event, index)"
        >{{ moduleLabel(module) }}</button>
      </div>

      <section :id="`settings-panel-${activeModule}`" class="panel settings-module" role="tabpanel" :aria-labelledby="`settings-tab-${activeModule}`">
        <div class="panel-body">
          <template v-if="activeModule === 'media'">
            <h2>Media</h2><p class="muted">{{ t('settingsMediaDescription') }}</p>
            <div class="settings-array-list">
              <SettingsListEditor :label="t('settingsMediaRoots')" :hint="t('settingsMediaRootsHint')" test-id="media-roots" rule="media-root" :values="drafts.media.roots" :directory-loader="api.mediaDirectories" @update:values="drafts.media.roots = $event" />
              <SettingsListEditor :label="t('settingsMediaExtensions')" :hint="t('settingsMediaExtensionsHint')" test-id="media-extensions" rule="extension" :values="drafts.media.extensions" @update:values="drafts.media.extensions = $event" />
              <SettingsListEditor :label="t('settingsMediaExcludeDirs')" :hint="t('settingsMediaExcludeDirsHint')" test-id="media-exclude_dirs" rule="exclude-dir" :values="drafts.media.exclude_dirs" @update:values="drafts.media.exclude_dirs = $event" />
              <SettingsListEditor :label="t('settingsMediaExcludePatterns')" :hint="t('settingsMediaExcludePatternsHint')" :help="t('settingsMediaExcludePatternsHelp')" test-id="media-exclude_patterns" rule="exclude-pattern" :values="drafts.media.exclude_patterns" @update:values="drafts.media.exclude_patterns = $event">
                <template #help>
                  <div class="settings-pattern-help">
                    <strong class="settings-pattern-help__summary">{{ t('settingsPatternHelpSummary') }}</strong>
                    <div class="settings-pattern-help__tokens">
                      <span class="settings-pattern-help__token"><code>*</code>{{ t('settingsPatternAnyChars') }}</span>
                      <span class="settings-pattern-help__token"><code>?</code>{{ t('settingsPatternSingleChar') }}</span>
                      <span class="settings-pattern-help__token"><code>[abc]</code>{{ t('settingsPatternListedChar') }}</span>
                      <span class="settings-pattern-help__token"><code>[0-9]</code>{{ t('settingsPatternSingleDigit') }}</span>
                    </div>
                    <div class="settings-pattern-help__examples">
                      <span class="settings-pattern-help__example"><code>*.sample.*</code><b>→</b><span>movie.sample.mkv</span></span>
                      <span class="settings-pattern-help__example"><code>*-trailer.*</code><b>→</b><span>movie-trailer.mkv</span></span>
                      <span class="settings-pattern-help__example"><code>Trailers/*</code><b>→</b><span>Trailers/demo.mkv</span></span>
                    </div>
                    <div class="settings-pattern-help__note">{{ t('settingsPatternDirectoryNote') }}</div>
                  </div>
                </template>
              </SettingsListEditor>
            </div>
          </template>

          <template v-else-if="activeModule === 'audio'">
            <h2>Audio</h2><p class="muted">{{ t('settingsAudioDescription') }}</p>
            <div class="settings-array-list">
              <div class="settings-array-summary settings-array-summary--readonly" data-testid="audio-version-summary-row">
                <span class="settings-array-summary__label">{{ t('settingsAudioVersion') }}</span>
                <strong class="settings-array-summary__value">v{{ drafts.audio.version }}</strong>
                <span class="settings-array-summary__count"></span>
                <span class="settings-array-summary__readonly">{{ t('settingsReadonly') }}</span>
              </div>
              <SettingsListEditor :label="t('settingsIncompatibleCodecs')" :hint="t('settingsIncompatibleCodecsHint')" test-id="audio-incompatible_codecs" rule="codec" :values="drafts.audio.incompatible_codecs" @update:values="drafts.audio.incompatible_codecs = $event" />
            </div>
          </template>

          <template v-else-if="activeModule === 'pipeline'">
            <h2>Pipeline</h2><p class="muted">{{ t('settingsPipelineDescription') }}</p>
            <div class="settings-form-grid">
              <label v-for="field in pipelineFields" :key="field" class="field settings-field--inline">
                <span>{{ t(`settingsField_${field}`) }}</span>
                <span class="unit-input"><input v-model.number="drafts.pipeline[field]" type="number" :data-testid="`pipeline-${field}`" /><i>{{ t(`settingsUnit_${field}`) }}</i></span>
                <span v-if="activeErrors[field]" :data-testid="`field-error-${field}`" class="field-error">{{ activeErrors[field] }}</span>
              </label>
            </div>
          </template>

          <template v-else-if="activeModule === 'validation'">
            <h2>Validation</h2><p class="muted">{{ t('settingsValidationDescription') }}</p>
            <div class="settings-form-grid">
              <label v-for="field in validationFields" :key="field" class="field settings-field--inline">
                <span>{{ t(`settingsField_${field}`) }}</span>
                <span class="unit-input"><input v-model.number="drafts.validation[field]" type="number" :step="field === 'max_size_ratio' ? 0.1 : 1" :data-testid="`validation-${field}`" /><i>{{ t(`settingsUnit_${field}`) }}</i></span>
                <span v-if="activeErrors[field]" class="field-error">{{ activeErrors[field] }}</span>
              </label>
            </div>
          </template>

          <template v-else-if="activeModule === 'scan'">
            <h2>{{ t('settingsScanTitle') }}</h2><p class="muted">{{ t('settingsScanDescription') }}</p>
            <div class="settings-scan-rows">
              <div class="settings-scan-row">
                <div><strong>{{ t('settingsStartupScan') }}</strong><small>{{ t('settingsStartupScanHint') }}</small></div>
                <label class="settings-toggle-control"><span>{{ drafts.scan.startup_scan_enabled ? t('settingsEnabled') : t('settingsDisabled') }}</span><input v-model="drafts.scan.startup_scan_enabled" data-testid="scan-startup_scan_enabled" type="checkbox" /><i aria-hidden="true"></i></label>
              </div>
              <div class="settings-scan-row">
                <div><strong>{{ t('settingsWatchdog') }}</strong><small>{{ t('settingsWatchdogHint') }}</small></div>
                <label class="settings-toggle-control"><span>{{ drafts.scan.watchdog_enabled ? t('settingsEnabled') : t('settingsDisabled') }}</span><input v-model="drafts.scan.watchdog_enabled" data-testid="scan-watchdog_enabled" type="checkbox" /><i aria-hidden="true"></i></label>
              </div>
              <label class="settings-scan-row">
                <div><strong>{{ t('settingsReconciliation') }}</strong><small>{{ t('settingsReconciliationHint') }}</small></div>
                <div class="settings-scan-number"><span class="unit-input"><input v-model.number="drafts.scan.reconciliation_interval_minutes" data-testid="scan-reconciliation_interval_minutes" type="number" min="1" max="10080" /><i>{{ t('settingsMinutes') }}</i></span><span v-if="activeErrors.reconciliation_interval_minutes" class="field-error">{{ activeErrors.reconciliation_interval_minutes }}</span></div>
              </label>
              <div class="settings-scan-row">
                <div><strong>{{ t('settingsUniquePathRule') }}</strong><small>{{ t('settingsUniquePathRuleHint') }}</small></div><span class="settings-enforced">{{ t('settingsAlwaysEnabled') }}</span>
              </div>
              <div class="settings-scan-row">
                <div><strong>{{ t('settingsWatcherRecovery') }}</strong><small>{{ t('settingsWatcherRecoveryHint') }}</small></div><span class="settings-enforced">{{ t('settingsAutomatic') }}</span>
              </div>
            </div>
          </template>

          <template v-else>
            <h2>UI</h2><p class="muted">{{ t('settingsUIDescription') }}</p>
            <div class="field settings-field--inline settings-language">
              <span id="settings-language-label">{{ t('settingsLanguage') }}</span>
              <button type="button" class="settings-language__trigger" data-testid="language-trigger" aria-haspopup="listbox" :aria-expanded="languageOpen" aria-controls="settings-language-options" @click.stop="languageOpen = !languageOpen">
                <span>{{ drafts.ui.language === 'zh-CN' ? '简体中文' : 'English' }}</span><ChevronDown aria-hidden="true" />
              </button>
              <div v-show="languageOpen" id="settings-language-options" class="settings-language__options" data-testid="language-options" role="listbox" aria-labelledby="settings-language-label">
                <button type="button" role="option" data-language="zh-CN" :aria-selected="drafts.ui.language === 'zh-CN'" @click="selectLanguage('zh-CN')">简体中文</button>
                <button type="button" role="option" data-language="en-US" :aria-selected="drafts.ui.language === 'en-US'" @click="selectLanguage('en-US')">English</button>
              </div>
            </div>
          </template>
        </div>
      </section>

      <div class="settings-actionbar">
        <div class="settings-actionbar__status" data-testid="settings-status" :data-status="moduleStatus(activeModule)"><strong>{{ moduleLabel(activeModule) }}</strong><span>{{ statusText }}</span></div>
        <div class="settings-actionbar__actions">
          <button v-if="moduleStatus(activeModule) === 'conflict' && conflicts[activeModule]" type="button" class="button" data-testid="settings-load-conflict" @click="loadConflictBaseline">{{ t('settingsLoadServerBaseline') }}</button>
          <button type="button" class="button" data-testid="settings-discard" :disabled="!isDirty(activeModule)" @click="discard"><X aria-hidden="true" />{{ t('settingsDiscard') }}</button>
          <button type="button" class="button" @click="restoreDefault"><RotateCcw aria-hidden="true" />{{ t('settingsRestoreDefault') }}</button>
          <button type="button" class="button primary" data-testid="settings-save" :disabled="!canSave" @click="requestSave"><Save aria-hidden="true" />{{ saveText }}</button>
        </div>
      </div>
    </template>

    <div v-if="confirmOpen" class="settings-confirm-overlay" role="presentation">
      <section class="settings-confirm" data-testid="settings-confirm" role="dialog" aria-modal="true" :aria-label="t('settingsConfirmTitle')">
        <h2>{{ t('settingsConfirmTitle') }} · {{ moduleLabel(activeModule) }}</h2>
        <p>{{ t('settingsConfirmSummary').replace('{count}', String(changedFields.length)) }}</p>
        <ul><li v-for="field in changedFields" :key="field"><code>{{ field }}</code></li></ul>
        <div class="settings-confirm__mode"><span>{{ t('settingsApplyMode') }}</span><code>{{ activeApplyMode }}</code></div>
        <p v-if="activeApplyMode === 'service_restart'" class="settings-confirm__warning">{{ t('settingsRestartWarning') }}</p>
        <div class="settings-confirm__actions"><button type="button" class="button" @click="confirmOpen = false">{{ t('confirmCancel') }}</button><button type="button" class="button primary" data-testid="settings-confirm-submit" @click="confirmSave">{{ saveText }}</button></div>
      </section>
    </div>
  </div>
</template>
