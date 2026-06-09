<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { eventStreamState } from '../composables/useEventStream'
import { eventStreamStatusLabel, t } from '../i18n'
import { api } from '../lib/api'

const emptyValue = computed(() => t('logsNoValue'))
const lastEventAt = computed(() => eventStreamState.lastEventAt ?? emptyValue.value)
const lastSnapshotID = computed(() => eventStreamState.lastSnapshotID?.toString() ?? emptyValue.value)
const refreshModeLabel = computed(() =>
  eventStreamState.refreshMode === 'partial' ? t('refreshModePartial') : t('refreshModeFull')
)
const logContent = ref('')
const logLineCount = ref(100)
const loadedLogLines = ref(0)
const hasRuntimeLogs = computed(() => logContent.value.trim().length > 0)
const loading = ref(false)
const error = ref('')

async function loadLogs(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await api.runtimeLogs(logLineCount.value)
    logContent.value = result.content || ''
    loadedLogLines.value = result.lines || 0
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
    logContent.value = ''
    loadedLogLines.value = 0
  } finally {
    loading.value = false
  }
}

onMounted(loadLogs)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">{{ t('pageLogsTitle') }}</h1>
      <div class="actions">
        <button class="button" type="button" :disabled="loading" @click="loadLogs">{{ t('actionRefresh') }}</button>
      </div>
    </div>

    <div v-if="error" class="alert error">
      <strong>{{ t('logsErrorLabel') }}</strong>
      <span>{{ error }}</span>
    </div>

    <div class="panel">
      <div class="panel-body">
        <h2>{{ t('logsEventStreamTitle') }}</h2>
        <table class="data-table">
          <tbody>
            <tr>
              <th scope="row">{{ t('logsStreamStatus') }}</th>
              <td>{{ eventStreamStatusLabel(eventStreamState.status) }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('logsLastEventAt') }}</th>
              <td>{{ lastEventAt }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('logsLastSnapshotID') }}</th>
              <td>{{ lastSnapshotID }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('logsRefreshMode') }}</th>
              <td>{{ refreshModeLabel }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="panel logs-panel">
      <div class="logs-toolbar">
        <h2>{{ t('logsRuntimeTitle') }} <span class="muted">({{ loadedLogLines }} {{ t('logsLines') }})</span></h2>
        <div class="logs-toolbar__actions">
          <label class="field field--inline">
            <span>{{ t('logsLineCount') }}</span>
            <select v-model.number="logLineCount" :disabled="loading" @change="loadLogs">
              <option :value="50">50 {{ t('logsLines') }}</option>
              <option :value="100">100 {{ t('logsLines') }}</option>
              <option :value="200">200 {{ t('logsLines') }}</option>
              <option :value="500">500 {{ t('logsLines') }}</option>
            </select>
          </label>
          <button class="button" type="button" :disabled="loading" @click="loadLogs">{{ t('actionRefresh') }}</button>
        </div>
      </div>
      <div v-if="loading" class="panel-body muted">{{ t('logsLoading') }}</div>
      <pre v-if="hasRuntimeLogs" class="runtime-log-viewer">{{ logContent }}</pre>
      <div v-else-if="!loading" class="runtime-log-viewer runtime-log-viewer--empty">{{ t('logsNoRecent') }}</div>
    </div>
  </div>
</template>
