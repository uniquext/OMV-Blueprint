<script setup lang="ts">
import { LoaderCircle, Play, RotateCcw, Trash2 } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DataPager from '../components/DataPager.vue'
import OverflowMarquee from '../components/OverflowMarquee.vue'
import RowActionMenu from '../components/RowActionMenu.vue'
import { eventStreamState } from '../composables/useEventStream'
import { t } from '../i18n'
import { api } from '../lib/api'
import { shouldRefreshMediaFiles } from '../lib/mediaFileRefresh'
import {
  assessmentActionKey,
  assessmentReasonKey,
  assessmentSourceKey,
  audioPlanSummary,
  audioTrackSummary,
  ruleMatchSummary
} from '../lib/compatibility'
import { semanticBadgeClass } from '../lib/semantic'
import { formatServiceTimestamp } from '../lib/time'
import type { CompatibilityAssessment, FileRecord } from '../lib/types'

type ComplianceFilter = 'all' | 'compliant' | 'noncompliant' | 'unknown'
type BackupFilter = 'all' | 'pending' | 'none'

const filePageFetchSize = 1000
const allFiles = ref<FileRecord[]>([])
const runtimePaths = ref<Set<string>>(new Set())
const page = ref(1)
const pageSize = ref(20)
const expandedFileIDs = ref<Set<number>>(new Set())
const loading = ref(false)
const operationPending = ref(false)
const error = ref('')
const confirmAction = ref<'process' | 'restore' | 'delete' | null>(null)
const selectedFile = ref<FileRecord | null>(null)
const complianceFilter = ref<ComplianceFilter>('all')
const backupFilter = ref<BackupFilter>('all')
const keyword = ref('')
const openFilter = ref<'compliance' | 'backup' | null>(null)
const openActionFileID = ref<number | null>(null)
const filesRoot = ref<HTMLElement | null>(null)
let loadSequence = 0
let loadScheduled = false

const complianceFilters: ComplianceFilter[] = ['all', 'compliant', 'noncompliant', 'unknown']
const backupFilters: BackupFilter[] = ['all', 'pending', 'none']
const busy = computed(() => loading.value || operationPending.value)

function assessmentReason(assessment: CompatibilityAssessment): string {
  const key = assessmentReasonKey(assessment)
  return key ? t(key) : assessment.reason || t('filesEmptyValue')
}
const filteredFiles = computed(() => {
  const query = keyword.value.trim().toLocaleLowerCase()
  return allFiles.value.filter((file) => {
    const complianceMatches =
      complianceFilter.value === 'all' ||
      (complianceFilter.value === 'unknown'
        ? !['compliant', 'noncompliant'].includes(file.compliance_status)
        : file.compliance_status === complianceFilter.value)
    const backupMatches =
      backupFilter.value === 'all' ||
      (backupFilter.value === 'pending' ? Boolean(file.backup_file) : !file.backup_file)
    const keywordMatches = !query || file.path.toLocaleLowerCase().includes(query) || String(file.id).includes(query)
    return complianceMatches && backupMatches && keywordMatches
  })
})
const files = computed(() =>
  filteredFiles.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value)
)
const total = computed(() => filteredFiles.value.length)
const summary = computed(() => ({
  total: allFiles.value.length,
  compliant: allFiles.value.filter((file) => file.compliance_status === 'compliant').length,
  noncompliant: allFiles.value.filter((file) => file.compliance_status === 'noncompliant').length,
  undetermined: allFiles.value.filter((file) => !['compliant', 'noncompliant'].includes(file.compliance_status)).length,
  unresolvedBackups: allFiles.value.filter((file) => Boolean(file.backup_file)).length
}))
const complianceFilterLabel = computed(() => t(`filesComplianceFilter${complianceFilter.value}`))
const backupFilterLabel = computed(() => t(`filesBackupFilter${backupFilter.value}`))
const confirmOpen = computed(() => confirmAction.value !== null)
const confirmTitle = computed(() => {
  if (confirmAction.value === 'process') return t('filesConfirmProcessTitle')
  if (confirmAction.value === 'restore') return t('filesConfirmRestoreTitle')
  return t('filesConfirmDeleteTitle')
})
const confirmMessage = computed(() => {
  const file = selectedFile.value
  if (confirmAction.value === 'process') return `${t('filesConfirmProcessMessage')} ${file?.path || ''}`
  const message = confirmAction.value === 'restore' ? t('filesConfirmRestoreMessage') : t('filesConfirmDeleteMessage')
  return `${message} ${file?.backup_file || ''}`
})

