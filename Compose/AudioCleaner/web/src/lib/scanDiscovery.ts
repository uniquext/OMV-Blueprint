import type { DiscoveryStatus, ScanSession } from './types'

export function scanIsActive(status: string | undefined): boolean {
  return status === 'running' || status === 'reconciling' || status === 'cancelling'
}

export function scanProgressPercent(scan: ScanSession | null): number {
  return Math.min(100, Math.max(0, scan?.progress_percent ?? 0))
}

export function scanStatusKey(status: string | undefined): string {
  const keys: Record<string, string> = {
    idle: 'dashboardScanIdle',
    running: 'dashboardScanRunning',
    reconciling: 'dashboardScanReconciling',
    cancelling: 'dashboardScanCancelling',
    completed: 'dashboardScanCompleted',
    cancelled: 'dashboardScanCancelled',
    failed: 'dashboardScanFailed'
  }
  return keys[status ?? 'idle'] ?? 'dashboardScanIdle'
}

export function scanSourceKey(source: string | undefined): string {
  if (!source) return 'dashboardScanNotStarted'
  const keys: Record<string, string> = {
    manual: 'dashboardScanSourceManual',
    startup: 'dashboardScanSourceStartup',
    reconciliation: 'dashboardScanSourceReconciliation',
    recovery: 'dashboardScanSourceRecovery'
  }
  return keys[source] ?? 'dashboardScanSourceManual'
}

export function watcherStatusKey(discovery: DiscoveryStatus | null): string {
  if (!discovery?.watcher_enabled || discovery.watcher_status === 'disabled') return 'dashboardDiscoveryDisabled'
  if (discovery.watcher_status === 'error') return 'dashboardDiscoveryAbnormal'
  return 'dashboardDiscoveryRunning'
}

export function recoveryStatusKey(status: string | undefined): string {
  if (status === 'completed') return 'dashboardDiscoveryRecovered'
  if (status === 'running') return 'dashboardDiscoveryRecovering'
  return 'dashboardDiscoveryPending'
}
