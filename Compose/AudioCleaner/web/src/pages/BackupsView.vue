<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DataPager from '../components/DataPager.vue'
import { backupAvailabilityLabel, t } from '../i18n'
import { api } from '../lib/api'
import {
  backupAvailability,
  backupID,
  backupPath,
  backupRestoredAt,
  backupsRequestPlan,
  filterBackups,
  loadAllBackupPages,
  type BackupFilter
} from '../lib/backups'
import { backupAvailabilityTone, semanticBadgeClass } from '../lib/semantic'
import { formatServiceTimestamp } from '../lib/time'
import type { BackupRecord } from '../lib/types'

const backups = ref<BackupRecord[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const filter = ref<BackupFilter>('all')
const keyword = ref('')
const loading = ref(false)
const operationPending = ref(false)
const error = ref('')
const confirmAction = ref<'restore' | 'cleanup' | null>(null)
const selectedBackup = ref<BackupRecord | null>(null)
let loadSequence = 0
let loadScheduled = false
let suppressNextPageLoad = false

const filters: BackupFilter[] = ['all', 'available', 'missing', 'restored', 'expired']
const busy = computed(() => loading.value || operationPending.value)
const confirmOpen = computed(() => confirmAction.value !== null)
const confirmTitle = computed(() =>
  confirmAction.value === 'cleanup' ? t('backupsConfirmCleanupTitle') : t('backupsConfirmRestoreTitle')
)
const confirmMessage = computed(() => {
  if (confirmAction.value === 'cleanup') {
    return t('backupsConfirmCleanupMessage')
  }
  return `${t('backupsConfirmRestoreMessage')} ${selectedBackup.value ? backupPath(selectedBackup.value) : ''}`
})

function pageCountFor(nextTotal: number): number {
  return Math.max(1, Math.ceil(nextTotal / pageSize.value))
}

function clampPage(nextTotal: number, suppressReload: boolean): boolean {
  const nextPage = Math.min(page.value, pageCountFor(nextTotal))
  if (nextPage === page.value) {
    return false
  }
  suppressNextPageLoad = suppressReload
  page.value = nextPage
  return true
}

function scheduleLoad(): void {
  if (loadScheduled) {
    return
  }
  loadScheduled = true
  void nextTick(async () => {
    loadScheduled = false
    await loadBackups()
  })
}

function isLatestLoad(sequence: number): boolean {
  return sequence === loadSequence
}

async function loadBackups(): Promise<void> {
  const sequence = ++loadSequence
  loading.value = true
  error.value = ''
  try {
    const plan = backupsRequestPlan(page.value, pageSize.value, filter.value, keyword.value)
    const result = plan.localPagination
      ? await loadAllBackupPages(plan.pageSize, (requestPage, requestPageSize) =>
          api.backupsPage({ page: requestPage, page_size: requestPageSize })
        )
      : await api.backupsPage({ page: plan.page, page_size: plan.pageSize })
    if (!isLatestLoad(sequence)) {
      return
    }

    if (plan.localPagination) {
      const filtered = filterBackups(result.items, filter.value, keyword.value)
      total.value = filtered.length
      clampPage(filtered.length, true)
      backups.value = filtered.slice((page.value - 1) * pageSize.value, page.value * pageSize.value)
    } else {
      total.value = result.total
      if (clampPage(result.total, false)) {
        backups.value = []
        return
      }
      backups.value = result.items
    }
  } catch (caught) {
    if (!isLatestLoad(sequence)) {
      return
    }
    error.value = caught instanceof Error ? caught.message : String(caught)
    backups.value = []
    total.value = 0
  } finally {
    if (isLatestLoad(sequence)) {
      loading.value = false
    }
  }
}

function askRestore(record: BackupRecord): void {
  selectedBackup.value = record
  confirmAction.value = 'restore'
}

function askCleanupExpired(): void {
  confirmAction.value = 'cleanup'
}

function closeConfirm(): void {
  confirmAction.value = null
  selectedBackup.value = null
}

async function confirmOperation(): Promise<void> {
  if (operationPending.value) {
    return
  }
  const action = confirmAction.value
  const record = selectedBackup.value
  closeConfirm()
  error.value = ''
  operationPending.value = true
  try {
    if (action === 'cleanup') {
      await api.cleanupExpired()
    } else if (action === 'restore' && record) {
      await api.restore(backupID(record))
    }
    await loadBackups()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    operationPending.value = false
  }
}

function updatePageSize(value: number): void {
  pageSize.value = value
}

function backupStateDisplay(record: BackupRecord): string {
  return backupAvailabilityLabel(backupAvailability(record))
}

function backupStateBadgeClass(record: BackupRecord): string {
  return semanticBadgeClass(backupAvailabilityTone(backupAvailability(record)))
}

function emptyValue(value: string): string {
  return value || t('backupsEmptyValue')
}

function backupDate(value: string): string {
  return emptyValue(formatServiceTimestamp(value, true))
}

watch(page, () => {
  if (suppressNextPageLoad) {
    suppressNextPageLoad = false
    return
  }
  scheduleLoad()
})
watch(pageSize, () => {
  if (page.value !== 1) {
    page.value = 1
    return
  }
  scheduleLoad()
})
watch([filter, keyword], () => {
  if (page.value !== 1) {
    page.value = 1
    return
  }
  scheduleLoad()
})

onMounted(loadBackups)
</script>

<template>
  <div>
    <div class="panel">
      <div class="jobs-toolbar">
        <label class="field">
          <span>{{ t('backupsStatusFilter') }}</span>
          <select v-model="filter">
            <option v-for="item in filters" :key="item" :value="item">{{ t(`backupFilter${item}`) }}</option>
          </select>
        </label>
        <label class="field field--wide">
          <span>{{ t('backupsPathSearch') }}</span>
          <input v-model="keyword" type="search" :placeholder="t('backupsPathSearchPlaceholder')" />
        </label>
        <div class="actions">
          <button class="button primary" type="button" :disabled="busy" @click="askCleanupExpired">
            {{ t('backupsCleanupExpired') }}
          </button>
        </div>
      </div>

      <div v-if="error" class="alert error">
        <strong>{{ t('backupsErrorLabel') }}</strong>
        <span>{{ error }}</span>
      </div>

      <div v-if="loading" class="panel-body muted">{{ t('backupsLoading') }}</div>

      <table class="data-table">
        <thead>
          <tr>
            <th>{{ t('backupsColumnOriginalPath') }}</th>
            <th>{{ t('backupsColumnBackupPath') }}</th>
            <th>{{ t('tableStatus') }}</th>
            <th>{{ t('backupsColumnCreated') }}</th>
            <th>{{ t('backupsColumnRestored') }}</th>
            <th>{{ t('backupsColumnActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && backups.length === 0">
            <td colspan="6" class="muted">{{ t('backupsNoData') }}</td>
          </tr>
          <tr v-for="backup in backups" :key="backupID(backup)">
            <td class="path-cell" :title="backupPath(backup)">{{ backupPath(backup) }}</td>
            <td class="path-cell" :title="backup.backup_path">{{ backup.backup_path }}</td>
            <td class="status-cell">
              <span :class="backupStateBadgeClass(backup)">{{ backupStateDisplay(backup) }}</span>
            </td>
            <td class="date-cell">{{ backupDate(backup.created_at) }}</td>
            <td class="date-cell">{{ backupDate(backupRestoredAt(backup)) }}</td>
            <td class="actions-cell">
              <div class="row-actions">
                <button class="button" type="button" :disabled="busy" @click="askRestore(backup)">
                  {{ t('backupsRestore') }}
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>

      <DataPager :page="page" :page-size="pageSize" :total="total" @update:page="page = $event" @update:page-size="updatePageSize" />
    </div>

    <ConfirmDialog
      :open="confirmOpen"
      :title="confirmTitle"
      :message="confirmMessage"
      :confirm-text="t('confirmConfirm')"
      :cancel-text="t('confirmCancel')"
      @confirm="confirmOperation"
      @cancel="closeConfirm"
    />
  </div>
</template>
