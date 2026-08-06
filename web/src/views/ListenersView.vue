<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
  useDialog,
  useMessage,
} from 'naive-ui'
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { cloneNode, deleteNode, getCapabilities, setNodeEnabled, setNodesEnabled, type CapabilityManifest, type Node } from '@/api/management'
import AppShell from '@/components/AppShell.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useManagementStore } from '@/stores/management'
import { errorTranslationKey } from '@/utils/errors'
import { protocolLabel } from '@/utils/capabilities'

const { t } = useI18n()
const router = useRouter()
const dialog = useDialog()
const message = useMessage()
const auth = useAuthStore()
const management = useManagementStore()
const capabilities = ref<CapabilityManifest | null>(null)
const search = ref('')
const protocol = ref('all')
const status = ref<'all' | 'enabled' | 'disabled'>('all')
const selectedNodeIDs = ref<string[]>([])
const showClone = ref(false)
const cloningNode = ref<Node | null>(null)
const cloneForm = reactive({ name: '', port: '', includeUsers: false })
const mutating = ref(false)
const protocolOptions = computed(() => [
  { label: t('nodes.allProtocols'), value: 'all' },
  ...(capabilities.value?.protocols ?? []).map((item) => ({ label: item.label, value: item.kind })),
])

const filteredNodes = computed(() => management.nodes.filter((node) => {
  const needle = search.value.trim().toLowerCase()
  return (!needle || node.name.toLowerCase().includes(needle) || `${node.listen}:${node.port}`.toLowerCase().includes(needle)) &&
    (protocol.value === 'all' || node.protocol === protocol.value) &&
    (status.value === 'all' || node.enabled === (status.value === 'enabled'))
}))
const allVisibleSelected = computed(() => filteredNodes.value.length > 0 && filteredNodes.value.every((node) => selectedNodeIDs.value.includes(node.id)))
const someVisibleSelected = computed(() => filteredNodes.value.some((node) => selectedNodeIDs.value.includes(node.id)))

onMounted(async () => {
  try {
    const [, manifest] = await Promise.all([management.refreshNodes(), getCapabilities()])
    capabilities.value = manifest
  }
  catch (error) { message.error(t(errorTranslationKey(error))) }
})

function confirmToggle(node: Node) {
  const target = !node.enabled
  dialog.warning({
    title: target ? t('nodes.enable') : t('nodes.disable'), content: t('nodes.publishWarning'),
    positiveText: t('common.confirm'), negativeText: t('common.cancel'),
    async onPositiveClick() {
      try { await setNodeEnabled(auth.csrfToken, node.id, target); await management.refreshNodes() }
      catch (error) { message.error(t(errorTranslationKey(error))) }
    },
  })
}

function confirmDelete(node: Node) {
  dialog.error({
    title: t('nodes.deleteTitle'), content: t('nodes.deleteBody'),
    positiveText: t('common.delete'), negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        await deleteNode(auth.csrfToken, node.id)
        selectedNodeIDs.value = selectedNodeIDs.value.filter((id) => id !== node.id)
        await management.refreshNodes()
        message.success(t('nodes.deleted'))
      }
      catch (error) { message.error(t(errorTranslationKey(error))) }
    },
  })
}

function toggleVisibleNodes(checked: boolean) {
  const visible = new Set(filteredNodes.value.map((node) => node.id))
  selectedNodeIDs.value = checked
    ? [...new Set([...selectedNodeIDs.value, ...visible])]
    : selectedNodeIDs.value.filter((id) => !visible.has(id))
}

function toggleNodeSelection(nodeID: string, checked: boolean) {
  selectedNodeIDs.value = checked
    ? [...new Set([...selectedNodeIDs.value, nodeID])]
    : selectedNodeIDs.value.filter((id) => id !== nodeID)
}

function confirmBatchEnabled(enabled: boolean) {
  if (!selectedNodeIDs.value.length) return
  if (selectedNodeIDs.value.length > 500) {
    message.error(t('nodes.batchLimit'))
    return
  }
  dialog.warning({
    title: enabled ? t('nodes.batchEnable') : t('nodes.batchDisable'),
    content: t('nodes.batchPublishBody', { count: selectedNodeIDs.value.length }),
    positiveText: t('common.confirm'), negativeText: t('common.cancel'),
    async onPositiveClick() {
      mutating.value = true
      try {
        await setNodesEnabled(auth.csrfToken, selectedNodeIDs.value, enabled)
        selectedNodeIDs.value = []
        await management.refreshNodes()
        message.success(t('nodes.batchUpdated'))
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      } finally {
        mutating.value = false
      }
    },
  })
}

function openClone(node: Node) {
  cloningNode.value = node
  const currentPort = Number.parseInt(node.port, 10)
  const suggestedPort = /^\d+$/.test(node.port) && currentPort < 65535 ? String(currentPort + 1) : ''
  Object.assign(cloneForm, { name: t('nodes.cloneName', { name: node.name }), port: suggestedPort, includeUsers: false })
  showClone.value = true
}

