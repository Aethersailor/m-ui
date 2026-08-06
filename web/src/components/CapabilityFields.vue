<script setup lang="ts">
import { NFormItem } from 'naive-ui'
import { computed } from 'vue'

import type { FieldCapability } from '@/api/management'
import CapabilityObjectListField from '@/components/CapabilityObjectListField.vue'
import CapabilityRecordField from '@/components/CapabilityRecordField.vue'
import CapabilityValueInput from '@/components/CapabilityValueInput.vue'
import {
  fieldVisible,
  pathValue,
  secretPathConfigured,
  withPathValue,
} from '@/utils/schemaForm'

const props = withDefaults(defineProps<{
  modelValue: Record<string, unknown>
  fields: FieldCapability[]
  showAdvanced?: boolean
  disabled?: boolean
  secretsSet?: Record<string, boolean>
  secretPathPrefix?: string
  secretPlaceholder?: string
}>(), {
  showAdvanced: false,
  disabled: false,
  secretsSet: () => ({}),
  secretPathPrefix: '',
  secretPlaceholder: '',
})

const emit = defineEmits<{ 'update:modelValue': [value: Record<string, unknown>] }>()

const visibleFields = computed(() => props.fields.filter((field) =>
  (props.showAdvanced || !field.advanced) && fieldVisible(props.modelValue, field),
))

function update(field: FieldCapability, value: unknown) {
  emit('update:modelValue', withPathValue(props.modelValue, field.path, value))
}

function secretStored(field: FieldCapability): boolean {
  return secretPathConfigured(props.secretsSet, field.path, props.secretPathPrefix)
}
</script>

<template>
  <div class="form-grid">
    <template v-for="field in visibleFields" :key="field.path">
      <CapabilityObjectListField
        v-if="field.type === 'object-list'"
        :field="field"
        :value="pathValue(modelValue, field.path)"
        :disabled="disabled"
        :secret-stored="secretStored(field)"
        :secret-placeholder="secretPlaceholder"
        @update:value="update(field, $event)"
      />
      <CapabilityRecordField
        v-else-if="field.type === 'record'"
        :field="field"
        :value="pathValue(modelValue, field.path)"
        :disabled="disabled"
        :secret-stored="secretStored(field)"
        :secret-placeholder="secretPlaceholder"
        @update:value="update(field, $event)"
      />
      <NFormItem v-else :label="field.label" :required="field.required">
        <CapabilityValueInput
          :field="field"
          :value="pathValue(modelValue, field.path)"
          :disabled="disabled"
          :secret-placeholder="field.secret && secretStored(field) ? secretPlaceholder : ''"
          @update:value="update(field, $event)"
        />
      </NFormItem>
    </template>
  </div>
</template>
