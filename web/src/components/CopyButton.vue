<script setup lang="ts">
import { NButton, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import { copyText } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    value: string
    label?: string
    size?: 'tiny' | 'small' | 'medium' | 'large'
  }>(),
  { label: '', size: 'small' },
)
const message = useMessage()
const { t } = useI18n()

async function copy() {
  await copyText(props.value)
  message.success(t('common.copied'))
}
</script>

<template>
  <NButton secondary :size="size" :disabled="!value" @click="copy">
    {{ label || t('common.copy') }}
  </NButton>
</template>
