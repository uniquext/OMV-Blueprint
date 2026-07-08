<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { t } from '../i18n'
import { api } from '../lib/api'

const logContent = ref('')
const logLineCount = ref(100)
const loadedLogLines = ref(0)
const hasRuntimeLogs = computed(() => logContent.value.trim().length > 0)
const logViewerRef = ref<HTMLDivElement | null>(null)
const loading = ref(false)
const error = ref('')

interface RuntimeLogEntry {
  timestamp: string
  level: string
  normalizedLevel: 'debug' | 'info' | 'warn' | 'error' | ''
  message: string
}

function normalizeLevel(level: string): RuntimeLogEntry['normalizedLevel'] {
  const upper = level.toUpperCase()
  if (upper === 'DEBUG') return 'debug'
  if (upper === 'INFO') return 'info'
  if (upper === 'WARN' || upper === 'WARNING') return 'warn'
  if (upper === 'ERROR' || upper === 'CRITICAL') return 'error'
  return ''
}

function parseRuntimeLogLine(line: string): RuntimeLogEntry {
  const pythonLogMatch = line.match(
    /^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2},\d{3})\s+-\s+.+?\s+-\s+(DEBUG|INFO|WARN|WARNING|ERROR|CRITICAL)\s+-\s+(.*)$/
  )
  if (pythonLogMatch) {
    const level = pythonLogMatch[2]
    return {
      timestamp: pythonLogMatch[1],
      level,
      normalizedLevel: normalizeLevel(level),
      message: pythonLogMatch[3]
    }
  }

  const goLogMatch = line.match(/^(?:audiocleaner:\s+)?(\d{4}\/\d{2}\/\d{2}\s+\d{2}:\d{2}:\d{2})\s+(.*)$/)
  return {
    timestamp: goLogMatch?.[1] ?? '',
    level: '',
    normalizedLevel: '',
    message: goLogMatch?.[2] ?? line
  }
}

const logEntries = computed(() =>
  logContent.value
    .split('\n')
    .filter((line) => line.trim().length > 0)
    .map(parseRuntimeLogLine)
)

function scrollLogToBottom(): void {
  nextTick(() => {
    if (logViewerRef.value) {
      logViewerRef.value.scrollTop = logViewerRef.value.scrollHeight
    }
  })
}

async function loadLogs(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const result = await api.runtimeLogs(logLineCount.value)
    logContent.value = result.content || ''
    loadedLogLines.value = result.lines || 0
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : String(caught)
    logContent.value = ''
    loadedLogLines.value = 0
  } finally {
    loading.value = false
  }
}

watch(logContent, scrollLogToBottom)

onMounted(loadLogs)
</script>

<template>
  <div>
    <div v-if="error" class="alert error">
      <strong>{{ t('logsErrorLabel') }}</strong>
      <span>{{ error }}</span>
    </div>

    <div class="panel logs-panel">
      <div class="logs-toolbar">
        <h2>{{ t('logsRuntimeTitle') }} <span class="muted">({{ loadedLogLines }} {{ t('logsLines') }})</span></h2>
        <div class="logs-toolbar__actions">
          <label class="field field--inline">
            <span>{{ t('logsLineCount') }}</span>
            <select v-model.number="logLineCount" :disabled="loading" @change="loadLogs">
              <option :value="50">50 {{ t('logsLines') }}</option>
              <option :value="100">100 {{ t('logsLines') }}</option>
              <option :value="200">200 {{ t('logsLines') }}</option>
              <option :value="500">500 {{ t('logsLines') }}</option>
            </select>
          </label>
        </div>
      </div>
      <div v-if="loading" class="panel-body muted">{{ t('logsLoading') }}</div>
      <div v-if="hasRuntimeLogs" ref="logViewerRef" class="runtime-log-viewer">
        <div
          v-for="(log, index) in logEntries"
          :key="`${index}-${log.message}`"
          class="runtime-log-line"
          :class="log.normalizedLevel ? `runtime-log-line--${log.normalizedLevel}` : ''"
        >
          <span v-if="log.timestamp" class="runtime-log-line__timestamp">{{ log.timestamp }}</span>
          <span
            v-if="log.level"
            class="runtime-log-line__level"
            :class="`runtime-log-line__level--${log.normalizedLevel}`"
          >[{{ log.level }}]</span>
          <span class="runtime-log-line__message">{{ log.message }}</span>
        </div>
      </div>
      <div v-else-if="!loading" class="runtime-log-viewer runtime-log-viewer--empty">{{ t('logsNoRecent') }}</div>
    </div>
  </div>
</template>
