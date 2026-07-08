<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import DataPager from '../components/DataPager.vue'
import { jobStatusLabel, t } from '../i18n'
import { api } from '../lib/api'
import {
  filterHistoryJobs,
  historyJobSummary,
  jobBasename,
  jobDirectory,
  jobDiscoverySource,
  jobDiscoverySourceDisplay,
  jobErrorSummaryLabelKey,
  type JobFilter,
  jobFilterKey,
  jobID,
  jobPath,
  jobStatus,
  loadAllJobPages
} from '../lib/jobs'
import { jobStatusTone, semanticBadgeClass } from '../lib/semantic'
import type { HistoryRecord } from '../lib/types'

const emptyValue = computed(() => t('logsNoValue'))
const allJobs = ref<HistoryRecord[]>([])
const page = ref(1)
const pageSize = ref(20)
const expandedJobIDs = ref<Set<number>>(new Set())
const selectedError = ref<{ path: string; message: string } | null>(null)
const errorDialog = ref<HTMLDialogElement | null>(null)
const loading = ref(false)
const error = ref('')
const resultFilter = ref<JobFilter>('all')
const sourceFilter = ref('all')
const dateFrom = ref('')
const dateTo = ref('')
const keyword = ref('')

const resultFilters: JobFilter[] = ['all', 'processed', 'compatible', 'failed', 'processing', 'restored']
const sourceFilters = computed(() => {
  const sources = new Set(allJobs.value.map((job) => jobDiscoverySource(job)).filter(Boolean))
  return ['all', ...Array.from(sources).sort()]
})
const filteredJobs = computed(() =>
  filterHistoryJobs(allJobs.value, {
    result: resultFilter.value,
    source: sourceFilter.value,
    dateFrom: dateFrom.value,
    dateTo: dateTo.value,
    keyword: keyword.value
  })
)
const jobs = computed(() => filteredJobs.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
const total = computed(() => filteredJobs.value.length)
const summary = computed(() => historyJobSummary(allJobs.value))
const successRateDisplay = computed(() => `${(summary.value.successRate * 100).toFixed(1)}%`)

function pageCountFor(nextTotal: number): number {
  return Math.max(1, Math.ceil(nextTotal / pageSize.value))
}

function clampPage(): void {
  page.value = Math.min(page.value, pageCountFor(total.value))
}

async function loadHistory(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await loadAllJobPages(1000, (requestPage, requestPageSize) =>
      api.historyPage({ page: requestPage, page_size: requestPageSize })
    )
    allJobs.value = result.items
    clampPage()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
    allJobs.value = []
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

function sourceFilterLabel(source: string): string {
  return source === 'all' ? t('historyFilterAllSources') : jobDiscoverySourceDisplay(source, t)
}

function historyDate(value: string, includeSeconds: boolean): string {
  if (!value) {
    return emptyValue.value
  }
  const normalized = value
    .replace('T', ' ')
    .replace(/\.\d+Z?$/, '')
    .replace(/Z$/, '')
  return includeSeconds ? normalized.slice(0, 19) : normalized.slice(0, 16)
}

function jobTableDate(job: HistoryRecord): string {
  return historyDate(job.updated_at, false)
}

function jobDetailDate(job: HistoryRecord): string {
  return historyDate(job.updated_at, true)
}

function jobErrorSummary(job: HistoryRecord): string {
  const labelKey = jobErrorSummaryLabelKey(job.last_error)
  return labelKey ? t(labelKey) : ''
}

function jobErrorDetail(job: HistoryRecord): string {
  const labelKey = jobErrorSummaryLabelKey(job.last_error)
  if (!labelKey) {
    return ''
  }
  return t(`${labelKey}Detail`)
}

function openErrorDialog(job: HistoryRecord): void {
  if (!job.last_error) {
    return
  }
  selectedError.value = {
    path: jobPath(job),
    message: jobErrorDetail(job)
  }
}

function closeErrorDialog(): void {
  selectedError.value = null
}

function showDialog(element: HTMLDialogElement): void {
  if (typeof element.showModal === 'function') {
    element.showModal()
    return
  }
  element.setAttribute('open', '')
}

watch(page, clampPage)
watch(pageSize, () => {
  if (page.value !== 1) {
    page.value = 1
    return
  }
  clampPage()
})
watch([resultFilter, sourceFilter, dateFrom, dateTo, keyword], () => {
  if (page.value !== 1) {
    page.value = 1
    return
  }
  clampPage()
})
watch(
  selectedError,
  (nextError) => {
    const element = errorDialog.value
    if (!nextError || !element || element.open) {
      return
    }
    showDialog(element)
  },
  { flush: 'post' }
)

onMounted(loadHistory)
</script>

<template>
  <div>
    <div class="panel">
      <div class="history-summary">
        <span>✅ {{ t('historySummaryDone') }}: <strong>{{ summary.done }}</strong></span>
        <span>⏭️ {{ t('historySummarySkipped') }}: <strong>{{ summary.skipped }}</strong></span>
        <span>❌ {{ t('historySummaryFailed') }}: <strong>{{ summary.failed }}</strong></span>
        <span>📈 {{ t('historySummarySuccessRate') }}: <strong>{{ successRateDisplay }}</strong></span>
      </div>

      <div class="history-filter-bar">
        <select v-model="resultFilter" class="history-filter-result" :aria-label="t('historyFilterResult')">
          <option v-for="item in resultFilters" :key="item" :value="item">{{ t(`jobFilter${item}`) }}</option>
        </select>
        <select v-model="sourceFilter" class="history-filter-source" :aria-label="t('historyFilterSource')">
          <option v-for="source in sourceFilters" :key="source" :value="source">{{ sourceFilterLabel(source) }}</option>
        </select>
        <input
          v-model="dateFrom"
          class="history-filter-date-from"
          type="date"
          :aria-label="t('historyFilterDateFrom')"
        />
        <input
          v-model="dateTo"
          class="history-filter-date-to"
          type="date"
          :aria-label="t('historyFilterDateTo')"
        />
        <input
          v-model="keyword"
          class="history-filter-keyword"
          type="search"
          :placeholder="t('historySearchPlaceholder')"
          :aria-label="t('historyFilterKeyword')"
        />
      </div>

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
                  {{ isExpanded(job) ? '▼' : '▶' }}
                </button>
              </td>
              <td class="history-id-cell">{{ jobID(job) }}</td>
              <td class="path-cell" :title="jobPath(job)">
                <span class="history-path-main">{{ jobBasename(job) }}</span>
                <span class="history-path-sub">{{ jobDirectory(job) }}</span>
              </td>
              <td class="source-cell">{{ jobDiscoverySourceLabel(job) }}</td>
              <td class="status-cell">
                <span :class="jobStatusBadgeClass(job)">{{ jobStatusDisplay(job) }}</span>
              </td>
              <td class="date-cell">{{ jobTableDate(job) }}</td>
            </tr>
            <tr v-if="isExpanded(job)" class="history-detail-row">
              <td class="history-detail-cell" colspan="6">
                <div class="history-detail-grid">
                  <div class="history-detail-field">
                    <span class="history-detail-key">JobID:</span>
                    <span class="history-detail-value">{{ jobID(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('jobsColumnSource') }}:</span>
                    <span class="history-detail-value">{{ jobDiscoverySourceLabel(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('tableUpdated') }}:</span>
                    <span class="history-detail-value">{{ jobDetailDate(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('historyColumnResult') }}:</span>
                    <span class="history-detail-value">
                      <span :class="jobStatusBadgeClass(job)">{{ jobStatusDisplay(job) }}</span>
                    </span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">Audio signature:</span>
                    <span class="history-detail-value placeholder-text">{{ job.audio_signature || '--' }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">Video signature:</span>
                    <span class="history-detail-value placeholder-text">{{ job.video_signature || '--' }}</span>
                  </div>
                  <div class="history-detail-field history-detail-field--wide">
                    <span class="history-detail-key">Media path:</span>
                    <span class="history-detail-value">{{ jobPath(job) }}</span>
                  </div>
                  <div v-if="job.last_error" class="history-detail-field history-detail-field--wide">
                    <span class="history-detail-key">{{ t('dashboardError') }}:</span>
                    <button class="history-error-link" type="button" @click="openErrorDialog(job)">
                      {{ jobErrorSummary(job) }}
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

    <dialog
      v-if="selectedError"
      ref="errorDialog"
      class="confirm-dialog log-error-dialog"
      @cancel.prevent="closeErrorDialog"
    >
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
