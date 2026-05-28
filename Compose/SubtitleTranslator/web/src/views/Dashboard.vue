<template>
  <div class="dashboard">
    <!-- 时钟回拨异常警告横幅 -->
    <div
      v-if="hasAnomaly"
      id="timing-anomaly-alert"
      class="anomaly-alert-banner"
    >
      <span class="warning-icon">⚠️</span>
      <div class="alert-content">
        <strong class="alert-title">时钟回拨异常警告</strong>
        <span class="alert-reason">{{ anomalyReason }}</span>
      </div>
    </div>

    <!-- 统计卡片 -->
    <div class="stats-grid">
      <div class="stat-card">
        <div class="label">⏳ 等待中 (L1)</div>
        <div class="value orange">{{ stats.waiting }}</div>
        <div class="sub">DebounceMap</div>
      </div>
      <div class="stat-card">
        <div class="label">📋 排队中 (L2)</div>
        <div class="value blue">{{ stats.queued }}</div>
        <div class="sub">FIFO 队列</div>
      </div>
      <div class="stat-card">
        <div class="label">🔍 漏斗/提取</div>
        <div class="value orange">{{ stats.funneling }}</div>
        <div class="sub">Worker Pool</div>
      </div>
      <div class="stat-card">
        <div class="label">🌐 翻译/回写</div>
        <div class="value blue">{{ stats.translating }}</div>
        <div class="sub">LLM 翻译中</div>
      </div>
      <div class="stat-card">
        <div class="label">✅ 完成</div>
        <div class="value green">{{ stats.completed }}</div>
      </div>
      <div class="stat-card">
        <div class="label">⏭️ 跳过</div>
        <div class="value" style="color:#999">{{ stats.skipped }}</div>
        <div class="sub">不计入成功率</div>
      </div>
      <div class="stat-card">
        <div class="label">❌ 失败</div>
        <div class="value red">{{ stats.failed }}</div>
      </div>
      <div class="stat-card">
        <div class="label">📈 成功率</div>
        <div class="value green">{{ stats.successRate }}</div>
        <div class="sub">done/(done+failed)</div>
      </div>
      <div class="stat-card">
        <div class="label">⏱️ 端到端耗时</div>
        <div class="value">{{ stats.avgTime }}</div>
        <div class="sub">入队→完成</div>
      </div>
      <div class="stat-card">
        <div class="label">🪙 Token 消耗</div>
        <div class="value">{{ formatTokens(stats.totalTokens || 0) }}</div>
        <div class="sub">累计模型消耗</div>
      </div>
    </div>

    <!-- 操作栏 -->
    <div class="action-bar">
      <button class="btn btn-primary" id="btn-scan" @click="handleScan" :disabled="isScanning">
        🔍 {{ isScanning ? '扫描中...' : '全盘扫描' }}
      </button>
      <span id="scan-status" style="font-size:12px;color:#d03050;">{{ scanStatus }}</span>
    </div>

    <!-- TAB 栏 -->
    <div class="tab-bar">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        :class="['tab-btn', { active: activeTab === tab.key }]"
        @click="switchTab(tab.key)"
      >
        {{ tab.label }}
        <span class="tab-count" v-if="getTabCount(tab.key) > 0">{{ getTabCount(tab.key) }}</span>
      </button>
    </div>

    <!-- TAB 内容 -->
    <div class="tab-content">
      
      <!-- 工具栏 (仅限执行和翻译阶段有分页) -->
      <div class="tab-toolbar">
        <span class="total-info">共 {{ currentTabTotal }} 条</span>
        <select v-model="pageSize" @change="handlePageSizeChange" class="page-size-select">
          <option :value="10">10 条/页</option>
          <option :value="20">20 条/页</option>
          <option :value="50">50 条/页</option>
        </select>
      </div>

      <!-- 等待 TAB 内容 -->
      <div class="task-list" v-if="activeTab === 'wait'">
        <div class="empty-state" v-if="debounceEntries.length === 0">暂无任务</div>
        <div class="task-item" v-for="task in paginatedWaitEntries" :key="task.media_path">
          <span class="icon">📁</span>
          <span class="path" :title="task.media_path">{{ basename(task.media_path) }}</span>
          <span class="meta">剩余 {{ task.remaining_seconds }}s</span>
          <span class="meta source-info">{{ task.source }}</span>
        </div>
      </div>

      <!-- 排队 TAB 内容 -->
      <div class="task-list" v-if="activeTab === 'queue'">
        <div class="empty-state" v-if="pendingEntries.length === 0">暂无任务</div>
        <div class="task-item" v-for="(task, index) in paginatedQueueEntries" :key="task.media_path">
          <span class="queue-rank">{{ (currentPage - 1) * pageSize + index + 1 }}</span>
          <span class="path" :title="task.media_path">{{ basename(task.media_path) }}</span>
          <span class="meta source-info">{{ task.source }}</span>
        </div>
      </div>

      <!-- 执行与翻译 TAB 任务列表 -->
      <div class="task-list" v-if="(activeTab === 'execute' || activeTab === 'translate')">
        <div class="empty-state" v-if="tabData.length === 0">暂无任务</div>
        <div class="task-item" v-for="task in tabData" :key="task.id">
          <span class="icon">{{ getStatusIcon(task) }}</span>
          <span class="path" :title="task.media_path">{{ basename(task.media_path) }}</span>
          <span :class="['status-tag', getStatusClass(task)]">{{ getStatusLabel(task) }}</span>
          <!-- 来源字幕 -->
          <span class="meta source-info" v-if="task.original_srt_path" :title="task.original_srt_path">
            {{ isEmbedded(task) ? '内嵌' : '外置' }}: {{ basename(task.original_srt_path) }}
          </span>
          <!-- 结果字幕 -->
          <span class="meta result-info" v-if="task.output_srt_path">
            → {{ basename(task.output_srt_path) }}
          </span>
          <!-- 翻译进度 -->
          <template v-if="isTranslating(task) && task.task_status === 'processing'">
            <div class="progress-bar"><div class="fill" :style="{width: getProgressPct(task) + '%'}"></div></div>
            <span class="meta batch-info">{{ task.current_batch || 0 }}/{{ task.total_batches || 0 }}</span>
          </template>
          <!-- 时间 -->
          <span class="meta time-info">{{ formatTime(task.created_at) }}</span>
        </div>
      </div>

      <!-- 分页 (仅限执行和翻译阶段) -->
      <div class="pagination" v-if="totalPages > 1">
        <button :disabled="currentPage === 1" @click="changePage(currentPage - 1)">上一页</button>
        <button v-for="p in pageNumbers" :key="p" :class="{active: p === currentPage}" @click="changePage(p)">
          {{ p }}
        </button>
        <button :disabled="currentPage === totalPages" @click="changePage(currentPage + 1)">下一页</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted } from 'vue'
