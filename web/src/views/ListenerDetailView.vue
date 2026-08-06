<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NDatePicker,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSpace,
  NSpin,
  NSwitch,
  NTabPane,
  NTabs,
  NTag,
  NText,
  useDialog,
  useMessage,
} from 'naive-ui'
import QrcodeVue from 'qrcode.vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import {
  createNode,
  createUser,
  createUsers,
  deleteUser,
  generateRealityKeypair,
  getCapabilities,
  getNode,
  getShare,
  setNodeEnabled,
  setUserEnabled,
  setUsersEnabled,
  updateNode,
  updateUser,
  type CapabilityManifest,
  type ComponentCapability,
  type ComponentGroup,
  type LayerCapability,
  type Node,
  type NodeInput,
  type ProtocolCapability,
  type ProtocolKind,
  type Share,
  type User,
  type UserInput,
} from '@/api/management'
import AppShell from '@/components/AppShell.vue'
import CapabilityFields from '@/components/CapabilityFields.vue'
import CopyButton from '@/components/CopyButton.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useManagementStore } from '@/stores/management'
import {
  cloneUserDefaults,
  componentConfig,
  componentIdentifier,
  componentOptions,
  componentSecretPrefix,
  componentSelection,
  evaluateComponentSelection,
  protocolCapability,
  replaceProtocolDefaults,
  selectComponentConfig,
  setComponentEnabled,
  updateComponentConfig,
} from '@/utils/capabilities'
import { errorTranslationKey } from '@/utils/errors'
import { formatCredentialSummary, formatDateTime } from '@/utils/format'
import { cloneJSONValue, pathValue, secretPathConfigured, withPathValue } from '@/utils/schemaForm'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const dialog = useDialog()
const message = useMessage()
const auth = useAuthStore()
const management = useManagementStore()
const preferredInitialProtocol = 'vless'

const node = ref<Node | null>(null)
const capabilities = ref<CapabilityManifest | null>(null)
const loading = ref(true)
const saving = ref(false)
const form = reactive<NodeInput>(baseNode())
const showAdvancedParameters = ref(false)
const showUserEdit = ref(false)
const showShare = ref(false)
const showBatchUsers = ref(false)
const editingUserID = ref('')
const share = ref<Share | null>(null)
const userForm = reactive({ name: '', enabled: true, expiresAt: null as number | null })
const userProtocolForm = ref<Record<string, unknown>>({})
const selectedUserIDs = ref<string[]>([])
const batchUserForm = reactive({ names: '', enabled: true, expiresAt: null as number | null })

const isCreate = computed(() => route.name === 'node-create')
const nodeID = computed(() => String(route.params.id ?? ''))
const title = computed(() => isCreate.value ? t('nodes.create') : (node.value?.name || t('nodes.edit')))
const protocolOptions = computed(() => (capabilities.value?.protocols ?? []).map((item) => ({ label: item.label, value: item.kind })))
const activeProtocol = computed(() => protocolCapability(capabilities.value, form.protocol))
const selectedComponents = computed(() => componentSelection(activeProtocol.value, form))
const primaryUserField = computed(() => activeProtocol.value?.user_fields.find((field) => field.secret) ?? activeProtocol.value?.user_fields[0])
// User capability paths are rooted in the user payload. Unlike component
// fields, their secrets_set keys do not need a component path prefix.
const userSecretPathPrefix = ''
const editingUserSecrets = computed(() =>
  node.value?.users.find((user) => user.id === editingUserID.value)?.secrets_set ?? {},
)
const allUsersSelected = computed(() => Boolean(node.value?.users.length) && node.value!.users.every((user) => selectedUserIDs.value.includes(user.id)))
const someUsersSelected = computed(() => node.value?.users.some((user) => selectedUserIDs.value.includes(user.id)) ?? false)

function baseNode(): NodeInput {
  return {
    name: '',
    enabled: false,
    listen: '0.0.0.0',
    port: '443',
    protocol: preferredInitialProtocol,
    access_profiles: [{
      name: 'default', default: true, public_host: '', public_port: 443,
      server_name: '', fingerprint: 'chrome', packet_encoding: 'xudp', allow_insecure: false,
    }],
  }
}

