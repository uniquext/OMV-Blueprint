<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { useEventStream } from './composables/useEventStream'
import { eventStreamStatusLabel, setLanguage, t } from './i18n'
import { api } from './lib/api'

type TranslationKey = Parameters<typeof t>[0]

const eventStream = useEventStream()
const route = useRoute()
const sidebarCollapsed = ref(false)

const navigation = computed(() => [
  { to: '/', label: t('navDashboard'), icon: 'dashboard' },
  { to: '/files', label: t('navFiles'), icon: 'files' },
  { to: '/history', label: t('navHistory'), icon: 'history' },
  { to: '/recovery', label: t('navRecovery'), icon: 'recovery' },
  { to: '/settings', label: t('navSettings'), icon: 'settings' },
  { to: '/logs', label: t('navLogs'), icon: 'logs' }
])
const pageTitle = computed(() => {
  const titleKey = route.meta.titleKey
  return typeof titleKey === 'string' ? t(titleKey as TranslationKey) : t('appName')
})

async function loadSavedLanguage(): Promise<void> {
  try {
    const config = await api.config()
    setLanguage(config.modules.ui.language)
  } catch {
    // Keep the default language when configuration is temporarily unavailable.
  }
}

onMounted(() => {
  void loadSavedLanguage()
})
</script>

<template>
  <div class="app-shell" :class="{ 'app-shell--sidebar-collapsed': sidebarCollapsed }">
    <aside class="sidebar" aria-label="Primary navigation">
      <div class="brand">
        <button
          v-if="sidebarCollapsed"
          type="button"
          class="brand-icon brand-icon--button"
          data-ui="brand-expand"
          aria-label="展开导航"
          title="展开导航"
          @click="sidebarCollapsed = false"
        >
          <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M4 14v-4" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
            <path d="M8 18V6" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
            <path d="M12 16V8" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
            <path d="M16 19V5" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
            <path d="M19 7l1-2 1 2 2 1-2 1-1 2-1-2-2-1 2-1Z" fill="currentColor" />
          </svg>
        </button>

        <div v-else class="brand-icon" data-ui="brand-icon" aria-label="AudioCleaner">
          <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M4 14v-4" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
            <path d="M8 18V6" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
            <path d="M12 16V8" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
            <path d="M16 19V5" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" />
            <path d="M19 7l1-2 1 2 2 1-2 1-1 2-1-2-2-1 2-1Z" fill="currentColor" />
          </svg>
        </div>

        <div v-if="!sidebarCollapsed" class="brand-copy">
          <div class="brand-name">{{ t('appName') }}</div>
        </div>

        <button
          v-if="!sidebarCollapsed"
          type="button"
          class="sidebar-toggle"
          data-ui="sidebar-toggle"
          aria-label="折叠导航"
          title="折叠导航"
          @click="sidebarCollapsed = true"
        >
          <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M4 7h16M4 12h16M4 17h16" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
          </svg>
        </button>
      </div>

      <nav class="nav-list">
        <RouterLink v-for="item in navigation" :key="item.to" :to="item.to" class="nav-link" :title="item.label">
          <span class="nav-link__icon" aria-hidden="true">
            <svg v-if="item.icon === 'dashboard'" viewBox="0 0 24 24" fill="none">
              <path d="M4 13h6V4H4v9Zm10 7h6V4h-6v16ZM4 20h6v-5H4v5Z" fill="currentColor" />
            </svg>
            <svg v-else-if="item.icon === 'history'" viewBox="0 0 24 24" fill="none">
              <path d="M5 5v14h14" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
              <path d="M8 15h2m3 0h3M8 11h8M8 7h5" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
			<svg v-else-if="item.icon === 'files'" viewBox="0 0 24 24" fill="none">
				<path d="M6 3h8l4 4v14H6V3Z" stroke="currentColor" stroke-width="2" />
				<path d="M14 3v5h5M9 12h6M9 16h6" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
            <svg v-else-if="item.icon === 'recovery'" viewBox="0 0 24 24" fill="none">
              <path d="M5 8V4m0 0h4M5 4l3 3a7 7 0 1 1-2 8" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
            <svg v-else-if="item.icon === 'settings'" viewBox="0 0 24 24" fill="none">
              <path d="M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z" stroke="currentColor" stroke-width="2" />
              <path d="M4 12h3m10 0h3M12 4v3m0 10v3" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
            <svg v-else viewBox="0 0 24 24" fill="none">
              <path d="M6 4h12v16H6V4Z" stroke="currentColor" stroke-width="2" />
              <path d="M9 8h6M9 12h6M9 16h3" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
          </span>
          <span v-if="!sidebarCollapsed" class="nav-link__label">{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div class="sidebar-version" data-ui="sidebar-version">{{ t('sidebarFooter') }}</div>
    </aside>

    <main class="content-shell">
      <header class="topbar">
        <div class="topbar-title">{{ pageTitle }}</div>
        <div class="sse-indicator" :data-status="eventStream.status">
          <span class="sse-dot" aria-hidden="true"></span>
          <span>{{ t('sseStatus') }}: {{ eventStreamStatusLabel(eventStream.status) }}</span>
        </div>
      </header>

      <section class="view-frame">
        <RouterView />
      </section>
    </main>
  </div>
</template>
