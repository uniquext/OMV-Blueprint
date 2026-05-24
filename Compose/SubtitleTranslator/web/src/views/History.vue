<template>
  <div class="history">
    <!-- 统计汇总 -->
    <div class="history-summary">
      <span>✅ 完成: <strong>{{ stats.done }}</strong></span>
      <span>⏭️ 跳过: <strong>{{ stats.skipped }}</strong></span>
      <span>❌ 失败: <strong>{{ stats.failed }}</strong></span>
      <span>📈 成功率: <strong>{{ (stats.success_rate * 100).toFixed(1) }}%</strong> <span style="color:#999;font-size:11px;">(跳过不计入)</span></span>
    </div>

    <!-- 筛选栏 -->
    <div class="filter-bar">
      <div class="multi-select" ref="statusDropdownRef">
        <button class="multi-select-trigger btn" @click="toggleStatusDropdown">
          {{ statusDropdownLabel }}
          <span class="arrow" :class="{ open: statusDropdownOpen }">▼</span>
        </button>
        <div class="multi-select-panel" v-if="statusDropdownOpen">
          <label v-for="opt in statusOptions" :key="opt.value" class="multi-select-option">
            <input type="checkbox" :value="opt.value" v-model="filterStatusList" />
            <span>{{ opt.label }}</span>
          </label>
        </div>
      </div>
      <select v-model="filterFunnelLevel">
        <option value="">全部漏斗级别</option>
        <option value="-1">Level -1 (无可用字幕)</option>
        <option value="0">Level 0 (已有中文)</option>
        <option value="1">Level 1 (繁体字幕)</option>
        <option value="2">Level 2 (外置字幕)</option>
        <option value="3">Level 3 (内嵌字幕)</option>
      </select>
      <input type="date" v-model="filterDateFrom" />
      <input type="date" v-model="filterDateTo" />
    </div>

    <!-- 任务列表 -->
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th style="width:40px"></th>
            <th>媒体文件</th>
            <th>状态</th>
            <th>漏斗级别</th>
            <th>创建时间</th>
            <th>翻译耗时</th>
            <th>耗时</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="(task, idx) in tasks" :key="task.id">
            <!-- 主行 -->
            <tr>
              <td>
                <button class="btn expand-btn" @click="toggleDetail(idx)">
                  {{ expandedRows[idx] ? '▼' : '▶' }}
                </button>
              </td>
              <td class="path-cell" :title="task.path">{{ task.path }}</td>
              <td><span class="status-tag" :class="task.status">{{ statusLabel(task.status) }}</span></td>
              <td>Level {{ task.level }}</td>
              <td>{{ task.created_at }}</td>
              <td>
                <template v-if="task.translate_duration === null || task.translate_duration === undefined">
                  --
                </template>
                <template v-else-if="task.translate_duration < 0">
                  <span class="anomaly-text error-text" style="color: #d03050; font-weight: 500;">异常数据</span>
                </template>
                <template v-else>
                  {{ task.translate_duration }}s
                </template>
              </td>
              <td>{{ task.duration }}</td>
            </tr>
            <!-- 详情展开行 -->
            <tr v-if="expandedRows[idx]" class="detail-row open">
              <td :colspan="7">
                <div class="detail-content">
                  <div class="field"><span class="k">Job ID:</span><span class="v">{{ task.id }}</span></div>
                  <div class="field"><span class="k">状态:</span><span class="v"><span class="status-tag" :class="task.status">{{ statusLabel(task.status) }}</span></span></div>
                  <div class="field" v-if="task.levelDesc"><span class="k">{{ task.status === 'skipped' ? '跳过原因:' : '漏斗级别:' }}</span><span class="v">Level {{ task.level }} — {{ task.levelDesc }}</span></div>
                  <div class="field"><span class="k">任务来源:</span><span class="v">{{ sourceLabel(task.source) }}</span></div>
                  <div class="field"><span class="k">创建时间:</span><span class="v">{{ task.created_at }}</span></div>
                  <div class="field"><span class="k">完成时间:</span><span class="v" :class="{ 'placeholder-text': !task.completed_at }">{{ task.completed_at || '--' }}</span></div>
                  <div class="field full-width"><span class="k">媒体路径:</span><span class="v">{{ task.path }}</span></div>
                  <div class="field full-width">
                    <span class="k">结果字幕:</span>
                    <span class="v" :class="{ 'placeholder-text': !task.output_srt }">{{ task.output_srt ? basename(task.output_srt) : '--' }}</span>
                  </div>
                  <div class="field full-width" v-if="task.error">
                    <span class="k">错误信息:</span>
                    <span
                      class="v error-text"
                      :class="{ 'error-clickable': task.status === 'failed' && task.translate_task_id }"
                      @click="task.status === 'failed' && task.translate_task_id && openDiagnostics(task.translate_task_id)"
                    >{{ task.error }}</span>
                  </div>
                </div>
              </td>
            </tr>
          </template>
          <tr v-if="tasks.length === 0">
            <td colspan="7" style="text-align:center;color:#999;padding:24px;">暂无历史记录</td>
          </tr>
        </tbody>
      </table>
      <div class="pagination" v-if="totalPages > 1">
        <button :disabled="currentPage === 1" @click="changePage(currentPage - 1)">上一页</button>
        <button v-for="p in pageNumbers" :key="p" :class="{active: p === currentPage}" @click="changePage(p)">
          {{ p }}
        </button>
        <button :disabled="currentPage === totalPages" @click="changePage(currentPage + 1)">下一页</button>
      </div>
    </div>

    <!-- LLM Diagnostics 模态框 -->
    <div class="diag-overlay" v-if="diagVisible" @click.self="closeDiagnostics">
      <div class="diag-modal">
        <div class="diag-header">
          <span class="diag-title">LLM 诊断信息</span>
          <button class="diag-close" @click="closeDiagnostics">&times;</button>
        </div>
        <div class="diag-body">
          <div v-if="diagLoading" style="text-align:center;padding:24px;color:#999;">加载中...</div>
          <div v-else-if="diagErrors.length === 0" style="text-align:center;padding:24px;color:#999;">无可用的诊断数据</div>
          <div v-else class="diag-collapse">
            <div v-for="(err, eidx) in diagErrors" :key="eidx" class="diag-panel">
              <div class="diag-panel-header" @click="toggleDiagPanel(eidx)">
                <span>{{ diagPanelExpanded[eidx] ? '▼' : '▶' }} Batch #{{ err.batch_idx }} — {{ err.error_reason }}</span>
              </div>
              <div v-if="diagPanelExpanded[eidx]" class="diag-panel-body">
                <!-- MetaData 标签 -->
                <div class="diag-meta" v-if="err.llm_settings">
                  <span class="diag-tag" v-if="err.llm_settings.model">Model: {{ err.llm_settings.model }}</span>
                  <span class="diag-tag" v-if="err.llm_settings.temperature !== undefined">Temperature: {{ err.llm_settings.temperature }}</span>
                </div>
                <!-- 可折叠的 System Prompt -->
                <div class="diag-section" v-if="err.system_prompt">
                  <div class="diag-section-header" @click="toggleDiagSection(`${eidx}-sys`)">
                    <span>{{ diagSectionExpanded[`${eidx}-sys`] ? '▼' : '▶' }} System Prompt</span>
                  </div>
                  <div v-if="diagSectionExpanded[`${eidx}-sys`]" class="diag-section-content">
                    <pre>{{ err.system_prompt }}</pre>
                  </div>
                </div>
                <!-- 可折叠的 Glossary -->
                <div class="diag-section" v-if="err.glossary">
                  <div class="diag-section-header" @click="toggleDiagSection(`${eidx}-glo`)">
                    <span>{{ diagSectionExpanded[`${eidx}-glo`] ? '▼' : '▶' }} Glossary</span>
                  </div>
                  <div v-if="diagSectionExpanded[`${eidx}-glo`]" class="diag-section-content">
                    <pre>{{ typeof err.glossary === 'string' ? err.glossary : JSON.stringify(err.glossary, null, 2) }}</pre>
                  </div>
                </div>
                <!-- User Prompt vs LLM Response 对比区域 -->
                <div class="diag-compare">
                  <div class="diag-compare-item" v-if="err.user_prompt">
                    <div class="diag-compare-label">User Prompt</div>
                    <pre class="diag-compare-code">{{ err.user_prompt }}</pre>
                  </div>
                  <div class="diag-compare-item">
                    <div class="diag-compare-label">LLM Response</div>
                    <pre class="diag-compare-code" v-if="err.llm_response">{{ err.llm_response }}</pre>
                    <div class="diag-compare-placeholder" v-else>无响应数据（可能为网络超时）</div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 日志查看器 -->
    <div style="margin-top:20px;">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px;">
        <div style="display:flex;align-items:center;gap:8px;">
          <span style="font-size:14px;font-weight:500;">📄 运行日志查看器</span>
          <span style="font-size:11px;color:#999;">/api/logs</span>
        </div>
        <div style="display:flex;align-items:center;gap:8px;">
          <select v-model="logLinesCount" @change="fetchLogsData" style="padding:2px 8px;font-size:11px;border-radius:4px;border:1px solid #d0d0d6;background:#fff;outline:none;">
            <option :value="50">50 行</option>
            <option :value="100">100 行</option>
            <option :value="200">200 行</option>
            <option :value="500">500 行</option>
          </select>
          <button class="btn" @click="fetchLogsData" style="padding:2px 8px;font-size:11px;">刷新</button>
        </div>
      </div>
      <div class="log-viewer" ref="logViewerRef">
        <div class="log-line" v-for="(log, idx) in logEntries" :key="idx">
          <span class="ts">{{ log.time }} </span>
          <span :class="{'lvl-info': log.level==='INFO', 'lvl-warn': log.level==='WARN' || log.level==='WARNING', 'lvl-err': log.level==='ERROR'}">[{{ log.level }}] </span>
          <span class="msg">{{ log.message }}</span>
        </div>
        <div v-if="logEntries.length === 0" style="color:#6c7086;text-align:center;padding:12px;">暂无日志记录</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { getJobs, getJobsStats, getLogs, getTaskErrors } from '../api/history'

