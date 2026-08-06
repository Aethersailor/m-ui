<script setup lang="ts">
import { NInput, NInputNumber, NSelect, NSwitch } from 'naive-ui'
import { computed } from 'vue'

import type { FieldCapability } from '@/api/management'
import { parseFieldText } from '@/utils/schemaForm'

const props = withDefaults(defineProps<{
  field: FieldCapability
  value: unknown
  disabled?: boolean
  secretPlaceholder?: string
}>(), { disabled: false, secretPlaceholder: '' })

const emit = defineEmits<{ 'update:value': [value: unknown] }>()

const textValue = computed(() => {
  if (props.field.type === 'string-list') return Array.isArray(props.value) ? props.value.join(', ') : ''
  return typeof props.value === 'string' ? props.value : ''
})
const numberValue = computed(() => typeof props.value === 'number' ? props.value : null)
const booleanValue = computed(() => props.value === true)
const options = computed(() => props.field.options?.map((value) => ({ label: value || 'none', value })) ?? [])
const secretInput = computed(() => props.field.type === 'secret' || props.field.secret === true)
</script>

<template>
  <NSwitch
    v-if="field.type === 'boolean'"
    :value="booleanValue"
    :disabled="disabled"
    @update:value="emit('update:value', $event)"
  />
  <NInputNumber
    v-else-if="field.type === 'integer'"
    :value="numberValue"
    :min="field.minimum"
    :max="field.maximum"
    :disabled="disabled"
    class="full-width"
    @update:value="emit('update:value', $event)"
  />
  <NSelect
    v-else-if="options.length"
    :value="textValue"
    :options="options"
    :disabled="disabled"
    @update:value="emit('update:value', $event)"
  />
  <NInput
    v-else
    :value="textValue"
    :type="secretInput ? 'password' : field.type === 'text' || field.type === 'string-list' ? 'textarea' : 'text'"
    :show-password-on="secretInput ? 'click' : undefined"
    :placeholder="secretInput ? secretPlaceholder : undefined"
    :autocomplete="secretInput ? 'new-password' : undefined"
    :disabled="disabled"
    @update:value="emit('update:value', parseFieldText(field, $event))"
  />
</template>
