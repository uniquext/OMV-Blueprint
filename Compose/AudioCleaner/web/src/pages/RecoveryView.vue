<script setup lang="ts">
import {
  AlertTriangle,
  ArchiveRestore,
  ChevronDown,
  ChevronRight,
  Copy,
  Files,
  Filter,
  GitBranch,
  RefreshCw,
  Search,
  ShieldCheck,
  Trash2
} from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DataPager from '../components/DataPager.vue'
import OverflowMarquee from '../components/OverflowMarquee.vue'
import RowActionMenu from '../components/RowActionMenu.vue'
import { t } from '../i18n'
import { api } from '../lib/api'
import { recoveryActionAllowed, type RecoveryAction } from '../lib/failureRecovery'
import { semanticBadgeClass } from '../lib/semantic'
import type { RecoveryItem } from '../lib/types'

type RecoveryFilter = 'all' | 'manual' | 'journal' | 'backup'
type RecoveryCommand = Exclude<RecoveryAction, 'retry'>

const items = ref<RecoveryItem[]>([])
const page = ref(1)
const pageSize = ref(20)
const expandedItemIDs = ref<Set<number>>(new Set())
const loading = ref(false)
const pending = ref<number | null>(null)
const error = ref('')
const selectedItem = ref<RecoveryItem | null>(null)
const selectedAction = ref<RecoveryCommand | null>(null)
const recoveryFilter = ref<RecoveryFilter>('all')
const keyword = ref('')
const openFilter = ref(false)
const openActionFileID = ref<number | null>(null)
const recoveryRoot = ref<HTMLElement | null>(null)

