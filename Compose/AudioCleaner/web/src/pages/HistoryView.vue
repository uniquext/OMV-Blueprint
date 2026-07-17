<script setup lang="ts">
import { EyeOff, RotateCcw } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DataPager from '../components/DataPager.vue'
import { eventStreamState } from '../composables/useEventStream'
import { jobStatusLabel, t } from '../i18n'
import { api } from '../lib/api'
import {
  filterHistoryJobs,
  historyJobSummary,
  type HistorySourceFilter,
  jobBasename,
  jobDirectory,
  jobDiscoverySource,
  jobDiscoverySourceDisplay,
  type JobFilter,
  jobFilterKey,
  jobID,
  jobPath,
  jobStatus,
  loadAllJobPages
} from '../lib/jobs'
import { jobStatusTone, semanticBadgeClass } from '../lib/semantic'
import { formatServiceTimestamp, serviceDatePart, serviceDurationMilliseconds } from '../lib/time'
import type { HistoryRecord } from '../lib/types'

const emptyValue = computed(() => t('logsNoValue'))
const allJobs = ref<HistoryRecord[]>([])
const page = ref(1)
const pageSize = ref(20)
const expandedJobIDs = ref<Set<number>>(new Set())
const selectedError = ref<{ path: string; message: string } | null>(null)
const errorDialog = ref<HTMLDialogElement | null>(null)
const historyRoot = ref<HTMLElement | null>(null)
const loading = ref(false)
const operationPending = ref(false)
const error = ref('')
const pendingAction = ref<'retry' | 'ignore' | null>(null)
const selectedJob = ref<HistoryRecord | null>(null)
const resultFilter = ref<JobFilter>('all')
const sourceFilter = ref<HistorySourceFilter>('all')
const dateFrom = ref('')
const dateTo = ref('')
const keyword = ref('')
const openFilter = ref<'result' | 'source' | 'dateFrom' | 'dateTo' | null>(null)
const calendarDraft = ref('')
const calendarMonth = ref('')

const resultFilters: JobFilter[] = ['all', 'processed', 'compatible', 'failed', 'processing']
const sourceFilters: HistorySourceFilter[] = ['all', 'scan', 'watchdog', 'manual']
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
const resultFilterLabel = computed(() => t(`jobFilter${resultFilter.value}`))
const sourceFilterDisplay = computed(() => sourceFilterLabel(sourceFilter.value))
const datePlaceholder = computed(() => t('historyFilterDatePlaceholder'))
const calendarTitle = computed(() => {
  const [year, month] = calendarMonth.value.split('-')
  return year && month ? `${year} 年 ${Number(month)} 月` : ''
})
const calendarDays = computed(() => buildCalendarDays(calendarMonth.value))
const calendarWeekdays = ['日', '一', '二', '三', '四', '五', '六']
const actionDialogOpen = computed(() => pendingAction.value !== null)
const actionDialogTitle = computed(() =>
  pendingAction.value === 'retry' ? t('historyConfirmRetryTitle') : t('historyConfirmIgnoreTitle')
)
const actionDialogMessage = computed(() => {
  const message = pendingAction.value === 'retry' ? t('historyConfirmRetryMessage') : t('historyConfirmIgnoreMessage')
  return `${message} ${selectedJob.value?.path || ''}`
})
const busy = computed(() => loading.value || operationPending.value)
const historyRefreshEvents = new Set(['compatible', 'processed', 'failed', 'job.retry_queued', 'job.ignored'])
let loadScheduled = false

interface CalendarDay {
  date: string
  day: number
  muted: boolean
}

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

function scheduleHistoryLoad(): void {
  if (loadScheduled) return
  loadScheduled = true
  void nextTick(async () => {
    loadScheduled = false
    await loadHistory()
  })
}

function askJobAction(action: 'retry' | 'ignore', job: HistoryRecord): void {
  pendingAction.value = action
  selectedJob.value = job
}

