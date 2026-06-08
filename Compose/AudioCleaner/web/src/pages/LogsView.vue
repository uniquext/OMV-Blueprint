<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { eventStreamState } from '../composables/useEventStream'
import { eventStreamStatusLabel, jobEventTypeLabel, jobPhaseLabel, t } from '../i18n'
import { api } from '../lib/api'
import type { JobEventRecord } from '../lib/types'

const emptyValue = computed(() => t('logsNoValue'))
const lastEventAt = computed(() => eventStreamState.lastEventAt ?? emptyValue.value)
const lastSnapshotID = computed(() => eventStreamState.lastSnapshotID?.toString() ?? emptyValue.value)
const refreshModeLabel = computed(() =>
  eventStreamState.refreshMode === 'partial' ? t('refreshModePartial') : t('refreshModeFull')
)
const logs = ref<JobEventRecord[]>([])
const loading = ref(false)
const error = ref('')

async function loadLogs(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    logs.value = await api.logs()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
    logs.value = []
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
      <div class="panel-body">
        <h2>{{ t('logsRecentTitle') }}</h2>
      </div>
      <div v-if="loading" class="panel-body muted">{{ t('logsLoading') }}</div>
      <table class="data-table">
        <thead>
          <tr>
            <th>{{ t('logsColumnTime') }}</th>
            <th>{{ t('logsColumnType') }}</th>
            <th>{{ t('logsColumnPhase') }}</th>
            <th>{{ t('logsColumnCommand') }}</th>
            <th>{{ t('logsColumnMessage') }}</th>
            <th>{{ t('logsColumnError') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && logs.length === 0">
            <td colspan="6" class="muted">{{ t('logsNoRecent') }}</td>
          </tr>
          <tr v-for="log in logs" :key="log.id">
            <td>{{ log.finished_at || log.started_at || emptyValue }}</td>
            <td>{{ log.event_type ? jobEventTypeLabel(log.event_type) : emptyValue }}</td>
            <td>{{ log.phase ? jobPhaseLabel(log.phase) : emptyValue }}</td>
            <td class="path-cell">{{ log.command || emptyValue }}</td>
            <td class="path-cell">{{ log.message || emptyValue }}</td>
            <td class="path-cell">{{ log.error || emptyValue }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
