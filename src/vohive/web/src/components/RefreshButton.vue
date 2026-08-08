<script setup lang="ts">
import { ArrowSync24Regular } from '@vicons/fluent'

const props = withDefaults(defineProps<{
  loading?: boolean
  disabled?: boolean
  label?: string
}>(), {
  label: '刷新'
})

defineEmits<{
  (e: 'click'): void
}>()
</script>

<template>
  <el-button :loading="props.loading" :disabled="props.disabled" class="!border-0 !bg-white/70 dark:!bg-white/5 ui-action-btn" @click="$emit('click')">
    <el-icon><ArrowSync24Regular /></el-icon>
    {{ props.label }}
  </el-button>
</template>
