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
	validation: ValidationConfig
  ui: UIConfig
  scan: ScanConfig
}

export interface MediaConfig {
  roots: string[]
  extensions: string[]
  exclude_dirs: string[]
  exclude_patterns: string[]
}

export interface AudioConfig {
  version: number
  incompatible_codecs: string[]
}

export interface PipelineConfig {
  workers: number
  max_retries: number
  retry_delay_seconds: number
  stat_quiet_seconds: number
  job_timeout_minutes: number
}

export interface OperationStatusResponse {
	status: string
}

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

export type JobStatus = 'compatible' | 'processing' | 'processed' | 'failed' | string
export type DiscoverySource = 'scan' | 'watchdog' | 'manual' | string
export type JobResult = 'processing' | 'compatible' | 'succeeded' | 'failed'
export type RuntimePhase =
  | 'queued'
  | 'retry_wait'
  | 'checking'
  | 'transcoding'
  | 'verifying'
  | 'backing_up'
  | 'replacing'
export interface HistoryRecord {
  id: number
  file_id: number
  path: string
  trigger_source: DiscoverySource
  result: JobResult
  final_error: string
  started_at: string
  finished_at: string
}

export interface RuntimeLogResponse {
  content: string
  lines: number
}

export interface FileRecord {
	id: number
	path: string
	size: number
	mtime_ns: number
	audio_signature: string
	video_signature: string
	compliance_status: 'compliant' | 'noncompliant' | '' | string
	audio_policy_version: number
	backup_file: string
	created_at: string
	updated_at: string
}

export interface ServiceStatus {
  status: 'running' | 'restarting' | 'restart_failed' | string
  counts: Record<string, number>
	backup_usage_bytes: number
	unresolved_backup_count: number
  transcode_success_rate: TranscodeSuccessRate
}

export interface RuntimeTask {
  path: string
  phase: RuntimePhase
  source?: DiscoverySource
}

export interface RuntimeTasksSnapshot {
  waiting_tasks: RuntimeTask[]
  active_tasks: RuntimeTask[]
}

export interface TranscodeSuccessRate {
  succeeded: number
  failed: number
  rate: number
}

export interface ScanResponse {
  status: 'queued' | string
}

export interface RestoreResult {
	backup_path: string
}

export type EventStreamStatus = 'connecting' | 'connected' | 'disconnected' | 'error'

export interface ServerSentEvent {
  type: string
  snapshot_id: number
  data: Record<string, unknown> | null
}
