export type SupportedLanguage = 'zh-CN' | 'en-US'

export interface ApiEnvelope<T> {
  code: number
  message: string
  data: T
}

export interface PageResult<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}

export interface PageRequest {
  page?: number
  page_size?: number
}

export interface AudioCleanerConfig {
  media: MediaConfig
  audio: AudioConfig
  pipeline: PipelineConfig
  backup: BackupConfig
  validation: ValidationConfig
  ui: UIConfig
  scan: ScanConfig
  notifications: NotificationsConfig
}

export interface MediaConfig {
  roots: string[]
  extensions: string[]
  exclude_dirs: string[]
  exclude_patterns: string[]
}

export interface AudioConfig {
  incompatible_codecs: string[]
}

export interface PipelineConfig {
  workers: number
  max_retries: number
  retry_delay_seconds: number
  stat_quiet_seconds: number
  job_timeout_minutes: number
}

export interface BackupConfig {
  retention_days: number
}

export interface OperationStatusResponse {
  status: string
}

export type BackupPatchResponse = BackupConfig | OperationStatusResponse

export interface ValidationConfig {
  max_size_ratio: number
  max_size_increase_mb: number
  duration_tolerance_seconds: number
}

export interface UIConfig {
  language: SupportedLanguage
}

export interface ScanConfig {
  startup_scan_enabled: boolean
  watchdog_enabled: boolean
}

export interface NotificationsConfig {
  enabled: boolean
  targets: string[]
}

export type JobStatus = 'compatible' | 'processing' | 'processed' | 'failed' | 'restored' | 'ignored' | string
export type DiscoverySource = 'scan' | 'watchdog' | 'manual' | string
export type PipelinePhase =
  | 'pending'
  | 'queued'
  | 'retry_wait'
  | 'checking'
  | 'transcoding'
  | 'verifying'
  | 'backing_up'
  | 'replacing'
  | string
export type FailureCause =
  | 'failed'
  | 'unsupported'
  | 'ffprobe_error'
  | 'verification_failed'
  | 'timeout'
  | 'restore_stat_error'
  | 'restore_probe_error'
  | 'restored_requires_transcoding'
  | string
export type EventKind = 'phase_transition' | 'status_change' | 'operation' | 'diagnostic' | string
export type EventCode =
  | 'queued'
  | 'checking'
  | 'transcoding'
  | 'verifying'
  | 'backing_up'
  | 'replacing'
  | 'retry_wait'
  | 'compatible'
  | 'processed'
  | 'failed'
  | 'ignored'
  | 'restore'
  | 'ffmpeg_data_stream_fallback'
  | string
export type EventOutcome = 'transcoded' | 'failed' | 'unsupported' | 'restored' | string
export type JobPhase = PipelinePhase

export interface JobRecord {
  id: number
  path: string
  status: JobStatus
  discovery_source: DiscoverySource
  failure_cause: FailureCause
  pipeline_phase: PipelinePhase
  fingerprint: string
  size: number
  mtime_ns: number
  audio_signature: string
  video_signature: string
  attempts: number
  last_error: string
  created_at: string
  updated_at: string
}

export interface JobEventRecord {
  id: number
  file_id: number
  event_kind: EventKind
  event_code: EventCode
  phase: PipelinePhase
  status: JobStatus
  outcome: EventOutcome
  attempt: number
  command: string
  message: string
  error: string
  started_at: string
  finished_at: string
}

export interface RuntimeLogResponse {
  content: string
  lines: number
}

export interface BackupRecord {
  id: number
  file_id: number
  original_path: string
  backup_path: string
  original_size: number
  original_mtime_ns: number
  created_at: string
  expires_at: string
  restored_at: string
  restore_safety_path: string
  missing: boolean
}

export interface ServiceStatus {
  status: 'running' | 'restarting' | 'restart_failed' | string
  workers: number
  media_roots: string[]
  snapshot_id: number
  queue_count: number
  current_processing: CurrentProcessing[]
  counts: Record<string, number>
  recent_failed: JobRecord[]
  backup_usage_bytes: number
  transcode_success_rate: TranscodeSuccessRate
}

export interface CurrentProcessing {
  path: string
  phase: PipelinePhase
}

export interface TranscodeSuccessRate {
  succeeded: number
  failed: number
  rate: number
}

export interface ScanResponse {
  status: 'queued' | string
}

export interface ActionIDResponse {
  id: number
}

export interface CleanupExpiredResponse {
  removed: number
}

export interface RestoreResult {
  safety_path: string
}

export type EventStreamStatus = 'connecting' | 'connected' | 'disconnected' | 'error'

export interface ServerSentEvent {
  type: string
  snapshot_id: number
  data: Record<string, unknown> | null
}
