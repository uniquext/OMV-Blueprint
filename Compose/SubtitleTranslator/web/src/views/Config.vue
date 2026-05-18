<template>
  <div class="config">

    <!-- 全屏重启遮罩 -->
    <div class="restart-mask" v-if="showRestart">
      <div class="mask-content">
        <div class="spinner" v-if="!pollTimeout"></div>
        <div class="mask-icon" v-else>⚠️</div>
        <span v-if="!pollTimeout">系统正在应用配置并重启，请稍候…</span>
        <span v-else>重启超时，请尝试手动刷新页面。</span>
      </div>
    </div>

    <!-- 加载中遮罩 -->
    <div class="loading-overlay" v-if="loadingInit">
      <div class="spinner"></div> 加载配置中...
    </div>

    <div v-if="initError" class="error-message">
      {{ initError }}
    </div>

    <!-- config.json 编辑 -->
    <div class="config-section" v-if="!loadingInit && !initError">
      <div class="config-section-title">⚙️ config.json 配置</div>
      
      <div v-if="configSaveError" class="error-message config-error">
        {{ configSaveError }}
      </div>

      <!-- ═══ LLM 区块 ═══ -->
      <div class="config-subsection">
        <div class="subsection-title">🤖 LLM</div>
        <div class="config-form">
          <div class="form-row">
            <div class="form-group">
              <label>API Key</label>
              <div class="input-with-toggle">
                <input :type="showApiKey ? 'text' : 'password'" v-model="config.llm.api_key" />
                <button type="button" class="toggle-btn" @click="showApiKey = !showApiKey" tabindex="-1">
                  {{ showApiKey ? '🙈' : '👁️' }}
                </button>
              </div>
              <div class="hint">OpenAI / 兼容 API 密钥，读取时脱敏展示</div>
            </div>
            <div class="form-group">
              <label>API Base URL</label>
              <input type="text" v-model="config.llm.api_url" placeholder="https://api.openai.com/v1" />
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>模型名称 (model)</label>
              <input type="text" v-model="config.llm.model" placeholder="gpt-4o" />
              <div class="hint">填写模型标识符，如 gpt-4o、deepseek-chat 等</div>
            </div>
            <div class="form-group">
              <label>模型类型 (model_type)</label>
              <select v-model="config.llm.model_type">
                <option value="chat">chat — 注入术语表 + 语义指令</option>
                <option value="mt">mt — 纯文本直译，不注入术语表</option>
              </select>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>温度 (temperature)</label>
              <input type="number" v-model.number="config.llm.temperature" min="0" max="2" step="0.1" />
            </div>
            <div class="form-group">
              <label>超时 (timeout)</label>
              <input type="number" v-model.number="config.llm.timeout" min="1" max="600" />
              <div class="hint">单次请求超时秒数</div>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>批次大小 (batch_size)</label>
              <input type="number" v-model.number="config.llm.batch_size" min="1" max="100" />
              <div class="hint">每次翻译请求的字幕行数</div>
            </div>
            <div class="form-group">
              <label>上下文条数 (context_size)</label>
              <input type="number" v-model.number="config.llm.context_size" min="0" max="20" />
              <div class="hint">携带前文翻译结果条数以提高连贯性</div>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>RPM 限制 (rpm_limit)</label>
              <input type="number" v-model.number="config.llm.rpm_limit" min="0" />
              <div class="hint">每分钟请求次数限制，0 表示不限制</div>
            </div>
            <div class="form-group">
              <label>TPM 限制 (tpm_limit)</label>
              <input type="number" v-model.number="config.llm.tpm_limit" min="0" />
              <div class="hint">每分钟 Token 数限制，0 表示不限制</div>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>最大重试次数 (max_retries)</label>
              <input type="number" v-model.number="config.llm.max_retries" min="0" max="10" />
            </div>
            <div class="form-group"></div>
          </div>
        </div>
      </div>

      <!-- ═══ Pipeline 区块 ═══ -->
      <div class="config-subsection">
        <div class="subsection-title">⚡ Pipeline</div>
        <div class="config-form">
          <div class="form-row">
            <div class="form-group">
              <label>并发工人数 (funnel_workers)</label>
              <input type="number" v-model.number="config.pipeline.funnel_workers" min="1" max="20" />
              <div class="hint">同时翻译的文件数上限</div>
            </div>
            <div class="form-group">
              <label>防抖等待秒数 (debounce_seconds)</label>
              <input type="number" v-model.number="config.pipeline.debounce_seconds" min="0" max="600" />
              <div class="hint">文件变化后等待该时间稳定后再处理</div>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>防抖轮询间隔 (debounce_poll_interval)</label>
              <input type="number" v-model.number="config.pipeline.debounce_poll_interval" min="1" max="60" />
              <div class="hint">等待层内部轮询间隔，检查到期条目推入执行层</div>
            </div>
            <div class="form-group">
              <label>定时扫描间隔 (scan_interval)</label>
              <input type="number" v-model.number="config.pipeline.scan_interval" min="0" />
              <div class="hint">全量扫描间隔秒数，0 表示禁用</div>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>扫描目录 (scan_dir)</label>
              <input type="text" v-model="config.pipeline.scan_dir" />
            </div>
            <div class="form-group"></div>
          </div>
        </div>
      </div>

      <!-- ═══ Media 区块 ═══ -->
      <div class="config-subsection">
        <div class="subsection-title">🎬 Media</div>
        <div class="config-form">
          <div class="form-group">
            <label>视频扩展名 (extensions)</label>
            <input type="text" v-model="extensionsStr" />
            <div class="hint">逗号分隔，如 .mkv,.mp4,.avi</div>
          </div>
          <div class="form-group">
            <label>语言映射覆盖 (lang_map_override)</label>
            <textarea v-model="langMapStr" class="json-textarea"></textarea>
            <div class="hint">JSON 对象，自定义字幕语言标识到标准语言代码的映射</div>
          </div>
        </div>
      </div>

      <!-- ═══ Watchdog 区块 ═══ -->
      <div class="config-subsection">
        <div class="subsection-title">🐕 Watchdog</div>
        <div class="config-form">
          <div class="form-row">
            <div class="form-group">
              <label>启用文件监控 (enabled)</label>
              <select v-model="config.watchdog.enabled">
                <option :value="true">是</option>
                <option :value="false">否</option>
              </select>
            </div>
            <div class="form-group">
              <label>监控路径 (path)</label>
              <input type="text" v-model="config.watchdog.path" />
            </div>
          </div>
        </div>
      </div>

      <div class="form-actions">
        <button class="btn" :class="{ 'btn-flash': configResetFlash }" @click="resetConfig" :disabled="savingConfig">重置</button>
        <button class="btn btn-primary" @click="saveConfig" :disabled="savingConfig">
          💾 {{ savingConfig ? '保存中...' : '保存配置' }}
        </button>
      </div>
    </div>

    <!-- Prompt 编辑 -->
    <div class="config-section" v-if="!loadingInit && !initError">
      <div class="config-section-title">📝 Prompt 编辑</div>
      
      <div v-if="promptSaveError" class="error-message config-error">
        {{ promptSaveError }}
      </div>
      <div v-if="promptSaveSuccess" class="success-message">
        {{ promptSaveSuccess }}
      </div>

      <div class="config-form">
        <div class="form-group">
          <label>系统提示词 (System Prompt)</label>
          <textarea v-model="prompts.system_prompt"></textarea>
        </div>
        <div class="form-group">
          <label>术语表 (Glossary)</label>
          <textarea v-model="prompts.glossaryStr"></textarea>
          <div class="hint">JSON 格式，键为英文原文，值为期望的翻译结果</div>
        </div>
      </div>
      <div class="form-actions">
        <button class="btn" :class="{ 'btn-flash': promptResetFlash }" @click="resetPrompts" :disabled="savingPrompt">重置</button>
        <button class="btn btn-primary" @click="savePrompts" :disabled="savingPrompt">
          💾 {{ savingPrompt ? '保存中...' : '保存 Prompt' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { getConfig, putConfig, getPrompts, putPrompts, pollHealth } from '../api/config'

const loadingInit = ref(true)
const initError = ref('')

const showApiKey = ref(false)

const config = reactive({ llm: {}, pipeline: {}, media: {}, watchdog: {} })
const prompts = reactive({ system_prompt: '', glossaryStr: '{}' })

// media.extensions: 数组 ↔ 逗号分隔字符串
const extensionsStr = computed({
  get: () => (config.media.extensions || []).join(','),
  set: (val) => { config.media.extensions = val.split(',').map(s => s.trim()).filter(Boolean) }
})

// media.lang_map_override: 对象 ↔ JSON 字符串
const langMapStr = ref('{}')

let originalConfig = null
let originalPrompts = null
let originalLangMapStr = '{}'

const showRestart = ref(false)
const pollTimeout = ref(false)
let pollTimer = null
let pollCount = 0

const savingConfig = ref(false)
const configSaveError = ref('')

const savingPrompt = ref(false)
const promptSaveError = ref('')
const promptSaveSuccess = ref('')

const configResetFlash = ref(false)
const promptResetFlash = ref(false)

onMounted(async () => {
  await loadData()
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})

async function loadData() {
  try {
    loadingInit.value = true
    initError.value = ''
    
    const [cfg, prmpt] = await Promise.all([
      getConfig(),
      getPrompts()
    ])
    
    originalConfig = JSON.parse(JSON.stringify(cfg))
    originalLangMapStr = JSON.stringify(cfg.media?.lang_map_override || {}, null, 2)
    originalPrompts = {
      system_prompt: prmpt.system_prompt,
      glossaryStr: JSON.stringify(prmpt.glossary, null, 2)
    }

    resetConfig()
    resetPrompts()
    
  } catch (err) {
    initError.value = err.message || '加载配置失败'
  } finally {
    loadingInit.value = false
  }
}

function resetConfig() {
  if (originalConfig) {
    Object.assign(config.llm, originalConfig.llm)
    Object.assign(config.pipeline, originalConfig.pipeline)
    Object.assign(config.media, { ...originalConfig.media })
    Object.assign(config.watchdog, originalConfig.watchdog)
    langMapStr.value = originalLangMapStr
  }
  configSaveError.value = ''
  configResetFlash.value = true
  setTimeout(() => { configResetFlash.value = false }, 800)
}

function resetPrompts() {
  if (originalPrompts) {
    prompts.system_prompt = originalPrompts.system_prompt
    prompts.glossaryStr = originalPrompts.glossaryStr
  }
  promptSaveError.value = ''
  promptSaveSuccess.value = ''
  promptResetFlash.value = true
  setTimeout(() => { promptResetFlash.value = false }, 800)
}

async function saveConfig() {
  try {
    savingConfig.value = true
    configSaveError.value = ''

    // 解析 lang_map_override JSON
    let parsedLangMap
    try {
      parsedLangMap = JSON.parse(langMapStr.value)
    } catch (e) {
      throw new Error('lang_map_override JSON 格式错误: ' + e.message)
    }
    if (typeof parsedLangMap !== 'object' || Array.isArray(parsedLangMap) || parsedLangMap === null) {
      throw new Error('lang_map_override 必须是 JSON 对象')
    }

    const payload = JSON.parse(JSON.stringify(config))
    payload.media.lang_map_override = parsedLangMap
    await putConfig(payload)
    
    // 触发重启遮罩和轮询
    showRestart.value = true
    pollCount = 0
    pollTimeout.value = false
    
    pollTimer = setInterval(async () => {
      pollCount++
      if (pollCount > 15) { // 30s
        clearInterval(pollTimer)
        pollTimeout.value = true
        return
      }
      const isUp = await pollHealth()
      if (isUp) {
        clearInterval(pollTimer)
        showRestart.value = false
        await loadData()
      }
    }, 2000)

  } catch (err) {
    configSaveError.value = err.message || '保存配置失败'
  } finally {
    savingConfig.value = false
  }
}

async function savePrompts() {
  try {
    savingPrompt.value = true
    promptSaveError.value = ''
    promptSaveSuccess.value = ''

    let parsedGlossary
    try {
      parsedGlossary = JSON.parse(prompts.glossaryStr)
    } catch (e) {
      throw new Error('Glossary JSON 格式错误: ' + e.message)
    }

    if (typeof parsedGlossary !== 'object' || Array.isArray(parsedGlossary) || parsedGlossary === null) {
      throw new Error('Glossary 必须是 JSON 对象')
    }

    const payload = {
      system_prompt: prompts.system_prompt,
      glossary: parsedGlossary
    }

    await putPrompts(payload)
    promptSaveSuccess.value = '保存成功'

    // 更新原始值
    originalPrompts.system_prompt = prompts.system_prompt
    originalPrompts.glossaryStr = JSON.stringify(parsedGlossary, null, 2)
    prompts.glossaryStr = originalPrompts.glossaryStr // 格式化
  } catch (err) {
    promptSaveError.value = err.message || '保存 Prompt 失败'
  } finally {
    savingPrompt.value = false
  }
}
</script>

<style scoped>
.loading-overlay { background: #fdf6ec; border: 1px solid #f0d88a; border-radius: 8px; padding: 12px 16px; margin-bottom: 16px; font-size: 13px; color: #94600; display: flex; align-items: center; gap: 8px; }
.error-message { background: #fee; border: 1px solid #fcc; border-radius: 8px; padding: 12px 16px; margin-bottom: 16px; font-size: 13px; color: #c00; }
.config-error { margin: 0 16px 16px; }
.success-message { background: #eefbee; border: 1px solid #cce8cc; border-radius: 8px; padding: 12px 16px; margin: 0 16px 16px; font-size: 13px; color: #18a058; }

.restart-mask { position: fixed; top: 0; left: 0; width: 100vw; height: 100vh; background: rgba(0,0,0,0.6); display: flex; justify-content: center; align-items: center; z-index: 9999; }
.mask-content { background: #fff; padding: 30px 40px; border-radius: 12px; display: flex; align-items: center; gap: 16px; font-size: 16px; color: #333; box-shadow: 0 4px 20px rgba(0,0,0,0.15); }
.mask-icon { font-size: 24px; }

.spinner { width: 18px; height: 18px; border: 2px solid #f0d88a; border-top-color: #f0a020; border-radius: 50%; animation: spin 1s linear infinite; }
.loading-overlay .spinner { border-color: #f0d88a; border-top-color: #f0a020; }
.restart-mask .spinner { width: 24px; height: 24px; border: 3px solid #eee; border-top-color: #18a058; }

@keyframes spin { to { transform: rotate(360deg); } }

.config-section { background: #fff; border-radius: 8px; border: 1px solid #e8e8ec; margin-bottom: 16px; overflow: hidden; }
.config-section-title { padding: 14px 16px; font-weight: 500; font-size: 14px; border-bottom: 1px solid #f0f0f4; background: #fafafa; }
.config-subsection { border-bottom: 1px solid #f0f0f4; }
.config-subsection:last-of-type { border-bottom: none; }
.subsection-title { padding: 10px 16px; font-size: 13px; font-weight: 500; color: #555; background: #f8f8fa; border-bottom: 1px solid #f0f0f4; }
.config-form { padding: 16px; }
.form-group { margin-bottom: 14px; }
.form-group label { display: block; font-size: 13px; color: #666; margin-bottom: 4px; }
.form-group input, .form-group select { width: 100%; padding: 8px 12px; border: 1px solid #d0d0d6; border-radius: 6px; font-size: 13px; outline: none; box-sizing: border-box; }
.form-group input:focus, .form-group select:focus { border-color: #18a058; }
.form-group .hint { font-size: 11px; color: #bbb; margin-top: 3px; }
.form-row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.input-with-toggle { position: relative; display: flex; }
.input-with-toggle input { flex: 1; padding-right: 40px; }
.toggle-btn { position: absolute; right: 4px; top: 50%; transform: translateY(-50%); border: none; background: none; cursor: pointer; font-size: 16px; padding: 4px 6px; line-height: 1; opacity: 0.6; }
.toggle-btn:hover { opacity: 1; }
.form-group textarea { width: 100%; padding: 10px 12px; border: 1px solid #d0d0d6; border-radius: 6px; font-size: 13px; outline: none; font-family: "SF Mono", "Fira Code", monospace; min-height: 180px; resize: vertical; line-height: 1.6; box-sizing: border-box; }
.form-group textarea.json-textarea { min-height: 80px; }
.form-group textarea:focus { border-color: #18a058; }
.form-actions { display: flex; gap: 8px; justify-content: flex-end; padding: 0 16px 16px; }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
.btn-flash { animation: flash-green 0.8s ease; }
@keyframes flash-green { 0% { box-shadow: 0 0 0 0 rgba(24,160,88,0.5); } 50% { box-shadow: 0 0 0 6px rgba(24,160,88,0.2); } 100% { box-shadow: 0 0 0 0 rgba(24,160,88,0); } }
</style>
