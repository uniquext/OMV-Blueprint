<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { CircleCheck, CircleStop, History, Radar, RadioTower, RefreshCw, ScanSearch } from '@lucide/vue'
import { eventStreamState } from '../composables/useEventStream'
import { serviceStatusLabel, t } from '../i18n'
import { api } from '../lib/api'
import { taskProgressDetails } from '../lib/healthObservability'
import { recoveryStatusKey, scanIsActive, scanProgressPercent, scanSourceKey, scanStatusKey, watcherStatusKey } from '../lib/scanDiscovery'
import type {
  AudioCleanerConfig,
  DiscoveryStatus,
  RuntimePhase,
  RuntimeTasksSnapshot,
  ScanSession,
  ServiceStatus
} from '../lib/types'

type DashboardTaskTab = 'waiting' | 'active'

const status = ref<ServiceStatus | null>(null)
const runtimeTasks = ref<RuntimeTasksSnapshot | null>(null)
const scan = ref<ScanSession | null>(null)
const discovery = ref<DiscoveryStatus | null>(null)
const config = ref<AudioCleanerConfig | null>(null)
const loading = ref(false)
const scanLoading = ref(false)
const cancelLoading = ref(false)
const cancelDialogOpen = ref(false)
const statusError = ref('')
const runtimeError = ref('')
const scanError = ref('')
const discoveryError = ref('')
const configError = ref('')
const operationError = ref('')
const toastMessage = ref('')
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
  'file.backup_created',
  'file.backup_restored',
  'file.backup_deleted',
  'job.retry_queued',
  'job.ignored',
  'service.restarting',
  'service.restart_failed'
])

let runtimePollTimer: number | undefined
let statusPollTimer: number | undefined
let runtimeEventTimer: number | undefined
let statusEventTimer: number | undefined
let scanEventTimer: number | undefined
let toastTimer: number | undefined

const dashboardTaskPageSizes = [10, 20, 50]
const error = computed(() =>
  [operationError.value, statusError.value, runtimeError.value, scanError.value, discoveryError.value, configError.value]
    .filter(Boolean)
    .join('; ')
)
const compatibleCount = computed(() => status.value?.counts.compatible ?? 0)
const processedCount = computed(() => status.value?.counts.processed ?? 0)
const failedCount = computed(() => status.value?.counts.failed ?? 0)
const mediaRoots = computed(() => config.value?.media.roots ?? [])
const waitingTasks = computed(() => runtimeTasks.value?.waiting_tasks ?? [])
const activeTasks = computed(() => runtimeTasks.value?.active_tasks ?? [])
const processingCount = computed(() => activeTasks.value.length)
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
  if (!rate) return t('dashboardEmptyValue')
  const total = rate.succeeded + rate.failed
  if (total === 0) return t('dashboardEmptyValue')
  return `${Math.round(rate.rate * 100)}% (${rate.succeeded}/${total})`
})
const scanActive = computed(() => scanIsActive(scan.value?.status))
const scanProgressLabel = computed(() => {
  if (!scan.value || scan.value.status === 'idle') return t('dashboardEmptyValue')
  if (scan.value.estimating && scan.value.progress_percent === undefined) return t('dashboardScanEstimating')
  return `${Math.round(scan.value.progress_percent ?? 0)}%`
})
const scanProgressWidth = computed(() => `${scanProgressPercent(scan.value)}%`)
const scanRateLabel = computed(() => {
  if (scan.value?.status === 'cancelling') return t('dashboardScanStopping')
  if (!scan.value || scan.value.status === 'idle') return t('dashboardEmptyValue')
  return `${formatNumber(Math.round(scan.value.rate_per_second))} ${t('dashboardScanItemsPerSecond')}`
})
const scanETALabel = computed(() => {
  if (!scan.value || scan.value.eta_seconds === undefined) {
    return scan.value?.estimating ? t('dashboardScanEstimating') : t('dashboardEmptyValue')
  }
  return formatDuration(scan.value.eta_seconds)
})
const scanCurrentRoot = computed(() => scan.value?.current_root || mediaRoots.value[0] || t('dashboardEmptyValue'))
const scanCurrentDirectory = computed(() => scan.value?.current_directory || t('dashboardEmptyValue'))
const discoveryHealthy = computed(() => discovery.value?.status === 'normal')