const stats = reactive({
  done: 0,
  skipped: 0,
  failed: 0,
  success_rate: 0.0
})

const tasks = ref([])
const totalCount = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

const statusOptions = [
  { value: 'funneling', label: '🔍 漏斗分析' },
  { value: 'extracting', label: '📤 字幕提取' },
  { value: 'translating', label: '🌐 翻译中' },
  { value: 'rebuilding', label: '🔨 字幕重建' },
  { value: 'done', label: '✅ 完成' },
  { value: 'skipped', label: '⏭️ 跳过' },
  { value: 'failed', label: '❌ 失败' }
]

const filterStatusList = ref([])
const filterFunnelLevel = ref('')
const filterDateFrom = ref('')
const filterDateTo = ref('')

const statusDropdownOpen = ref(false)
const statusDropdownRef = ref(null)

function toggleStatusDropdown() {
  statusDropdownOpen.value = !statusDropdownOpen.value
}

const statusDropdownLabel = computed(() => {
  if (filterStatusList.value.length === 0) return '全部状态'
  if (filterStatusList.value.length === statusOptions.length) return '全部状态'
  const labels = filterStatusList.value.map(v => {
    const opt = statusOptions.find(o => o.value === v)
    return opt ? opt.label.replace(/^[^\s]+\s/, '') : v
  })
  return labels.join(', ')
})

