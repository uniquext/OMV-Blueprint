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
      <!-- TAB 切换 -->
      <div class="config-tabs">
        <button class="config-tab" :class="{ active: activeTab === 'config' }" @click="activeTab = 'config'">⚙️ Config</button>
        <button class="config-tab" :class="{ active: activeTab === 'strategy' }" @click="activeTab = 'strategy'">🎯 Strategy</button>
        <button class="config-tab" :class="{ active: activeTab === 'prompt' }" @click="activeTab = 'prompt'">📝 Prompt</button>
      </div>

      <!-- ═══ Config TAB ═══ -->
      <div v-show="activeTab === 'config'">
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
                  <option value="chat">Chat — 智能对话</option>
                  <option value="mt">MT — 机器翻译</option>
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
              <div class="form-group">
                <label>扫描忽略列表 (ignore_list)</label>
                <input type="text" :value="config.pipeline.ignore_list" readonly class="readonly-input" @click="openIgnoreModal" />
                <div class="hint">点击编辑，仅支持选择 scan_dir 下的目录路径，路径前缀精确匹配</div>
              </div>
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

      <!-- ═══ Strategy TAB ═══ -->
      <div v-show="activeTab === 'strategy'">
        <div v-if="configSaveError" class="error-message config-error">
          {{ configSaveError }}
        </div>
        <div class="config-subsection">
          <div class="subsection-title">🎯 漏斗策略矩阵</div>
          <div class="config-form">
            <div class="form-row">
              <div class="form-group">
                <label>位置偏好 (location_priority)</label>
                <select v-model="config.strategy.location_priority">
                  <option value="internal">内置字幕优先 (Internal First)</option>
                  <option value="external">外置字幕优先 (External First)</option>
                </select>
                <div class="hint">当内外置同时存在时优先处理哪种来源</div>
              </div>
              <div class="form-group">
                <label>语言偏好 (language_priority)</label>
                <select v-model="config.strategy.language_priority">
                  <option value="chinese">中文字幕优先 (Chinese First)</option>
                  <option value="absolute">绝对优先 (Absolute First)</option>
                </select>
                <div class="hint">优先寻找中文字幕，或按位置偏好严格筛选</div>
              </div>
            </div>
            <div class="strategy-preview">
              <div class="preview-title">当前组合优先级</div>
              <div class="preview-order">{{ strategyPreview }}</div>
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

      <!-- ═══ Prompt TAB ═══ -->
      <div v-show="activeTab === 'prompt'">
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
    <!-- 忽略列表编辑模态框 -->
    <div class="modal-overlay" v-if="showIgnoreModal" @click.self="showIgnoreModal = false">
      <div class="modal-box">
        <div class="modal-header">
          <span class="modal-title">编辑忽略列表</span>
          <button class="modal-close" @click="showIgnoreModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <div class="ignore-list-editor">
            <div class="ignore-item" v-for="(item, idx) in ignoreListItems" :key="idx">
              <input 
                type="text" 
                :value="ignoreListItems[idx]" 
                readonly
                placeholder="点击选择目录..."
                class="ignore-item-input readonly-input" 
                @click="openDirectoryPicker(idx)"
              />
              <button class="btn-icon" @click="moveIgnoreItem(idx, -1)" :disabled="idx === 0">▲</button>
              <button class="btn-icon" @click="moveIgnoreItem(idx, 1)" :disabled="idx === ignoreListItems.length - 1">▼</button>
              <button class="btn-icon btn-icon-danger" @click="removeIgnoreItem(idx)">✕</button>
            </div>
            <div v-if="ignoreListItems.length === 0" class="empty-hint">暂无忽略项，点击下方"新增"添加</div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn" @click="addIgnoreItem">➕ 新增</button>
          <div class="modal-footer-right">
            <button class="btn" @click="showIgnoreModal = false">取消</button>
            <button class="btn btn-primary" @click="confirmIgnoreList">确认</button>
          </div>
        </div>
      </div>
    </div>
    <!-- 目录选择器模态框 -->
    <DirectoryPicker 
      v-if="showDirectoryPicker" 
      :selected-paths="ignoreListItems"
      @close="showDirectoryPicker = false"
      @select="handleDirectorySelect"
    />
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import { getConfig, putConfig, getPrompts, putPrompts } from '../api/config'
import { useSSE } from '../composables/useSSE'

const loadingInit = ref(true)
const initError = ref('')

const showApiKey = ref(false)
const activeTab = ref('config')

const config = reactive({ llm: {}, pipeline: {}, media: {}, watchdog: {}, strategy: {} })
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
const isRestarting = ref(false)
const pollTimeout = ref(false)
let pollTimer = null

const { connected } = useSSE('/api/events')

watch(connected, async (newVal) => {
  if (newVal && isRestarting.value) {
    if (pollTimer) clearTimeout(pollTimer)
    isRestarting.value = false
    showRestart.value = false
    await loadData()
  }
})

const savingConfig = ref(false)
const configSaveError = ref('')

const savingPrompt = ref(false)
const promptSaveError = ref('')
const promptSaveSuccess = ref('')

const configResetFlash = ref(false)
const promptResetFlash = ref(false)

const showIgnoreModal = ref(false)
const ignoreListItems = ref([])

const showDirectoryPicker = ref(false)
const currentEditingIndex = ref(-1)