function formatBytes(bytes: number | undefined): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

function formatNumber(value: number | undefined): string {
  return new Intl.NumberFormat().format(value ?? 0)
}

function formatDuration(totalSeconds: number): string {
  if (totalSeconds <= 0) return `0 ${t('dashboardScanSeconds')}`
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = Math.floor(totalSeconds % 60)
  const parts: string[] = []
  if (hours > 0) parts.push(`${hours} ${t('dashboardScanHours')}`)
  if (minutes > 0) parts.push(`${minutes} ${t('dashboardScanMinutes')}`)
  if (hours === 0 && seconds > 0) parts.push(`${seconds} ${t('dashboardScanSeconds')}`)
  return parts.join(' ')
}

function formatClock(value: string | undefined, includeSeconds = false): string {
  if (!value) return t('dashboardEmptyValue')
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return t('dashboardEmptyValue')
  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    ...(includeSeconds ? { second: '2-digit' as const } : {})
  }).format(date)
}

function scanStatusLabel(value: string | undefined): string {
  return t(scanStatusKey(value))
}

function scanSourceLabel(value: string | undefined): string {
  return t(scanSourceKey(value))
}

function watcherStatusLabel(): string {
  return t(watcherStatusKey(discovery.value))
}

function recoveryStatusLabel(): string {
  return t(recoveryStatusKey(discovery.value?.recovery?.status))
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
  return separatorIndex <= 0 ? '/' : path.slice(0, separatorIndex)
}

function dashboardTaskPhaseLabel(phase: RuntimePhase): string {
  const keys: Record<string, string> = {
    queued: 'dashboardPhaseQueued',
    retry_wait: 'dashboardPhaseRetryWait',
    capacity_wait: 'dashboardPhaseCapacityWait',
    checking: 'dashboardPhaseChecking',
    transcoding: 'dashboardPhaseTranscoding',
    verifying: 'dashboardPhaseVerifying',
    backing_up: 'dashboardPhaseBackingUp',
    replacing: 'dashboardPhaseReplacing'
  }
  const key = keys[phase]
  return key ? t(key) : t('dashboardPhaseUnknown')
}

function dashboardTaskDetails(task: (typeof selectedTasks.value)[number]) {
  return taskProgressDetails(task)
}

function capacityLabel(capability: string): string {
  const labels: Record<string, string> = { backup: 'Backup', work: 'Work', media: 'Media' }
  return labels[capability] ?? capability
}

function dashboardTaskPhaseClass(): string {
  return dashboardTaskTab.value === 'waiting' ? 'dashboard-task-chip--waiting' : 'dashboard-task-chip--active'
}

function errorMessage(caught: unknown): string {
  return caught instanceof Error ? caught.message : String(caught)
}

function showToast(message: string): void {
  toastMessage.value = message
  if (toastTimer !== undefined) window.clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => {
    toastMessage.value = ''
    toastTimer = undefined
  }, 2800)
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

const refreshStatus = createRefreshTask(api.status, (value) => (status.value = value), (message) => (statusError.value = message))
const refreshRuntimeTasks = createRefreshTask(
  api.runtimeTasks,
  (value) => (runtimeTasks.value = value),
  (message) => (runtimeError.value = message)
)
const refreshScan = createRefreshTask(api.currentScan, (value) => (scan.value = value), (message) => (scanError.value = message))
const refreshDiscovery = createRefreshTask(
  api.discoveryStatus,
  (value) => (discovery.value = value),
  (message) => (discoveryError.value = message)
)

async function refreshConfig(): Promise<void> {
  try {
    config.value = (await api.config()).modules
    configError.value = ''
  } catch (caught) {
    configError.value = errorMessage(caught)
  }
}