function replaceForm(value: Record<string, unknown>) {
  for (const key of Object.keys(form)) delete form[key]
  Object.assign(form, cloneJSONValue(value) as NodeInput)
}

function hydrate(value: Node) {
  node.value = value
  const input = cloneJSONValue(value) as Record<string, unknown>
  for (const key of ['id', 'users', 'secrets_set', 'schema_version', 'created_at', 'updated_at']) delete input[key]
  input.access_profiles = value.access_profiles.map((profile) => ({
    id: profile.id,
    name: profile.name,
    default: profile.default,
    public_host: profile.public_host,
    public_port: profile.public_port,
    server_name: profile.server_name,
    fingerprint: profile.fingerprint,
    packet_encoding: profile.packet_encoding,
    allow_insecure: profile.allow_insecure,
  }))
  replaceForm(input)
  const currentUserIDs = new Set(value.users.map((user) => user.id))
  selectedUserIDs.value = selectedUserIDs.value.filter((id) => currentUserIDs.has(id))
}

onMounted(async () => {
  try {
    capabilities.value = await getCapabilities()
    if (isCreate.value) {
      const firstProtocol = capabilities.value.protocols.some((item) => item.kind === form.protocol)
        ? form.protocol
        : capabilities.value.protocols[0]?.kind
      if (!firstProtocol) throw new Error('capability manifest has no protocols')
      replaceForm(replaceProtocolDefaults(form, capabilities.value, firstProtocol))
    } else {
      hydrate(await getNode(nodeID.value))
    }
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    loading.value = false
  }
})

function switchProtocol(kind: ProtocolKind) {
  if (!capabilities.value) return
  showAdvancedParameters.value = false
  replaceForm(replaceProtocolDefaults(form, capabilities.value, kind))
}

function layerLabel(group: ComponentGroup): string {
  const labels: Record<ComponentGroup, string> = {
    transport: t('nodes.transport'),
    security: t('nodes.security'),
    extension: t('nodes.extensions'),
  }
  return labels[group]
}

function layerComponents(layer: LayerCapability): ComponentCapability[] {
  return activeProtocol.value?.components.filter((item) => item.group === layer.group) ?? []
}

function selectedLayerKind(layer: LayerCapability): string {
  const prefix = `${layer.group}:`
  return [...selectedComponents.value].find((identifier) => identifier.startsWith(prefix))?.slice(prefix.length) ?? ''
}

function layerOptions(layer: LayerCapability) {
  return componentOptions(activeProtocol.value, layer.group, selectedComponents.value)
}

function visibleLayerComponents(layer: LayerCapability): ComponentCapability[] {
  return layerComponents(layer).filter((component) => selectedComponents.value.has(componentIdentifier(component)))
}

function selectLayerComponent(layer: LayerCapability, kind: string) {
  const protocol = activeProtocol.value
  const candidate = layerComponents(layer).find((item) => item.kind === kind)
  if (!protocol || !candidate) return
  const evaluation = evaluateComponentSelection(protocol, candidate, selectedComponents.value)
  if (!evaluation.allowed) {
    message.error(evaluation.reasons.join('; '))
    return
  }
  showAdvancedParameters.value = false
  replaceForm(selectComponentConfig(form, candidate))
}

function componentIsEnabled(component: ComponentCapability): boolean {
  return selectedComponents.value.has(componentIdentifier(component))
}

function componentCanEnable(protocol: ProtocolCapability, component: ComponentCapability): boolean {
  return evaluateComponentSelection(protocol, component, selectedComponents.value).allowed
}

function toggleComponent(protocol: ProtocolCapability, component: ComponentCapability, enabled: boolean) {
  if (enabled) {
    const evaluation = evaluateComponentSelection(protocol, component, selectedComponents.value)
    if (!evaluation.allowed) {
      message.error(evaluation.reasons.join('; '))
      return
    }
  }
  replaceForm(setComponentEnabled(form, component, enabled))
}

function componentModel(component: ComponentCapability): Record<string, unknown> {
  return componentConfig(form, component)
}

