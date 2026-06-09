<template>
  <div class="modal-overlay picker-overlay" @click.self="$emit('close')">
    <div class="modal-box picker-box">
      <div class="modal-header">
        <span class="modal-title">选择目录</span>
        <button class="modal-close" @click="$emit('close')">&times;</button>
      </div>
      <div class="modal-body picker-body">
        <div v-if="loadingRoot" class="loading-state">
          <div class="spinner"></div> 加载中...
        </div>
        <div v-else-if="error" class="error-message">
          {{ error }}
        </div>
        <div v-else class="tree-container">
          <!-- Root node is implicit, we render its children -->
          <DirectoryNode 
            v-for="child in rootNode.children" 
            :key="child.path"
            :node="child"
            :selected-paths="selectedPaths"
            @select="onSelect"
          />
        </div>
      </div>
      <div class="modal-footer">
        <button class="btn" @click="$emit('close')">取消</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import DirectoryNode from './DirectoryNode.vue'

const props = defineProps({
  selectedPaths: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['close', 'select'])

const rootNode = ref({ path: '', children: [] })
const loadingRoot = ref(false)
const error = ref('')

async function loadBrowse(path = null) {
  const url = path ? `/api/browse?path=${encodeURIComponent(path)}` : '/api/browse'
  const res = await fetch(url)
  if (!res.ok) {
    const data = await res.json()
    throw new Error(data.detail?.message || '加载目录失败')
  }
  const data = await res.json()
  return data.data
}

onMounted(async () => {
  loadingRoot.value = true
  error.value = ''
  try {
    // 1. 获取根目录
    const rootData = await loadBrowse()
    
    // 2. 将返回的 children 转换成节点，并默认展开它们（即加载它们的第一层）
    const childrenNodes = (rootData.children || []).map(child => ({
      name: child.name,
      path: child.path,
      children: [], // 将在下面加载
      expanded: true,
      loading: true,
      isLeaf: false
    }))
    
    rootNode.value = {
      path: rootData.path,
      children: childrenNodes
    }

    // 3. 并发加载第一层的所有子目录
    await Promise.all(childrenNodes.map(async (node) => {
      try {
        const subData = await loadBrowse(node.path)
        node.children = (subData.children || []).map(c => ({
          name: c.name,
          path: c.path,
          children: null,
          expanded: false,
          loading: false,
          isLeaf: false // Will be determined on expansion
        }))
        if (node.children.length === 0) {
          node.isLeaf = true
        }
      } catch (e) {
        console.error("加载子目录失败:", node.path, e)
      } finally {
        node.loading = false
      }
    }))
    
  } catch (err) {
    error.value = err.message
  } finally {
    loadingRoot.value = false
  }
})

function onSelect(path) {
  emit('select', path)
}
</script>

<style scoped>
.picker-overlay {
  z-index: 1050; /* Higher than the first modal */
}
.picker-box {
  width: 95%;
  max-width: 600px;
  height: 80vh;
  display: flex;
  flex-direction: column;
}
.picker-body {
  padding: 12px;
  background: #fdfdfd;
  flex: 1;
  overflow-y: auto;
}
.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 32px;
  color: #888;
}
.error-message {
  color: #d03050;
  padding: 16px;
  background: #fee;
  border-radius: 6px;
  text-align: center;
}
.tree-container {
  border: 1px solid #e8e8ec;
  border-radius: 6px;
  background: #fff;
  padding: 8px;
  min-height: 200px;
}
/* Reusing spinner from global config */
.spinner { width: 16px; height: 16px; border: 2px solid #ccc; border-top-color: #18a058; border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

/* Shared modal styles (duplicated here to avoid deep CSS leakage issues, or could be global) */
.modal-overlay { position: fixed; top: 0; left: 0; width: 100vw; height: 100vh; background: rgba(0,0,0,0.45); display: flex; align-items: center; justify-content: center; }
.modal-box { background: #fff; border-radius: 10px; box-shadow: 0 8px 32px rgba(0,0,0,0.18); }
.modal-header { display: flex; align-items: center; justify-content: space-between; padding: 14px 20px; border-bottom: 1px solid #e8e8ec; }
.modal-title { font-size: 15px; font-weight: 600; color: #333; }
.modal-close { background: none; border: none; font-size: 22px; cursor: pointer; color: #999; padding: 0 4px; line-height: 1; }
.modal-close:hover { color: #333; }
.modal-footer { display: flex; align-items: center; justify-content: flex-end; padding: 12px 20px; border-top: 1px solid #e8e8ec; }
.btn { padding: 6px 14px; border: 1px solid #d0d0d6; border-radius: 6px; background: #fff; cursor: pointer; font-size: 13px; font-weight: 500; color: #333; }
.btn:hover { border-color: #18a058; color: #18a058; }
</style>
