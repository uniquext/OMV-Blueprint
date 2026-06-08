<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { jobPhaseLabel, serviceStatusLabel, t } from '../i18n'
import { api } from '../lib/api'
import type { AudioCleanerConfig, ServiceStatus } from '../lib/types'

const status = ref<ServiceStatus | null>(null)
const config = ref<AudioCleanerConfig | null>(null)
const loading = ref(false)
const error = ref('')

const qualifiedCount = computed(() => status.value?.counts.qualified ?? 0)
const unqualifiedCount = computed(() => status.value?.counts.unqualified ?? 0)
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
    <div class="page-header">
      <h1 class="page-title">{{ t('pageDashboardTitle') }}</h1>
      <div class="actions">
        <button class="button" type="button" :disabled="loading" @click="loadDashboard">{{ t('actionRefresh') }}</button>
      </div>
    </div>

    <div v-if="error" class="alert error">
      <strong>{{ t('dashboardErrorLabel') }}</strong>
      <span>{{ error }}</span>
    </div>

    <div v-if="loading" class="panel">
      <div class="panel-body muted">{{ t('dashboardLoading') }}</div>
    </div>

    <div v-if="status" class="dashboard-grid">
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardServiceStatus') }}</h2>
          <p class="metric-value">{{ serviceStatusLabel(status.status) }}</p>
        </div>
      </section>
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardQualified') }}</h2>
          <p class="metric-value">{{ qualifiedCount }}</p>
        </div>
      </section>
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardUnqualified') }}</h2>
          <p class="metric-value">{{ unqualifiedCount }}</p>
        </div>
      </section>
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardProcessing') }}</h2>
          <p class="metric-value">{{ processingCount }}</p>
        </div>
      </section>
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardQueueCount') }}</h2>
          <p class="metric-value">{{ status.queue_count }}</p>
        </div>
      </section>
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardWorkerCount') }}</h2>
          <p class="metric-value">{{ status.workers }}</p>
        </div>
      </section>
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardBackupUsage') }}</h2>
          <p class="metric-value">{{ formatBytes(status.backup_usage_bytes) }}</p>
        </div>
      </section>
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardSuccessRate') }}</h2>
          <p class="metric-value">{{ successRate }}</p>
        </div>
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
                <th>{{ t('dashboardPhase') }}</th>
                <th>{{ t('dashboardError') }}</th>
                <th>{{ t('tableUpdated') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="status.recent_failed.length === 0">
                <td colspan="4" class="muted">{{ t('dashboardNoFailures') }}</td>
              </tr>
              <tr v-for="job in status.recent_failed" :key="job.id">
                <td class="path-cell">{{ job.path }}</td>
                <td>{{ job.phase ? jobPhaseLabel(job.phase) : t('dashboardEmptyValue') }}</td>
                <td>{{ job.last_error || t('dashboardEmptyValue') }}</td>
                <td>{{ job.updated_at || t('dashboardEmptyValue') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </div>
</template>