function closeActionDialog(): void {
  pendingAction.value = null
  selectedJob.value = null
}

async function confirmJobAction(): Promise<void> {
  if (operationPending.value || !pendingAction.value || !selectedJob.value) return
  const action = pendingAction.value
  const id = selectedJob.value.id
  closeActionDialog()
  operationPending.value = true
  error.value = ''
  try {
    if (action === 'retry') await api.retryHistoryJob(id)
    else await api.ignoreHistoryJob(id)
    await loadHistory()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    operationPending.value = false
  }
}

function updatePageSize(value: number): void {
  pageSize.value = value
}

function closeFilter(): void {
  openFilter.value = null
}

function toggleMenu(menu: 'result' | 'source'): void {
  openFilter.value = openFilter.value === menu ? null : menu
}

function selectResultFilter(value: JobFilter): void {
  resultFilter.value = value
  closeFilter()
}

function selectSourceFilter(value: HistorySourceFilter): void {
  sourceFilter.value = value
  closeFilter()
}

function displayDate(value: string): string {
  return value ? value.replaceAll('-', '/') : datePlaceholder.value
}

function newestHistoryDate(): string {
  const dates = allJobs.value
    .map((job) => serviceDatePart(job.finished_at || job.started_at))
    .filter(Boolean)
    .sort()
  return dates.at(-1) ?? localToday()
}

function localToday(): string {
  const now = new Date()
  return formatDateParts(now.getFullYear(), now.getMonth() + 1, now.getDate())
}

function formatDateParts(year: number, month: number, day: number): string {
  return `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`
}

function monthKey(value: string): string {
  return value.slice(0, 7)
}

function openDateFilter(field: 'dateFrom' | 'dateTo'): void {
  const value = field === 'dateFrom' ? dateFrom.value : dateTo.value
  calendarDraft.value = value
  calendarMonth.value = monthKey(value || newestHistoryDate())
  openFilter.value = field
}

function selectCalendarDate(value: string): void {
  calendarDraft.value = value
  calendarMonth.value = monthKey(value)
}

function clearCalendarDate(field: 'dateFrom' | 'dateTo'): void {
  calendarDraft.value = ''
  if (field === 'dateFrom') {
    dateFrom.value = ''
  } else {
    dateTo.value = ''
  }
  closeFilter()
}

function confirmCalendarDate(field: 'dateFrom' | 'dateTo'): void {
  if (field === 'dateFrom') {
    dateFrom.value = calendarDraft.value
  } else {
    dateTo.value = calendarDraft.value
  }
  closeFilter()
}

function shiftCalendarMonth(offset: number): void {
  const [year, month] = calendarMonth.value.split('-').map(Number)
  const next = new Date(year, month - 1 + offset, 1)
  calendarMonth.value = monthKey(formatDateParts(next.getFullYear(), next.getMonth() + 1, 1))
}

function buildCalendarDays(month: string): CalendarDay[] {
  const [year, monthNumber] = month.split('-').map(Number)
  if (!year || !monthNumber) {
    return []
  }
  const firstDay = new Date(year, monthNumber - 1, 1).getDay()
  const currentMonthDays = new Date(year, monthNumber, 0).getDate()
  const previousMonthDays = new Date(year, monthNumber - 1, 0).getDate()
  const days: CalendarDay[] = []

  for (let index = 0; index < 42; index += 1) {
    const dayOffset = index - firstDay + 1
    if (dayOffset < 1) {
      const date = new Date(year, monthNumber - 2, previousMonthDays + dayOffset)
      days.push({
        date: formatDateParts(date.getFullYear(), date.getMonth() + 1, date.getDate()),
        day: date.getDate(),
        muted: true
      })
      continue
    }
    if (dayOffset > currentMonthDays) {
      const date = new Date(year, monthNumber, dayOffset - currentMonthDays)
      days.push({
        date: formatDateParts(date.getFullYear(), date.getMonth() + 1, date.getDate()),
        day: date.getDate(),
        muted: true
      })
      continue
    }
    days.push({
      date: formatDateParts(year, monthNumber, dayOffset),
      day: dayOffset,
      muted: false
    })
  }
  return days
}