function handleClickOutsideStatusDropdown(e) {
  if (statusDropdownRef.value && !statusDropdownRef.value.contains(e.target)) {
    statusDropdownOpen.value = false
  }
}

const expandedRows = reactive({})
const logContent = ref('')
const logLinesCount = ref(100)
const logViewerRef = ref(null)

let autoRefreshTimer = null

function scrollLogToBottom() {
  nextTick(() => {
    if (logViewerRef.value) {
      logViewerRef.value.scrollTop = logViewerRef.value.scrollHeight
    }
  })
}

watch(logContent, () => {
  scrollLogToBottom()
})

function toggleDetail(idx) {
  expandedRows[idx] = !expandedRows[idx]
}

function statusLabel(status) {
  const map = { done: '完成', skipped: '跳过', failed: '失败', translating: '翻译中', funneling: '漏斗分析', extracting: '字幕提取', rebuilding: '字幕重建' }
  return map[status] || status
}

function sourceLabel(source) {
  const map = { scheduler: '定时扫描', watchdog: '文件监控', notify: 'API 通知', scan: '手动扫描', startup: '启动恢复' }
  return map[source] || source || '--'
}

// ============ LLM Diagnostics 模态框 ============

const diagVisible = ref(false)
const diagLoading = ref(false)
const diagErrors = ref([])
const diagPanelExpanded = reactive({})
const diagSectionExpanded = reactive({})