import { createDiscreteApi } from 'naive-ui'
import { getStats, getDebouncing, getPending, getJobsByStatuses, triggerScan } from '../api/dashboard'
import { useSSE } from '../composables/useSSE'

const { message } = createDiscreteApi(['message'])

const stats = reactive({
  waiting: 0,
  queued: 0,
  funneling: 0,
  translating: 0,
  completed: 0,
  skipped: 0,
  failed: 0,
  successRate: '0.0%',
  avgTime: '0s',
  totalTokens: 0
})

const hasAnomaly = ref(false)
const anomalyReason = ref('')

// TAB 相关状态
const tabs = [
  { key: 'wait', label: '⏳ 等待' },
  { key: 'queue', label: '📋 排队' },
  { key: 'execute', label: '🔍 执行' },
  { key: 'translate', label: '🌐 翻译' }
]

const activeTab = ref('translate')
const tabData = ref([])
const tabTotal = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

// 实时队列数据
const debounceEntries = ref([])
const pendingEntries = ref([])

const getTabCount = (key) => {
  if (key === 'wait') return debounceEntries.value.length
  if (key === 'queue') return pendingEntries.value.length
  if (key === 'execute' || key === 'translate') return activeTab.value === key ? tabTotal.value : 0
  return 0
}

const isScanning = ref(false)
const scanStatus = ref('')

