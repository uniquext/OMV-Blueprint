<template>
  <div class="app-container">
    <NavBar />
    <div class="content">
      <router-view />
    </div>
  </div>
</template>

<script setup>
import { provide } from 'vue'
import NavBar from './components/NavBar.vue'
import { useSSE } from './composables/useSSE'

const { connected } = useSSE('/api/events')
provide('sseConnected', connected)
</script>

<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { 
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif; 
  background: #f5f7f9; 
  color: #333; 
  min-height: 100vh; 
}
.app-container {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
}
.content { 
  max-width: 1200px; 
  margin: 0 auto; 
  padding: 20px 24px; 
  width: 100%;
}

/* 按钮通用 */
.btn { padding: 8px 20px; border-radius: 6px; border: 1px solid #d0d0d6; background: #fff; cursor: pointer; font-size: 14px; transition: all 0.2s; display: inline-flex; align-items: center; gap: 6px; }
.btn:hover { border-color: #18a058; color: #18a058; }
.btn-primary { background: #18a058; color: #fff; border-color: #18a058; }
.btn-primary:hover { background: #36ad6a; border-color: #36ad6a; color: #fff; }
.btn-primary:disabled { background: #b0b8c0; border-color: #b0b8c0; cursor: not-allowed; }
</style>