async function saveClone() {
  if (!cloningNode.value) return
  mutating.value = true
  try {
    const result = await cloneNode(auth.csrfToken, cloningNode.value.id, {
      name: cloneForm.name.trim(), port: cloneForm.port.trim(), include_users: cloneForm.includeUsers,
    })
    showClone.value = false
    await management.refreshNodes()
    message.success(t('nodes.cloned'))
    await router.push({ name: 'node-detail', params: { id: result.node.id } })
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    mutating.value = false
  }
}
</script>

<template>
  <AppShell>
    <main class="page-container">
      <PageHeader :title="t('nodes.title')" :description="t('nodes.description')">
        <template #actions><NButton type="primary" @click="router.push({ name: 'node-create' })">{{ t('nodes.create') }}</NButton></template>
      </PageHeader>

      <NCard :bordered="false" class="surface-card filters-card">
        <div class="node-filters">
          <NInput v-model:value="search" clearable :placeholder="t('nodes.search')" />
          <NSelect v-model:value="protocol" :options="protocolOptions" />
          <NSelect v-model:value="status" :options="[{ label: t('nodes.allStatuses'), value: 'all' }, { label: t('common.enabled'), value: 'enabled' }, { label: t('common.disabled'), value: 'disabled' }]" />
        </div>
        <NSpace v-if="selectedNodeIDs.length" class="section-gap" :wrap="true">
          <NTag :bordered="false">{{ t('nodes.selectedCount', { count: selectedNodeIDs.length }) }}</NTag>
          <NButton size="small" :loading="mutating" @click="confirmBatchEnabled(true)">{{ t('nodes.batchEnable') }}</NButton>
          <NButton size="small" :loading="mutating" @click="confirmBatchEnabled(false)">{{ t('nodes.batchDisable') }}</NButton>
        </NSpace>
      </NCard>

      <NCard :bordered="false" class="surface-card section-gap">
        <NEmpty v-if="!filteredNodes.length" :description="t('nodes.empty')" />
        <div v-else class="table-scroll"><table class="data-table"><thead><tr><th><NCheckbox :checked="allVisibleSelected" :indeterminate="someVisibleSelected && !allVisibleSelected" :aria-label="t('nodes.selectVisible')" @update:checked="toggleVisibleNodes" /></th><th>{{ t('nodes.name') }}</th><th>{{ t('nodes.protocol') }}</th><th>{{ t('nodes.endpoint') }}</th><th>{{ t('nodes.users') }}</th><th>{{ t('nodes.status') }}</th><th class="actions-cell">{{ t('common.actions') }}</th></tr></thead><tbody>
          <tr v-for="node in filteredNodes" :key="node.id">
            <td><NCheckbox :checked="selectedNodeIDs.includes(node.id)" :aria-label="t('nodes.selectNode', { name: node.name })" @update:checked="toggleNodeSelection(node.id, $event)" /></td>
            <td><a href="#" @click.prevent="router.push({ name: 'node-detail', params: { id: node.id } })"><strong>{{ node.name }}</strong></a></td>
            <td><NTag type="info" :bordered="false">{{ protocolLabel(capabilities, node.protocol) }}</NTag></td>
            <td><code>{{ node.listen }}:{{ node.port }}</code></td>
            <td>{{ node.users.filter((user) => user.enabled).length }} / {{ node.users.length }}</td>
            <td><NTag :type="node.enabled ? 'success' : 'default'" :bordered="false">{{ node.enabled ? t('common.enabled') : t('common.disabled') }}</NTag></td>
            <td class="actions-cell"><NSpace justify="end" :wrap="false"><NButton size="small" @click="router.push({ name: 'node-detail', params: { id: node.id } })">{{ t('common.edit') }}</NButton><NButton size="small" @click="openClone(node)">{{ t('nodes.clone') }}</NButton><NButton size="small" @click="confirmToggle(node)">{{ node.enabled ? t('nodes.disable') : t('nodes.enable') }}</NButton><NButton size="small" type="error" secondary @click="confirmDelete(node)">{{ t('common.delete') }}</NButton></NSpace></td>
          </tr>
        </tbody></table></div>
      </NCard>
    </main>

    <NModal v-model:show="showClone" :mask-closable="!mutating">
      <NCard class="modal-card" :title="t('nodes.cloneTitle')" :bordered="false">
        <NForm @submit.prevent="saveClone">
          <NFormItem :label="t('nodes.name')" required><NInput v-model:value="cloneForm.name" /></NFormItem>
          <NFormItem :label="t('nodes.port')" required><NInput v-model:value="cloneForm.port" placeholder="443 or 20000-30000" /></NFormItem>
          <NFormItem :label="t('nodes.cloneUsers')"><NSwitch v-model:value="cloneForm.includeUsers" /></NFormItem>
          <NAlert type="info" :bordered="false">{{ t('nodes.cloneHint') }}</NAlert>
          <NSpace justify="end" class="section-gap"><NButton @click="showClone = false">{{ t('common.cancel') }}</NButton><NButton type="primary" attr-type="submit" :loading="mutating">{{ t('nodes.clone') }}</NButton></NSpace>
        </NForm>
      </NCard>
    </NModal>
  </AppShell>
</template>
