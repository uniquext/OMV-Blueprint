<template>
  <div class="history">
    <!-- 统计汇总 -->
    <div class="history-summary">
      <span>✅ 完成: <strong>{{ stats.completed }}</strong></span>
      <span>⏭️ 跳过: <strong>{{ stats.skipped }}</strong></span>
      <span>❌ 失败: <strong>{{ stats.failed }}</strong></span>
      <span>📈 成功率: <strong>{{ stats.successRate }}</strong> <span style="color:#999;font-size:11px;">(跳过不计入)</span></span>
    </div>

    <!-- 筛选栏 -->
    <div class="filter-bar">
      <select>
        <option value="">全部状态</option>
        <option value="done">✅ 完成</option>
        <option value="skipped">⏭️ 跳过</option>
        <option value="failed">❌ 失败</option>
        <option value="translating">🌐 翻译中</option>
        <option value="funneling">🔍 漏斗分析</option>
      </select>
      <select>
        <option value="">全部漏斗级别</option>
        <option value="-1">Level -1 (无可用字幕)</option>
        <option value="0">Level 0 (已有中文)</option>
        <option value="1">Level 1</option>
        <option value="2">Level 2</option>
        <option value="3">Level 3</option>
      </select>
      <input type="date" />
      <input type="date" />
      <button class="btn">筛选</button>
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
            <th>来源</th>
            <th>创建时间</th>
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
              <td class="path-cell">{{ task.path }}</td>
              <td><span class="status-tag" :class="task.status">{{ statusLabel(task.status) }}</span></td>
              <td>Level {{ task.level }}</td>
              <td><span class="source-tag">{{ task.source }}</span></td>
              <td>{{ task.created_at }}</td>
              <td>{{ task.duration }}</td>
            </tr>
            <!-- 详情展开行 -->
            <tr v-if="expandedRows[idx]" class="detail-row open">
              <td :colspan="7">
                <div class="detail-content">
                  <div class="field"><span class="k">Job ID:</span><span class="v">{{ task.id }}</span></div>
                  <div class="field"><span class="k">媒体路径:</span><span class="v">{{ task.path }}</span></div>
                  <div class="field" v-if="task.original_srt"><span class="k">原始字幕:</span><span class="v">{{ task.original_srt }}</span></div>
                  <div class="field" v-if="task.output_srt"><span class="k">输出字幕:</span><span class="v">{{ task.output_srt }}</span></div>
                  <div class="field" v-if="task.levelDesc"><span class="k">{{ task.status === 'skipped' ? '跳过原因:' : '漏斗级别:' }}</span><span class="v">Level {{ task.level }} — {{ task.levelDesc }}</span></div>
                  <div class="field" v-if="task.batch_info"><span class="k">翻译批次:</span><span class="v">{{ task.batch_info }}</span></div>
                  <div class="field" v-if="task.error"><span class="k">错误信息:</span><span class="v error-text">{{ task.error }}</span></div>
                  <div class="field"><span class="k">创建时间:</span><span class="v">{{ task.created_at }}</span></div>
                  <div class="field" v-if="task.completed_at"><span class="k">完成时间:</span><span class="v">{{ task.completed_at }}</span></div>
                </div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <div class="pagination">
        <button disabled>上一页</button>
        <button class="active">1</button>
        <button>2</button>
        <button>3</button>
        <button>4</button>
        <button>下一页</button>
      </div>
    </div>

    <!-- 日志查看器 -->
    <div style="margin-top:20px;">
      <div style="display:flex;align-items:center;gap:8px;margin-bottom:8px;">
        <span style="font-size:14px;font-weight:500;">📄 日志查看器</span>
        <span style="font-size:11px;color:#999;">GET /api/logs?lines=100</span>
      </div>
      <div class="log-viewer">
        <div class="log-line" v-for="(log, idx) in logEntries" :key="idx">
          <span class="ts">{{ log.time }} </span>
          <span :class="{'lvl-info': log.level==='INFO', 'lvl-warn': log.level==='WARN', 'lvl-err': log.level==='ERROR'}">[{{ log.level }}] </span>
          <span class="msg">{{ log.message }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { reactive } from 'vue'
import { historyStats, historyTasks, logs } from '../mock/history'

const stats = historyStats
const tasks = historyTasks
const logEntries = logs

// 展开行状态管理
const expandedRows = reactive({})

function toggleDetail(idx) {
  expandedRows[idx] = !expandedRows[idx]
}

function statusLabel(status) {
  const map = { done: '完成', skipped: '跳过', failed: '失败', translating: '翻译中', funneling: '漏斗分析', extracting: '字幕提取' }
  return map[status] || status
}
</script>

<style scoped>
.filter-bar { display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; align-items: center; }
.filter-bar select, .filter-bar input { padding: 6px 12px; border: 1px solid #d0d0d6; border-radius: 6px; font-size: 13px; background: #fff; outline: none; }
.filter-bar select:focus, .filter-bar input:focus { border-color: #18a058; }

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

/* 来源标签 */
.source-tag { font-size: 11px; background: #f0f0f4; padding: 2px 6px; border-radius: 3px; color: #888; }

/* 详情展开行 */
.detail-row td { background: #fafbfc; padding: 12px 20px !important; }
.detail-content { display: grid; grid-template-columns: 1fr 1fr; gap: 8px 24px; font-size: 12px; }
.detail-content .field { display: flex; gap: 8px; }
.detail-content .field .k { color: #999; min-width: 80px; }
.detail-content .field .v { color: #555; word-break: break-all; }
.detail-content .field .v.error-text { color: #d03050; }

/* 分页 */
.pagination { display: flex; justify-content: center; align-items: center; gap: 4px; padding: 14px; }
.pagination button { padding: 4px 10px; border: 1px solid #d0d0d6; border-radius: 4px; background: #fff; cursor: pointer; font-size: 12px; }
.pagination button:hover { border-color: #18a058; color: #18a058; }
.pagination button.active { background: #18a058; color: #fff; border-color: #18a058; }
.pagination button:disabled { opacity: 0.4; cursor: not-allowed; }

/* 日志查看器 */
.log-viewer { background: #1e1e2e; border-radius: 8px; padding: 14px; max-height: 260px; overflow-y: auto; font-family: "SF Mono", "Fira Code", monospace; font-size: 12px; line-height: 1.7; }
.log-line { color: #a0a8b8; }
.log-line .ts { color: #6c7086; }
.log-line .lvl-info { color: #89b4fa; }
.log-line .lvl-warn { color: #f9e2af; }
.log-line .lvl-err { color: #f38ba8; }
.log-line .msg { color: #cdd6f4; }
</style>