function updateComponent(component: ComponentCapability, value: Record<string, unknown>) {
  replaceForm(updateComponentConfig(form, component, value))
}

function updateNodeFields(value: Record<string, unknown>) {
  replaceForm(value)
}

function hasRealityKeyAction(component: ComponentCapability): boolean {
  return componentIdentifier(component) === 'security:reality' && isRecord(pathValue(componentModel(component), 'reality'))
}

async function generateKeys(component: ComponentCapability) {
  try {
    const generated = await generateRealityKeypair(auth.csrfToken)
    let config = cloneJSONValue(componentModel(component))
    config = withPathValue(config, 'reality.private_key', generated.private_key)
    config = withPathValue(config, 'reality.public_key', generated.public_key)
    config = withPathValue(config, 'reality.short_ids', [generated.short_id])
    updateComponent(component, config)
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

function payload(): NodeInput {
  return cloneJSONValue(form)
}

function addAccessProfile() {
  const parsedPort = Number.parseInt(form.port, 10)
  form.access_profiles ??= []
  form.access_profiles.push({
    name: `profile-${form.access_profiles.length + 1}`,
    default: form.access_profiles.length === 0,
    public_host: '',
    public_port: Number.isInteger(parsedPort) && parsedPort > 0 && parsedPort <= 65535 ? parsedPort : 443,
    server_name: '',
    fingerprint: 'chrome',
    packet_encoding: 'xudp',
    allow_insecure: false,
  })
}

function removeAccessProfile(index: number) {
  if (!form.access_profiles || form.access_profiles.length <= 1) return
  const removedDefault = form.access_profiles[index]?.default
  form.access_profiles.splice(index, 1)
  if (removedDefault && form.access_profiles[0]) form.access_profiles[0].default = true
}

function makeDefaultProfile(index: number) {
  form.access_profiles?.forEach((profile, current) => { profile.default = current === index })
}

async function save() {
  saving.value = true
  try {
    const saved = isCreate.value
      ? await createNode(auth.csrfToken, payload())
      : await updateNode(auth.csrfToken, nodeID.value, payload())
    hydrate(saved)
    await management.refreshNodes()
    message.success(t(isCreate.value ? 'nodes.created' : 'nodes.updated'))
    if (isCreate.value) await router.replace({ name: 'node-detail', params: { id: saved.id } })
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}

async function toggleNode() {
  if (!node.value) return
  try {
    hydrate(await setNodeEnabled(auth.csrfToken, node.value.id, !node.value.enabled))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

function protocolUserDefaults(): Record<string, unknown> {
  return cloneUserDefaults(capabilities.value, form.protocol) as Record<string, unknown> ?? {}
}

function openNewUser() {
  editingUserID.value = ''
  Object.assign(userForm, { name: '', enabled: true, expiresAt: null })
  userProtocolForm.value = protocolUserDefaults()
  showUserEdit.value = true
}

function openBatchUsers() {
  Object.assign(batchUserForm, { names: '', enabled: true, expiresAt: null })
  showBatchUsers.value = true
}

function openUserEdit(user: User) {
  editingUserID.value = user.id
  Object.assign(userForm, {
    name: user.name,
    enabled: user.enabled,
    expiresAt: user.expires_at ? new Date(user.expires_at).getTime() : null,
  })
  let protocolInput = protocolUserDefaults()
  for (const field of activeProtocol.value?.user_fields ?? []) {
    const value = pathValue(user, field.path)
    if (value !== undefined) protocolInput = withPathValue(protocolInput, field.path, cloneJSONValue(value))
  }
  userProtocolForm.value = protocolInput
  showUserEdit.value = true
}

function updateUserProtocol(value: Record<string, unknown>) {
  userProtocolForm.value = value
}

function userPayload(): UserInput {
  return {
    name: userForm.name.trim(),
    enabled: userForm.enabled,
    expires_at: userForm.expiresAt ? new Date(userForm.expiresAt).toISOString() : null,
    ...cloneJSONValue(userProtocolForm.value),
  }
}

async function saveUser() {
  if (!node.value) return
  saving.value = true
  try {
    if (editingUserID.value) await updateUser(auth.csrfToken, node.value.id, editingUserID.value, userPayload())
    else await createUser(auth.csrfToken, node.value.id, userPayload())
    hydrate(await getNode(node.value.id))
    showUserEdit.value = false
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}

async function toggleUser(user: User) {
  if (!node.value) return
  try {
    await setUserEnabled(auth.csrfToken, node.value.id, user.id, !user.enabled)
    hydrate(await getNode(node.value.id))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

function toggleAllUsers(checked: boolean) {
  selectedUserIDs.value = checked ? (node.value?.users.map((user) => user.id) ?? []) : []
}

function toggleUserSelection(userID: string, checked: boolean) {
  selectedUserIDs.value = checked
    ? [...new Set([...selectedUserIDs.value, userID])]
    : selectedUserIDs.value.filter((id) => id !== userID)
}

function confirmBatchUsersEnabled(enabled: boolean) {
  if (!node.value || !selectedUserIDs.value.length) return
  if (selectedUserIDs.value.length > 500) {
    message.error(t('users.batchLimit'))
    return
  }
  dialog.warning({
    title: enabled ? t('users.batchEnable') : t('users.batchDisable'),
    content: t('users.batchPublishBody', { count: selectedUserIDs.value.length }),
    positiveText: t('common.confirm'), negativeText: t('common.cancel'),
    async onPositiveClick() {
      saving.value = true
      try {
        await setUsersEnabled(auth.csrfToken, node.value!.id, selectedUserIDs.value, enabled)
        selectedUserIDs.value = []
        hydrate(await getNode(node.value!.id))
        message.success(t('users.batchUpdated'))
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      } finally {
        saving.value = false
      }
    },
  })
}

async function saveBatchUsers() {
  if (!node.value) return
  const names = [...new Set(batchUserForm.names.split(/\r?\n/).map((name) => name.trim()).filter(Boolean))]
  if (!names.length) return
  if (names.length > 500) {
    message.error(t('users.batchLimit'))
    return
  }
  if (activeProtocol.value?.features?.includes('single-active-user') && names.length > 1) {
    message.error(t('users.singleUserProtocol'))
    return
  }
  const expiresAt = batchUserForm.expiresAt ? new Date(batchUserForm.expiresAt).toISOString() : null
  const users = names.map<UserInput>((name) => ({
    name,
    enabled: batchUserForm.enabled,
    expires_at: expiresAt,
    ...protocolUserDefaults(),
  }))
  saving.value = true
  try {
    await createUsers(auth.csrfToken, node.value.id, users)
    showBatchUsers.value = false
    hydrate(await getNode(node.value.id))
    message.success(t('users.batchCreated', { count: names.length }))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}

function confirmDeleteUser(user: User) {
  if (!node.value) return
  dialog.error({
    title: t('users.deleteTitle'), content: t('users.deleteBody'),
    positiveText: t('common.delete'), negativeText: t('common.cancel'),
    async onPositiveClick() {
      await deleteUser(auth.csrfToken, node.value!.id, user.id)
      hydrate(await getNode(node.value!.id))
    },
  })
}

async function openShare(user: User) {
  if (!node.value) return
  try {
    share.value = await getShare(node.value.id, user.id)
    showShare.value = true
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

function credential(user: User): string {
  const field = primaryUserField.value
  const value = field ? pathValue(user, field.path) : undefined
  const configured = field?.secret === true
    ? secretPathConfigured(user.secrets_set, field.path, userSecretPathPrefix)
    : false
  return formatCredentialSummary(value, configured, t('users.credentialConfigured'))
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
</script>

<template>
  <AppShell>
    <main class="page-container node-editor-page">
      <NSpin :show="loading">
        <PageHeader :title="title" :description="t('nodes.editorDescription')">
          <template #actions>
            <NSpace>
              <NButton @click="router.push({ name: 'nodes' })">{{ t('common.back') }}</NButton>
              <NButton v-if="node" @click="toggleNode">{{ node.enabled ? t('nodes.disable') : t('nodes.enable') }}</NButton>
              <NButton type="primary" :loading="saving" @click="save">{{ t('common.save') }}</NButton>
            </NSpace>
          </template>
        </PageHeader>

        <NAlert v-if="capabilities" type="info" :bordered="false" class="section-gap">
          {{ t('nodes.sourceContract', { branch: capabilities.source.branch, commit: capabilities.source.commit.slice(0, 12) }) }}
        </NAlert>

        <NForm size="large">
          <NCard :title="t('nodes.general')" :bordered="false" class="surface-card section-gap">
            <div class="form-grid">
              <NFormItem :label="t('nodes.name')" required><NInput v-model:value="form.name" maxlength="64" /></NFormItem>
              <NFormItem :label="t('nodes.protocol')" required><NSelect :value="form.protocol" :options="protocolOptions" :disabled="Boolean(node?.users.length)" @update:value="switchProtocol" /></NFormItem>
              <NFormItem :label="t('nodes.listen')" required><NInput v-model:value="form.listen" /></NFormItem>
              <NFormItem :label="t('nodes.port')" required><NInput v-model:value="form.port" placeholder="443 or 20000-30000" /></NFormItem>
              <NFormItem v-if="!isCreate" :label="t('nodes.enabled')"><NSwitch v-model:value="form.enabled" /></NFormItem>
            </div>
          </NCard>

          <NCard v-if="activeProtocol" :title="activeProtocol.label" :bordered="false" class="surface-card section-gap">
            <div class="form-grid">
              <NFormItem :label="t('nodes.advancedParameters')"><NSwitch v-model:value="showAdvancedParameters" /></NFormItem>
            </div>
            <CapabilityFields
              :model-value="form as unknown as Record<string, unknown>"
              :fields="activeProtocol.fields ?? []"
              :show-advanced="showAdvancedParameters"
              :secrets-set="node?.secrets_set ?? {}"
              :secret-placeholder="t('nodes.secretStored')"
              @update:model-value="updateNodeFields"
            />
            <NTabs v-if="activeProtocol.layers.length" type="line" animated>
              <NTabPane v-for="layer in activeProtocol.layers" :key="layer.group" :name="layer.group" :tab="layerLabel(layer.group)">
                <template v-if="!layer.multiple">
                  <div class="form-grid">
                    <NFormItem :label="layerLabel(layer.group)">
                      <NTag v-if="layer.locked" :bordered="false">{{ visibleLayerComponents(layer)[0]?.label ?? selectedLayerKind(layer) }}</NTag>
                      <NSelect v-else :value="selectedLayerKind(layer)" :options="layerOptions(layer)" @update:value="selectLayerComponent(layer, $event)" />
                    </NFormItem>
                  </div>
                  <template v-for="component in visibleLayerComponents(layer)" :key="componentIdentifier(component)">
                    <CapabilityFields
                      :model-value="componentModel(component)"
                      :fields="component.fields ?? []"
                      :show-advanced="showAdvancedParameters"
                      :secrets-set="node?.secrets_set ?? {}"
                      :secret-path-prefix="componentSecretPrefix(component)"
                      :secret-placeholder="t('nodes.secretStored')"
                      @update:model-value="updateComponent(component, $event)"
                    />
                    <NSpace v-if="hasRealityKeyAction(component)" class="section-gap">
                      <NButton secondary @click="generateKeys(component)">{{ t('nodes.generateReality') }}</NButton>
                    </NSpace>
                  </template>
                </template>
                <template v-else>
                  <section v-for="component in layerComponents(layer)" :key="componentIdentifier(component)" class="section-gap">
                    <NFormItem :label="component.label">
                      <NSwitch
                        :value="componentIsEnabled(component)"
                        :disabled="!componentIsEnabled(component) && !componentCanEnable(activeProtocol, component)"
                        @update:value="toggleComponent(activeProtocol, component, $event)"
                      />
                    </NFormItem>
                    <CapabilityFields
                      v-if="componentIsEnabled(component)"
                      :model-value="componentModel(component)"
                      :fields="component.fields ?? []"
                      :show-advanced="showAdvancedParameters"
                      :secrets-set="node?.secrets_set ?? {}"
                      :secret-path-prefix="componentSecretPrefix(component)"
                      :secret-placeholder="t('nodes.secretStored')"
                      @update:model-value="updateComponent(component, $event)"
                    />
                  </section>
                </template>
              </NTabPane>
            </NTabs>
          </NCard>

          <NCard :title="t('nodes.accessProfiles')" :bordered="false" class="surface-card section-gap">
            <NAlert type="info" :bordered="false" class="section-gap">{{ t('nodes.accessProfilesHint') }}</NAlert>
            <NSpace class="section-gap"><NButton secondary @click="addAccessProfile">{{ t('nodes.addAccessProfile') }}</NButton></NSpace>
            <div v-for="(profile, index) in form.access_profiles" :key="profile.id || index" class="form-grid section-gap">
              <NFormItem :label="t('nodes.profileName')"><NInput v-model:value="profile.name" /></NFormItem>
              <NFormItem :label="t('nodes.publicHost')"><NInput v-model:value="profile.public_host" :placeholder="t('nodes.globalPublicHost')" /></NFormItem>
              <NFormItem :label="t('nodes.publicPort')"><NInputNumber v-model:value="profile.public_port" :min="1" :max="65535" class="full-width" /></NFormItem>
              <NFormItem label="SNI / Server Name"><NInput v-model:value="profile.server_name" /></NFormItem>
              <NFormItem label="Client fingerprint"><NInput v-model:value="profile.fingerprint" placeholder="chrome" /></NFormItem>
              <NFormItem label="Packet encoding"><NInput v-model:value="profile.packet_encoding" placeholder="xudp" /></NFormItem>
              <NFormItem label="Allow insecure"><NSwitch v-model:value="profile.allow_insecure" /></NFormItem>
              <NFormItem label="Default"><NButton :type="profile.default ? 'primary' : 'default'" secondary @click="makeDefaultProfile(index)">{{ profile.default ? 'Default profile' : 'Set default' }}</NButton></NFormItem>
              <NFormItem><NButton type="error" secondary :disabled="(form.access_profiles?.length ?? 0) <= 1" @click="removeAccessProfile(index)">{{ t('common.delete') }}</NButton></NFormItem>
            </div>
          </NCard>
        </NForm>

        <NCard v-if="node" :bordered="false" class="surface-card section-gap">
          <div class="card-heading"><div><NText tag="h2" class="section-title">{{ t('users.title') }}</NText><NText depth="3">{{ node.users.length }}</NText></div><NSpace :wrap="true"><NButton secondary @click="openBatchUsers">{{ t('users.batchAdd') }}</NButton><NButton type="primary" @click="openNewUser">{{ t('users.add') }}</NButton></NSpace></div>
          <NSpace v-if="selectedUserIDs.length" class="section-gap" :wrap="true">
            <NTag :bordered="false">{{ t('users.selectedCount', { count: selectedUserIDs.length }) }}</NTag>
            <NButton size="small" :loading="saving" @click="confirmBatchUsersEnabled(true)">{{ t('users.batchEnable') }}</NButton>
            <NButton size="small" :loading="saving" @click="confirmBatchUsersEnabled(false)">{{ t('users.batchDisable') }}</NButton>
          </NSpace>
          <NAlert v-if="!node.users.length" type="warning" :bordered="false">{{ t('users.empty') }}</NAlert>
          <div v-else class="table-scroll"><table class="data-table"><thead><tr><th><NCheckbox :checked="allUsersSelected" :indeterminate="someUsersSelected && !allUsersSelected" :aria-label="t('users.selectAll')" @update:checked="toggleAllUsers" /></th><th>{{ t('users.name') }}</th><th>{{ primaryUserField?.label ?? t('nodes.credential') }}</th><th>{{ t('users.expiresAt') }}</th><th>{{ t('users.status') }}</th><th class="actions-cell">{{ t('common.actions') }}</th></tr></thead><tbody>
            <tr v-for="user in node.users" :key="user.id"><td><NCheckbox :checked="selectedUserIDs.includes(user.id)" :aria-label="t('users.selectUser', { name: user.name })" @update:checked="toggleUserSelection(user.id, $event)" /></td><td><strong>{{ user.name }}</strong></td><td><code>{{ credential(user) }}</code></td><td>{{ user.expires_at ? formatDateTime(user.expires_at, locale) : t('common.never') }}</td><td><NTag :type="user.enabled ? 'success' : 'default'" :bordered="false">{{ user.enabled ? t('common.enabled') : t('common.disabled') }}</NTag></td><td class="actions-cell"><NSpace justify="end" :wrap="false"><NButton size="small" secondary :disabled="!node.enabled || !user.enabled" @click="openShare(user)">{{ t('users.share') }}</NButton><NButton size="small" @click="openUserEdit(user)">{{ t('common.edit') }}</NButton><NButton size="small" @click="toggleUser(user)">{{ user.enabled ? t('nodes.disable') : t('nodes.enable') }}</NButton><NButton size="small" type="error" secondary @click="confirmDeleteUser(user)">{{ t('common.delete') }}</NButton></NSpace></td></tr>
          </tbody></table></div>
        </NCard>
      </NSpin>
    </main>

    <NModal v-model:show="showUserEdit" :mask-closable="!saving"><NCard class="modal-card" :title="editingUserID ? t('users.edit') : t('users.add')" :bordered="false"><NForm @submit.prevent="saveUser">
      <NFormItem :label="t('users.name')" required><NInput v-model:value="userForm.name" /></NFormItem>
      <CapabilityFields
        :model-value="userProtocolForm"
        :fields="activeProtocol?.user_fields ?? []"
        :secrets-set="editingUserSecrets"
        :secret-path-prefix="userSecretPathPrefix"
        :secret-placeholder="t('nodes.secretStored')"
        show-advanced
        @update:model-value="updateUserProtocol"
      />
      <NFormItem :label="t('users.expiresAt')"><NDatePicker v-model:value="userForm.expiresAt" type="datetime" clearable class="full-width" /></NFormItem>
      <NFormItem :label="t('nodes.enabled')"><NSwitch v-model:value="userForm.enabled" /></NFormItem>
      <NSpace justify="end"><NButton @click="showUserEdit = false">{{ t('common.cancel') }}</NButton><NButton type="primary" attr-type="submit" :loading="saving">{{ t('common.save') }}</NButton></NSpace>
    </NForm></NCard></NModal>

    <NModal v-model:show="showBatchUsers" :mask-closable="!saving"><NCard class="modal-card" :title="t('users.batchAdd')" :bordered="false"><NForm @submit.prevent="saveBatchUsers">
      <NFormItem :label="t('users.batchNames')" required><NInput v-model:value="batchUserForm.names" type="textarea" :rows="8" :placeholder="t('users.batchNamesPlaceholder')" /></NFormItem>
      <NAlert type="info" :bordered="false">{{ t('users.batchCredentialHint') }}</NAlert>
      <NFormItem :label="t('users.expiresAt')" class="section-gap"><NDatePicker v-model:value="batchUserForm.expiresAt" type="datetime" clearable class="full-width" /></NFormItem>
      <NFormItem :label="t('nodes.enabled')"><NSwitch v-model:value="batchUserForm.enabled" /></NFormItem>
      <NSpace justify="end"><NButton @click="showBatchUsers = false">{{ t('common.cancel') }}</NButton><NButton type="primary" attr-type="submit" :loading="saving">{{ t('users.batchCreate') }}</NButton></NSpace>
    </NForm></NCard></NModal>

    <NModal v-model:show="showShare"><NCard class="modal-card share-modal" :title="t('share.title')" :bordered="false"><NTabs v-if="share" type="segment"><NTabPane name="uri" :tab="t('share.uri')"><div class="share-block"><code class="break-all">{{ share.uri }}</code><CopyButton :value="share.uri" /></div></NTabPane><NTabPane name="qr" :tab="t('share.qr')"><div class="qr-wrap"><QrcodeVue :value="share.qr_content" :size="220" /></div></NTabPane><NTabPane name="yaml" :tab="t('share.yaml')"><pre class="code-panel">{{ share.client_yaml }}</pre><CopyButton :value="share.client_yaml" /></NTabPane></NTabs></NCard></NModal>
  </AppShell>
</template>
