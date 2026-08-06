<script setup lang="ts">
import { NButton, NFormItem, NText } from 'naive-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { FieldCapability } from '@/api/management'
import CapabilityValueInput from '@/components/CapabilityValueInput.vue'
import {
  emptyObjectForFields,
  fieldVisible,
  objectListValue,
  pathValue,
  withPathValue,
} from '@/utils/schemaForm'

const props = withDefaults(defineProps<{
  field: FieldCapability
  value: unknown
  disabled?: boolean
  secretStored?: boolean
  secretPlaceholder?: string
}>(), { disabled: false, secretStored: false, secretPlaceholder: '' })

const emit = defineEmits<{ 'update:value': [value: Array<Record<string, unknown>>] }>()
const { t } = useI18n()
const items = computed(() => objectListValue(props.value))

function updateItem(index: number, field: FieldCapability, value: unknown) {
  const updated = items.value.slice()
  updated[index] = withPathValue(updated[index] ?? {}, field.path, value)
  emit('update:value', updated)
}

function addItem() {
  emit('update:value', [...items.value, emptyObjectForFields(props.field.item_fields ?? [])])
}

function removeItem(index: number) {
  emit('update:value', items.value.filter((_, itemIndex) => itemIndex !== index))
}
</script>

<template>
  <section class="schema-complex-field">
    <div class="schema-complex-field__heading">
      <NText strong>{{ field.label }}<span v-if="field.required"> *</span></NText>
      <NButton size="small" secondary :disabled="disabled" @click="addItem">
        {{ t('common.create') }}
      </NButton>
    </div>
    <div v-for="(item, index) in items" :key="index" class="schema-complex-field__item">
      <div class="schema-complex-field__item-heading">
        <NText depth="3">{{ field.label }} {{ index + 1 }}</NText>
        <NButton
          size="tiny"
          type="error"
          secondary
          :disabled="disabled || Boolean(field.required && items.length <= 1)"
          @click="removeItem(index)"
        >
          {{ t('common.delete') }}
        </NButton>
      </div>
      <div class="form-grid">
        <NFormItem
          v-for="itemField in (field.item_fields ?? []).filter((candidate) => fieldVisible(item, candidate))"
          :key="itemField.path"
          :label="itemField.label"
          :required="itemField.required"
        >
          <CapabilityValueInput
            :field="itemField"
            :value="pathValue(item, itemField.path)"
            :disabled="disabled"
            :secret-placeholder="itemField.secret && secretStored ? secretPlaceholder : ''"
            @update:value="updateItem(index, itemField, $event)"
          />
        </NFormItem>
      </div>
    </div>
  </section>
</template>

<style scoped>
.schema-complex-field {
  grid-column: 1 / -1;
  display: grid;
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--mui-border);
  border-radius: 12px;
}

.schema-complex-field__heading,
.schema-complex-field__item-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.schema-complex-field__item {
  padding: 12px;
  border-radius: 10px;
  background: var(--mui-surface-soft);
}
</style>
