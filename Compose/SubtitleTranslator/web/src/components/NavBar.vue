<template>
  <div class="navbar">
    <div class="logo">🎞️ SubtitleTranslator</div>
    <div class="nav-links">
      <router-link to="/dashboard" class="nav-link" active-class="active">仪表盘</router-link>
      <router-link to="/history" class="nav-link" active-class="active">历史</router-link>
      <router-link to="/config" class="nav-link" active-class="active">配置</router-link>
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
.navbar .nav-link { padding: 8px 16px; border-radius: 4px; cursor: pointer; font-size: 14px; color: #666; transition: all 0.2s; border: none; background: none; text-decoration: none; }
.navbar .nav-link:hover { color: #18a058; background: #f0faf4; }
.navbar .nav-link.active { color: #18a058; background: #e8f8ef; font-weight: 500; }
.navbar .right-info { margin-left: auto; font-size: 12px; color: #999; display: flex; align-items: center; gap: 8px; }
.sse-dot { width: 8px; height: 8px; border-radius: 50%; background: #18a058; display: inline-block; animation: pulse 2s infinite; }
.sse-dot.disconnected { background: #d03050; animation: none; }
@keyframes pulse { 0%,100%{ opacity:1; } 50%{ opacity:0.4; } }
</style>
