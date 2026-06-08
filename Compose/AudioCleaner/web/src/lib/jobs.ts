import type { JobRecord } from './types'
import type { PageResult } from './types'

export type JobFilter = 'all' | 'processing' | 'qualified' | 'unqualified' | 'ignored' | 'restored'

export interface JobsRequestPlan {
  page: number
  pageSize: number
  localPagination: boolean
}

type CompatJob = Partial<JobRecord> & {
  ID?: number
  Path?: string
  Status?: string
  QualificationSource?: string
  UnqualifiedReason?: string
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

export function jobReason(job: CompatJob): string {
  return job.unqualified_reason ?? job.UnqualifiedReason ?? ''
}

export function jobSource(job: CompatJob): string {
  return job.qualification_source ?? job.QualificationSource ?? ''
}

export function jobStatusKey(job: CompatJob): Exclude<JobFilter, 'all'> | null {
  if (jobReason(job) === 'ignored') {
    return 'ignored'
  }
  if (jobReason(job) === 'restored' || jobSource(job) === 'restored') {
    return 'restored'
  }

  const status = jobStatus(job)
  if (status === 'qualified' || status === 'unqualified' || status === 'processing') {
    return status
  }
  return null
}

export function jobReasonLabelKey(reason: string): string | null {
  const keys: Record<string, string> = {
    failed: 'jobReasonFailed',
    unsupported: 'jobReasonUnsupported',
    ffprobe_error: 'jobReasonFfprobeError',
    verification_failed: 'jobReasonVerificationFailed',
    timeout: 'jobReasonTimeout',
    ignored: 'jobReasonIgnored',
    restored: 'jobReasonRestored'
  }
  return keys[reason] ?? null
}

export function jobSourceLabelKey(source: string): string | null {
  const keys: Record<string, string> = {
    transcoded: 'jobSourceTranscoded',
    already_compatible: 'jobSourceAlreadyCompatible',
    observed: 'jobSourceObserved',
    restored: 'jobSourceRestored'
  }
  return keys[source] ?? null
}

export function filterJobs<T extends CompatJob>(jobs: T[], filter: JobFilter, keyword: string): T[] {
  const normalizedKeyword = keyword.trim().toLowerCase()
  return jobs.filter((job) => {
    const matchesFilter = filter === 'all' || jobStatusKey(job) === filter
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