function pageCountFor(nextTotal: number): number {
  return Math.max(1, Math.ceil(nextTotal / pageSize.value))
}

function clampPage(): void {
  page.value = Math.min(page.value, pageCountFor(total.value))
}

function scheduleLoad(): void {
  if (loadScheduled) return
  loadScheduled = true
  void nextTick(async () => {
    loadScheduled = false
    await loadFiles()
  })
}

async function loadAllFiles(): Promise<FileRecord[]> {
  const firstPage = await api.filesPage({ page: 1, page_size: filePageFetchSize })
  const items = [...firstPage.items]
  const responsePageSize = Math.max(1, firstPage.page_size || filePageFetchSize)
  const pageCount = Math.ceil(firstPage.total / responsePageSize)
  for (let requestPage = 2; requestPage <= pageCount; requestPage += 1) {
    const result = await api.filesPage({ page: requestPage, page_size: filePageFetchSize })
    items.push(...result.items)
  }
  return items
}

async function loadFiles(): Promise<void> {
  const sequence = ++loadSequence
  openActionFileID.value = null
  loading.value = true
  error.value = ''
  try {
    const [result, runtime] = await Promise.all([
      loadAllFiles(),
      api.runtimeTasks().catch(() => null)
    ])
    if (sequence !== loadSequence) return
    allFiles.value = result
    if (runtime) {
      runtimePaths.value = new Set([...runtime.waiting_tasks, ...runtime.active_tasks].map((task) => task.path))
    }
    clampPage()
  } catch (caught) {
    if (sequence !== loadSequence) return
    allFiles.value = []
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    if (sequence === loadSequence) loading.value = false
  }
}

function toggleMenu(menu: 'compliance' | 'backup'): void {
  openActionFileID.value = null
  openFilter.value = openFilter.value === menu ? null : menu
}

function toggleActionMenu(fileID: number): void {
  openFilter.value = null
  openActionFileID.value = openActionFileID.value === fileID ? null : fileID
}

function selectComplianceFilter(value: ComplianceFilter): void {
  complianceFilter.value = value
  openFilter.value = null
}

function selectBackupFilter(value: BackupFilter): void {
  backupFilter.value = value
  openFilter.value = null
}

function closeFilterOnOutside(event: MouseEvent): void {
  const target = event.target
  if (!(target instanceof Node) || filesRoot.value?.contains(target)) return
  openFilter.value = null
}

function isExpanded(file: FileRecord): boolean {
  return expandedFileIDs.value.has(file.id)
}

function toggleFile(file: FileRecord): void {
  const next = new Set(expandedFileIDs.value)
  if (next.has(file.id)) next.delete(file.id)
  else next.add(file.id)
  expandedFileIDs.value = next
}

function askOperation(action: 'process' | 'restore' | 'delete', file: FileRecord): void {
  openActionFileID.value = null
  selectedFile.value = file
  confirmAction.value = action
}

function closeConfirm(): void {
  confirmAction.value = null
  selectedFile.value = null
}

async function confirmOperation(): Promise<void> {
  if (operationPending.value || !selectedFile.value || !confirmAction.value) return
  const file = selectedFile.value
  const action = confirmAction.value
  closeConfirm()
  operationPending.value = true
  error.value = ''
  try {
    if (action === 'process') {
      await api.processFile(file.id)
      runtimePaths.value = new Set(runtimePaths.value).add(file.path)
    } else if (action === 'restore') {
      await api.restoreFileBackup(file.id)
    } else {
      await api.deleteFileBackup(file.id)
    }
    await loadFiles()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    operationPending.value = false
  }
}

function canProcess(file: FileRecord): boolean {
  return file.compliance_status === 'noncompliant' && !file.backup_file
}

function isProcessing(file: FileRecord): boolean {
  return runtimePaths.value.has(file.path)
}

function complianceLabel(status: string): string {
  if (status === 'compliant') return t('filesComplianceCompliant')
  if (status === 'noncompliant') return t('filesComplianceNoncompliant')
  return t('filesComplianceUnknown')
}

