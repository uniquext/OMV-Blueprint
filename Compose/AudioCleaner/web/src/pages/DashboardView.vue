<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { serviceStatusLabel, t } from '../i18n'
import { api } from '../lib/api'
import { jobFailureCause, jobFailureCauseLabelKey } from '../lib/jobs'
import type { AudioCleanerConfig, ServiceStatus } from '../lib/types'

const status = ref<ServiceStatus | null>(null)
const config = ref<AudioCleanerConfig | null>(null)
const loading = ref(false)
const error = ref('')

const compatibleCount = computed(() => status.value?.counts.compatible ?? 0)
const processedCount = computed(() => status.value?.counts.processed ?? 0)
const failedCount = computed(() => status.value?.counts.failed ?? 0)
const processingCount = computed(() => status.value?.counts.processing ?? status.value?.current_processing.length ?? 0)
const mediaRoots = computed(() => config.value?.media.roots ?? status.value?.media_roots ?? [])
const successRate = computed(() => {
  const rate = status.value?.transcode_success_rate
  if (!rate) {
    return t('dashboardEmptyValue')
  }
  const total = rate.succeeded + rate.failed
  if (total === 0) {
    return t('dashboardEmptyValue')
  }
  return `${Math.round(rate.rate * 100)}% (${rate.succeeded}/${total})`
})

function formatBytes(bytes: number | undefined): string {
  if (!bytes) {
    return '0 B'
  }
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

function failureCauseLabel(job: { failure_cause: string }): string {
  const cause = jobFailureCause(job)
  if (!cause) {
    return '/'
  }
  const key = jobFailureCauseLabelKey(cause)
  return key ? t(key) : cause
}

async function loadDashboard(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const [nextStatus, nextConfig] = await Promise.all([api.status(), api.config()])
    status.value = nextStatus
    config.value = nextConfig
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    loading.value = false
  }
}

onMounted(loadDashboard)
</script>

<template>
  <div>
    <div v-if="error" class="alert error">
      <strong>{{ t('dashboardErrorLabel') }}</strong>
      <span>{{ error }}</span>
    </div>

    <div v-if="loading" class="panel">
      <div class="panel-body muted">{{ t('dashboardLoading') }}</div>
    </div>

    <div v-if="status" class="dashboard-overview">
      <section class="dashboard-metric dashboard-metric--service panel">
        <span class="dashboard-metric__label">{{ t('dashboardServiceStatus') }}</span>
        <strong class="dashboard-metric__value">{{ serviceStatusLabel(status.status) }}</strong>
      </section>
      <section class="dashboard-metric dashboard-metric--compatible panel">
        <span class="dashboard-metric__label">{{ t('dashboardCompatible') }}</span>
        <strong class="dashboard-metric__value">{{ compatibleCount }}</strong>
      </section>
      <section class="dashboard-metric dashboard-metric--processed panel">
        <span class="dashboard-metric__label">{{ t('dashboardProcessed') }}</span>
        <strong class="dashboard-metric__value">{{ processedCount }}</strong>
      </section>
      <section class="dashboard-metric dashboard-metric--failed panel">
        <span class="dashboard-metric__label">{{ t('dashboardFailed') }}</span>
        <strong class="dashboard-metric__value">{{ failedCount }}</strong>
      </section>
      <section class="dashboard-metric dashboard-metric--processing panel">
        <span class="dashboard-metric__label">{{ t('dashboardProcessing') }}</span>
        <strong class="dashboard-metric__value">{{ processingCount }}</strong>
      </section>
      <section class="dashboard-metric dashboard-metric--queue panel">
        <span class="dashboard-metric__label">{{ t('dashboardQueueCount') }}</span>
        <strong class="dashboard-metric__value">{{ status.queue_count }}</strong>
      </section>
      <section class="dashboard-metric dashboard-metric--workers panel">
        <span class="dashboard-metric__label">{{ t('dashboardWorkerCount') }}</span>
        <strong class="dashboard-metric__value">{{ status.workers }}</strong>
      </section>
      <section class="dashboard-metric dashboard-metric--backup panel">
        <span class="dashboard-metric__label">{{ t('dashboardBackupUsage') }}</span>
        <strong class="dashboard-metric__value">{{ formatBytes(status.backup_usage_bytes) }}</strong>
      </section>
      <section class="dashboard-metric dashboard-metric--success panel">
        <span class="dashboard-metric__label">{{ t('dashboardSuccessRate') }}</span>
        <strong class="dashboard-metric__value">{{ successRate }}</strong>
      </section>
    </div>

    <div v-if="status" class="dashboard-sections">
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardMediaRoots') }}</h2>
          <ul class="plain-list">
            <li v-for="root in mediaRoots" :key="root">{{ root }}</li>
            <li v-if="mediaRoots.length === 0" class="muted">{{ t('dashboardEmptyValue') }}</li>
          </ul>
        </div>
      </section>

      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardRecentFailed') }}</h2>
          <table class="data-table">
            <thead>
              <tr>
                <th>{{ t('dashboardPath') }}</th>
                <th>{{ t('jobsColumnReason') }}</th>
                <th>{{ t('dashboardError') }}</th>
                <th>{{ t('tableUpdated') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="status.recent_failed.length === 0">
                <td colspan="4" class="muted">{{ t('dashboardNoFailures') }}</td>
              </tr>
              <tr v-for="job in status.recent_failed" :key="job.id">
                <td class="path-cell" :title="job.path">{{ job.path }}</td>
                <td class="status-cell">{{ failureCauseLabel(job) }}</td>
                <td class="path-cell" :title="job.last_error || t('dashboardEmptyValue')">{{ job.last_error || t('dashboardEmptyValue') }}</td>
                <td class="date-cell">{{ job.updated_at || t('dashboardEmptyValue') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </div>
</template>
