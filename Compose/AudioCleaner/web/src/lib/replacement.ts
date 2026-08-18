import type { HistoryRecord } from './types'

const qualityGateKeys: Record<string, string> = {
  structure: 'historyQualityGateStructure',
  video: 'historyQualityGateVideo',
  audio: 'historyQualityGateAudio',
  duration: 'historyQualityGateDuration',
  size: 'historyQualityGateSize'
}

const qualityStatusKeys: Record<string, string> = {
  passed: 'historyQualityPassed',
  failed: 'historyQualityFailed',
  not_run: 'historyQualityNotRun'
}

const failureCodeKeys: Record<string, string> = {
  source_changed: 'historyFailureSourceChanged',
  manual_recovery: 'historyFailureManualRecovery',
  verification_failed: 'historyFailureVerification',
  timeout: 'historyFailureTimeout',
  ffprobe_error: 'historyFailureProbe',
  restore_stat_error: 'historyFailureRestoreStat',
  restore_probe_error: 'historyFailureRestoreProbe',
  unsupported: 'historyFailureUnsupported',
  interrupted_replacement: 'historyFailureInterrupted'
}

export function qualityGateKey(gate: string): string {
  return qualityGateKeys[gate] ?? 'historyQualityGateUnknown'
}

export function qualityStatusKey(status: string): string {
  return qualityStatusKeys[status] ?? 'historyQualityUnknown'
}

export function failureCodeKey(code: string): string {
  if (!code) return 'historyFailureNone'
  return failureCodeKeys[code] ?? 'historyFailureGeneric'
}

export function canRetryReplacementJob(job: Pick<HistoryRecord, 'result' | 'failure_code'>): boolean {
  return job.result === 'failed' && job.failure_code !== 'manual_recovery'
}