async function loadDashboard(): Promise<void> {
  loading.value = true
  try {
    await Promise.all([refreshStatus(), refreshRuntimeTasks(), refreshScan(), refreshDiscovery(), refreshConfig()])
  } finally {
    loading.value = false
  }
}

function scheduleRuntimeRefresh(): void {
  if (runtimeEventTimer !== undefined) window.clearTimeout(runtimeEventTimer)
  runtimeEventTimer = window.setTimeout(() => {
    runtimeEventTimer = undefined
    void refreshRuntimeTasks()
  }, eventRefreshDelayMs)
}

function scheduleStatusRefresh(): void {
  if (statusEventTimer !== undefined) window.clearTimeout(statusEventTimer)
  statusEventTimer = window.setTimeout(() => {
    statusEventTimer = undefined
    void Promise.all([refreshStatus(), refreshDiscovery()])
  }, eventRefreshDelayMs)
}

function scheduleScanRefresh(): void {
  if (scanEventTimer !== undefined) window.clearTimeout(scanEventTimer)
  scanEventTimer = window.setTimeout(() => {
    scanEventTimer = undefined
    void refreshScan()
  }, eventRefreshDelayMs)
}

function refreshVisibleDashboard(): void {
  if (document.visibilityState === 'hidden') return
  void Promise.all([refreshRuntimeTasks(), refreshStatus(), refreshScan(), refreshDiscovery()])
}

async function quickScan(): Promise<void> {
  scanLoading.value = true
  operationError.value = ''
  try {
    const result = await api.startScan()
    scan.value = result.scan
    showToast(
      result.reused
        ? `${t('dashboardScanReused')} ${result.scan.scan_id}`
        : `${t('dashboardScanCreated')} ${result.scan.scan_id}`
    )
  } catch (caught) {
    operationError.value = errorMessage(caught)
  } finally {
    scanLoading.value = false
  }
}

async function confirmCancelScan(): Promise<void> {
  if (!scan.value?.scan_id) return
  cancelLoading.value = true
  operationError.value = ''
  try {
    scan.value = await api.cancelScan(scan.value.scan_id)
    cancelDialogOpen.value = false
    showToast(`${t('dashboardScanCancelQueued')} ${waitingTaskCount.value} ${t('dashboardScanQueuedTasksContinue')}`)
  } catch (caught) {
    operationError.value = errorMessage(caught)
  } finally {
    cancelLoading.value = false
  }
}

watch(
  () => eventStreamState.lastEvent,
  (event) => {
    if (!event) return
    scheduleRuntimeRefresh()
    if (statusInvalidationEvents.has(event.type)) scheduleStatusRefresh()
    if (event.type.startsWith('scan.')) scheduleScanRefresh()
  }
)

watch(
  () => eventStreamState.status,
  (nextStatus, previousStatus) => {
    if (nextStatus === 'connected' && previousStatus !== 'connected') {
      scheduleRuntimeRefresh()
      scheduleStatusRefresh()
      scheduleScanRefresh()
    }
  }
)

onMounted(() => {
  void loadDashboard()
  runtimePollTimer = window.setInterval(() => {
    if (document.visibilityState !== 'hidden') void Promise.all([refreshRuntimeTasks(), refreshScan()])
  }, runtimePollIntervalMs)
  statusPollTimer = window.setInterval(() => {
    if (document.visibilityState !== 'hidden') void Promise.all([refreshStatus(), refreshDiscovery()])
  }, statusPollIntervalMs)
  document.addEventListener('visibilitychange', refreshVisibleDashboard)
})