function toggleDiagPanel(idx) {
  diagPanelExpanded[idx] = !diagPanelExpanded[idx]
}

function toggleDiagSection(key) {
  diagSectionExpanded[key] = !diagSectionExpanded[key]
}

async function openDiagnostics(taskId) {
  diagVisible.value = true
  diagLoading.value = true
  diagErrors.value = []
  // 重置展开状态
  Object.keys(diagPanelExpanded).forEach(k => delete diagPanelExpanded[k])
  Object.keys(diagSectionExpanded).forEach(k => delete diagSectionExpanded[k])

  try {
    const data = await getTaskErrors(taskId)
    diagErrors.value = data || []
    // 默认展开第一个面板
    if (diagErrors.value.length > 0) {
      diagPanelExpanded[0] = true
    }
  } catch (err) {
    console.error('Failed to fetch diagnostics:', err)
    diagErrors.value = []
  } finally {
    diagLoading.value = false
  }
}

function closeDiagnostics() {
  diagVisible.value = false
}

// ============ 日志解析 ============

// 自动解析后端返回的原始日志字符串为 UI 终端日志条目
const logEntries = computed(() => {
  if (!logContent.value) return []
  return logContent.value.split('\n').filter(line => line.trim()).map(line => {
    let level = 'INFO'
    if (line.includes('ERROR') || line.includes('failed') || line.includes('Exception') || line.includes('CRITICAL')) level = 'ERROR'
    else if (line.includes('WARNING') || line.includes('WARN')) level = 'WARN'

    let time = ''
    let msg = line

    // 正则提取时间戳和消息，匹配标准 Python logging 格式："2026-05-20 15:23:57,892 - module - LEVEL - message"
    const match = line.match(/^(\d{4}-\d{2}-\d{2}\s\d{2}:\d{2}:\d{2},\d{3})\s*-\s*\w+\s*-\s*([A-Z]+)\s*-\s*(.*)$/)
    if (match) {
      time = match[1]
      level = match[2]
      msg = match[3]
    }
    return { time, level, message: msg }
  })
})

async function fetchStats() {
  try {
    const data = await getJobsStats()
    stats.done = data.done || 0
    stats.skipped = data.skipped || 0
    stats.failed = data.failed || 0
    stats.success_rate = data.success_rate !== undefined ? data.success_rate : 0.0
  } catch (err) {
    console.error('Failed to fetch stats:', err)
  }
}

