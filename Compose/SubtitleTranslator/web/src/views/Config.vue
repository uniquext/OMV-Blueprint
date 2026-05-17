<template>
  <div class="config">

    <!-- 重启提示遮罩 -->
    <div class="restart-notice" :class="{ show: showRestart }">
      <div class="spinner"></div>
      <span>服务正在重启，SSE 重连后将自动恢复…</span>
    </div>

    <!-- config.json 编辑 -->
    <div class="config-section">
      <div class="config-section-title">⚙️ config.json 配置</div>
      <div class="config-form">
        <div class="form-row">
          <div class="form-group">
            <label>API Key</label>
            <input type="password" :value="config.llm.api_key" />
            <div class="hint">OpenAI / 兼容 API 密钥，保存时脱敏存储</div>
          </div>
          <div class="form-group">
            <label>API Base URL</label>
            <input type="text" :value="config.llm.api_url" />
          </div>
        </div>
        <div class="form-row">
          <div class="form-group">
            <label>翻译模型</label>
            <select :value="config.llm.model">
              <option value="gpt-4-turbo">gpt-4-turbo</option>
              <option value="gpt-4o-mini">gpt-4o-mini</option>
              <option value="gpt-4o">gpt-4o</option>
              <option value="gpt-3.5-turbo">gpt-3.5-turbo</option>
              <option value="deepseek-chat">deepseek-chat</option>
            </select>
          </div>
          <div class="form-group">
            <label>目标语言</label>
            <select>
              <option>zh-CN (简体中文)</option>
              <option>zh-TW (繁体中文)</option>
            </select>
          </div>
        </div>
        <div class="form-row">
          <div class="form-group">
            <label>最大并发数</label>
            <input type="number" :value="config.pipeline.funnel_workers" min="1" max="10" />
          </div>
          <div class="form-group">
            <label>防抖等待 (秒)</label>
            <input type="number" :value="config.pipeline.debounce_seconds" min="5" max="300" />
          </div>
        </div>
        <div class="form-row">
          <div class="form-group">
            <label>媒体根目录</label>
            <input type="text" :value="config.watchdog.path" />
          </div>
          <div class="form-group">
            <label>字幕语言</label>
            <input type="text" value="en,eng" />
            <div class="hint">逗号分隔，匹配这些语言的字幕才会触发翻译</div>
          </div>
        </div>
      </div>
      <div class="form-actions">
        <button class="btn" @click="resetConfig">重置</button>
        <button class="btn btn-primary" @click="saveConfig">💾 保存配置</button>
      </div>
    </div>

    <!-- Prompt 编辑 -->
    <div class="config-section">
      <div class="config-section-title">📝 Prompt 编辑</div>
      <div class="config-form">
        <div class="form-group">
          <label>系统提示词 (System Prompt)</label>
          <textarea readonly :value="prompts.system_prompt"></textarea>
        </div>
        <div class="form-group">
          <label>术语表 (Glossary)</label>
          <textarea readonly :value="prompts.glossary"></textarea>
          <div class="hint">JSON 格式，键为英文原文，值为期望的翻译结果</div>
        </div>
      </div>
      <div class="form-actions">
        <button class="btn" @click="resetPrompts">重置</button>
        <button class="btn btn-primary" @click="savePrompts">💾 保存 Prompt</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { configData, promptsData } from '../mock/config'

const config = reactive({ ...configData, llm: { ...configData.llm }, pipeline: { ...configData.pipeline }, media: { ...configData.media }, watchdog: { ...configData.watchdog } })
const prompts = reactive({ ...promptsData })
const showRestart = ref(false)

function saveConfig() {
  showRestart.value = true
}

function resetConfig() {
  Object.assign(config.llm, configData.llm)
  Object.assign(config.pipeline, configData.pipeline)
  Object.assign(config.media, configData.media)
  Object.assign(config.watchdog, configData.watchdog)
}

function savePrompts() {
}

function resetPrompts() {
  Object.assign(prompts, promptsData)
}
</script>

<style scoped>
.restart-notice { background: #fdf6ec; border: 1px solid #f0d88a; border-radius: 8px; padding: 12px 16px; margin-bottom: 16px; font-size: 13px; color: #94600; display: none; align-items: center; gap: 8px; }
.restart-notice.show { display: flex; }
.restart-notice .spinner { width: 14px; height: 14px; border: 2px solid #f0d88a; border-top-color: #f0a020; border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.config-section { background: #fff; border-radius: 8px; border: 1px solid #e8e8ec; margin-bottom: 16px; overflow: hidden; }
.config-section-title { padding: 14px 16px; font-weight: 500; font-size: 14px; border-bottom: 1px solid #f0f0f4; background: #fafafa; }
.config-form { padding: 16px; }
.form-group { margin-bottom: 14px; }
.form-group label { display: block; font-size: 13px; color: #666; margin-bottom: 4px; }
.form-group input, .form-group select { width: 100%; padding: 8px 12px; border: 1px solid #d0d0d6; border-radius: 6px; font-size: 13px; outline: none; }
.form-group input:focus, .form-group select:focus { border-color: #18a058; }
.form-group .hint { font-size: 11px; color: #bbb; margin-top: 3px; }
.form-row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.form-group textarea { width: 100%; padding: 10px 12px; border: 1px solid #d0d0d6; border-radius: 6px; font-size: 13px; outline: none; font-family: "SF Mono", "Fira Code", monospace; min-height: 180px; resize: vertical; line-height: 1.6; }
.form-group textarea:focus { border-color: #18a058; }
.form-actions { display: flex; gap: 8px; justify-content: flex-end; padding: 0 16px 16px; }
</style>