const recoveryFilters: RecoveryFilter[] = ['all', 'manual', 'journal', 'backup']
const confirmOpen = computed(() => selectedAction.value !== null)
const busy = computed(() => loading.value || pending.value !== null)
const filterLabel = computed(() => t(`recoveryFilter${recoveryFilter.value}`))
const summary = computed(() => ({
  total: items.value.length,
  manual: items.value.filter((item) => recoveryKind(item) === 'manual').length,
  journal: items.value.filter((item) => recoveryKind(item) === 'journal').length,
  backup: items.value.filter((item) => recoveryKind(item) === 'backup').length
}))
const filteredItems = computed(() => {
  const query = keyword.value.trim().toLocaleLowerCase()
  return items.value.filter((item) => {
    const kindMatches = recoveryFilter.value === 'all' || recoveryKind(item) === recoveryFilter.value
    const text = [item.path, String(item.file_id), item.issue?.code, item.issue?.summary, item.journal?.phase]
      .filter(Boolean)
      .join(' ')
      .toLocaleLowerCase()
    return kindMatches && (!query || text.includes(query))
  })
})
const visibleItems = computed(() => filteredItems.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
const total = computed(() => filteredItems.value.length)
const confirmTitle = computed(() => {
  if (selectedAction.value === 'restore') return t('recoveryConfirmRestoreTitle')
  if (selectedAction.value === 'retain') return t('recoveryConfirmRetainTitle')
  return t('recoveryConfirmDeleteTitle')
})
const confirmMessage = computed(() => {
  const item = selectedItem.value
  if (!item) return ''
  if (selectedAction.value === 'restore') return `${item.path}\n${t('recoveryConfirmRestoreMessage')}\n${item.backup_path}`
  if (selectedAction.value === 'retain') return `${item.path}\n${t('recoveryConfirmRetainMessage')}`
  return `${item.path}\n${t('recoveryConfirmDeleteMessage')}\n${item.backup_path}`
})

function recoveryKind(item: RecoveryItem): Exclude<RecoveryFilter, 'all'> {
  if (item.issue?.category === 'recovery' || item.journal?.phase === 'manual_recovery') return 'manual'
  if (item.journal) return 'journal'
  return 'backup'
}

function recoveryKindLabel(item: RecoveryItem): string {
  return t(`recoveryKind${recoveryKind(item)}`)
}

function recoveryKindTone(item: RecoveryItem): string {
  const kind = recoveryKind(item)
  return semanticBadgeClass(kind === 'manual' ? 'danger' : kind === 'journal' ? 'warning' : 'info')
}

function candidateCount(item: RecoveryItem): number {
  return [item.original_exists, item.backup_exists, item.output_exists].filter(Boolean).length
}

function candidateSummary(item: RecoveryItem): string {
  const expected = 1 + Number(Boolean(item.backup_path)) + Number(Boolean(item.output_path))
  return t('recoveryCandidateSummary').replace('{available}', String(candidateCount(item))).replace('{expected}', String(expected))
}

function candidateTone(item: RecoveryItem): string {
  const expected = 1 + Number(Boolean(item.backup_path)) + Number(Boolean(item.output_path))
  return semanticBadgeClass(candidateCount(item) === expected ? 'success' : 'warning')
}

function fileName(path: string): string {
  return path.split('/').filter(Boolean).pop() || path
}

function directory(path: string): string {
  const index = path.lastIndexOf('/')
  return index > 0 ? path.slice(0, index) : '/'
}

function pageCountFor(nextTotal: number): number {
  return Math.max(1, Math.ceil(nextTotal / pageSize.value))
}

function clampPage(): void {
  page.value = Math.min(page.value, pageCountFor(total.value))
}

function resetPageOrClamp(): void {
  openActionFileID.value = null
  if (page.value !== 1) {
    page.value = 1
    return
  }
  clampPage()
}

function isExpanded(item: RecoveryItem): boolean {
  return expandedItemIDs.value.has(item.file_id)
}

function toggleItem(item: RecoveryItem): void {
  const next = new Set(expandedItemIDs.value)
  if (next.has(item.file_id)) next.delete(item.file_id)
  else next.add(item.file_id)
  expandedItemIDs.value = next
}

function toggleFilter(): void {
  openActionFileID.value = null
  openFilter.value = !openFilter.value
}

function selectFilter(filter: RecoveryFilter): void {
  recoveryFilter.value = filter
  openFilter.value = false
}

function toggleActionMenu(item: RecoveryItem): void {
  openFilter.value = false
  openActionFileID.value = openActionFileID.value === item.file_id ? null : item.file_id
}

function closeFilterOnOutside(event: MouseEvent): void {
  const target = event.target
  if (!(target instanceof Node) || recoveryRoot.value?.contains(target)) return
  openFilter.value = false
}

async function load(): Promise<void> {
  openActionFileID.value = null
  loading.value = true
  error.value = ''
  try {
    items.value = await api.recovery()
    clampPage()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
    items.value = []
  } finally {
    loading.value = false
  }
}

function askAction(item: RecoveryItem, action: RecoveryCommand): void {
  if (pending.value !== null) return
  openActionFileID.value = null
  selectedItem.value = item
  selectedAction.value = action
  error.value = ''
}

function closeConfirm(): void {
  selectedItem.value = null
  selectedAction.value = null
}

async function confirmAction(): Promise<void> {
  if (pending.value !== null || !selectedItem.value || !selectedAction.value) return
  const item = selectedItem.value
  const action = selectedAction.value
  closeConfirm()
  pending.value = item.file_id
  error.value = ''
  try {
    if (action === 'restore') await api.restoreRecovery(item.file_id)
    else if (action === 'delete') await api.deleteRecoveryBackup(item.file_id)
    else await api.retainRecovery(item.file_id)
    await load()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    pending.value = null
  }
}

watch(page, clampPage)
watch([pageSize, recoveryFilter, keyword], resetPageOrClamp)

onMounted(() => {
  void load()
  document.addEventListener('click', closeFilterOnOutside)
})

onBeforeUnmount(() => document.removeEventListener('click', closeFilterOnOutside))
</script>

<template>
  <div ref="recoveryRoot">
    <div class="panel">
      <div class="history-toolbar recovery-toolbar">
        <div class="history-summary recovery-summary" aria-live="polite">
          <span class="recovery-summary-item"><span class="recovery-summary-icon recovery-summary-icon--total" aria-hidden="true"><Files :size="16" /></span><strong data-ui="recovery-summary-total">{{ summary.total }}</strong> {{ t('recoverySummaryTotal') }}</span>
          <span class="recovery-summary-item"><span class="recovery-summary-icon recovery-summary-icon--manual" aria-hidden="true"><AlertTriangle :size="16" /></span><strong>{{ summary.manual }}</strong> {{ t('recoverySummaryManual') }}</span>
          <span class="recovery-summary-item"><span class="recovery-summary-icon recovery-summary-icon--journal" aria-hidden="true"><GitBranch :size="16" /></span><strong>{{ summary.journal }}</strong> {{ t('recoverySummaryJournal') }}</span>
          <span class="recovery-summary-item"><span class="recovery-summary-icon recovery-summary-icon--backup" aria-hidden="true"><Copy :size="16" /></span><strong>{{ summary.backup }}</strong> {{ t('recoverySummaryBackup') }}</span>
        </div>

        <div class="history-filter-bar recovery-filter-bar">
          <div class="history-filter-menu">
            <button class="history-filter-control history-filter-trigger" type="button" :aria-expanded="openFilter" :aria-label="t('recoveryFilterKind')" @click="toggleFilter">
              <Filter :size="15" class="recovery-filter-icon" aria-hidden="true" />
              <span>{{ filterLabel }}</span>
            </button>
            <div v-if="openFilter" class="history-filter-popover">
              <button v-for="filter in recoveryFilters" :key="filter" class="history-filter-option" :class="{ 'history-filter-option--selected': recoveryFilter === filter }" type="button" @click="selectFilter(filter)">
                <span class="history-filter-check" aria-hidden="true"></span>
                <span>{{ t(`recoveryFilter${filter}`) }}</span>
              </button>
            </div>
          </div>
          <label class="recovery-search-field">
            <Search :size="15" class="recovery-search-icon" aria-hidden="true" />
            <input v-model="keyword" class="history-filter-control history-filter-keyword recovery-filter-keyword" type="search" :placeholder="t('recoverySearchPlaceholder')" :aria-label="t('recoveryFilterKeyword')" />
          </label>
          <button class="icon-button" type="button" :title="t('recoveryRefresh')" :aria-label="t('recoveryRefresh')" :disabled="busy" @click="load">
            <RefreshCw :size="18" aria-hidden="true" />
          </button>
        </div>
      </div>

      <div v-if="error" class="alert error"><strong>{{ t('recoveryErrorLabel') }}</strong><span>{{ error }}</span></div>
      <div v-if="loading" class="panel-body muted">{{ t('recoveryLoading') }}</div>

      <div class="data-table-scroll">
        <table class="data-table history-table recovery-table">
          <thead><tr><th></th><th class="history-id-heading">ID</th><th>{{ t('filesColumnFile') }}</th><th>{{ t('recoveryColumnKind') }}</th><th>{{ t('recoveryColumnCandidates') }}</th><th>{{ t('recoveryColumnRecommendation') }}</th><th class="actions-cell recovery-actions-cell">{{ t('jobsColumnActions') }}</th></tr></thead>
          <tbody>
            <tr v-if="!loading && visibleItems.length === 0"><td colspan="7" class="muted recovery-empty-cell">{{ t('recoveryNoData') }}</td></tr>
            <template v-for="item in visibleItems" :key="item.file_id">
              <tr data-ui="recovery-row">
                <td class="history-expand-cell"><button class="button history-expand-button recovery-expand-button" type="button" :aria-expanded="isExpanded(item)" :aria-label="isExpanded(item) ? t('recoveryCollapseDetails') : t('recoveryExpandDetails')" @click="toggleItem(item)"><ChevronDown v-if="isExpanded(item)" :size="15" aria-hidden="true" /><ChevronRight v-else :size="15" aria-hidden="true" /></button></td>
                <td class="history-id-cell">{{ item.file_id }}</td>
                <td class="path-cell file-column-cell"><OverflowMarquee class="history-path-main" :text="fileName(item.path)" /><OverflowMarquee class="history-path-sub" :text="directory(item.path)" /></td>
                <td class="status-cell"><span :class="recoveryKindTone(item)">{{ recoveryKindLabel(item) }}</span></td>
                <td class="status-cell"><span :class="candidateTone(item)">{{ candidateSummary(item) }}</span></td>
                <td class="recovery-recommendation-cell"><OverflowMarquee :text="item.recommendation" /></td>
                <td class="actions-cell recovery-actions-cell">
                  <RowActionMenu :open="openActionFileID === item.file_id" :label="t('rowActionsMore')" :width="144" :ui-id="`recovery-actions-${item.file_id}`" @toggle="toggleActionMenu(item)" @close="openActionFileID = null">
                    <button v-if="recoveryActionAllowed(item.actions, 'restore')" class="row-action-item" role="menuitem" type="button" :data-ui="`recovery-restore-${item.file_id}`" :disabled="busy" @click="askAction(item, 'restore')"><ArchiveRestore :size="16" aria-hidden="true" /><span>{{ t('recoveryRestore') }}</span></button>
                    <button v-if="recoveryActionAllowed(item.actions, 'retain')" class="row-action-item" role="menuitem" type="button" :data-ui="`recovery-retain-${item.file_id}`" :disabled="busy" @click="askAction(item, 'retain')"><ShieldCheck :size="16" aria-hidden="true" /><span>{{ t('recoveryRetain') }}</span></button>
                    <button v-if="recoveryActionAllowed(item.actions, 'delete')" class="row-action-item row-action-item--danger" role="menuitem" type="button" :data-ui="`recovery-delete-${item.file_id}`" :disabled="busy" @click="askAction(item, 'delete')"><Trash2 :size="16" aria-hidden="true" /><span>{{ t('recoveryDelete') }}</span></button>
                  </RowActionMenu>
                </td>
              </tr>

              <tr v-if="isExpanded(item)" class="history-detail-row" data-ui="recovery-detail-row">
                <td class="history-detail-cell" colspan="7">
                  <div class="history-detail-grid recovery-detail-grid">
                    <div class="history-detail-field"><span class="history-detail-key">{{ t('recoveryDetailFileID') }}:</span><span class="history-detail-value">{{ item.file_id }}</span></div>
                    <div class="history-detail-field"><span class="history-detail-key">{{ t('recoveryDetailKind') }}:</span><span class="history-detail-value"><span :class="recoveryKindTone(item)">{{ recoveryKindLabel(item) }}</span></span></div>
                    <div v-if="item.issue" class="history-detail-field"><span class="history-detail-key">{{ t('recoveryDetailFailureStage') }}:</span><span class="history-detail-value">{{ item.issue.stage }}</span></div>
                    <div v-if="item.journal" class="history-detail-field"><span class="history-detail-key">{{ t('recoveryDetailJournalPhase') }}:</span><span class="history-detail-value">{{ item.journal.phase }}</span></div>
                    <div class="history-detail-field history-detail-field--wide"><span class="history-detail-key">{{ t('recoveryDetailOriginalPath') }}:</span><span class="history-detail-value">{{ item.path }} · {{ item.original_exists ? t('recoveryCandidatePresent') : t('recoveryCandidateMissing') }}</span></div>
                    <div class="history-detail-field history-detail-field--wide"><span class="history-detail-key">{{ t('recoveryDetailRecommendation') }}:</span><span class="history-detail-value">{{ item.recommendation }}</span></div>
                    <div class="history-detail-field history-detail-field--wide"><span class="history-detail-key">{{ t('recoveryDetailRisk') }}:</span><span class="history-detail-value recovery-risk-value">{{ item.risk }}</span></div>
                    <div v-if="item.issue" class="history-detail-field history-detail-field--wide"><span class="history-detail-key">{{ t('recoveryDetailFailure') }}:</span><span class="history-detail-value">{{ item.issue.code }} · {{ item.issue.summary }}</span></div>
                    <div v-if="item.issue" class="history-detail-field history-detail-field--wide"><span class="history-detail-key">{{ t('recoveryDetailUnlock') }}:</span><span class="history-detail-value">{{ item.issue.unlock_condition }}</span></div>

                    <section v-if="item.backup_path || item.output_path" class="recovery-detail-section history-detail-field--wide">
                      <h3>{{ t('recoveryDetailCandidates') }}</h3>
                      <dl class="recovery-candidate-list">
                        <template v-if="item.backup_path"><dt>{{ t('recoveryDetailBackup') }}</dt><dd>{{ item.backup_path }} · {{ item.backup_exists ? t('recoveryCandidatePresent') : t('recoveryCandidateMissing') }}</dd></template>
                        <template v-if="item.output_path"><dt>{{ t('recoveryDetailOutput') }}</dt><dd>{{ item.output_path }} · {{ item.output_exists ? t('recoveryCandidatePresent') : t('recoveryCandidateMissing') }}</dd></template>
                      </dl>
                    </section>

                    <section class="recovery-detail-section history-detail-field--wide">
                      <h3>{{ t('recoveryDetailAudit') }}</h3>
                      <div v-if="item.audit.length" class="recovery-audit-list"><div v-for="entry in item.audit" :key="entry.id" class="recovery-audit-entry"><span>{{ entry.created_at }}</span><strong>{{ entry.action }}</strong><span>{{ entry.result }}</span><span>{{ entry.actor }}</span></div></div>
                      <p v-else class="muted recovery-audit-empty">{{ t('recoveryAuditEmpty') }}</p>
                    </section>
                  </div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>

      <DataPager :page="page" :page-size="pageSize" :total="total" @update:page="page = $event" @update:page-size="pageSize = $event" />
    </div>

    <ConfirmDialog :open="confirmOpen" :title="confirmTitle" :message="confirmMessage" :confirm-text="t('confirmConfirm')" :cancel-text="t('confirmCancel')" :confirm-disabled="pending !== null" @confirm="confirmAction" @cancel="closeConfirm" />
  </div>
</template>
