<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { eventStreamState } from '../composables/useEventStream'
import { serviceStatusLabel, t } from '../i18n'
import { api } from '../lib/api'
import type { AudioCleanerConfig, RuntimePhase, RuntimeTasksSnapshot, ServiceStatus } from '../lib/types'

type DashboardTaskTab = 'waiting' | 'active'

const status = ref<ServiceStatus | null>(null)
const runtimeTasks = ref<RuntimeTasksSnapshot | null>(null)
const config = ref<AudioCleanerConfig | null>(null)
const loading = ref(false)
const scanLoading = ref(false)
const statusError = ref('')
const runtimeError = ref('')
const configError = ref('')
const operationError = ref('')
const dashboardTaskTab = ref<DashboardTaskTab>('waiting')
const dashboardTaskPage = ref(1)
const dashboardTaskPageSize = ref(10)

const runtimePollIntervalMs = 1_000
const statusPollIntervalMs = 5_000
const eventRefreshDelayMs = 100
const statusInvalidationEvents = new Set([
  'compatible',
  'processed',
  'failed',
  'job.restored',
  'service.restarting',
  'service.restart_failed'
])

let runtimePollTimer: number | undefined
let statusPollTimer: number | undefined
let runtimeEventTimer: number | undefined
let statusEventTimer: number | undefined

const dashboardTaskPageSizes = [10, 20, 50]
const error = computed(() =>
  [operationError.value, statusError.value, runtimeError.value, configError.value].filter(Boolean).join('; ')
)
const compatibleCount = computed(() => status.value?.counts.compatible ?? 0)
const processedCount = computed(() => status.value?.counts.processed ?? 0)
const failedCount = computed(() => status.value?.counts.failed ?? 0)
const processingCount = computed(() => activeTasks.value.length)
const mediaRoots = computed(() => config.value?.media.roots ?? [])
const waitingTasks = computed(() => runtimeTasks.value?.waiting_tasks ?? [])
const activeTasks = computed(() => runtimeTasks.value?.active_tasks ?? [])
const waitingTaskCount = computed(() => waitingTasks.value.length)
const activeTaskCount = computed(() => activeTasks.value.length)
const selectedTasks = computed(() => (dashboardTaskTab.value === 'waiting' ? waitingTasks.value : activeTasks.value))
const dashboardTaskTotal = computed(() => selectedTasks.value.length)
const dashboardTaskPageCount = computed(() => Math.max(1, Math.ceil(dashboardTaskTotal.value / dashboardTaskPageSize.value)))
const effectiveDashboardTaskPage = computed(() => Math.min(dashboardTaskPage.value, dashboardTaskPageCount.value))
const pagedDashboardTasks = computed(() => {
  const start = (effectiveDashboardTaskPage.value - 1) * dashboardTaskPageSize.value
  return selectedTasks.value.slice(start, start + dashboardTaskPageSize.value)
})
const canGoPreviousDashboardTaskPage = computed(() => effectiveDashboardTaskPage.value > 1)
const canGoNextDashboardTaskPage = computed(() => effectiveDashboardTaskPage.value < dashboardTaskPageCount.value)
const dashboardTaskSummary = computed(
  () =>
    `${t('dashboardTaskTotalPrefix')} ${dashboardTaskTotal.value} ${t('pagerItems')} · ${t('pagerPage')} ${
      effectiveDashboardTaskPage.value
    } / ${dashboardTaskPageCount.value} ${t('dashboardTaskPageUnit')}`
)
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

function selectDashboardTaskTab(tab: DashboardTaskTab): void {
  dashboardTaskTab.value = tab
  dashboardTaskPage.value = 1
}

function updateDashboardTaskPageSize(event: Event): void {
  dashboardTaskPageSize.value = Number((event.target as HTMLSelectElement).value)
  dashboardTaskPage.value = 1
}

function goDashboardTaskPage(page: number): void {
  dashboardTaskPage.value = Math.min(Math.max(1, page), dashboardTaskPageCount.value)
}

function dashboardTaskFileName(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.at(-1) ?? path
}

function dashboardTaskDirectory(path: string): string {
  const separatorIndex = path.lastIndexOf('/')
  if (separatorIndex <= 0) {
    return '/'
  }
  return path.slice(0, separatorIndex)
}