function closeFilterOnOutside(event: MouseEvent): void {
  const target = event.target
  if (!(target instanceof Node) || historyRoot.value?.contains(target)) {
    return
  }
  closeFilter()
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

function jobTableDate(job: HistoryRecord): string {
  return formatServiceTimestamp(job.finished_at || job.started_at, true) || emptyValue.value
}

function jobStartedDate(job: HistoryRecord): string {
  return formatServiceTimestamp(job.started_at, true) || emptyValue.value
}

function jobFinishedDate(job: HistoryRecord): string {
  return formatServiceTimestamp(job.finished_at, true) || emptyValue.value
}

function jobDurationDisplay(job: HistoryRecord): string {
  if (!job.finished_at) {
    return t('historyDetailDurationPending')
  }
  const duration = serviceDurationMilliseconds(job.started_at, job.finished_at)
  if (duration === null) {
    return emptyValue.value
  }
  const hours = Math.floor(duration / 3_600_000)
  const minutes = Math.floor((duration % 3_600_000) / 60_000)
  const secondsWithMilliseconds = (duration % 60_000) / 1000
  const seconds = secondsWithMilliseconds.toFixed(3).replace(/\.?0+$/, '')
  const parts: string[] = []
  if (hours > 0) {
    parts.push(`${hours} ${t('historyDurationHour')}`)
  }
  if (hours > 0 || minutes > 0) {
    parts.push(`${minutes} ${t('historyDurationMinute')}`)
  }
  parts.push(`${seconds} ${t('historyDurationSecond')}`)
  return parts.join(' ')
}

function openErrorDialog(job: HistoryRecord): void {
  if (!job.final_error) {
    return
  }
  selectedError.value = {
    path: jobPath(job),
    message: job.final_error
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
watch(
  () => eventStreamState.lastEvent,
  (event) => {
    if (event && historyRefreshEvents.has(event.type)) scheduleHistoryLoad()
  }
)

onMounted(() => {
  void loadHistory()
  document.addEventListener('click', closeFilterOnOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', closeFilterOnOutside)
})
</script>

<template>
  <div ref="historyRoot">
    <div class="panel">
      <div class="history-toolbar">
        <div class="history-summary">
          <span>✅ {{ t('historySummaryDone') }}: <strong>{{ summary.done }}</strong></span>
          <span>⏭️ {{ t('historySummarySkipped') }}: <strong>{{ summary.skipped }}</strong></span>
          <span>❌ {{ t('historySummaryFailed') }}: <strong>{{ summary.failed }}</strong></span>
          <span>
            📈 {{ t('historySummarySuccessRate') }}: <strong>{{ successRateDisplay }}</strong>
            <small class="history-summary-note">{{ t('historySummarySkippedExcluded') }}</small>
          </span>
        </div>

        <div class="history-filter-bar">
          <div class="history-filter-menu">
            <button
              class="history-filter-control history-filter-trigger history-filter-result"
              type="button"
              :aria-expanded="openFilter === 'result'"
              :aria-label="t('historyFilterResult')"
              @click="toggleMenu('result')"
            >
              <span>{{ resultFilterLabel }}</span>
            </button>
            <div v-if="openFilter === 'result'" class="history-filter-popover">
              <button
                v-for="item in resultFilters"
                :key="item"
                class="history-filter-option"
                :class="{ 'history-filter-option--selected': resultFilter === item }"
                type="button"
                @click="selectResultFilter(item)"
              >
                <span class="history-filter-check" aria-hidden="true"></span>
                <span>{{ t(`jobFilter${item}`) }}</span>
              </button>
            </div>
          </div>
          <div class="history-filter-menu">
            <button
              class="history-filter-control history-filter-trigger history-filter-source"
              type="button"
              :aria-expanded="openFilter === 'source'"
              :aria-label="t('historyFilterSource')"
              @click="toggleMenu('source')"
            >
              <span>{{ sourceFilterDisplay }}</span>
            </button>
            <div v-if="openFilter === 'source'" class="history-filter-popover">
              <button
                v-for="source in sourceFilters"
                :key="source"
                class="history-filter-option"
                :class="{ 'history-filter-option--selected': sourceFilter === source }"
                type="button"
                @click="selectSourceFilter(source)"
              >
                <span class="history-filter-check" aria-hidden="true"></span>
                <span>{{ sourceFilterLabel(source) }}</span>
              </button>
            </div>
          </div>
          <div class="history-filter-menu history-date-menu">
            <button
              class="history-filter-control history-filter-trigger history-filter-date history-filter-date-from"
              type="button"
              :aria-expanded="openFilter === 'dateFrom'"
              :aria-label="t('historyFilterDateFrom')"
              @click="openDateFilter('dateFrom')"
            >
              <span>{{ displayDate(dateFrom) }}</span>
            </button>
            <div v-if="openFilter === 'dateFrom'" class="history-filter-popover history-calendar-popover">
              <div class="history-calendar-heading">
                <strong>{{ calendarTitle }}</strong>
                <div class="history-calendar-nav">
                  <button type="button" @click="shiftCalendarMonth(-1)">‹</button>
                  <button type="button" @click="shiftCalendarMonth(1)">›</button>
                </div>
              </div>
              <div class="history-calendar-grid history-calendar-weekdays">
                <span v-for="weekday in calendarWeekdays" :key="weekday">{{ weekday }}</span>
              </div>
              <div class="history-calendar-grid">
                <button
                  v-for="day in calendarDays"
                  :key="day.date"
                  class="history-calendar-day"
                  :class="{
                    'history-calendar-day--muted': day.muted,
                    'history-calendar-day--selected': calendarDraft === day.date
                  }"
                  type="button"
                  :data-date="day.date"
                  @click="selectCalendarDate(day.date)"
                >
                  {{ day.day }}
                </button>
              </div>
              <div class="history-calendar-actions">
                <button class="button history-calendar-clear" type="button" @click="clearCalendarDate('dateFrom')">
                  {{ t('historyFilterDateClear') }}
                </button>
                <button class="button primary history-calendar-confirm" type="button" @click="confirmCalendarDate('dateFrom')">
                  {{ t('confirmConfirm') }}
                </button>
              </div>
            </div>
          </div>
          <div class="history-filter-menu history-date-menu">
            <button
              class="history-filter-control history-filter-trigger history-filter-date history-filter-date-to"
              type="button"
              :aria-expanded="openFilter === 'dateTo'"
              :aria-label="t('historyFilterDateTo')"
              @click="openDateFilter('dateTo')"
            >
              <span>{{ displayDate(dateTo) }}</span>
            </button>
            <div v-if="openFilter === 'dateTo'" class="history-filter-popover history-calendar-popover">
              <div class="history-calendar-heading">
                <strong>{{ calendarTitle }}</strong>
                <div class="history-calendar-nav">
                  <button type="button" @click="shiftCalendarMonth(-1)">‹</button>
                  <button type="button" @click="shiftCalendarMonth(1)">›</button>
                </div>
              </div>
              <div class="history-calendar-grid history-calendar-weekdays">
                <span v-for="weekday in calendarWeekdays" :key="weekday">{{ weekday }}</span>
              </div>
              <div class="history-calendar-grid">
                <button
                  v-for="day in calendarDays"
                  :key="day.date"
                  class="history-calendar-day"
                  :class="{
                    'history-calendar-day--muted': day.muted,
                    'history-calendar-day--selected': calendarDraft === day.date
                  }"
                  type="button"
                  :data-date="day.date"
                  @click="selectCalendarDate(day.date)"
                >
                  {{ day.day }}
                </button>
              </div>
              <div class="history-calendar-actions">
                <button class="button history-calendar-clear" type="button" @click="clearCalendarDate('dateTo')">
                  {{ t('historyFilterDateClear') }}
                </button>
                <button class="button primary history-calendar-confirm" type="button" @click="confirmCalendarDate('dateTo')">
                  {{ t('confirmConfirm') }}
                </button>
              </div>
            </div>
          </div>
          <input
            v-model="keyword"
            class="history-filter-control history-filter-keyword"
            type="search"
            :placeholder="t('historySearchPlaceholder')"
            :aria-label="t('historyFilterKeyword')"
          />
        </div>
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
            <th class="history-id-heading">ID</th>
            <th>{{ t('jobsColumnPath') }}</th>
            <th>{{ t('jobsColumnSource') }}</th>
            <th>{{ t('historyColumnResult') }}</th>
            <th>{{ t('tableUpdated') }}</th>
            <th>{{ t('jobsColumnActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && jobs.length === 0">
            <td colspan="7" class="muted">{{ t('jobsNoData') }}</td>
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
              <td class="actions-cell history-actions-cell">
                <div class="row-actions">
                  <button
                    class="button"
                    type="button"
                    :data-testid="`history-retry-${job.id}`"
                    :disabled="busy || job.result !== 'failed'"
                    @click="askJobAction('retry', job)"
                  >
                    <RotateCcw :size="14" aria-hidden="true" />
                    {{ t('historyRetry') }}
                  </button>
                  <button
                    class="button danger"
                    type="button"
                    :data-testid="`history-ignore-${job.id}`"
                    :disabled="busy || job.result !== 'failed'"
                    @click="askJobAction('ignore', job)"
                  >
                    <EyeOff :size="14" aria-hidden="true" />
                    {{ t('historyIgnore') }}
                  </button>
                </div>
              </td>
            </tr>
            <tr v-if="isExpanded(job)" class="history-detail-row">
              <td class="history-detail-cell" colspan="7">
                <div class="history-detail-grid">
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('historyDetailJobID') }}:</span>
                    <span class="history-detail-value">{{ jobID(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('historyDetailStartedAt') }}:</span>
                    <span class="history-detail-value">{{ jobStartedDate(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('jobsColumnSource') }}:</span>
                    <span class="history-detail-value">{{ jobDiscoverySourceLabel(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('historyDetailFinishedAt') }}:</span>
                    <span class="history-detail-value">{{ jobFinishedDate(job) }}</span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('historyColumnResult') }}:</span>
                    <span class="history-detail-value">
                      <span :class="jobStatusBadgeClass(job)">{{ jobStatusDisplay(job) }}</span>
                    </span>
                  </div>
                  <div class="history-detail-field">
                    <span class="history-detail-key">{{ t('historyDetailDuration') }}:</span>
                    <span class="history-detail-value">{{ jobDurationDisplay(job) }}</span>
                  </div>
                  <div class="history-detail-field history-detail-field--wide">
                    <span class="history-detail-key">{{ t('historyDetailPath') }}:</span>
                    <span class="history-detail-value">{{ jobPath(job) }}</span>
                  </div>
                  <div class="history-detail-field history-detail-field--wide">
                    <span class="history-detail-key">{{ t('historyDetailFinalError') }}:</span>
                    <span class="history-detail-value">
                      <button v-if="job.final_error" class="history-error-link" type="button" @click="openErrorDialog(job)">
                        {{ job.final_error }}
                      </button>
                      <span v-else>{{ emptyValue }}</span>
                    </span>
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

    <ConfirmDialog
      :open="actionDialogOpen"
      :title="actionDialogTitle"
      :message="actionDialogMessage"
      :confirm-text="t('confirmConfirm')"
      :cancel-text="t('confirmCancel')"
      @confirm="confirmJobAction"
      @cancel="closeActionDialog"
    />

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
