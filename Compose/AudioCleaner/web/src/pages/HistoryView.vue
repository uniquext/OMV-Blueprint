<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import DataPager from '../components/DataPager.vue'
import { jobStatusLabel, t } from '../i18n'
import { api } from '../lib/api'
import {
  jobDiscoverySource,
  jobDiscoverySourceDisplay,
  jobFilterKey,
  jobID,
  jobPath,
  jobStatus
} from '../lib/jobs'
import { jobStatusTone, semanticBadgeClass } from '../lib/semantic'
import type { HistoryRecord } from '../lib/types'

const emptyValue = computed(() => t('logsNoValue'))
const jobs = ref<HistoryRecord[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const expandedJobIDs = ref<Set<number>>(new Set())
const selectedError = ref<{ path: string; message: string } | null>(null)
const loading = ref(false)
const error = ref('')

async function loadHistory(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await api.historyPage({ page: page.value, page_size: pageSize.value })
    jobs.value = result.items
    total.value = result.total
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
    jobs.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function updatePageSize(value: number): void {
  pageSize.value = value
}

function isExpanded(job: HistoryRecord): boolean {
  return expandedJobIDs.value.has(jobID(job))
}

function toggleJob(job: HistoryRecord): void {
  const next = new Set(expandedJobIDs.value)
  const id = jobID(job)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  expandedJobIDs.value = next
}

function jobStatusDisplay(job: HistoryRecord): string {
  return jobStatusLabel(jobFilterKey(job) ?? jobStatus(job))
}

function jobStatusBadgeClass(job: HistoryRecord): string {
  return semanticBadgeClass(jobStatusTone(jobFilterKey(job) ?? jobStatus(job)))
}

function jobDiscoverySourceLabel(job: HistoryRecord): string {
  return jobDiscoverySourceDisplay(jobDiscoverySource(job), t)
}

function openErrorDialog(job: HistoryRecord): void {
  if (!job.last_error) {
    return
  }
  selectedError.value = {
    path: jobPath(job),
    message: job.last_error
  }
}

function closeErrorDialog(): void {
  selectedError.value = null
}

watch(page, loadHistory)
watch(pageSize, () => {
  if (page.value !== 1) {
    page.value = 1
    return
  }
  void loadHistory()
})

onMounted(loadHistory)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">{{ t('pageHistoryTitle') }}</h1>
      <div class="actions">
        <button class="button" type="button" :disabled="loading" @click="loadHistory">{{ t('actionRefresh') }}</button>
      </div>
    </div>

    <div class="panel">
      <div v-if="error" class="alert error">
        <strong>{{ t('jobsErrorLabel') }}</strong>
        <span>{{ error }}</span>
      </div>

      <div v-if="loading" class="panel-body muted">{{ t('jobsLoading') }}</div>
      <table class="data-table history-table">
        <thead>
          <tr>
            <th></th>
            <th>ID</th>
            <th>{{ t('jobsColumnPath') }}</th>
            <th>{{ t('jobsColumnSource') }}</th>
            <th>{{ t('historyColumnResult') }}</th>
            <th>{{ t('tableUpdated') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && jobs.length === 0">
            <td colspan="6" class="muted">{{ t('jobsNoData') }}</td>
          </tr>
          <template v-for="job in jobs" :key="jobID(job)">
            <tr>
              <td class="history-expand-cell">
                <button class="button history-expand-button" type="button" @click="toggleJob(job)">
                  {{ isExpanded(job) ? 'v' : '>' }}
                </button>
              </td>
              <td class="history-id-cell">{{ jobID(job) }}</td>
              <td class="path-cell" :title="jobPath(job)">{{ jobPath(job) }}</td>
              <td class="source-cell">{{ jobDiscoverySourceLabel(job) }}</td>
              <td class="status-cell">
                <span :class="jobStatusBadgeClass(job)">{{ jobStatusDisplay(job) }}</span>
              </td>
              <td class="date-cell">{{ job.updated_at || emptyValue }}</td>
            </tr>
            <tr v-if="isExpanded(job)" class="history-detail-row">
              <td colspan="6">
                <div class="history-detail-grid">
                  <div class="history-detail-field">
                    <span class="history-detail-key">JobID</span>
                    <span class="history-detail-value">{{ jobID(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('jobsColumnSource') }}</span>
                    <span class="history-detail-value">{{ jobDiscoverySourceLabel(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('tableUpdated') }}</span>
                    <span class="history-detail-value">{{ job.updated_at || emptyValue }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('tableStatus') }}</span>
                    <span class="history-detail-value">{{ jobStatusDisplay(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">Audio signature</span>
                    <span class="history-detail-value">{{ job.audio_signature || emptyValue }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">Video signature</span>
                    <span class="history-detail-value">{{ job.video_signature || emptyValue }}</span>
                  </div>
                  <div class="history-detail-field history-detail-field--wide">
                    <span class="history-detail-key">Media path</span>
                    <span class="history-detail-value">{{ jobPath(job) }}</span>
                  </div>
                  <div v-if="job.last_error" class="history-detail-field history-detail-field--wide">
                    <span class="history-detail-key">{{ t('dashboardError') }}</span>
                    <button class="history-error-link" type="button" @click="openErrorDialog(job)">
                      {{ job.last_error }}
                    </button>
                  </div>
                </div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <DataPager
        :page="page"
        :page-size="pageSize"
        :total="total"
        @update:page="page = $event"
        @update:page-size="updatePageSize"
      />
    </div>

    <dialog v-if="selectedError" class="confirm-dialog log-error-dialog" open>
      <div class="confirm-dialog__content">
        <h2 class="confirm-dialog__title">{{ t('dashboardError') }}</h2>
        <p class="confirm-dialog__message">{{ selectedError?.path }}</p>
        <pre class="log-error-dialog__message">{{ selectedError?.message }}</pre>
        <div class="confirm-dialog__actions">
          <button class="button" type="button" @click="closeErrorDialog">{{ t('confirmConfirm') }}</button>
        </div>
      </div>
    </dialog>
  </div>
</template>
