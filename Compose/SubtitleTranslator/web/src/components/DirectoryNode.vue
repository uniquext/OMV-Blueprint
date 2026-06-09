<template>
  <div class="tree-node">
    <div class="node-row" :class="{ 'is-selected': isSelected }">
      <div class="node-expander" @click="toggleExpand" :style="{ visibility: node.isLeaf ? 'hidden' : 'visible' }">
        <span v-if="node.loading" class="spinner node-spinner"></span>
        <span v-else class="arrow" :class="{ expanded: node.expanded }">▶</span>
      </div>
      <div class="node-icon" @click="toggleExpand">
        📁
      </div>
      <div class="node-name" @click="toggleExpand" :title="node.path">
        {{ node.name }}
      </div>
      
      <div class="node-actions">
        <span v-if="isSelected" class="selected-mark" title="已在忽略列表中">✅</span>
        <button 
          class="btn-select" 
          :disabled="isSelected"
          @click="$emit('select', node.path)"
        >
          {{ isSelected ? '已添加' : '选择' }}
        </button>
      </div>
    </div>
    
    <div v-if="node.expanded && node.children && node.children.length > 0" class="node-children">
      <DirectoryNode 
        v-for="child in node.children" 
        :key="child.path"
        :node="child"
        :selected-paths="selectedPaths"
        @select="$emit('select', $event)"
      />
    </div>
    <div v-if="node.expanded && node.children && node.children.length === 0 && !node.loading" class="node-empty">
      (空目录)
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  node: {
    type: Object,
    required: true
  },
  selectedPaths: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['select'])

const isSelected = computed(() => {
  return props.selectedPaths.includes(props.node.path)
})

async function toggleExpand() {
  if (props.node.isLeaf) return
  
  props.node.expanded = !props.node.expanded
  
  if (props.node.expanded && !props.node.children) {
    props.node.loading = true
    try {
      const url = `/api/browse?path=${encodeURIComponent(props.node.path)}`
      const res = await fetch(url)
      if (!res.ok) throw new Error('Failed to load')
      const data = await res.json()
      
      const children = data.data.children || []
      props.node.children = children.map(c => ({
        name: c.name,
        path: c.path,
        children: null,
        expanded: false,
        loading: false,
        isLeaf: false
      }))
      
      if (props.node.children.length === 0) {
        props.node.isLeaf = true
      }
    } catch (err) {
      console.error("加载目录失败:", err)
      // On error, reset expansion
      props.node.expanded = false
    } finally {
      props.node.loading = false
    }
  }
}
</script>

<style scoped>
.tree-node {
  display: flex;
  flex-direction: column;
}
.node-row {
  display: flex;
  align-items: center;
  padding: 6px 4px;
  border-radius: 4px;
  cursor: pointer;
  transition: background-color 0.2s;
}
.node-row:hover {
  background: #f0f0f4;
}
.node-row.is-selected {
  background: #f8fff8;
}
.node-expander {
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #999;
  font-size: 10px;
}
.arrow {
  transition: transform 0.2s;
}
.arrow.expanded {
  transform: rotate(90deg);
}
.node-icon {
  margin: 0 6px;
  font-size: 14px;
}
.node-name {
  flex: 1;
  font-size: 13px;
  color: #333;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  user-select: none;
}
.node-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: 8px;
}
.selected-mark {
  font-size: 14px;
}
.btn-select {
  padding: 4px 10px;
  border: 1px solid #18a058;
  background: #fff;
  color: #18a058;
  border-radius: 4px;
  font-size: 12px;
  cursor: pointer;
}
.btn-select:hover:not(:disabled) {
  background: #18a058;
  color: #fff;
}
.btn-select:disabled {
  border-color: #d0d0d6;
  color: #999;
  background: #f0f0f4;
  cursor: not-allowed;
}
.node-children {
  padding-left: 20px;
  border-left: 1px dashed #e8e8ec;
  margin-left: 10px;
}
.node-empty {
  padding-left: 30px;
  font-size: 12px;
  color: #bbb;
  font-style: italic;
  margin: 4px 0;
}
.node-spinner {
  width: 12px;
  height: 12px;
  border: 2px solid #ccc;
  border-top-color: #18a058;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
