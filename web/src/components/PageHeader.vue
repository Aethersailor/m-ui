<script setup lang="ts">
import { NText } from 'naive-ui'

defineProps<{
  title: string
  description?: string
}>()
</script>

<template>
  <header class="page-header">
    <div>
      <NText tag="h1" class="page-title">{{ title }}</NText>
      <NText v-if="description" depth="3" class="page-description">
        {{ description }}
      </NText>
    </div>
    <div v-if="$slots.actions" class="page-actions">
      <slot name="actions" />
    </div>
  </header>
</template>
