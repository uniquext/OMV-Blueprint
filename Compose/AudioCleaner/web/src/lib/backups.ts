import type { BackupAvailability, BackupRecord, PageResult } from './types'

export type BackupFilter = 'all' | 'available' | 'missing' | 'restored' | 'expired'

export interface BackupsRequestPlan {
  page: number
  pageSize: number
  localPagination: boolean
}

type BackupLike = Partial<BackupRecord>

function normalizeOptionalTime(value: string | undefined): string {
  return value ?? ''
}

export function backupID(record: BackupLike): number {
  return record.id ?? 0
}

export function backupPath(record: BackupLike): string {
  return record.original_path ?? ''
}

export function backupRestoredAt(record: BackupLike): string {
  return normalizeOptionalTime(record.restored_at)
}

export function backupAvailability(record: BackupLike): Exclude<BackupFilter, 'all'> {
  const availability = record.availability
  if (availability === 'missing' || availability === 'restored' || availability === 'expired') {
    return availability
  }
  return 'available'
}

export function filterBackups<T extends BackupLike>(
  records: T[],
  filter: BackupFilter,
  keyword: string
): T[] {
  const normalizedKeyword = keyword.trim().toLowerCase()
  return records.filter((record) => {
    const matchesFilter = filter === 'all' || backupAvailability(record) === filter
    const matchesKeyword = !normalizedKeyword || backupPath(record).toLowerCase().includes(normalizedKeyword)
    return matchesFilter && matchesKeyword
  })
}

export async function loadAllBackupPages<T>(
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

export function backupsRequestPlan(
  page: number,
  pageSize: number,
  filter: BackupFilter,
  keyword: string
): BackupsRequestPlan {
  const localPagination = filter !== 'all' || keyword.trim().length > 0
  return {
    page: localPagination ? 1 : page,
    pageSize: localPagination ? 1000 : pageSize,
    localPagination
  }
}
