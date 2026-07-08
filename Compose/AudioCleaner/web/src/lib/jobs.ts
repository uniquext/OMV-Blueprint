import type { HistoryRecord, JobRecord } from './types'
import type { PageResult } from './types'

export type JobFilter = 'all' | 'compatible' | 'processing' | 'processed' | 'failed' | 'restored'
export type HistorySourceFilter = 'all' | string

export interface HistoryFilter {
  result: JobFilter
  source: HistorySourceFilter
  dateFrom: string
  dateTo: string
  keyword: string
}

export interface HistoryJobSummary {
  done: number
  skipped: number
  failed: number
  successRate: number
}

export interface JobsRequestPlan {
  page: number
  pageSize: number
  localPagination: boolean
}

type JobLike = Partial<HistoryRecord>
type FailureCauseLike = Partial<Pick<JobRecord, 'failure_cause'>>

export function jobID(job: JobLike): number {
  return job.id ?? 0
}

export function jobPath(job: JobLike): string {
  return job.path ?? ''
}

export function jobBasename(job: JobLike): string {
  const path = jobPath(job)
  const parts = path.split('/').filter((part) => part.length > 0)
  return parts.at(-1) ?? path
}

export function jobDirectory(job: JobLike): string {
  const path = jobPath(job)
  const lastSlash = path.lastIndexOf('/')
  if (lastSlash <= 0) {
    return ''
  }
  return path.slice(0, lastSlash)
}

export function jobStatus(job: JobLike): string {
  return job.status ?? ''
}

export function jobDiscoverySource(job: JobLike): string {
  return job.discovery_source ?? ''
}

export function jobFailureCause(job: FailureCauseLike): string {
  return job.failure_cause ?? ''
}

export function jobFilterKey(job: JobLike): Exclude<JobFilter, 'all'> | null {
  const status = jobStatus(job)
  if (
    status === 'compatible' ||
    status === 'processing' ||
    status === 'processed' ||
    status === 'failed' ||
    status === 'restored'
  ) {
    return status
  }
  return null
}

export function jobFailureCauseLabelKey(cause: string): string | null {
  const keys: Record<string, string> = {
    failed: 'jobFailureCauseFailed',
    unsupported: 'jobFailureCauseUnsupported',
    ffprobe_error: 'jobFailureCauseFfprobeError',
    verification_failed: 'jobFailureCauseVerificationFailed',
    timeout: 'jobFailureCauseTimeout',
    restore_stat_error: 'jobFailureCauseRestoreStatError',
    restore_probe_error: 'jobFailureCauseRestoreProbeError',
    restored_requires_transcoding: 'jobFailureCauseRestoredRequiresTranscoding'
  }
  return keys[cause] ?? null
}

export function jobDiscoverySourceLabelKey(source: string): string | null {
  const keys: Record<string, string> = {
    scan: 'jobDiscoverySourceScan',
    watchdog: 'jobDiscoverySourceWatchdog',
    manual: 'jobDiscoverySourceManual'
  }
  return keys[source] ?? null
}

export function jobDiscoverySourceDisplay(source: string, translate: (key: string) => string): string {
  if (!source) {
    return ''
  }
  const key = jobDiscoverySourceLabelKey(source)
  return key ? translate(key) : source
}

export function filterJobs<T extends JobLike>(jobs: T[], filter: JobFilter, keyword: string): T[] {
  const normalizedKeyword = keyword.trim().toLowerCase()
  return jobs.filter((job) => {
    const matchesFilter = filter === 'all' || jobFilterKey(job) === filter
    const matchesKeyword = !normalizedKeyword || jobPath(job).toLowerCase().includes(normalizedKeyword)
    return matchesFilter && matchesKeyword
  })
}

export function historyJobSummary(jobs: JobLike[]): HistoryJobSummary {
  const done = jobs.filter((job) => jobStatus(job) === 'processed').length
  const skipped = jobs.filter((job) => jobStatus(job) === 'compatible').length
  const failed = jobs.filter((job) => jobStatus(job) === 'failed').length
  const rateBase = done + failed
  return {
    done,
    skipped,
    failed,
    successRate: rateBase > 0 ? done / rateBase : 0
  }
}

function datePart(value: string | undefined): string {
  return value ? value.slice(0, 10) : ''
}

export function filterHistoryJobs<T extends JobLike>(jobs: T[], filter: HistoryFilter): T[] {
  const normalizedKeyword = filter.keyword.trim().toLowerCase()
  return jobs.filter((job) => {
    const matchesResult = filter.result === 'all' || jobFilterKey(job) === filter.result
    const matchesSource = filter.source === 'all' || jobDiscoverySource(job) === filter.source
    const updatedDate = datePart(job.updated_at)
    const matchesDateFrom = !filter.dateFrom || (!!updatedDate && updatedDate >= filter.dateFrom)
    const matchesDateTo = !filter.dateTo || (!!updatedDate && updatedDate <= filter.dateTo)
    const searchable = `${jobID(job)} ${jobPath(job)} ${jobBasename(job)}`.toLowerCase()
    const matchesKeyword = !normalizedKeyword || searchable.includes(normalizedKeyword)
    return matchesResult && matchesSource && matchesDateFrom && matchesDateTo && matchesKeyword
  })
}

export function jobErrorSummaryLabelKey(error: string): string | null {
  const normalized = error.trim().toLowerCase()
  if (!normalized) {
    return null
  }
  if (normalized.includes('ffprobe') || normalized.includes('probe')) {
    return 'historyErrorMediaProbeFailed'
  }
  if (normalized.includes('verification')) {
    return 'historyErrorVerificationFailed'
  }
  if (normalized.includes('timeout')) {
    return 'historyErrorTimeout'
  }
  return 'historyErrorProcessingFailed'
}

export async function loadAllJobPages<T>(
  pageSize: number,
  loadPage: (page: number, pageSize: number) => Promise<PageResult<T>>
): Promise<PageResult<T>> {
  const items: T[] = []
  let total = 0
  let page = 1
  let lastResult: PageResult<T> | null = null

  while (true) {
    const result = await loadPage(page, pageSize)
    lastResult = result
    total = result.total
    items.push(...result.items)

    if (result.items.length === 0 || items.length >= result.total) {
      break
    }
    page += 1
  }

  return {
    items,
    page: 1,
    page_size: pageSize,
    total: lastResult ? total : 0
  }
}

export function jobsRequestPlan(page: number, pageSize: number, filter: JobFilter, keyword: string): JobsRequestPlan {
  const localPagination = filter !== 'all' || keyword.trim().length > 0
  return {
    page: localPagination ? 1 : page,
    pageSize: localPagination ? 1000 : pageSize,
    localPagination
  }
}