function dashboardTaskPhaseLabel(phase: RuntimePhase): string {
  const keys: Record<string, string> = {
    queued: 'dashboardPhaseQueued',
    retry_wait: 'dashboardPhaseRetryWait',
    checking: 'dashboardPhaseChecking',
    transcoding: 'dashboardPhaseTranscoding',
    verifying: 'dashboardPhaseVerifying',
    backing_up: 'dashboardPhaseBackingUp',
    replacing: 'dashboardPhaseReplacing'
  }
  const key = keys[phase]
  return key ? t(key) : t('dashboardPhaseUnknown')
}

function dashboardTaskPhaseClass(): string {
  return dashboardTaskTab.value === 'waiting' ? 'dashboard-task-chip--waiting' : 'dashboard-task-chip--active'
}

function errorMessage(caught: unknown): string {
  return caught instanceof Error ? caught.message : String(caught)
}

function createRefreshTask<T>(
  request: () => Promise<T>,
  apply: (value: T) => void,
  setError: (message: string) => void
): () => Promise<void> {
  let inFlight: Promise<void> | null = null
  let refreshAgain = false

  const refresh = async (): Promise<void> => {
    if (inFlight) {
      refreshAgain = true
      return inFlight
    }

    inFlight = (async () => {
      try {
        apply(await request())
        setError('')
      } catch (caught) {
        setError(errorMessage(caught))
      }
    })()

    try {
      await inFlight
    } finally {
      inFlight = null
      if (refreshAgain) {
        refreshAgain = false
        void refresh()
      }
    }
  }

  return refresh
}

const refreshStatus = createRefreshTask(
  api.status,
  (nextStatus) => {
    status.value = nextStatus
  },
  (message) => {
    statusError.value = message
  }
)

const refreshRuntimeTasks = createRefreshTask(
  api.runtimeTasks,
  (nextRuntimeTasks) => {
    runtimeTasks.value = nextRuntimeTasks
  },
  (message) => {
    runtimeError.value = message
  }
)

async function refreshConfig(): Promise<void> {
  try {
    config.value = await api.config()
    configError.value = ''
  } catch (caught) {
    configError.value = errorMessage(caught)
  }
}

async function loadDashboard(): Promise<void> {
  loading.value = true
  try {
    await Promise.all([refreshStatus(), refreshRuntimeTasks(), refreshConfig()])
  } finally {
    loading.value = false
  }
}

function scheduleRuntimeRefresh(): void {
  if (runtimeEventTimer !== undefined) {
    window.clearTimeout(runtimeEventTimer)
  }
  runtimeEventTimer = window.setTimeout(() => {
    runtimeEventTimer = undefined
    void refreshRuntimeTasks()
  }, eventRefreshDelayMs)
}

function scheduleStatusRefresh(): void {
  if (statusEventTimer !== undefined) {
    window.clearTimeout(statusEventTimer)
  }
  statusEventTimer = window.setTimeout(() => {
    statusEventTimer = undefined
    void refreshStatus()
  }, eventRefreshDelayMs)
}

function refreshVisibleDashboard(): void {
  if (document.visibilityState === 'hidden') {
    return
  }
  void refreshRuntimeTasks()
  void refreshStatus()
}

async function quickScan(): Promise<void> {
  scanLoading.value = true
  operationError.value = ''
  try {
    await api.scan()
    await Promise.all([refreshStatus(), refreshRuntimeTasks()])
  } catch (caught) {
    operationError.value = errorMessage(caught)
  } finally {
    scanLoading.value = false
  }
}

watch(
  () => eventStreamState.lastEvent,
  (event) => {
    if (!event) {
      return
    }
    scheduleRuntimeRefresh()
    if (statusInvalidationEvents.has(event.type)) {
      scheduleStatusRefresh()
    }
  }
)

watch(
  () => eventStreamState.status,
  (nextStatus, previousStatus) => {
    if (nextStatus === 'connected' && previousStatus !== 'connected') {
      scheduleRuntimeRefresh()
      scheduleStatusRefresh()
    }
  }
)

onMounted(() => {
  void loadDashboard()
  runtimePollTimer = window.setInterval(() => {
    if (document.visibilityState !== 'hidden') {
      void refreshRuntimeTasks()
    }
  }, runtimePollIntervalMs)
  statusPollTimer = window.setInterval(() => {
    if (document.visibilityState !== 'hidden') {
      void refreshStatus()
    }
  }, statusPollIntervalMs)
  document.addEventListener('visibilitychange', refreshVisibleDashboard)
})

