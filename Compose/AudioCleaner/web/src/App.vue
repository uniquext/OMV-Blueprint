<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { useEventStream } from './composables/useEventStream'
import { eventStreamStatusLabel, t } from './i18n'

type TranslationKey = Parameters<typeof t>[0]

const eventStream = useEventStream()
const route = useRoute()

const navigation = computed(() => [
  { to: '/', label: t('navDashboard') },
  { to: '/history', label: t('navHistory') },
  { to: '/backups', label: t('navBackups') },
  { to: '/settings', label: t('navSettings') },
  { to: '/logs', label: t('navLogs') }
])
const pageTitle = computed(() => {
  const titleKey = route.meta.titleKey
  return typeof titleKey === 'string' ? t(titleKey as TranslationKey) : t('appName')
})
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar" aria-label="Primary navigation">
      <div class="brand">
        <div class="brand-mark">AC</div>
        <div>
          <div class="brand-name">{{ t('appName') }}</div>
          <div class="brand-subtitle">{{ t('sidebarFooter') }}</div>
        </div>
      </div>

      <nav class="nav-list">
        <RouterLink v-for="item in navigation" :key="item.to" :to="item.to" class="nav-link">
          {{ item.label }}
        </RouterLink>
      </nav>
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
