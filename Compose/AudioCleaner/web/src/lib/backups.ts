import type { BackupRecord, PageResult } from './types'

export type BackupFilter = 'all' | 'available' | 'missing' | 'restored' | 'expired'

export interface BackupsRequestPlan {
  page: number
  pageSize: number
  localPagination: boolean
}

type CompatBackup = Partial<BackupRecord> & {
  ID?: number
  OriginalPath?: string
  RestoredAt?: string
  ExpiresAt?: string
  Missing?: boolean
}

function normalizeOptionalTime(value: string | undefined): string {
  return value ?? ''
}

export function backupID(record: CompatBackup): number {
  return record.id ?? record.ID ?? 0
}

export function backupPath(record: CompatBackup): string {
  return record.original_path ?? record.OriginalPath ?? ''
}

export function backupRestoredAt(record: CompatBackup): string {
  return normalizeOptionalTime(record.restored_at ?? record.RestoredAt)
}

export function backupExpiresAt(record: CompatBackup): string {
  return normalizeOptionalTime(record.expires_at ?? record.ExpiresAt)
}

export function backupMissing(record: CompatBackup): boolean {
  return record.missing ?? record.Missing ?? false
}

export function backupState(record: CompatBackup, now = new Date()): Exclude<BackupFilter, 'all'> {
  if (backupMissing(record)) {
    return 'missing'
  }
  if (backupRestoredAt(record)) {
    return 'restored'
  }

  const expiresAt = backupExpiresAt(record)
  if (expiresAt && new Date(expiresAt).getTime() <= now.getTime()) {
    return 'expired'
  }

  return 'available'
}

export function filterBackups<T extends CompatBackup>(
  records: T[],
  filter: BackupFilter,
  keyword: string,
  now = new Date()
): T[] {
  const normalizedKeyword = keyword.trim().toLowerCase()
  return records.filter((record) => {
    const matchesFilter = filter === 'all' || backupState(record, now) === filter
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