onBeforeUnmount(() => {
  for (const timer of [runtimePollTimer, statusPollTimer, runtimeEventTimer, statusEventTimer, scanEventTimer, toastTimer]) {
    if (timer !== undefined) window.clearTimeout(timer)
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
      <section class="dashboard-metric dashboard-metric--service panel"><span class="dashboard-metric__label">{{ t('dashboardServiceStatus') }}</span><strong class="dashboard-metric__value">{{ serviceStatusLabel(status.status) }}</strong></section>
      <section class="dashboard-metric dashboard-metric--compatible panel"><span class="dashboard-metric__label">{{ t('dashboardCompatible') }}</span><strong class="dashboard-metric__value">{{ compatibleCount }}</strong></section>
      <section class="dashboard-metric dashboard-metric--processed panel"><span class="dashboard-metric__label">{{ t('dashboardProcessed') }}</span><strong class="dashboard-metric__value">{{ processedCount }}</strong></section>
      <section class="dashboard-metric dashboard-metric--failed panel"><span class="dashboard-metric__label">{{ t('dashboardFailed') }}</span><strong class="dashboard-metric__value">{{ failedCount }}</strong></section>
      <section class="dashboard-metric dashboard-metric--processing panel"><span class="dashboard-metric__label">{{ t('dashboardProcessing') }}</span><strong class="dashboard-metric__value">{{ processingCount }}</strong></section>
      <section class="dashboard-metric dashboard-metric--queue panel"><span class="dashboard-metric__label">{{ t('dashboardQueueCount') }}</span><strong class="dashboard-metric__value">{{ waitingTaskCount }}</strong></section>
      <section class="dashboard-metric dashboard-metric--backup panel"><span class="dashboard-metric__label">{{ t('dashboardBackupUsage') }}</span><strong class="dashboard-metric__value">{{ formatBytes(status.backup_usage_bytes) }}</strong></section>
      <section class="dashboard-metric dashboard-metric--backup panel"><span class="dashboard-metric__label">{{ t('dashboardUnresolvedBackups') }}</span><strong class="dashboard-metric__value">{{ status.unresolved_backup_count }}</strong></section>
      <section class="dashboard-metric dashboard-metric--success panel"><span class="dashboard-metric__label">{{ t('dashboardSuccessRate') }}</span><strong class="dashboard-metric__value">{{ successRate }}</strong></section>
    </div>

    <section v-if="status" class="dashboard-health-panel panel" :data-status="status.status" data-testid="dashboard-business-health">
      <header class="dashboard-health-header">
        <div><h2>{{ t('dashboardHealthTitle') }}</h2><strong>{{ serviceStatusLabel(status.status) }}</strong></div>
        <span>{{ status.intake_accepting ? t('serviceStatusNormal') : serviceStatusLabel(status.status) }}</span>
      </header>
      <div v-if="status.health_reasons.length" class="dashboard-health-reasons">
        <div v-for="reason in status.health_reasons" :key="reason.code" class="dashboard-health-reason">
          <strong>{{ reason.summary }}</strong>
          <span>{{ t('dashboardAffectedCapability') }}: {{ reason.affected_capability }}</span>
          <span>{{ t('dashboardSuggestedAction') }}: {{ reason.advice }}</span>
        </div>
      </div>
      <div v-else class="dashboard-health-normal">{{ t('dashboardHealthNormal') }}</div>
      <div class="dashboard-capacity-list">
        <div v-for="(volume, index) in status.capacity.volumes" :key="`${volume.capability}:${volume.path}:${index}`" class="dashboard-capacity-row" :data-ready="volume.ready">
          <strong>{{ capacityLabel(volume.capability) }}</strong>
          <span :title="volume.path">{{ volume.path }}</span>
          <span>{{ t('dashboardCapacityAvailable') }} {{ formatBytes(volume.available_bytes) }}</span>
          <span>{{ t('dashboardCapacityRequired') }} {{ formatBytes(volume.required_bytes) }}</span>
        </div>
      </div>
    </section>

    <section v-if="scan" class="dashboard-scan-panel panel" data-testid="dashboard-current-scan">
      <header class="dashboard-scan-header">
        <div class="dashboard-scan-title">
          <h2>{{ t('dashboardCurrentScan') }}</h2>
          <span class="dashboard-scan-source">{{ scanSourceLabel(scan.source) }}</span>
          <span class="dashboard-scan-badge" :data-status="scan.status">{{ scanStatusLabel(scan.status) }}</span>
          <span v-if="scan.scan_id" class="dashboard-scan-id">{{ scan.scan_id }}</span>
        </div>
        <div class="dashboard-scan-actions">
          <button v-if="!scanActive" type="button" class="button primary" data-testid="dashboard-quick-scan" :disabled="scanLoading || loading" @click="quickScan"><ScanSearch aria-hidden="true" />{{ t('dashboardQuickScan') }}</button>
          <button v-else type="button" class="button danger" data-testid="dashboard-cancel-scan" :disabled="scan.status === 'cancelling' || cancelLoading" @click="cancelDialogOpen = true"><CircleStop aria-hidden="true" />{{ t('dashboardCancelScan') }}</button>
        </div>
      </header>

      <div class="dashboard-scan-context">
        <div><span>{{ t('dashboardScanCurrentRoot') }}</span><strong>{{ scanCurrentRoot }}</strong></div>
        <div><span>{{ t('dashboardScanCurrentDirectory') }}</span><strong :title="scanCurrentDirectory">{{ scanCurrentDirectory }}</strong></div>
      </div>

      <div class="dashboard-scan-progress">
        <div class="dashboard-scan-progress__labels">
          <span>{{ t('dashboardScanEstimatedProgress') }} <strong>{{ scanProgressLabel }}</strong></span>
          <span>{{ t('dashboardScanRate') }} <strong>{{ scanRateLabel }}</strong></span>
          <span>{{ t('dashboardScanETA') }} <strong>{{ scanETALabel }}</strong></span>
        </div>
        <div class="dashboard-scan-progress__track" :class="{ 'is-indeterminate': scan.estimating && scanActive }"><i :style="{ width: scanProgressWidth }"></i></div>
      </div>

      <div class="dashboard-scan-stats">
        <div><span>{{ t('dashboardScanVisited') }}</span><strong>{{ formatNumber(scan.visited) }}</strong></div>
        <div><span>{{ t('dashboardScanDiscovered') }}</span><strong>{{ formatNumber(scan.discovered) }}</strong></div>
        <div><span>{{ t('dashboardScanSkipped') }}</span><strong>{{ formatNumber(scan.skipped) }}</strong></div>
        <div><span>{{ t('dashboardScanEnqueued') }}</span><strong>{{ formatNumber(scan.enqueued) }}</strong></div>
        <div><span>{{ t('dashboardScanFailures') }}</span><strong class="is-danger">{{ formatNumber(scan.failed) }}</strong></div>
        <div><span>{{ t('dashboardScanMerged') }}</span><strong class="is-warning">{{ formatNumber(scan.merged) }}</strong></div>
        <div><span>{{ t('dashboardScanMissing') }}</span><strong>{{ formatNumber(scan.missing) }}</strong></div>
      </div>

      <div v-if="scan.last_merged_path || scan.updated_at" class="dashboard-scan-foot">
        <span v-if="scan.last_merged_path">{{ t('dashboardScanMergedSignal') }} <b :title="scan.last_merged_path">{{ scan.last_merged_path }}</b></span>
        <span v-else></span>
        <span>{{ t('dashboardScanLastUpdated') }} {{ formatClock(scan.updated_at, true) }}</span>
      </div>
      <div v-if="scan.last_error" class="dashboard-scan-error"><span>{{ t('dashboardScanRecentError') }}</span><strong>{{ scan.last_error }}</strong></div>
    </section>

    <section v-if="discovery" class="dashboard-discovery-panel panel" data-testid="dashboard-discovery-status">
      <header class="dashboard-discovery-header">
        <div class="dashboard-discovery-title"><Radar aria-hidden="true" /><h2>{{ t('dashboardDiscoveryTitle') }}</h2></div>
        <span class="dashboard-discovery-overall" :data-status="discovery.status">{{ discoveryHealthy ? t('dashboardDiscoveryHealthy') : t('dashboardDiscoveryLimited') }}</span>
      </header>
      <div class="dashboard-discovery-grid">
        <div class="dashboard-discovery-item"><span class="dashboard-discovery-icon"><RadioTower aria-hidden="true" /></span><div><span>{{ t('dashboardDiscoveryWatcher') }}</span><strong>{{ watcherStatusLabel() }}</strong><small>{{ t('dashboardDiscoveryWatchingPrefix') }} {{ discovery.watched_directories }} {{ t('dashboardDiscoveryDirectories') }}</small></div></div>
        <div class="dashboard-discovery-item"><span class="dashboard-discovery-icon"><RefreshCw aria-hidden="true" /></span><div><span>{{ t('dashboardDiscoveryReconciliation') }}</span><strong>{{ t('dashboardDiscoveryEvery') }} {{ discovery.reconciliation_interval_minutes }} {{ t('dashboardScanMinutes') }}</strong><small>{{ t('dashboardDiscoveryNext') }} {{ formatClock(discovery.next_reconciliation_at) }}</small></div></div>
        <div class="dashboard-discovery-item"><span class="dashboard-discovery-icon"><History aria-hidden="true" /></span><div><span>{{ t('dashboardDiscoveryLatest') }}</span><strong>{{ discovery.last_reconciliation ? `${formatClock(discovery.last_reconciliation.completed_at)} ${t('dashboardDiscoveryCompleted')}` : t('dashboardEmptyValue') }}</strong><small v-if="discovery.last_reconciliation" class="dashboard-discovery-result">{{ t('dashboardDiscoveryAdded') }} {{ discovery.last_reconciliation.added }} · {{ t('dashboardDiscoveryModified') }} {{ discovery.last_reconciliation.modified }} · {{ t('dashboardDiscoveryDeleted') }} {{ discovery.last_reconciliation.deleted }}</small></div></div>
      </div>
      <div v-if="discovery.recovery" class="dashboard-discovery-recovery"><CircleCheck aria-hidden="true" /><span>{{ formatClock(discovery.recovery.detected_at) }} {{ discovery.recovery.status === 'completed' ? t('dashboardDiscoveryRecoveryCompleted') : t('dashboardDiscoveryRecoveryScheduled') }}</span><strong>{{ recoveryStatusLabel() }}</strong></div>
    </section>

    <section v-if="runtimeTasks" class="dashboard-task-panel panel" data-testid="dashboard-task-queue">
      <div class="dashboard-task-tabbar">
        <div class="dashboard-task-tabs" role="tablist">
          <button type="button" role="tab" class="dashboard-task-tab" :class="{ 'dashboard-task-tab--active': dashboardTaskTab === 'waiting' }" :aria-selected="dashboardTaskTab === 'waiting'" @click="selectDashboardTaskTab('waiting')">{{ t('dashboardTaskWaitingTab') }} {{ waitingTaskCount }}</button>
          <button type="button" role="tab" class="dashboard-task-tab" :class="{ 'dashboard-task-tab--active': dashboardTaskTab === 'active' }" :aria-selected="dashboardTaskTab === 'active'" @click="selectDashboardTaskTab('active')">{{ t('dashboardTaskActiveTab') }} {{ activeTaskCount }}</button>
        </div>
      </div>
      <div class="dashboard-task-list">
        <div v-if="pagedDashboardTasks.length === 0" class="dashboard-task-empty muted">{{ t('dashboardTaskEmpty') }}</div>
        <div v-for="task in pagedDashboardTasks" :key="`${task.phase}:${task.path}`" class="dashboard-task-row" :data-stalled="task.stalled">
          <div class="dashboard-task-path"><strong>{{ dashboardTaskFileName(task.path) }}</strong><span>{{ dashboardTaskDirectory(task.path) }}</span></div>
          <div class="dashboard-task-state"><span class="dashboard-task-chip" :class="dashboardTaskPhaseClass()">{{ dashboardTaskPhaseLabel(task.phase) }}</span><b v-if="task.stalled">{{ t('dashboardTaskStalled') }}</b></div>
          <div v-if="dashboardTaskDetails(task).waitReason" class="dashboard-task-wait"><span>{{ t('dashboardTaskWaitReason') }}: <strong>{{ dashboardTaskDetails(task).waitReason }}</strong></span><span>{{ t('dashboardTaskUnlock') }}: <strong>{{ dashboardTaskDetails(task).unlockCondition }}</strong></span></div>
          <div v-else class="dashboard-task-progress"><span>{{ t('dashboardTaskElapsed') }} <strong>{{ formatDuration(dashboardTaskDetails(task).elapsedSeconds) }}</strong></span><span>{{ t('dashboardTaskPosition') }} <strong>{{ formatDuration(dashboardTaskDetails(task).positionSeconds) }}</strong></span><span>{{ t('dashboardTaskSpeed') }} <strong>{{ dashboardTaskDetails(task).speed.toFixed(2) }}x</strong></span><span>{{ t('dashboardTaskOutput') }} <strong>{{ formatBytes(dashboardTaskDetails(task).outputBytes) }}</strong></span><span>{{ t('dashboardTaskETA') }} <strong>{{ formatDuration(dashboardTaskDetails(task).etaSeconds) }}</strong></span></div>
        </div>
      </div>
      <div class="dashboard-task-pager">
        <span class="dashboard-task-pager__summary">{{ dashboardTaskSummary }}</span>
        <div class="dashboard-task-pager__controls">
          <label class="dashboard-task-page-size"><span>{{ t('dashboardTaskPageSize') }}</span><select :value="dashboardTaskPageSize" aria-label="dashboard task page size" @change="updateDashboardTaskPageSize"><option v-for="size in dashboardTaskPageSizes" :key="size" :value="size">{{ size }} {{ t('pagerItems') }}</option></select></label>
          <div class="dashboard-task-pagination" aria-label="dashboard task pagination">
            <button type="button" class="dashboard-task-page-button" :disabled="!canGoPreviousDashboardTaskPage" :aria-label="t('pagerPrevious')" @click="goDashboardTaskPage(effectiveDashboardTaskPage - 1)">‹</button>
            <button type="button" class="dashboard-task-page-button dashboard-task-page-button--active" disabled>{{ effectiveDashboardTaskPage }}</button>
            <button type="button" class="dashboard-task-page-button" :disabled="!canGoNextDashboardTaskPage" :aria-label="t('pagerNext')" @click="goDashboardTaskPage(effectiveDashboardTaskPage + 1)">›</button>
          </div>
        </div>
      </div>
    </section>

    <div v-if="config" class="dashboard-sections">
      <section class="panel"><div class="panel-body"><h2>{{ t('dashboardMediaRoots') }}</h2><ul class="plain-list"><li v-for="root in mediaRoots" :key="root">{{ root }}</li><li v-if="mediaRoots.length === 0" class="muted">{{ t('dashboardEmptyValue') }}</li></ul></div></section>
    </div>

    <div v-if="cancelDialogOpen" class="dashboard-dialog-overlay" role="presentation" @click.self="cancelDialogOpen = false">
      <section class="dashboard-scan-dialog" role="dialog" aria-modal="true" :aria-label="t('dashboardCancelDialogTitle')">
        <header><CircleStop aria-hidden="true" /><h2>{{ t('dashboardCancelDialogTitle') }}</h2></header>
        <div class="dashboard-scan-dialog__body"><p>{{ t('dashboardCancelDialogMessage') }}</p><div class="dashboard-scan-dialog__impact"><div><span>{{ t('dashboardCancelStops') }}</span><strong>{{ t('dashboardCancelStopsValue') }}</strong></div><div><span>{{ t('dashboardCancelContinues') }}</span><strong>{{ t('dashboardCancelContinuesValue') }}</strong></div></div></div>
        <footer><button type="button" class="button" @click="cancelDialogOpen = false">{{ t('dashboardKeepScanning') }}</button><button type="button" class="button danger" :disabled="cancelLoading" data-testid="dashboard-confirm-cancel-scan" @click="confirmCancelScan">{{ t('dashboardStopDiscovery') }}</button></footer>
      </section>
    </div>

    <div v-if="toastMessage" class="dashboard-toast" role="status"><CircleCheck aria-hidden="true" /><span>{{ toastMessage }}</span></div>
  </div>
</template>
