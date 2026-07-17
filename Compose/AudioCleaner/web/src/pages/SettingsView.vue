<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { setLanguage, supportedLanguages, t } from '../i18n'
import { api } from '../lib/api'
import type { AudioCleanerConfig, SupportedLanguage } from '../lib/types'

const config = ref<AudioCleanerConfig | null>(null)
const loading = ref(false)
const error = ref('')
const languageDraft = ref<SupportedLanguage>('zh-CN')
const savingLanguage = ref(false)
const languageMessage = ref('')

const notificationsState = computed(() => {
  if (!config.value?.notifications.enabled) {
    return `${t('settingsReserved')}, ${t('settingsInactive')}`
  }
  return `${t('settingsReserved')}, ${t('settingsActive')}`
})

function joinValue(values: unknown[] | undefined): string {
  return values && values.length > 0 ? values.join(', ') : t('settingsEmptyValue')
}

function booleanValue(value: boolean): string {
  return value ? t('settingsEnabled') : t('settingsDisabled')
}

async function loadConfig(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const nextConfig = await api.config()
		config.value = nextConfig
		languageDraft.value = nextConfig.ui.language
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    loading.value = false
  }
}

async function saveLanguage(): Promise<void> {
  savingLanguage.value = true
  languageMessage.value = ''
  error.value = ''
  try {
    const nextUI = await api.patchUI(languageDraft.value)
    if (config.value) {
      config.value.ui = nextUI
    }
    setLanguage(languageDraft.value)
    languageMessage.value = t('settingsLanguageSaved')
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    savingLanguage.value = false
  }
}

onMounted(loadConfig)
</script>

<template>
  <div>
    <div v-if="error" class="alert error">
      <strong>{{ t('settingsErrorLabel') }}</strong>
      <span>{{ error }}</span>
    </div>

    <div v-if="loading" class="panel">
      <div class="panel-body muted">{{ t('settingsLoading') }}</div>
    </div>

    <div v-if="config" class="settings-grid settings-grid--compact settings-grid--masonry">
      <section class="panel settings-panel">
        <div class="panel-body">
          <h2>{{ t('settingsBlockMedia') }}</h2>
          <p class="muted">{{ t('settingsMediaDescription') }}</p>
          <dl class="settings-list">
            <dt>{{ t('settingsMediaRoots') }}</dt>
            <dd>{{ joinValue(config.media.roots) }}</dd>
            <dt>{{ t('settingsMediaExtensions') }}</dt>
            <dd>{{ joinValue(config.media.extensions) }}</dd>
            <dt>{{ t('settingsMediaExcludeDirs') }}</dt>
            <dd>{{ joinValue(config.media.exclude_dirs) }}</dd>
            <dt>{{ t('settingsMediaExcludePatterns') }}</dt>
            <dd>{{ joinValue(config.media.exclude_patterns) }}</dd>
          </dl>
        </div>
      </section>

      <section class="panel settings-panel">
        <div class="panel-body">
          <h2>{{ t('settingsBlockAudio') }}</h2>
          <p class="muted">{{ t('settingsAudioDescription') }}</p>
          <dl class="settings-list">
            <dt>{{ t('settingsAudioVersion') }}</dt>
            <dd>{{ config.audio.version }}</dd>
            <dt>{{ t('settingsIncompatibleCodecs') }}</dt>
            <dd>{{ joinValue(config.audio.incompatible_codecs) }}</dd>
          </dl>
        </div>
      </section>

      <section class="panel settings-panel">
        <div class="panel-body">
          <h2>{{ t('settingsBlockScan') }}</h2>
          <p class="muted">{{ t('settingsScanDescription') }}</p>
          <dl class="settings-list">
            <dt>{{ t('settingsStartupScan') }}</dt>
            <dd>{{ booleanValue(config.scan.startup_scan_enabled) }}</dd>
            <dt>{{ t('settingsWatchdog') }}</dt>
            <dd>{{ booleanValue(config.scan.watchdog_enabled) }}</dd>
          </dl>
        </div>
      </section>

      <section class="panel settings-panel">
        <div class="panel-body">
          <h2>{{ t('settingsBlockPipeline') }}</h2>
          <p class="muted">{{ t('settingsPipelineDescription') }}</p>
          <dl class="settings-list">
            <dt>{{ t('settingsWorkers') }}</dt>
            <dd>{{ config.pipeline.workers }}</dd>
            <dt>{{ t('settingsMaxRetries') }}</dt>
            <dd>{{ config.pipeline.max_retries }}</dd>
            <dt>{{ t('settingsRetryDelay') }}</dt>
            <dd>{{ config.pipeline.retry_delay_seconds }}</dd>
            <dt>{{ t('settingsStatQuiet') }}</dt>
            <dd>{{ config.pipeline.stat_quiet_seconds }}</dd>
            <dt>{{ t('settingsJobTimeout') }}</dt>
            <dd>{{ config.pipeline.job_timeout_minutes }}</dd>
          </dl>
        </div>
      </section>

      <section class="panel settings-panel">
        <div class="panel-body">
          <h2>{{ t('settingsBlockValidation') }}</h2>
          <p class="muted">{{ t('settingsValidationDescription') }}</p>
          <dl class="settings-list">
            <dt>{{ t('settingsMaxSizeRatio') }}</dt>
            <dd>{{ config.validation.max_size_ratio }}</dd>
            <dt>{{ t('settingsMaxSizeIncrease') }}</dt>
            <dd>{{ config.validation.max_size_increase_mb }}</dd>
            <dt>{{ t('settingsDurationTolerance') }}</dt>
            <dd>{{ config.validation.duration_tolerance_seconds }}</dd>
          </dl>
        </div>
      </section>

      <section class="panel settings-panel">
        <div class="panel-body">
          <h2>{{ t('settingsBlockUI') }}</h2>
          <p class="muted">{{ t('settingsUIDescription') }}</p>
          <form class="settings-form" @submit.prevent="saveLanguage">
            <label class="field">
              <span>{{ t('settingsLanguage') }}</span>
              <select name="language" v-model="languageDraft">
                <option v-for="language in supportedLanguages" :key="language" :value="language">{{ language }}</option>
              </select>
            </label>
            <div class="row-actions">
              <button class="button primary" type="submit" :disabled="savingLanguage">{{ t('settingsSaveUI') }}</button>
              <span v-if="languageMessage" class="muted">{{ languageMessage }}</span>
            </div>
          </form>
        </div>
      </section>

      <section class="panel settings-panel">
        <div class="panel-body">
          <h2>{{ t('settingsBlockNotifications') }}</h2>
          <p class="muted">{{ t('settingsNotificationsDescription') }}</p>
          <dl class="settings-list">
            <dt>{{ t('settingsState') }}</dt>
            <dd>{{ notificationsState }}</dd>
            <dt>{{ t('settingsTargets') }}</dt>
            <dd>{{ joinValue(config.notifications.targets) }}</dd>
          </dl>
        </div>
      </section>
    </div>
  </div>
</template>
