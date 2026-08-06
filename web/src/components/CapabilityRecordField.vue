<script setup lang="ts">
import { NButton, NFormItem, NInput, NText } from 'naive-ui'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { FieldCapability } from '@/api/management'
import CapabilityValueInput from '@/components/CapabilityValueInput.vue'
import {
  emptyObjectForFields,
  fieldVisible,
  pathValue,
  recordEntries,
  recordFromEntries,
  type RecordEntry,
  withPathValue,
} from '@/utils/schemaForm'

interface EditableRecordEntry extends RecordEntry {
  id: number
}

const props = withDefaults(defineProps<{
  field: FieldCapability
  value: unknown
  disabled?: boolean
  secretStored?: boolean
  secretPlaceholder?: string
}>(), { disabled: false, secretStored: false, secretPlaceholder: '' })

const emit = defineEmits<{ 'update:value': [value: Record<string, Record<string, unknown>>] }>()
const { t } = useI18n()
let nextID = 1
const rows = ref(toEditableRows(props.value))
const duplicateKeys = computed(() => {
  const counts = new Map<string, number>()
  for (const row of rows.value) {
    const key = row.key.trim()
    if (key) counts.set(key, (counts.get(key) ?? 0) + 1)
  }
  return new Set([...counts].filter(([, count]) => count > 1).map(([key]) => key))
})

watch(() => props.value, (value) => {
  const incoming = recordEntries(value)
  const current = rows.value.map(({ key, value: item }) => ({ key, value: item }))
  if (JSON.stringify(recordFromEntries(incoming)) !== JSON.stringify(recordFromEntries(current))) {
    rows.value = toEditableRows(value)
  }
}, { deep: true })

function toEditableRows(value: unknown): EditableRecordEntry[] {
  return recordEntries(value).map((entry) => ({ ...entry, id: nextID++ }))
}

function updateKey(index: number, value: string) {
  const row = rows.value[index]
  if (row) row.key = value
}

function updateItem(index: number, field: FieldCapability, value: unknown) {
  const row = rows.value[index]
  if (!row) return
  row.value = withPathValue(row.value, field.path, value)
  commitRows()
}

function addItem() {
  rows.value.push({ id: nextID++, key: '', value: emptyObjectForFields(props.field.item_fields ?? []) })
}

function removeItem(index: number) {
  rows.value.splice(index, 1)
  commitRows()
}

function commitRows() {
  if (duplicateKeys.value.size) return
  emit('update:value', recordFromEntries(rows.value))
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
    <div v-for="(row, index) in rows" :key="row.id" class="schema-complex-field__item">
      <div class="schema-complex-field__item-heading">
        <NText depth="3">{{ field.label }} {{ index + 1 }}</NText>
        <NButton size="tiny" type="error" secondary :disabled="disabled" @click="removeItem(index)">
          {{ t('common.delete') }}
        </NButton>
      </div>
      <div class="form-grid">
        <NFormItem :label="field.item_key_label || 'Key'" required :validation-status="duplicateKeys.has(row.key.trim()) ? 'error' : undefined">
          <NInput
            :value="row.key"
            :disabled="disabled"
            @update:value="updateKey(index, $event)"
            @blur="commitRows"
          />
        </NFormItem>
        <NFormItem
          v-for="itemField in (field.item_fields ?? []).filter((candidate) => fieldVisible(row.value, candidate))"
          :key="itemField.path"
          :label="itemField.label"
          :required="itemField.required"
        >
          <CapabilityValueInput
            :field="itemField"
            :value="pathValue(row.value, itemField.path)"
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
