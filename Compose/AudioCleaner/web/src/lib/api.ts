import type {
  ActionIDResponse,
  ApiEnvelope,
  AudioCleanerConfig,
  BackupPatchResponse,
  BackupRecord,
  CleanupExpiredResponse,
  JobEventRecord,
  JobRecord,
  PageRequest,
  PageResult,
  RestoreResult,
  ScanResponse,
  ServiceStatus,
  SupportedLanguage,
  UIConfig
} from './types'

type CollectionResponse<T> = T[] | Partial<PageResult<T>> | null | undefined

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init)
  const payload = (await response.json()) as Partial<ApiEnvelope<T>>
  const message = payload.message || response.statusText || 'request failed'

  if (!response.ok || payload.code !== 0) {
    throw new Error(message)
  }

  return payload.data as T
}

export function unwrapItems<T>(value: CollectionResponse<T>): T[] {
  if (Array.isArray(value)) {
    return value
  }
  if (value && Array.isArray(value.items)) {
    return value.items
  }
  return []
}

function unwrapPage<T>(value: CollectionResponse<T>, page?: PageRequest): PageResult<T> {
  if (Array.isArray(value)) {
    return {
      items: value,
      page: page?.page ?? 1,
      page_size: page?.page_size ?? value.length,
      total: value.length
    }
  }
  return {
    items: value?.items ?? [],
    page: value?.page ?? page?.page ?? 1,
    page_size: value?.page_size ?? page?.page_size ?? value?.items?.length ?? 0,
    total: value?.total ?? value?.items?.length ?? 0
  }
}

function jsonPatch(body: unknown): RequestInit {
  return {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  }
}

function post(): RequestInit {
  return { method: 'POST' }
}

function withPage(path: string, page?: PageRequest): string {
  if (!page || (page.page === undefined && page.page_size === undefined)) {
    return path
  }

  const params = new URLSearchParams()
  if (page.page !== undefined) {
    params.set('page', String(page.page))
  }
  if (page.page_size !== undefined) {
    params.set('page_size', String(page.page_size))
  }
  return `${path}?${params.toString()}`
}

export const api = {
  config: () => request<AudioCleanerConfig>('/api/config'),
  status: () => request<ServiceStatus>('/api/status'),
  logs: async () => unwrapItems(await request<CollectionResponse<JobEventRecord>>('/api/logs/recent')),
  jobs: async (page?: PageRequest) => unwrapItems(await request<CollectionResponse<JobRecord>>(withPage('/api/jobs', page))),
  jobsPage: async (page?: PageRequest) =>
    unwrapPage(await request<CollectionResponse<JobRecord>>(withPage('/api/jobs', page)), page),
  backups: async (page?: PageRequest) =>
    unwrapItems(await request<CollectionResponse<BackupRecord>>(withPage('/api/backups', page))),
  backupsPage: async (page?: PageRequest) =>
    unwrapPage(await request<CollectionResponse<BackupRecord>>(withPage('/api/backups', page)), page),
  scan: () => request<ScanResponse>('/api/scan', post()),
  retry: (id: number) => request<ActionIDResponse>(`/api/jobs/${id}/retry`, post()),
  ignore: (id: number) => request<ActionIDResponse>(`/api/jobs/${id}/ignore`, post()),
  restore: (id: number) => request<RestoreResult>(`/api/backups/${id}/restore`, post()),
  cleanupExpired: () => request<CleanupExpiredResponse>('/api/backups/cleanup', post()),
  patchUI: (language: SupportedLanguage) => request<UIConfig>('/api/config/ui', jsonPatch({ language })),
  patchBackup: (retentionDays: number) =>
    request<BackupPatchResponse>('/api/config/backup', jsonPatch({ retention_days: retentionDays }))
}