async function fetchJobs() {
  try {
    const statusParam = filterStatusList.value.length > 0 ? filterStatusList.value : null
    const data = await getJobs({
      page: currentPage.value,
      page_size: pageSize.value,
      status: statusParam,
      funnel_level: filterFunnelLevel.value !== '' ? parseInt(filterFunnelLevel.value) : null,
      date_from: filterDateFrom.value ? `${filterDateFrom.value}T00:00:00` : null,
      date_to: filterDateTo.value ? `${filterDateTo.value}T23:59:59` : null
    })

    tasks.value = (data.items || []).map(item => {
      let duration = '-'
      if (item.created_at && item.completed_at) {
        const start = new Date(item.created_at)
        const end = new Date(item.completed_at)
        const diffMs = end - start
        if (diffMs > 0) {
          const diffSec = Math.floor(diffMs / 1000)
          if (diffSec < 60) duration = `${diffSec}s`
          else duration = `${Math.floor(diffSec / 60)}m ${diffSec % 60}s`
        }
      }

      let levelDesc = ''
      const lvl = item.funnel_level
      if (lvl === 0) levelDesc = '存在中文字幕（直接跳过）'
      else if (lvl === 1) levelDesc = '存在繁体中文字幕（仅做繁简转换）'
      else if (lvl === 2) levelDesc = '外部英/日文字幕翻译（LLM）'
      else if (lvl === 3) levelDesc = '内嵌英/日文字幕提取并翻译（ffmpeg + LLM）'
      else if (lvl === -1) levelDesc = '图形字幕、ffprobe 报错或无可用字幕轨道'

      return {
        id: item.id,
        path: item.media_path,
        status: item.status,
        level: lvl !== null && lvl !== undefined ? lvl : '-',
        levelDesc,
        source: item.source || 'scheduler',
        created_at: item.created_at ? new Date(item.created_at).toLocaleString() : '-',
        completed_at: item.completed_at ? new Date(item.completed_at).toLocaleString() : null,
        original_srt: item.original_srt_path,
        output_srt: item.output_srt_path,
        error: item.error,
        translate_task_id: item.translate_task_id,
        translate_duration: item.translate_duration,
        duration
      }
    })

    totalCount.value = data.total || 0
    // 清空展开行的缓存
    for (const key in expandedRows) {
      expandedRows[key] = false
    }
  } catch (err) {
    console.error('Failed to fetch jobs:', err)
  }
}

async function fetchLogsData() {
  try {
    const data = await getLogs(logLinesCount.value)
    logContent.value = data.content || ''
  } catch (err) {
    console.error('Failed to fetch logs:', err)
  }
}

function handleSearch() {
  currentPage.value = 1
  fetchJobs()
}

// 筛选条件变化时自动搜索
watch([filterStatusList, filterFunnelLevel, filterDateFrom, filterDateTo], () => {
  handleSearch()
})

function changePage(page) {
  if (page < 1 || page > totalPages.value) return
  currentPage.value = page
  fetchJobs()
}

const totalPages = computed(() => Math.ceil(totalCount.value / pageSize.value))

const pageNumbers = computed(() => {
  const pages = []
  const maxPagesToShow = 5
  let start = Math.max(1, currentPage.value - 2)
  let end = Math.min(totalPages.value, start + maxPagesToShow - 1)

  if (end - start + 1 < maxPagesToShow) {
    start = Math.max(1, end - maxPagesToShow + 1)
  }

  for (let i = start; i <= end; i++) {
    pages.push(i)
  }
  return pages
})

onMounted(() => {
  fetchStats()
  fetchJobs()
  fetchLogsData()

  autoRefreshTimer = setInterval(() => {
    fetchStats()
    fetchLogsData()
  }, 10000)

  document.addEventListener('click', handleClickOutsideStatusDropdown)
})

onUnmounted(() => {
  if (autoRefreshTimer) {
    clearInterval(autoRefreshTimer)
  }
  document.removeEventListener('click', handleClickOutsideStatusDropdown)
})
function basename(path) {
  if (!path) return ''
  const parts = path.split(/[/\\]/)
  return parts.pop()
}
</script>

