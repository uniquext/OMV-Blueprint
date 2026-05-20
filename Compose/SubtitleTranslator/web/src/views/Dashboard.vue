<template>
  <div class="dashboard">
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
        <div class="label">⏱️ 平均耗时</div>
        <div class="value">{{ stats.avgTime }}</div>
        <div class="sub">入队→完成</div>
      </div>
    </div>

    <!-- 操作栏 -->
    <div class="action-bar">
      <button class="btn btn-primary" id="btn-scan" @click="handleScan" :disabled="isScanning">
        🔍 {{ isScanning ? '扫描中...' : '全盘扫描' }}
      </button>
      <span id="scan-status" style="font-size:12px;color:#d03050;">{{ scanStatus }}</span>
    </div>

    <!-- L1 等待中 -->
    <div class="task-section">
      <div class="task-section-header">
        ⏳ L1 等待中
        <span class="badge warn-badge">{{ active.l1_waiting.length }}</span>
      </div>
      <template v-if="active.l1_waiting.length > 0">
        <div class="task-item" v-for="task in active.l1_waiting" :key="task.media_path">
          <span class="icon">📁</span>
          <span class="path">{{ task.media_path }}</span>
          <span class="meta">剩余 {{ task.remaining_seconds }}s</span>
        </div>
      </template>
      <div class="empty-state" v-else>暂无活跃任务</div>
    </div>

    <!-- L2 排队中 -->
    <div class="task-section">
      <div class="task-section-header">
        📋 L2 排队中
        <span class="badge active-badge">{{ active.l2_queued.length }}</span>
      </div>
      <template v-if="active.l2_queued.length > 0">
        <div class="task-item" v-for="(task, index) in active.l2_queued" :key="task.media_path">
          <span class="icon">📁</span>
          <span class="path">{{ task.media_path }}</span>
          <span class="meta">位置 #{{ index + 1 }}</span>
        </div>
      </template>
      <div class="empty-state" v-else>暂无活跃任务</div>
    </div>

    <!-- L2 漏斗/提取 -->
    <div class="task-section">
      <div class="task-section-header">
        🔍 L2 漏斗/提取
        <span class="badge warn-badge">{{ active.l2_extracting.length }}</span>
      </div>
      <template v-if="active.l2_extracting.length > 0">
        <div class="task-item" v-for="task in active.l2_extracting" :key="task.id">
          <span class="icon">🔍</span>
          <span class="path">{{ task.media_path }}</span>
          <span class="status-tag funneling">{{ task.status === 'funneling' ? '漏斗中' : (task.status === 'extracting' ? '提取中' : task.status) }}</span>
        </div>
      </template>
      <div class="empty-state" v-else>暂无活跃任务</div>
    </div>

    <!-- L3 翻译/回写 -->
    <div class="task-section">
      <div class="task-section-header">
        🌐 L3 翻译/回写
        <span class="badge blue-badge">{{ active.l3_translating.length }}</span>
      </div>
      <template v-if="active.l3_translating.length > 0">
        <div class="task-item" v-for="task in active.l3_translating" :key="task.id">
          <span class="icon">🌐</span>
          <span class="path">{{ task.media_path }}</span>
          <span class="status-tag translating">{{ task.status === 'translating' ? '翻译中' : (task.status === 'rebuilding' ? '回写中' : task.status) }}</span>
          <div class="progress-bar"><div class="fill" :style="{width: (task.task_progress || 0) + '%'}"></div></div>
          <span class="meta" v-if="task.status === 'translating' || task.status === '翻译中'">批次 {{ task.current_batch || 0 }}/{{ task.total_batch || 0 }}</span>
        </div>
      </template>
      <div class="empty-state" v-else>暂无活跃任务</div>
    </div>

  </div>
</template>

<script setup>
import { reactive, ref, onMounted } from 'vue'
import { createDiscreteApi } from 'naive-ui'
import { getStats, getDebouncing, getPending, getActiveJobs, triggerScan } from '../api/dashboard'
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
  avgTime: '0s'
})

const active = reactive({
  l1_waiting: [],
  l2_queued: [],
  l2_extracting: [],
  l3_translating: []
})

const isScanning = ref(false)
const scanStatus = ref('')

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

const fetchData = async () => {
  try {
    const [statsData, debouncingData, pendingData, activeJobsData] = await Promise.all([
      getStats(),
      getDebouncing(),
      getPending(),
      getActiveJobs()
    ])

    // Map stats
    stats.waiting = statsData.debouncing || 0
    stats.queued = statsData.queued || 0
    stats.funneling = statsData.funneling || 0
    stats.translating = (statsData.translating || 0) + (statsData.rebuilding || 0)
    stats.completed = statsData.done || 0
    stats.skipped = statsData.skipped || 0
    stats.failed = statsData.failed || 0
    stats.successRate = formatSuccessRate(statsData.success_rate)
    stats.avgTime = formatAvgTime(statsData.avg_duration_seconds)
    isScanning.value = statsData.scanning || false
    if (isScanning.value) {
      scanStatus.value = '扫描中...'
    } else if (scanStatus.value === '扫描中...') {
      scanStatus.value = ''
    }

    // Map active tasks
    active.l1_waiting = debouncingData || []
    active.l2_queued = pendingData.items || []
    
    const activeJobs = activeJobsData.items || []
    active.l2_extracting = activeJobs.filter(j => j.status === 'funneling' || j.status === 'extracting')
    active.l3_translating = activeJobs.filter(j => j.status === 'translating' || j.status === 'rebuilding')
  } catch (error) {
    console.error('Failed to load dashboard data:', error)
    // Clear list to show empty states on error
    active.l1_waiting = []
    active.l2_queued = []
    active.l2_extracting = []
    active.l3_translating = []
  }
}

