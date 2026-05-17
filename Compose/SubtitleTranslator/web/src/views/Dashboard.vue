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
      <button class="btn btn-primary" id="btn-scan">🔍 全盘扫描</button>
      <span id="scan-status" style="font-size:12px;color:#999;"></span>
    </div>

    <!-- L1 等待中 -->
    <div class="task-section">
      <div class="task-section-header">
        ⏳ L1 等待中
        <span class="badge warn-badge">{{ active.l1_waiting.length }}</span>
      </div>
      <template v-if="active.l1_waiting.length > 0">
        <div class="task-item" v-for="task in active.l1_waiting" :key="task.id">
          <span class="icon">📁</span>
          <span class="path">{{ task.path }}</span>
          <span class="meta">剩余 {{ task.wait_seconds }}s</span>
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
        <div class="task-item" v-for="task in active.l2_queued" :key="task.id">
          <span class="icon">📁</span>
          <span class="path">{{ task.path }}</span>
          <span class="meta">位置 #{{ task.position }}</span>
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
          <span class="path">{{ task.path }}</span>
          <span class="status-tag funneling">{{ task.status }}</span>
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
          <span class="path">{{ task.path }}</span>
          <span class="status-tag translating">翻译中</span>
          <div class="progress-bar"><div class="fill" :style="{width: task.progress + '%'}"></div></div>
          <span class="meta">批次 {{ task.current_batch }}/{{ task.total_batch }}</span>
        </div>
      </template>
      <div class="empty-state" v-else>暂无活跃任务</div>
    </div>

  </div>
</template>

<script setup>
import { reactive } from 'vue'
import { dashboardStats, activeTasks } from '../mock/dashboard'

const stats = dashboardStats
const active = reactive({ ...activeTasks, l1_waiting: [...activeTasks.l1_waiting], l2_queued: [...activeTasks.l2_queued], l2_extracting: [...activeTasks.l2_extracting], l3_translating: [...activeTasks.l3_translating] })
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
