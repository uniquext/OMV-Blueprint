import type { JobRecord } from './types'
import type { PageResult } from './types'

export type JobFilter = 'all' | 'compatible' | 'processing' | 'processed' | 'failed' | 'restored' | 'ignored'

export interface JobsRequestPlan {
  page: number
  pageSize: number
  localPagination: boolean
}

type CompatJob = Partial<JobRecord> & {
  ID?: number
  Path?: string
  Status?: string
  DiscoverySource?: string
  FailureCause?: string
  PipelinePhase?: string
}

export function jobID(job: CompatJob): number {
  return job.id ?? job.ID ?? 0
}

export function jobPath(job: CompatJob): string {
  return job.path ?? job.Path ?? ''
}

export function jobStatus(job: CompatJob): string {
  return job.status ?? job.Status ?? ''
}

export function jobDiscoverySource(job: CompatJob): string {
  return job.discovery_source ?? job.DiscoverySource ?? ''
}

export function jobFailureCause(job: CompatJob): string {
  return job.failure_cause ?? job.FailureCause ?? ''
}

export function jobPipelinePhase(job: CompatJob): string {
  return job.pipeline_phase ?? job.PipelinePhase ?? ''
}

export function jobFilterKey(job: CompatJob): Exclude<JobFilter, 'all'> | null {
  const status = jobStatus(job)
  if (
    status === 'compatible' ||
    status === 'processing' ||
    status === 'processed' ||
    status === 'failed' ||
    status === 'restored' ||
    status === 'ignored'
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

export function filterJobs<T extends CompatJob>(jobs: T[], filter: JobFilter, keyword: string): T[] {
  const normalizedKeyword = keyword.trim().toLowerCase()
  return jobs.filter((job) => {
    const matchesFilter = filter === 'all' || jobFilterKey(job) === filter
    const matchesKeyword = !normalizedKeyword || jobPath(job).toLowerCase().includes(normalizedKeyword)
    return matchesFilter && matchesKeyword
  })
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