function complianceTone(status: string): string {
  if (status === 'compliant') return semanticBadgeClass('success')
  if (status === 'noncompliant') return semanticBadgeClass('warning')
  return semanticBadgeClass('muted')
}

function backupTone(file: FileRecord): string {
  return semanticBadgeClass(file.backup_file ? 'warning' : 'muted')
}

function backupStatusLabel(file: FileRecord): string {
  return file.backup_file ? t('filesBackupPending') : t('filesBackupNone')
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) return t('filesEmptyValue')
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024
    index += 1
  }
  return `${amount.toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

function fileName(path: string): string {
  return path.split('/').filter(Boolean).pop() || path
}

function directory(path: string): string {
  const index = path.lastIndexOf('/')
  return index > 0 ? path.slice(0, index) : '/'
}

function displayTimestamp(value: string): string {
  return formatServiceTimestamp(value, true) || t('filesEmptyValue')
}

watch(pageSize, () => {
  openActionFileID.value = null
  if (page.value !== 1) page.value = 1
  else clampPage()
})
watch([complianceFilter, backupFilter, keyword], () => {
  openActionFileID.value = null
  if (page.value !== 1) page.value = 1
  else clampPage()
})
watch(
  () => eventStreamState.lastEvent,
  (event) => {
    if (shouldRefreshMediaFiles(event)) scheduleLoad()
  }
)

onMounted(() => {
  void loadFiles()
  document.addEventListener('click', closeFilterOnOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', closeFilterOnOutside)
})
</script>

<template>
  <div ref="filesRoot">
    <div class="panel">
      <div class="history-toolbar files-toolbar">
        <div class="history-summary files-summary">
          <span><span class="files-summary-icon" aria-hidden="true">📁</span> {{ t('filesSummaryTotal') }}: <strong data-testid="files-summary-total">{{ summary.total }}</strong></span>
          <span><span class="files-summary-icon" aria-hidden="true">✅</span> {{ t('filesSummaryCompliant') }}: <strong>{{ summary.compliant }}</strong></span>
          <span><span class="files-summary-icon" aria-hidden="true">❌</span> {{ t('filesSummaryNoncompliant') }}: <strong>{{ summary.noncompliant }}</strong></span>
          <span><span class="files-summary-icon" aria-hidden="true">❓</span> {{ t('filesSummaryUndetermined') }}: <strong>{{ summary.undetermined }}</strong></span>
          <span><span class="files-summary-icon" aria-hidden="true">💾</span> {{ t('filesSummaryUnresolvedBackups') }}: <strong>{{ summary.unresolvedBackups }}</strong></span>
        </div>

        <div class="history-filter-bar files-filter-bar">
          <div class="history-filter-menu">
            <button
              class="history-filter-control history-filter-trigger"
              type="button"
              :aria-expanded="openFilter === 'compliance'"
              :aria-label="t('filesFilterCompliance')"
              @click="toggleMenu('compliance')"
            >
              <span>{{ complianceFilterLabel }}</span>
            </button>
            <div v-if="openFilter === 'compliance'" class="history-filter-popover">
              <button
                v-for="item in complianceFilters"
                :key="item"
                class="history-filter-option"
                :class="{ 'history-filter-option--selected': complianceFilter === item }"
                type="button"
                @click="selectComplianceFilter(item)"
              >
                <span class="history-filter-check" aria-hidden="true"></span>
                <span>{{ t(`filesComplianceFilter${item}`) }}</span>
              </button>
            </div>
          </div>

          <div class="history-filter-menu">
            <button
              class="history-filter-control history-filter-trigger"
              type="button"
              :aria-expanded="openFilter === 'backup'"
              :aria-label="t('filesFilterBackup')"
              @click="toggleMenu('backup')"
            >
              <span>{{ backupFilterLabel }}</span>
            </button>
            <div v-if="openFilter === 'backup'" class="history-filter-popover">
              <button
                v-for="item in backupFilters"
                :key="item"
                class="history-filter-option"
                :class="{ 'history-filter-option--selected': backupFilter === item }"
                type="button"
                @click="selectBackupFilter(item)"
              >
                <span class="history-filter-check" aria-hidden="true"></span>
                <span>{{ t(`filesBackupFilter${item}`) }}</span>
              </button>
            </div>
          </div>

          <input
            v-model="keyword"
            class="history-filter-control history-filter-keyword files-filter-keyword"
            type="search"
            :placeholder="t('filesSearchPlaceholder')"
            :aria-label="t('filesFilterKeyword')"
          />
        </div>
      </div>

      <div v-if="error" class="alert error">
        <strong>{{ t('filesErrorLabel') }}</strong>
        <span>{{ error }}</span>
      </div>

      <div v-if="loading" class="panel-body muted">{{ t('filesLoading') }}</div>

      <div class="data-table-scroll">
        <table class="data-table history-table files-table">
        <thead>
          <tr>
            <th></th>
            <th class="history-id-heading">ID</th>
            <th>{{ t('filesColumnFile') }}</th>
            <th>{{ t('filesColumnCompliance') }}</th>
            <th>{{ t('filesColumnBackup') }}</th>
            <th class="date-cell">{{ t('filesColumnUpdated') }}</th>
            <th class="actions-cell files-actions-cell">{{ t('filesColumnActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && files.length === 0">
            <td colspan="7" class="muted">{{ t('filesNoData') }}</td>
          </tr>
          <template v-for="file in files" :key="file.id">
            <tr data-testid="file-row">
              <td class="history-expand-cell">
                <button
                  class="button history-expand-button"
                  type="button"
                  :aria-label="isExpanded(file) ? t('filesCollapseDetails') : t('filesExpandDetails')"
                  @click="toggleFile(file)"
                >
                  {{ isExpanded(file) ? '▼' : '▶' }}
                </button>
              </td>
              <td class="history-id-cell">{{ file.id }}</td>
              <td class="path-cell file-column-cell">
                <OverflowMarquee class="history-path-main" :text="fileName(file.path)" />
                <OverflowMarquee class="history-path-sub" :text="directory(file.path)" />
              </td>
              <td class="status-cell">
                <span :class="complianceTone(file.compliance_status)">{{ complianceLabel(file.compliance_status) }}</span>
              </td>
              <td class="status-cell" :title="file.backup_file">
                <span :class="backupTone(file)">{{ backupStatusLabel(file) }}</span>
              </td>
              <td class="date-cell">{{ displayTimestamp(file.updated_at) }}</td>
              <td class="actions-cell files-actions-cell">
                <RowActionMenu
                  :open="openActionFileID === file.id"
                  :label="t('rowActionsMore')"
                  :width="132"
                  :test-id="`file-actions-${file.id}`"
                  @toggle="toggleActionMenu(file.id)"
                  @close="openActionFileID = null"
                >
                  <button
                    class="row-action-item"
                    role="menuitem"
                    type="button"
                    :data-testid="`file-process-${file.id}`"
                    :disabled="busy || !canProcess(file) || isProcessing(file)"
                    @click="askOperation('process', file)"
                  >
                    <LoaderCircle v-if="isProcessing(file)" :size="16" aria-hidden="true" />
                    <Play v-else :size="16" aria-hidden="true" />
                    <span>{{ isProcessing(file) ? t('filesProcessing') : t('filesProcess') }}</span>
                  </button>
                  <button
                    class="row-action-item"
                    role="menuitem"
                    type="button"
                    :data-testid="`file-restore-${file.id}`"
                    :disabled="busy || !file.backup_file"
                    @click="askOperation('restore', file)"
                  >
                    <RotateCcw :size="16" aria-hidden="true" />
                    <span>{{ t('filesRestore') }}</span>
                  </button>
                  <button
                    class="row-action-item row-action-item--danger"
                    role="menuitem"
                    type="button"
                    :data-testid="`file-delete-${file.id}`"
                    :disabled="busy || !file.backup_file"
                    @click="askOperation('delete', file)"
                  >
                    <Trash2 :size="16" aria-hidden="true" />
                    <span>{{ t('filesDeleteBackup') }}</span>
                  </button>
                </RowActionMenu>
              </td>
            </tr>

            <tr v-if="isExpanded(file)" class="history-detail-row" data-testid="file-detail-row">
              <td class="history-detail-cell" colspan="7">
                <div class="history-detail-grid files-detail-grid">
                  <div class="files-detail-column">
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailID') }}:</span>
                      <span class="history-detail-value">{{ file.id }}</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailMtimeNS') }}:</span>
                      <span class="history-detail-value">{{ file.mtime_ns }}</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailAudioPolicyVersion') }}:</span>
                      <span class="history-detail-value">{{ file.audio_policy_version || t('filesEmptyValue') }}</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailUpdatedAt') }}:</span>
                      <span class="history-detail-value">{{ displayTimestamp(file.updated_at) }}</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailPath') }}:</span>
                      <span class="history-detail-value">{{ file.path }}</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailBackupFile') }}:</span>
                      <span class="history-detail-value">{{ file.backup_file || t('filesEmptyValue') }}</span>
                    </div>
                  </div>
                  <div class="files-detail-column">
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailSize') }}:</span>
                      <span class="history-detail-value">{{ file.size }} ({{ formatBytes(file.size) }})</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailComplianceStatus') }}:</span>
                      <span class="history-detail-value">{{ complianceLabel(file.compliance_status) }}</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailCreatedAt') }}:</span>
                      <span class="history-detail-value">{{ displayTimestamp(file.created_at) }}</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailAudioSignature') }}:</span>
                      <span class="history-detail-value">{{ file.audio_signature || t('filesEmptyValue') }}</span>
                    </div>
                    <div class="history-detail-field">
                      <span class="history-detail-key">{{ t('filesDetailVideoSignature') }}:</span>
                      <span class="history-detail-value">{{ file.video_signature || t('filesEmptyValue') }}</span>
                    </div>
                  </div>
                </div>
                <section
                  v-if="file.compatibility_assessment"
                  class="compatibility-assessment"
                  data-testid="compatibility-assessment"
                >
                  <header class="compatibility-assessment-head">
                    <h3>{{ t('filesAssessmentTitle') }}</h3>
                    <span :class="complianceTone(file.compliance_status)">
                      {{ t(assessmentActionKey(file.compatibility_assessment)) }}
                    </span>
                  </header>
                  <dl class="compatibility-assessment-facts">
                    <div>
                      <dt>{{ t('filesAssessmentSource') }}</dt>
                      <dd>{{ t(assessmentSourceKey(file.compatibility_assessment)) }}</dd>
                    </div>
                    <div>
                      <dt>{{ t('filesAssessmentPolicy') }}</dt>
                      <dd>v{{ file.compatibility_assessment.policy_version }}</dd>
                    </div>
                    <div class="compatibility-assessment-reason">
                      <dt>{{ t('filesAssessmentReason') }}</dt>
                      <dd>{{ assessmentReason(file.compatibility_assessment) }}</dd>
                    </div>
                  </dl>
                  <div class="compatibility-assessment-columns">
                    <div>
                      <h4>{{ t('filesAssessmentTracks') }}</h4>
                      <ul v-if="file.compatibility_assessment.audio_tracks.length">
                        <li v-for="track in file.compatibility_assessment.audio_tracks" :key="track.stream_index">
                          {{ audioTrackSummary(track) }}
                        </li>
                      </ul>
                      <p v-else class="muted">{{ t('filesAssessmentNoTracks') }}</p>
                    </div>
                    <div>
                      <h4>{{ t('filesAssessmentRules') }}</h4>
                      <ul v-if="file.compatibility_assessment.matched_rules.length">
                        <li v-for="rule in file.compatibility_assessment.matched_rules" :key="rule.codec">
                          {{ ruleMatchSummary(rule) }}
                        </li>
                      </ul>
                      <p v-else class="muted">{{ t('filesAssessmentNoRules') }}</p>
                    </div>
                    <div>
                      <h4>{{ t('filesAssessmentPlan') }}</h4>
                      <ul v-if="file.compatibility_assessment.audio_plans.length">
                        <li v-for="plan in file.compatibility_assessment.audio_plans" :key="plan.stream_index">
                          {{ audioPlanSummary(plan) }}
                        </li>
                      </ul>
                      <p v-else class="muted">{{ t('filesAssessmentNoPlan') }}</p>
                    </div>
                  </div>
                </section>
                <div v-else class="compatibility-assessment-missing muted" data-testid="compatibility-assessment-missing">
                  {{ t('filesAssessmentMissing') }}
                </div>
              </td>
            </tr>
          </template>
        </tbody>
        </table>
      </div>

      <DataPager
        :page="page"
        :page-size="pageSize"
        :total="total"
        @update:page="page = $event"
        @update:page-size="pageSize = $event"
      />
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