<style scoped>
.filter-bar { display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; align-items: center; }
.filter-bar select, .filter-bar input { padding: 6px 12px; border: 1px solid #d0d0d6; border-radius: 6px; font-size: 13px; background: #fff; outline: none; }
.filter-bar select:focus, .filter-bar input:focus { border-color: #18a058; }

.multi-select { position: relative; }
.multi-select-trigger { display: flex; align-items: center; gap: 6px; padding: 6px 12px; font-size: 13px; white-space: nowrap; }
.multi-select-trigger .arrow { font-size: 10px; transition: transform 0.2s; }
.multi-select-trigger .arrow.open { transform: rotate(180deg); }
.multi-select-panel { position: absolute; top: 100%; left: 0; z-index: 100; margin-top: 4px; background: #fff; border: 1px solid #d0d0d6; border-radius: 6px; box-shadow: 0 4px 12px rgba(0,0,0,0.1); padding: 6px 0; min-width: 180px; }
.multi-select-option { display: flex; align-items: center; gap: 8px; padding: 6px 12px; cursor: pointer; font-size: 13px; }
.multi-select-option:hover { background: #f5f5f8; }
.multi-select-option input[type="checkbox"] { accent-color: #18a058; }

.history-summary { background: #fff; border-radius: 8px; padding: 14px 16px; border: 1px solid #e8e8ec; margin-bottom: 16px; display: flex; gap: 24px; font-size: 13px; }
.history-summary span { color: #666; }
.history-summary strong { color: #333; }

/* 表格 */
.table-wrap { background: #fff; border-radius: 8px; border: 1px solid #e8e8ec; overflow: hidden; }
table { width: 100%; border-collapse: collapse; font-size: 13px; }
thead th { background: #fafafa; padding: 10px 14px; text-align: left; font-weight: 500; color: #666; border-bottom: 1px solid #e8e8ec; font-size: 12px; }
tbody td { padding: 10px 14px; border-bottom: 1px solid #f5f5f8; }
tbody tr:hover { background: #fafbfc; }
tbody tr:last-child td { border-bottom: none; }

.path-cell { max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* 展开按钮 */
.expand-btn { padding: 2px 6px; font-size: 11px; }

/* 状态标签 */
.status-tag { font-size: 11px; padding: 2px 8px; border-radius: 4px; font-weight: 500; }
.status-tag.done { background: #e8f8ef; color: #18a058; }
.status-tag.skipped { background: #f5f5f8; color: #999; }
.status-tag.failed { background: #fde8ec; color: #d03050; }
.status-tag.translating { background: #e8f0fe; color: #2080f0; }
.status-tag.funneling { background: #fdf6ec; color: #f0a020; }
.status-tag.extracting { background: #fdf6ec; color: #e0a010; }
.status-tag.rebuilding { background: #faf0fc; color: #a020f0; }

/* 来源标签 */
.source-tag { font-size: 11px; background: #f0f0f4; padding: 2px 6px; border-radius: 3px; color: #888; }

/* 详情展开行 */
.detail-row td { background: #fafbfc; padding: 12px 20px !important; }
.detail-content { display: grid; grid-template-columns: 1fr 1fr; gap: 8px 24px; font-size: 12px; }
.detail-content .field { display: flex; gap: 8px; }
.detail-content .field.full-width { grid-column: span 2; }
.detail-content .field .k { color: #999; min-width: 80px; }
.detail-content .field .v { color: #555; word-break: break-all; }
.detail-content .field .v.error-text { color: #d03050; }
.detail-content .field .v.error-clickable { cursor: pointer; text-decoration: underline dotted; }
.detail-content .field .v.error-clickable:hover { color: #b02040; }
.detail-content .field .v.placeholder-text { color: #bbb; }

/* 分页 */
.pagination { display: flex; justify-content: center; align-items: center; gap: 4px; padding: 14px; }
.pagination button { padding: 4px 10px; border: 1px solid #d0d0d6; border-radius: 4px; background: #fff; cursor: pointer; font-size: 12px; }
.pagination button:hover { border-color: #18a058; color: #18a058; }
.pagination button.active { background: #18a058; color: #fff; border-color: #18a058; }
.pagination button:disabled { opacity: 0.4; cursor: not-allowed; }

/* ============ LLM Diagnostics 模态框 ============ */
.diag-overlay { position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.45); z-index: 1000; display: flex; align-items: center; justify-content: center; }
.diag-modal { background: #fff; border-radius: 10px; width: 94%; max-width: 1200px; max-height: 85vh; display: flex; flex-direction: column; box-shadow: 0 8px 32px rgba(0,0,0,0.18); }
.diag-header { display: flex; align-items: center; justify-content: space-between; padding: 14px 20px; border-bottom: 1px solid #e8e8ec; }
.diag-title { font-size: 15px; font-weight: 600; color: #333; }
.diag-close { background: none; border: none; font-size: 22px; cursor: pointer; color: #999; padding: 0 4px; line-height: 1; }
.diag-close:hover { color: #333; }
.diag-body { padding: 16px 20px; overflow-y: auto; flex: 1; }

/* 折叠面板 */
.diag-panel { border: 1px solid #e8e8ec; border-radius: 6px; margin-bottom: 10px; overflow: hidden; }
.diag-panel-header { padding: 10px 14px; background: #fafafa; cursor: pointer; font-size: 13px; font-weight: 500; color: #333; user-select: none; }
.diag-panel-header:hover { background: #f0f0f4; }
.diag-panel-body { padding: 14px; }

/* MetaData 标签 */
.diag-meta { display: flex; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; }
.diag-tag { display: inline-block; padding: 2px 10px; border-radius: 4px; font-size: 12px; background: #e8f0fe; color: #2080f0; font-weight: 500; }

/* 可折叠区域 */
.diag-section { margin-bottom: 10px; border: 1px solid #f0f0f4; border-radius: 4px; overflow: hidden; }
.diag-section-header { padding: 8px 12px; background: #fafafa; cursor: pointer; font-size: 12px; font-weight: 500; color: #666; user-select: none; }
.diag-section-header:hover { background: #f0f0f4; }
.diag-section-content { padding: 10px 12px; }
.diag-section-content pre { margin: 0; white-space: pre-wrap; word-break: break-all; font-size: 12px; color: #555; font-family: "SF Mono", "Fira Code", monospace; line-height: 1.6; background: #f8f8fa; padding: 8px; border-radius: 4px; max-height: 200px; overflow-y: auto; }

/* 对比区域：左右栏 */
.diag-compare { display: flex; gap: 12px; margin-top: 12px; }
.diag-compare-item { flex: 1; min-width: 0; }
.diag-compare-label { font-size: 12px; font-weight: 600; color: #666; margin-bottom: 4px; }
.diag-compare-code { margin: 0; white-space: pre-wrap; word-break: break-all; font-size: 12px; color: #333; font-family: "SF Mono", "Fira Code", monospace; line-height: 1.6; background: #1e1e2e; color: #cdd6f4; padding: 10px; border-radius: 6px; max-height: 300px; overflow-y: auto; }
.diag-compare-placeholder { font-size: 12px; color: #999; font-style: italic; padding: 10px; background: #f8f8fa; border-radius: 6px; }

/* 日志查看器 */
.log-viewer { background: #1e1e2e; border-radius: 8px; padding: 14px; max-height: 260px; overflow-y: auto; font-family: "SF Mono", "Fira Code", monospace; font-size: 12px; line-height: 1.7; }
.log-line { color: #a0a8b8; }
.log-line .ts { color: #6c7086; }
.log-line .lvl-info { color: #89b4fa; }
.log-line .lvl-warn { color: #f9e2af; }
.log-line .lvl-err { color: #f38ba8; }
.log-line .msg { color: #cdd6f4; }
</style>
