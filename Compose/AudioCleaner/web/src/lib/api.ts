import type {
  ApiEnvelope,
  AudioCleanerConfig,
  ConfigModuleName,
  DiscoveryStatus,
  FileRecord,
  HistoryRecord,
  MediaDirectoryListing,
  OperationStatusResponse,
  PageRequest,
  PageResult,
  RestoreResult,
  RuntimeLogResponse,
  RuntimeTasksSnapshot,
  ScanResponse,
  ScanSession,
  ScanStartResponse,
  ServiceStatus,
  SettingsView,
  ModuleUpdateResult
} from './types'

type CollectionResponse<T> = T[] | Partial<PageResult<T>> | null | undefined

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init)
  const payload = (await response.json()) as Partial<ApiEnvelope<T>>
  const message = payload.message || response.statusText || 'request failed'

  if (!response.ok || payload.code !== 0) {
    throw new ApiError(message, response.status, payload.error_code, payload.data)
  }

  return payload.data as T
}

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly code: number,
    public readonly errorCode?: string,
    public readonly data?: unknown
  ) {
    super(message)
    this.name = 'ApiError'
  }
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

function jsonPatch(body: unknown, revision?: string): RequestInit {
  return {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      ...(revision ? { 'If-Match': revision } : {})
    },
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
  config: () => request<SettingsView>('/api/config'),
  configDefaults: () => request<SettingsView>('/api/config/defaults'),
  mediaDirectories: (parent: string) => {
    const params = new URLSearchParams({ parent })
    return request<MediaDirectoryListing>(`/api/media/directories?${params.toString()}`)
  },
  patchConfig: <M extends ConfigModuleName>(module: M, revision: string, config: AudioCleanerConfig[M]) =>
    request<ModuleUpdateResult>(
      `/api/config/${module}`,
      jsonPatch(module === 'audio' ? { incompatible_codecs: (config as AudioCleanerConfig['audio']).incompatible_codecs } : config, revision)
    ),
  health: () => request<{ status: string }>('/api/health'),
  status: () => request<ServiceStatus>('/api/status'),
  runtimeTasks: () => request<RuntimeTasksSnapshot>('/api/runtime-tasks'),
  runtimeLogs: (lines = 100) => request<RuntimeLogResponse>(`/api/logs?lines=${encodeURIComponent(String(lines))}`),
  historyPage: async (page?: PageRequest) =>
    unwrapPage(await request<CollectionResponse<HistoryRecord>>(withPage('/api/history', page)), page),
	retryHistoryJob: (id: number) => request<OperationStatusResponse>(`/api/history/${id}/retry`, post()),
	ignoreHistoryJob: (id: number) => request<OperationStatusResponse>(`/api/history/${id}/ignore`, post()),
	filesPage: async (page?: PageRequest) =>
		unwrapPage(await request<CollectionResponse<FileRecord>>(withPage('/api/files', page)), page),
		processFile: (id: number) => request<ScanResponse>(`/api/files/${id}/process`, post()),
		startScan: () => request<ScanStartResponse>('/api/scans', post()),
		currentScan: () => request<ScanSession>('/api/scans/current'),
		cancelScan: (id: string) => request<ScanSession>(`/api/scans/${encodeURIComponent(id)}`, { method: 'DELETE' }),
		discoveryStatus: () => request<DiscoveryStatus>('/api/discovery-status'),
	restoreFileBackup: (id: number) => request<RestoreResult>(`/api/files/${id}/restore-backup`, post()),
	deleteFileBackup: (id: number) => request<OperationStatusResponse>(`/api/files/${id}/backup`, { method: 'DELETE' }),
}