onMounted(() => {
  fetchData()
})

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

const handleSSEEvent = (type, payload) => {
  if (type === 'job_created') {
    // stats
    stats.funneling++
    
    // active list
    const exists = active.l2_extracting.some(j => j.id === payload.job_id)
    if (!exists) {
      active.l2_extracting.push({
        id: payload.job_id,
        media_path: payload.media_path,
        status: 'funneling'
      })
    }
  } else if (type === 'job_status_changed') {
    const finalStatuses = ['done', 'failed', 'skipped']
    if (finalStatuses.includes(payload.status)) {
      // Remove from all active lists
      active.l2_extracting = active.l2_extracting.filter(j => j.id !== payload.job_id)
      active.l3_translating = active.l3_translating.filter(j => j.id !== payload.job_id)
      // Done/Failed/Skipped should trigger full HTTP refresh to update database rates & times
      fetchData()
    } else {
      // Internal transition: funneling/extracting/translating/rebuilding
      // Find where it currently resides and move it
      let task = active.l2_extracting.find(j => j.id === payload.job_id) || 
                 active.l3_translating.find(j => j.id === payload.job_id)
      
      const newStatus = payload.status
      
      if (!task) {
        task = {
          id: payload.job_id,
          media_path: payload.media_path,
          status: newStatus
        }
      } else {
        // Remove from old lists
        active.l2_extracting = active.l2_extracting.filter(j => j.id !== payload.job_id)
        active.l3_translating = active.l3_translating.filter(j => j.id !== payload.job_id)
        task.status = newStatus
      }

      // Add to new list
      if (newStatus === 'funneling' || newStatus === 'extracting') {
        active.l2_extracting.push(task)
      } else if (newStatus === 'translating' || newStatus === 'rebuilding') {
        if (newStatus === 'translating' && task.task_progress === undefined) {
          task.task_progress = 0
          task.current_batch = 0
          task.total_batch = 0
        }
        active.l3_translating.push(task)
      }

      // Trigger stats refresh to stay consistent
      fetchData()
    }
  } else if (type === 'task_progress') {
    const task = active.l3_translating.find(j => j.translate_task_id === payload.task_id)
    if (task) {
      const total = payload.total_batches || 1
      task.task_progress = Math.round((payload.current_batch / total) * 100)
      task.current_batch = payload.current_batch
      task.total_batch = payload.total_batches
    }
  } else if (type === 'scan_completed') {
    isScanning.value = false
    scanStatus.value = ''
    message.success(`全盘扫描完成，已入队 ${payload.count || 0} 个文件`)
    fetchData()
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

/* 活跃任务区域 */
.task-section { background: #fff; border-radius: 8px; border: 1px solid #e8e8ec; margin-bottom: 16px; overflow: hidden; }
.task-section-header { padding: 12px 16px; font-size: 14px; font-weight: 500; border-bottom: 1px solid #f0f0f4; display: flex; align-items: center; gap: 8px; background: #fafafa; }
.task-section-header .badge { font-size: 11px; background: #e8e8ec; padding: 2px 8px; border-radius: 10px; color: #666; }
.task-section-header .badge.active-badge { background: #e8f8ef; color: #18a058; }
.task-section-header .badge.warn-badge { background: #fdf6ec; color: #f0a020; }
.task-section-header .badge.blue-badge { background: #e8f0fe; color: #2080f0; }
.task-item { padding: 10px 16px; border-bottom: 1px solid #f5f5f8; font-size: 13px; display: flex; align-items: center; gap: 12px; transition: background 0.15s; }
.task-item:last-child { border-bottom: none; }
.task-item:hover { background: #fafbfc; }
.task-item .icon { font-size: 16px; flex-shrink: 0; }
.task-item .path { color: #555; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.task-item .meta { font-size: 12px; color: #999; flex-shrink: 0; }

/* 状态标签 */
.status-tag { font-size: 11px; padding: 2px 8px; border-radius: 4px; font-weight: 500; }
.status-tag.translating { background: #e8f0fe; color: #2080f0; }
.status-tag.funneling { background: #fdf6ec; color: #f0a020; }

/* 进度条 */
.progress-bar { height: 4px; background: #e8e8ec; border-radius: 2px; width: 120px; flex-shrink: 0; }
.progress-bar .fill { height: 100%; background: #18a058; border-radius: 2px; transition: width 0.3s; }

/* 空状态 */
.empty-state { text-align: center; padding: 32px 16px; color: #ccc; font-size: 13px; }
</style>
