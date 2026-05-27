<template>
  <div class="navbar">
    <div class="logo">🎞️ SubtitleTranslator</div>
    <div class="nav-links">
      <router-link to="/dashboard" class="nav-link" active-class="active">
        <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/></svg>
        仪表盘
      </router-link>
      <router-link to="/history" class="nav-link" active-class="active">
        <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
        历史
      </router-link>
      <router-link to="/config" class="nav-link" active-class="active">
        <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
        配置
      </router-link>
    </div>
    <div class="right-info">
      <span class="sse-dot" :class="{ disconnected: !sseConnected }"></span>
      <span>{{ sseConnected ? 'SSE 已连接' : 'SSE 未连接' }}</span>
    </div>
  </div>
</template>

<script setup>
import { inject } from 'vue'

const sseConnected = inject('sseConnected')
</script>

<style scoped>
.navbar { background: #fff; border-bottom: 1px solid #e0e0e6; padding: 0 24px; display: flex; align-items: center; height: 56px; position: sticky; top: 0; z-index: 100; box-shadow: 0 1px 2px rgba(0,0,0,0.04); }
.navbar .logo { font-size: 16px; font-weight: 600; color: #18a058; margin-right: 32px; white-space: nowrap; }
.navbar .nav-links { display: flex; gap: 4px; }
.navbar .nav-link { padding: 8px 16px; border-radius: 4px; cursor: pointer; font-size: 14px; color: #666; transition: all 0.2s; border: none; background: none; text-decoration: none; display: flex; align-items: center; gap: 6px; }
.navbar .nav-link:hover { color: #18a058; background: #f0faf4; }
.navbar .nav-link.active { color: #18a058; background: #e8f8ef; font-weight: 500; }
.nav-icon { width: 16px; height: 16px; flex-shrink: 0; }
.navbar .right-info { margin-left: auto; font-size: 12px; color: #999; display: flex; align-items: center; gap: 8px; }
.sse-dot { width: 8px; height: 8px; border-radius: 50%; background: #18a058; display: inline-block; animation: pulse 2s infinite; }
.sse-dot.disconnected { background: #d03050; animation: none; }
@keyframes pulse { 0%,100%{ opacity:1; } 50%{ opacity:0.4; } }
</style>