const currentTabTotal = computed(() => {
  if (activeTab.value === 'wait') return debounceEntries.value.length
  if (activeTab.value === 'queue') return pendingEntries.value.length
  return tabTotal.value
})

const totalPages = computed(() => Math.ceil(currentTabTotal.value / pageSize.value))

const paginatedWaitEntries = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return debounceEntries.value.slice(start, start + pageSize.value)
})

const paginatedQueueEntries = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return pendingEntries.value.slice(start, start + pageSize.value)
})

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

// ============ 工具函数 ============

const formatSuccessRate = (rate) => {
  if (rate === undefined || rate === null) return '0.0%'
  return (rate * 100).toFixed(1) + '%'
}

const formatAvgTime = (seconds) => {
  if (seconds === undefined || seconds === null) return '0s'
  if (seconds >= 60) {
    const mins = Math.floor(seconds / 60)
    const secs = seconds % 60
    return secs > 0 ? `${mins}m ${secs}s` : `${mins}m`
  }
  return `${seconds}s`
}

const formatTokens = (tokens) => {
  return new Intl.NumberFormat('en-US').format(tokens || 0)
}

const formatTime = (isoStr) => {
  if (!isoStr) return ''
  const d = new Date(isoStr)
  const now = new Date()
  const isToday = d.toDateString() === now.toDateString()
  if (isToday) {
    return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }
  return d.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' }) + ' ' +
    d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

const basename = (path) => {
  if (!path) return ''
  return path.split(/[/\\]/).pop()
}

const isEmbedded = (task) => {
  // Level 3 = 内嵌字幕，或 original_srt_path 包含 .emb.
  return task.funnel_level === 3 || (task.original_srt_path && task.original_srt_path.includes('.emb.'))
}

const isTranslating = (task) => {
  return (task.status === 'translating' || task.status === 'rebuilding')
}

const getStatusIcon = (task) => {
  const map = { done: '✅', skipped: '⏭️', failed: '❌', funneling: '🔍', extracting: '📤', rebuilding: '🔨' }
  if (task.status === 'translating') {
    // 区分等待翻译和正在翻译
    if (task.task_status === 'queued') return '⏳'
    return '🌐'
  }
  return map[task.status] || '📄'
}

const getStatusClass = (task) => {
  const map = { done: 'done', skipped: 'skipped', failed: 'failed', funneling: 'funneling', extracting: 'extracting', rebuilding: 'rebuilding' }
  if (task.status === 'translating') {
    if (task.task_status === 'queued') return 'waiting'
    if (task.task_status === 'processing') return 'translating'
    return 'translating'
  }
  return map[task.status] || ''
}

const getStatusLabel = (task) => {
  const map = { done: '完成', skipped: '跳过', failed: '失败', rebuilding: '字幕回写' }
  if (task.status === 'funneling') return '漏斗分流'
  if (task.status === 'extracting') {
    if (task.funnel_level === 1) return '繁简转换'
    return '文本提取'
  }
  if (task.status === 'translating') {
    if (task.task_status === 'queued') return '排队翻译'
    if (task.task_status === 'processing') return '大模型翻译中'
    return '大模型翻译中'
  }
  return map[task.status] || task.status
}

const getProgressPct = (task) => {
  if (!task.total_batches) return 0
  return Math.round(((task.current_batch || 0) / task.total_batches) * 100)
}

// ============ 数据获取 ============

const fetchStats = async () => {
  try {
    const data = await getStats()
    stats.waiting = data.debouncing || 0
    stats.queued = data.queued || 0
    stats.funneling = (data.funneling || 0) + (data.extracting || 0)
    stats.translating = (data.translating || 0) + (data.rebuilding || 0)
    stats.completed = data.done || 0
    stats.skipped = data.skipped || 0
    stats.failed = data.failed || 0
    stats.successRate = formatSuccessRate(data.success_rate)
    stats.avgTime = formatAvgTime(data.avg_duration_seconds)
    stats.totalTokens = data.total_tokens || 0
    hasAnomaly.value = data.has_timing_anomaly || false
    anomalyReason.value = data.anomaly_reason || ''
    isScanning.value = data.scanning || false
    if (isScanning.value) {
      scanStatus.value = '扫描中...'
    } else if (scanStatus.value === '扫描中...') {
      scanStatus.value = ''
    }
  } catch (error) {
    console.error('Failed to load stats:', error)
  }
}

const fetchTabData = async () => {
  try {
    if (activeTab.value === 'wait' || activeTab.value === 'queue') {
      tabData.value = []
      tabTotal.value = 0
      return
    }

    let statuses = []
    if (activeTab.value === 'execute') {
      statuses = ['funneling', 'extracting']
    } else if (activeTab.value === 'translate') {
      statuses = ['translating', 'rebuilding']
    }
    
    if (statuses.length === 0) return

    const data = await getJobsByStatuses(statuses, currentPage.value, pageSize.value)
    tabData.value = (data.items || []).map(item => ({
      id: item.id,
      media_path: item.media_path,
      status: item.status,
      funnel_level: item.funnel_level,
      original_srt_path: item.original_srt_path,
      output_srt_path: item.output_srt_path,
      error: item.error,
      source: item.source,
      created_at: item.created_at,
      completed_at: item.completed_at,
      translate_task_id: item.translate_task_id,
      task_status: item.task_status,
      current_batch: item.current_batch || 0,
      total_batches: item.total_batches || 0,
      task_progress: item.task_progress
    }))
    tabTotal.value = data.total || 0
  } catch (error) {
    console.error('Failed to load tab data:', error)
    tabData.value = []
    tabTotal.value = 0
  }
}

const fetchRealtimeQueues = async () => {
  try {
    const [debouncingData, pendingData] = await Promise.all([
      getDebouncing(),
      getPending()
    ])
    debounceEntries.value = debouncingData || []
    pendingEntries.value = pendingData.items || []
  } catch (error) {
    console.error('Failed to load realtime queues:', error)
  }
}

const fetchData = async () => {
  await Promise.all([
    fetchStats(),
    fetchTabData(),
    fetchRealtimeQueues()
  ])
}

onMounted(() => {
  fetchData()
})

// ============ 交互处理 ============

const switchTab = (key) => {
  activeTab.value = key
  currentPage.value = 1
  fetchTabData()
}

const changePage = (page) => {
  if (page < 1 || page > totalPages.value) return
  currentPage.value = page
  fetchTabData()
}

const handlePageSizeChange = () => {
  currentPage.value = 1
  fetchTabData()
}

const handleScan = async () => {
  scanStatus.value = '扫描中...'
  isScanning.value = true
  try {
    await triggerScan()
  } catch (error) {
    scanStatus.value = error.message || '正在扫描中，请勿重复操作'
    isScanning.value = false
  }
}

// ============ SSE 事件处理 ============

const handleSSEEvent = (type, payload) => {
  if (type === 'job_created') {
    stats.funneling++
    // 刷新当前 tab 和队列
    fetchTabData()
    fetchRealtimeQueues()
  } else if (type === 'job_status_changed') {
    const finalStatuses = ['done', 'failed', 'skipped']
    if (finalStatuses.includes(payload.status)) {
      if (payload.status === 'done') stats.completed++
      else if (payload.status === 'skipped') stats.skipped++
      else if (payload.status === 'failed') stats.failed++
      // 终态触发全量刷新
      fetchData()
    } else {
      // 非终态：刷新当前 tab 和统计
      fetchStats()
      fetchTabData()
      fetchRealtimeQueues()
    }
  } else if (type === 'task_progress') {
    // 增量更新当前 tab 中匹配任务的进度
    const task = tabData.value.find(j => j.translate_task_id === payload.task_id)
    if (task) {
      task.current_batch = payload.current_batch
      task.total_batches = payload.total_batches
      task.task_status = 'processing'
    }
  } else if (type === 'scan_completed') {
    isScanning.value = false
    scanStatus.value = ''
    message.success(`全盘扫描完成，已入队 ${payload.count || 0} 个文件`)
    fetchData()
  } else if (type === 'timing_anomaly_detected') {
    hasAnomaly.value = true
    anomalyReason.value = payload.reason || ''
    message.error("检测到数据库存在时钟回拨或负数耗时异常！已启用系统警告屏障。")
  }
}

const handleSnapshotGap = (newId, expectedId) => {
  console.warn(`Snapshot gap detected! expected ${expectedId}, got ${newId}. Reloading...`)
  fetchData()
}

useSSE('/api/events', {
  onEvent: handleSSEEvent,
  onSnapshotGap: handleSnapshotGap
})
</script>

<style scoped>
/* 统计卡片 */
.stats-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 12px; margin-bottom: 20px; }
.stat-card { background: #fff; border-radius: 8px; padding: 16px; border: 1px solid #e8e8ec; transition: box-shadow 0.2s; }
.stat-card:hover { box-shadow: 0 2px 8px rgba(0,0,0,0.06); }
.stat-card .label { font-size: 12px; color: #999; margin-bottom: 6px; }
.stat-card .value { font-size: 24px; font-weight: 600; color: #333; }
.stat-card .value.green { color: #18a058; }
.stat-card .value.orange { color: #f0a020; }
.stat-card .value.red { color: #d03050; }
.stat-card .value.blue { color: #2080f0; }
.stat-card .sub { font-size: 11px; color: #bbb; margin-top: 4px; }

/* 操作栏 */
.action-bar { display: flex; align-items: center; gap: 12px; margin-bottom: 20px; }

/* TAB 栏 */
.tab-bar { display: flex; gap: 0; border-bottom: 2px solid #e8e8ec; margin-bottom: 0; }
.tab-btn {
  padding: 10px 20px; font-size: 13px; font-weight: 500; color: #666;
  background: none; border: none; border-bottom: 2px solid transparent;
  cursor: pointer; transition: all 0.2s; margin-bottom: -2px;
  display: flex; align-items: center; gap: 6px;
}
.tab-btn:hover { color: #18a058; background: #f0faf4; }
.tab-btn.active { color: #18a058; border-bottom-color: #18a058; background: #f0faf4; }
.tab-icon { font-size: 14px; }
.tab-count {
  font-size: 11px; background: #e8e8ec; padding: 1px 7px; border-radius: 10px; color: #666;
}
.tab-btn.active .tab-count { background: #e8f8ef; color: #18a058; }

/* TAB 内容 */
.tab-content { background: #fff; border: 1px solid #e8e8ec; border-top: none; border-radius: 0 0 8px 8px; padding: 0; }

/* 实时队列区域 */
.realtime-section { border-bottom: 1px solid #f0f0f4; }
.realtime-header { padding: 10px 16px; font-size: 13px; font-weight: 500; color: #666; display: flex; align-items: center; gap: 8px; background: #fafafa; }

/* 工具栏 */
.tab-toolbar { display: flex; align-items: center; justify-content: space-between; padding: 10px 16px; border-bottom: 1px solid #f0f0f4; }
.total-info { font-size: 12px; color: #999; }
.page-size-select { padding: 4px 8px; font-size: 12px; border: 1px solid #d0d0d6; border-radius: 4px; background: #fff; outline: none; }

/* 任务列表 */
.task-item { padding: 10px 16px; border-bottom: 1px solid #f5f5f8; font-size: 13px; display: flex; align-items: center; gap: 10px; transition: background 0.15s; }
.task-item:last-child { border-bottom: none; }
.task-item:hover { background: #fafbfc; }
.task-item .icon { font-size: 16px; flex-shrink: 0; }
.task-item .path { color: #555; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.queue-rank { display: flex; align-items: center; justify-content: center; width: 20px; height: 20px; background-color: #f0f0f4; color: #666; font-size: 11px; font-weight: 600; border-radius: 50%; flex-shrink: 0; margin-right: 4px; }

/* 状态标签 */
.status-tag { font-size: 11px; padding: 2px 8px; border-radius: 4px; font-weight: 500; flex-shrink: 0; }
.status-tag.done { background: #e8f8ef; color: #18a058; }
.status-tag.skipped { background: #f5f5f8; color: #999; }
.status-tag.failed { background: #fde8ec; color: #d03050; }
.status-tag.funneling { background: #fdf6ec; color: #f0a020; }
.status-tag.extracting { background: #fdf6ec; color: #e0a010; }
.status-tag.translating { background: #e8f0fe; color: #2080f0; }
.status-tag.rebuilding { background: #faf0fc; color: #a020f0; }
.status-tag.waiting { background: #fff8e1; color: #f0a020; }

/* 来源/结果字幕信息 */
.source-info { color: #2080f0; font-size: 11px; max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.result-info { color: #18a058; font-size: 11px; max-width: 160px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* 进度条 */
.progress-bar { height: 4px; background: #e8e8ec; border-radius: 2px; width: 80px; flex-shrink: 0; }
.progress-bar .fill { height: 100%; background: #18a058; border-radius: 2px; transition: width 0.3s; }

.batch-info { font-size: 11px; color: #2080f0; flex-shrink: 0; }
.time-info { font-size: 11px; color: #bbb; flex-shrink: 0; }

/* 徽章 */
.badge { font-size: 11px; padding: 2px 8px; border-radius: 10px; }
.badge.active-badge { background: #e8f8ef; color: #18a058; }
.badge.warn-badge { background: #fdf6ec; color: #f0a020; }

/* 空状态 */
.empty-state { text-align: center; padding: 32px 16px; color: #ccc; font-size: 13px; }

/* 分页 */
.pagination { display: flex; justify-content: center; align-items: center; gap: 4px; padding: 14px; border-top: 1px solid #f0f0f4; }
.pagination button { padding: 4px 10px; border: 1px solid #d0d0d6; border-radius: 4px; background: #fff; cursor: pointer; font-size: 12px; }
.pagination button:hover { border-color: #18a058; color: #18a058; }
.pagination button.active { background: #18a058; color: #fff; border-color: #18a058; }
.pagination button:disabled { opacity: 0.4; cursor: not-allowed; }

/* 异常报警条 */
.anomaly-alert-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  background: #fdf6ec;
  border: 1px solid #faecd8;
  border-radius: 8px;
  padding: 12px 16px;
  margin-bottom: 20px;
  color: #e6a23c;
  animation: shake 0.5s ease-in-out;
}
.anomaly-alert-banner .warning-icon { font-size: 20px; }
.anomaly-alert-banner .alert-content { display: flex; flex-direction: column; gap: 2px; }
.anomaly-alert-banner .alert-title { font-weight: 600; font-size: 14px; }
.anomaly-alert-banner .alert-reason { font-size: 12px; color: #e6a23c; opacity: 0.85; }

@keyframes shake {
  0%, 100% { transform: translateX(0); }
  25% { transform: translateX(-4px); }
  75% { transform: translateX(4px); }
}
</style>
