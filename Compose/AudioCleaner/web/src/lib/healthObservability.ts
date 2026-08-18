export interface TaskProgressInput {
  phase_elapsed_seconds?: number
  media_position_seconds?: number
  speed?: number
  output_bytes?: number
  eta_seconds?: number
  wait_reason?: string
  unlock_condition?: string
  stalled?: boolean
}

export interface FailureDiagnosticInput {
  failure_summary?: string
  failure_advice?: string
  final_error?: string
}

const failureAdviceKeys: Record<string, string> = {
  ffprobe_error: 'historyFailureAdviceMedia',
  unsupported: 'historyFailureAdviceMedia',
  permission_denied: 'historyFailureAdvicePermission',
  temporary_storage: 'historyFailureAdviceStorage',
  restore_stat_error: 'historyFailureAdviceStorage',
  restore_probe_error: 'historyFailureAdviceStorage',
  disk_full: 'historyFailureAdviceSpace',
  capacity_exhausted: 'historyFailureAdviceSpace',
  timeout: 'historyFailureAdviceTimeout',
  verification_failed: 'historyFailureAdviceVerification',
  manual_recovery: 'historyFailureAdviceRecovery',
  interrupted_replacement: 'historyFailureAdviceRecovery',
  source_changed: 'historyFailureAdviceSourceChanged'
}

export function failureAdviceKey(code: string): string {
  return failureAdviceKeys[code] ?? 'historyFailureAdviceGeneric'
}

export function failureDiagnosticDetails(input: FailureDiagnosticInput) {
  return {
    summary: input.failure_summary?.trim() ?? '',
    advice: input.failure_advice?.trim() ?? '',
    diagnostic: input.final_error?.trim() ?? ''
  }
}

export function healthStatusKey(status: string): string {
  const keys: Record<string, string> = {
    normal: 'serviceStatusNormal',
    degraded: 'serviceStatusDegraded',
    intake_stopped: 'serviceStatusIntakeStopped',
    manual_recovery: 'serviceStatusManualRecovery'
  }
  return keys[status] ?? status
}

export function taskProgressDetails(task: TaskProgressInput) {
  return {
    elapsedSeconds: Math.max(0, task.phase_elapsed_seconds ?? 0),
    positionSeconds: Math.max(0, task.media_position_seconds ?? 0),
    speed: Math.max(0, task.speed ?? 0),
    outputBytes: Math.max(0, task.output_bytes ?? 0),
    etaSeconds: Math.max(0, task.eta_seconds ?? 0),
    waitReason: task.wait_reason ?? '',
    unlockCondition: task.unlock_condition ?? '',
    stalled: task.stalled ?? false
  }
}
