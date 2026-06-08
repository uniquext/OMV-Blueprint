<script setup lang="ts">
import { computed } from 'vue'
import { t } from '../i18n'

const props = defineProps<{
  page: number
  pageSize: number
  total: number
}>()

const emit = defineEmits<{
  'update:page': [page: number]
  'update:pageSize': [pageSize: number]
}>()

const pageSizes = [20, 50, 100]
const pageCount = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
const canGoPrevious = computed(() => props.page > 1)
const canGoNext = computed(() => props.page < pageCount.value)

function updatePageSize(event: Event): void {
  const value = Number((event.target as HTMLSelectElement).value)
  emit('update:pageSize', value)
}
</script>

<template>
  <div class="data-pager">
    <label class="data-pager__size">
      <span>{{ t('pagerPageSize') }}</span>
      <select :value="pageSize" @change="updatePageSize">
        <option v-for="size in pageSizes" :key="size" :value="size">{{ size }}</option>
      </select>
    </label>

    <div class="data-pager__summary">
      {{ t('pagerPage') }} {{ page }} / {{ pageCount }}
      <span class="muted">({{ total }} {{ t('pagerItems') }})</span>
    </div>

    <div class="data-pager__actions">
      <button class="button" type="button" :disabled="!canGoPrevious" @click="emit('update:page', page - 1)">
        {{ t('pagerPrevious') }}
      </button>
      <button class="button" type="button" :disabled="!canGoNext" @click="emit('update:page', page + 1)">
        {{ t('pagerNext') }}
      </button>
    </div>
  </div>
</template>
