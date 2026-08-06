export type SupportedLanguage = 'zh-CN' | 'en-US'

export interface ApiEnvelope<T> {
  code: number
  error_code?: string
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

export type ConfigModuleName = 'media' | 'audio' | 'pipeline' | 'validation' | 'scan' | 'ui'
export type ConfigApplyMode = 'hot_reload' | 'module_reload' | 'service_restart'
export type ConfigApplyStatus = 'active' | 'restarting'

export interface ConfigModuleMetadata {
  revision: string
  apply_mode: ConfigApplyMode
}

export interface SettingsView {
  revision: string
  modules: AudioCleanerConfig
  metadata: Record<ConfigModuleName, ConfigModuleMetadata>
}

export interface ModuleUpdateResult {
  module: ConfigModuleName
  changed: boolean
  apply_mode: ConfigApplyMode
  status: ConfigApplyStatus
  revision: string
  config: AudioCleanerConfig[ConfigModuleName]
  notices?: string[]
}

export interface MediaConfig {
  roots: string[]
  extensions: string[]
  exclude_dirs: string[]
  exclude_patterns: string[]
}

export interface MediaDirectory {
  name: string
  path: string
  has_children: boolean
}

export interface MediaDirectoryListing {
  parent: string
  directories: MediaDirectory[]
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
  reconciliation_interval_minutes: number
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

export interface AudioTrackEvidence {
  stream_index: number
  codec: string
  channels: number
  language?: string
  title?: string
  disposition?: string
}

export interface RuleMatch {
  codec: string
  stream_indexes: number[]
}

export interface AudioPlanEvidence {
  stream_index: number
  action: 'copy' | 'transcode' | string
  target_codec?: string
  bitrate?: string
}

export interface CompatibilityAssessment {
  schema_version: number
  source: 'source_probe' | 'cache' | string
  policy_version: number
  policy_incompatible_codecs: string[]
  audio_tracks: AudioTrackEvidence[]
  matched_rules: RuleMatch[]
  action: 'already_compatible' | 'transcode' | 'unsupported' | string
  audio_plans: AudioPlanEvidence[]
  reason: string
  assessed_at: string
  reused_at?: string
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
	compatibility_assessment?: CompatibilityAssessment
	missing_at?: string
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

export type ScanSource = 'manual' | 'startup' | 'reconciliation' | 'recovery' | string
export type ScanStatus = 'idle' | 'running' | 'reconciling' | 'cancelling' | 'completed' | 'cancelled' | 'failed' | string

export interface ScanSession {
  scan_id: string
  source: ScanSource
  status: ScanStatus
  started_at?: string
  updated_at?: string
  finished_at?: string
  current_root: string
  current_directory: string
  progress_percent?: number
  estimating: boolean
  rate_per_second: number
  eta_seconds?: number
  visited: number
  discovered: number
  skipped: number
  enqueued: number
  failed: number
  merged: number
  missing: number
  added: number
  modified: number
  last_merged_path?: string
  last_error?: string
}

export interface ScanStartResponse {
  scan: ScanSession
  reused: boolean
}

export interface ReconciliationResult {
  completed_at: string
  added: number
  modified: number
  deleted: number
}

export interface RecoveryResult {
  detected_at: string
  completed_at?: string
  reason: string
  status: 'pending' | 'running' | 'completed' | string
}

export interface DiscoveryStatus {
  status: 'normal' | 'limited' | 'degraded' | string
  watcher_enabled: boolean
  watcher_status: 'running' | 'disabled' | 'error' | string
  watcher_error?: string
  watched_directories: number
  reconciliation_interval_minutes: number
  next_reconciliation_at?: string
  last_reconciliation?: ReconciliationResult
  recovery?: RecoveryResult
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