onBeforeUnmount(() => {
  if (runtimePollTimer !== undefined) {
    window.clearInterval(runtimePollTimer)
  }
  if (statusPollTimer !== undefined) {
    window.clearInterval(statusPollTimer)
  }
  if (runtimeEventTimer !== undefined) {
    window.clearTimeout(runtimeEventTimer)
  }
  if (statusEventTimer !== undefined) {
    window.clearTimeout(statusEventTimer)
  }
  document.removeEventListener('visibilitychange', refreshVisibleDashboard)
})
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
        <strong class="dashboard-metric__value">{{ waitingTaskCount }}</strong>
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

    <section v-if="runtimeTasks" class="dashboard-task-panel panel" data-testid="dashboard-task-queue">
      <div class="dashboard-task-tabbar">
        <div class="dashboard-task-tabs" role="tablist">
          <button
            type="button"
            class="dashboard-task-tab"
            :class="{ 'dashboard-task-tab--active': dashboardTaskTab === 'waiting' }"
            :aria-selected="dashboardTaskTab === 'waiting'"
            @click="selectDashboardTaskTab('waiting')"
          >
            {{ t('dashboardTaskWaitingTab') }} {{ waitingTaskCount }}
          </button>
          <button
            type="button"
            class="dashboard-task-tab"
            :class="{ 'dashboard-task-tab--active': dashboardTaskTab === 'active' }"
            :aria-selected="dashboardTaskTab === 'active'"
            @click="selectDashboardTaskTab('active')"
          >
            {{ t('dashboardTaskActiveTab') }} {{ activeTaskCount }}
          </button>
        </div>
        <button
          type="button"
          class="button primary"
          data-testid="dashboard-quick-scan"
          :disabled="scanLoading || loading"
          @click="quickScan"
        >
          {{ t('dashboardQuickScan') }}
        </button>
      </div>

      <div class="dashboard-task-list">
        <div v-if="pagedDashboardTasks.length === 0" class="dashboard-task-empty muted">
          {{ t('dashboardTaskEmpty') }}
        </div>
        <div v-for="task in pagedDashboardTasks" :key="`${task.phase}:${task.path}`" class="dashboard-task-row">
          <div class="dashboard-task-path">
            <strong>{{ dashboardTaskFileName(task.path) }}</strong>
            <span>{{ dashboardTaskDirectory(task.path) }}</span>
          </div>
          <span class="dashboard-task-chip" :class="dashboardTaskPhaseClass()">{{ dashboardTaskPhaseLabel(task.phase) }}</span>
        </div>
      </div>

      <div class="dashboard-task-pager">
        <span class="dashboard-task-pager__summary">{{ dashboardTaskSummary }}</span>
        <div class="dashboard-task-pager__controls">
          <label class="dashboard-task-page-size">
            <span>{{ t('dashboardTaskPageSize') }}</span>
            <select :value="dashboardTaskPageSize" aria-label="dashboard task page size" @change="updateDashboardTaskPageSize">
              <option v-for="size in dashboardTaskPageSizes" :key="size" :value="size">{{ size }} {{ t('pagerItems') }}</option>
            </select>
          </label>
          <div class="dashboard-task-pagination" aria-label="dashboard task pagination">
            <button
              type="button"
              class="dashboard-task-page-button"
              :disabled="!canGoPreviousDashboardTaskPage"
              @click="goDashboardTaskPage(effectiveDashboardTaskPage - 1)"
            >
              ‹
            </button>
            <button type="button" class="dashboard-task-page-button dashboard-task-page-button--active" disabled>
              {{ effectiveDashboardTaskPage }}
            </button>
            <button
              type="button"
              class="dashboard-task-page-button"
              :disabled="!canGoNextDashboardTaskPage"
              @click="goDashboardTaskPage(effectiveDashboardTaskPage + 1)"
            >
              ›
            </button>
          </div>
        </div>
      </div>
    </section>

    <div v-if="config" class="dashboard-sections">
      <section class="panel">
        <div class="panel-body">
          <h2>{{ t('dashboardMediaRoots') }}</h2>
          <ul class="plain-list">
            <li v-for="root in mediaRoots" :key="root">{{ root }}</li>
            <li v-if="mediaRoots.length === 0" class="muted">{{ t('dashboardEmptyValue') }}</li>
          </ul>
        </div>
      </section>
    </div>
  </div>
</template>
