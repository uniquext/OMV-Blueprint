<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DataPager from '../components/DataPager.vue'
import { jobStatusLabel, t } from '../i18n'
import { api } from '../lib/api'
import {
  filterJobs,
  jobID,
  jobPath,
  jobReason,
  jobReasonLabelKey,
  jobsRequestPlan,
  jobStatus,
  loadAllJobPages,
  jobSource,
  jobSourceLabelKey,
  jobStatusKey,
  type JobFilter
} from '../lib/jobs'
import type { JobRecord } from '../lib/types'

const jobs = ref<JobRecord[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const filter = ref<JobFilter>('all')
const keyword = ref('')
const loading = ref(false)
const error = ref('')
const confirmAction = ref<'scan' | 'ignore' | null>(null)
const selectedJob = ref<JobRecord | null>(null)
let loadSequence = 0
let loadScheduled = false

const filters: JobFilter[] = ['all', 'processing', 'qualified', 'unqualified', 'ignored', 'restored']
const confirmOpen = computed(() => confirmAction.value !== null)
const confirmTitle = computed(() => (confirmAction.value === 'scan' ? t('jobsConfirmScanTitle') : t('jobsConfirmIgnoreTitle')))
const confirmMessage = computed(() => {
  if (confirmAction.value === 'scan') {
    return t('jobsConfirmScanMessage')
  }
  return `${t('jobsConfirmIgnoreMessage')} ${selectedJob.value ? jobPath(selectedJob.value) : ''}`
})

function pageCountFor(nextTotal: number): number {
  return Math.max(1, Math.ceil(nextTotal / pageSize.value))
}

function clampPage(nextTotal: number): boolean {
  const nextPage = Math.min(page.value, pageCountFor(nextTotal))
  if (nextPage === page.value) {
    return false
  }
  page.value = nextPage
  return true
}

function scheduleLoad(): void {
  if (loadScheduled) {
    return
  }
  loadScheduled = true
  void nextTick(async () => {
    loadScheduled = false
    await loadJobs()
  })
}

function isLatestLoad(sequence: number): boolean {
  return sequence === loadSequence
}

async function loadJobs(): Promise<void> {
  const sequence = ++loadSequence
  loading.value = true
  error.value = ''
  try {
    const plan = jobsRequestPlan(page.value, pageSize.value, filter.value, keyword.value)
    const result = plan.localPagination
      ? await loadAllJobPages(plan.pageSize, (requestPage, requestPageSize) =>
          api.jobsPage({ page: requestPage, page_size: requestPageSize })
        )
      : await api.jobsPage({ page: plan.page, page_size: plan.pageSize })
    if (!isLatestLoad(sequence)) {
      return
    }
    const filtered = filterJobs(result.items, filter.value, keyword.value)

    if (plan.localPagination) {
      total.value = filtered.length
      clampPage(filtered.length)
      jobs.value = filtered.slice((page.value - 1) * pageSize.value, page.value * pageSize.value)
    } else {
      total.value = result.total
      if (clampPage(result.total)) {
        jobs.value = []
        return
      }
      jobs.value = result.items
    }
  } catch (caught) {
    if (!isLatestLoad(sequence)) {
      return
    }
    error.value = caught instanceof Error ? caught.message : String(caught)
    jobs.value = []
    total.value = 0
  } finally {
    if (isLatestLoad(sequence)) {
      loading.value = false
    }
  }
}

async function retryJob(id: number): Promise<void> {
  error.value = ''
  try {
    await api.retry(id)
    await loadJobs()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  }
}

function askScanAll(): void {
  confirmAction.value = 'scan'
}

function askIgnore(job: JobRecord): void {
  selectedJob.value = job
  confirmAction.value = 'ignore'
}

function closeConfirm(): void {
  confirmAction.value = null
  selectedJob.value = null
}

async function confirmOperation(): Promise<void> {
  const action = confirmAction.value
  const job = selectedJob.value
  closeConfirm()
  error.value = ''
  try {
    if (action === 'scan') {
      await api.scan()
    } else if (action === 'ignore' && job) {
      await api.ignore(jobID(job))
    }
    await loadJobs()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  }
}

function updatePageSize(value: number): void {
  pageSize.value = value
}

function jobStatusDisplay(job: JobRecord): string {
  return jobStatusLabel(jobStatusKey(job) ?? jobStatus(job))
}

function jobReasonLabel(job: JobRecord): string {
  const reason = jobReason(job)
  if (!reason) {
    return t('jobsEmptyValue')
  }
  const key = jobReasonLabelKey(reason)
  return key ? t(key) : reason
}

function jobSourceLabel(job: JobRecord): string {
  const source = jobSource(job)
  if (!source) {
    return t('jobsEmptyValue')
  }
  const key = jobSourceLabelKey(source)
  return key ? t(key) : source
}

watch(page, scheduleLoad)
watch(pageSize, () => {
  if (page.value !== 1) {
    page.value = 1
    return
  }
  scheduleLoad()
})
watch([filter, keyword], () => {
  if (page.value !== 1) {
    page.value = 1
    return
  }
  scheduleLoad()
})

onMounted(loadJobs)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">{{ t('pageJobsTitle') }}</h1>
      <div class="actions">
        <button class="button" type="button" :disabled="loading" @click="loadJobs">{{ t('actionRefresh') }}</button>
        <button class="button primary" type="button" :disabled="loading" @click="askScanAll">
          {{ t('jobsScanAll') }}
        </button>
      </div>
    </div>

    <div class="panel">
      <div class="jobs-toolbar">
        <label class="field">
          <span>{{ t('jobsStatusFilter') }}</span>
          <select v-model="filter">
            <option v-for="item in filters" :key="item" :value="item">{{ t(`jobFilter${item}`) }}</option>
          </select>
        </label>
        <label class="field field--wide">
          <span>{{ t('jobsPathSearch') }}</span>
          <input v-model="keyword" type="search" :placeholder="t('jobsPathSearchPlaceholder')" />
        </label>
      </div>

      <div v-if="error" class="alert error">
        <strong>{{ t('jobsErrorLabel') }}</strong>
        <span>{{ error }}</span>
      </div>

      <div v-if="loading" class="panel-body muted">{{ t('jobsLoading') }}</div>

      <table class="data-table">
        <thead>
          <tr>
            <th>{{ t('jobsColumnPath') }}</th>
            <th>{{ t('tableStatus') }}</th>
            <th>{{ t('jobsColumnReason') }}</th>
            <th>{{ t('jobsColumnSource') }}</th>
            <th>{{ t('jobsColumnAttempts') }}</th>
            <th>{{ t('tableUpdated') }}</th>
            <th>{{ t('jobsColumnActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && jobs.length === 0">
            <td colspan="7" class="muted">{{ t('jobsNoData') }}</td>
          </tr>
          <tr v-for="job in jobs" :key="jobID(job)">
            <td class="path-cell">{{ jobPath(job) }}</td>
            <td>{{ jobStatusDisplay(job) }}</td>
            <td>{{ jobReasonLabel(job) }}</td>
            <td>{{ jobSourceLabel(job) }}</td>
            <td>{{ job.attempts }}</td>
            <td>{{ job.updated_at || t('jobsEmptyValue') }}</td>
            <td>
              <div class="row-actions">
                <button class="button" type="button" :disabled="loading" @click="retryJob(jobID(job))">
                  {{ t('jobsRetry') }}
                </button>
                <button class="button" type="button" :disabled="loading" @click="askIgnore(job)">
                  {{ t('jobsIgnore') }}
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>

      <DataPager :page="page" :page-size="pageSize" :total="total" @update:page="page = $event" @update:page-size="updatePageSize" />
    </div>

    <ConfirmDialog
      :open="confirmOpen"
      :title="confirmTitle"
      :message="confirmMessage"
      :confirm-text="t('confirmConfirm')"
      :cancel-text="t('confirmCancel')"
      @confirm="confirmOperation"
      @cancel="closeConfirm"
    />
  </div>
</template>