function openDirectoryPicker(idx) {
  currentEditingIndex.value = idx
  showDirectoryPicker.value = true
}

function handleDirectorySelect(path) {
  if (currentEditingIndex.value >= 0 && currentEditingIndex.value < ignoreListItems.value.length) {
    ignoreListItems.value[currentEditingIndex.value] = path
  }
  showDirectoryPicker.value = false
}

const strategyPreview = computed(() => {
  const loc = config.strategy.location_priority
  const lang = config.strategy.language_priority
  const labels = { 10: '内置中文', 11: '内置繁体', 12: '内置外语', 20: '外置中文', 21: '外置繁体', 22: '外置外语' }
  const orders = {
    'internal-absolute': [10, 11, 12, 20, 21, 22],
    'internal-chinese': [10, 20, 11, 21, 12, 22],
    'external-absolute': [20, 21, 22, 10, 11, 12],
    'external-chinese': [20, 10, 21, 11, 22, 12]
  }
  const order = orders[`${loc}-${lang}`] || orders['external-chinese']
  return order.map(t => labels[t]).join(' → ')
})

function openIgnoreModal() {
  ignoreListItems.value = (config.pipeline.ignore_list || '').split(',').map(s => s.trim()).filter(Boolean)
  showIgnoreModal.value = true
}

function addIgnoreItem() {
  ignoreListItems.value.push('')
}

function removeIgnoreItem(idx) {
  ignoreListItems.value.splice(idx, 1)
}

function moveIgnoreItem(idx, direction) {
  const newIdx = idx + direction
  if (newIdx < 0 || newIdx >= ignoreListItems.value.length) return
  const items = ignoreListItems.value
  ;[items[idx], items[newIdx]] = [items[newIdx], items[idx]]
}

function confirmIgnoreList() {
  config.pipeline.ignore_list = ignoreListItems.value.filter(s => s.trim()).join(',')
  showIgnoreModal.value = false
}

onMounted(async () => {
  await loadData()
})

onUnmounted(() => {
  if (pollTimer) clearTimeout(pollTimer)
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
    Object.assign(config.strategy, originalConfig.strategy || { location_priority: "external", language_priority: "chinese" })
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
    
    // 触发重启遮罩
    showRestart.value = true
    isRestarting.value = true
    pollTimeout.value = false
    
    if (pollTimer) clearTimeout(pollTimer)
    pollTimer = setTimeout(() => {
      if (isRestarting.value) {
        pollTimeout.value = true
      }
    }, 30000)

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
.config-tabs { display: flex; border-bottom: 1px solid #e8e8ec; background: #fafafa; }
.config-tab { flex: 1; padding: 12px 16px; border: none; background: none; cursor: pointer; font-size: 14px; font-weight: 500; color: #999; border-bottom: 2px solid transparent; transition: all 0.2s; }
.config-tab:hover { color: #555; background: #f0f0f4; }
.config-tab.active { color: #18a058; border-bottom-color: #18a058; background: #fff; }
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

.readonly-input { cursor: pointer; background: #f8f8fa; }
.readonly-input:hover { border-color: #18a058; }

/* Strategy TAB */
.strategy-preview { margin-top: 16px; padding: 12px 16px; background: #f8f8fa; border-radius: 6px; border: 1px solid #e8e8ec; }
.preview-title { font-size: 12px; color: #999; margin-bottom: 6px; }
.preview-order { font-size: 13px; color: #18a058; font-weight: 500; }

/* Modal */
.modal-overlay { position: fixed; top: 0; left: 0; width: 100vw; height: 100vh; background: rgba(0,0,0,0.45); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal-box { background: #fff; border-radius: 10px; width: 90%; max-width: 560px; max-height: 80vh; display: flex; flex-direction: column; box-shadow: 0 8px 32px rgba(0,0,0,0.18); }
.modal-header { display: flex; align-items: center; justify-content: space-between; padding: 14px 20px; border-bottom: 1px solid #e8e8ec; }
.modal-title { font-size: 15px; font-weight: 600; color: #333; }
.modal-close { background: none; border: none; font-size: 22px; cursor: pointer; color: #999; padding: 0 4px; line-height: 1; }
.modal-close:hover { color: #333; }
.modal-body { padding: 16px 20px; overflow-y: auto; flex: 1; }
.modal-footer { display: flex; align-items: center; justify-content: space-between; padding: 12px 20px; border-top: 1px solid #e8e8ec; }
.modal-footer-right { display: flex; gap: 8px; }

.ignore-list-editor { display: flex; flex-direction: column; gap: 8px; }
.ignore-item { display: flex; align-items: center; gap: 6px; }
.ignore-item-input { flex: 1; padding: 6px 10px; border: 1px solid #d0d0d6; border-radius: 4px; font-size: 13px; outline: none; }
.ignore-item-input:focus { border-color: #18a058; }
.btn-icon { padding: 4px 8px; border: 1px solid #d0d0d6; border-radius: 4px; background: #fff; cursor: pointer; font-size: 12px; line-height: 1; }
.btn-icon:hover { border-color: #18a058; color: #18a058; }
.btn-icon:disabled { opacity: 0.3; cursor: not-allowed; }
.btn-icon-danger:hover { border-color: #d03050; color: #d03050; }
.empty-hint { text-align: center; color: #bbb; font-size: 13px; padding: 16px; }
</style>
