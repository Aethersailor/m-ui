<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NDatePicker,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
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
  createUser,
  deleteUser,
  generateRealityKeypair,
  getListener,
  getShare,
  setListenerEnabled,
  setUserEnabled,
  updateListener,
  updateUser,
  type Listener,
  type ListenerInput,
  type Share,
  type User,
  type UserInput,
} from '@/api/management'
import AppShell from '@/components/AppShell.vue'
import CopyButton from '@/components/CopyButton.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { errorTranslationKey } from '@/utils/errors'
import { formatDateTime, maskCredential } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const dialog = useDialog()
const message = useMessage()
const auth = useAuthStore()
const listenerID = computed(() => String(route.params.id))
const listener = ref<Listener | null>(null)
const loading = ref(true)
const saving = ref(false)
const showListenerEdit = ref(false)
const showUserEdit = ref(false)
const showShare = ref(false)
const editingUserID = ref('')
const generatedPrivateKey = ref(false)
const share = ref<Share | null>(null)

const listenerForm = reactive<ListenerInput>({
  name: '',
  listen_address: '',
  listen_port: 443,
  public_host_override: '',
  public_port_override: null,
  server_name: '',
  reality_dest: '',
  reality_private_key: '',
  reality_public_key: '',
  short_id: '',
  udp_enabled: true,
})
const userForm = reactive({
  name: '',
  uuid: '',
  expiresAt: null as number | null,
})

onMounted(load)

async function load() {
  loading.value = true
  try {
    listener.value = await getListener(listenerID.value)
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    loading.value = false
  }
}

function openListenerEdit() {
  if (!listener.value) {
    return
  }
  Object.assign(listenerForm, {
    name: listener.value.name,
    listen_address: listener.value.listen_address,
    listen_port: listener.value.listen_port,
    public_host_override: listener.value.public_host_override,
    public_port_override: listener.value.public_port_override,
    server_name: listener.value.server_name,
    reality_dest: listener.value.reality_dest,
    reality_private_key: '',
    reality_public_key: listener.value.reality_public_key,
    short_id: listener.value.short_id,
    udp_enabled: listener.value.udp_enabled,
  })
  generatedPrivateKey.value = false
  showListenerEdit.value = true
}

async function saveListener() {
  saving.value = true
  try {
    listener.value = await updateListener(
      auth.csrfToken,
      listenerID.value,
      { ...listenerForm },
    )
    showListenerEdit.value = false
    message.success(t('listeners.updated'))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}

function confirmGenerateKeys() {
  dialog.warning({
    title: t('listeners.generateKeysTitle'),
    content: t('listeners.generateKeysBody'),
    positiveText: t('common.confirm'),
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        const generated = await generateRealityKeypair(auth.csrfToken)
        listenerForm.reality_private_key = generated.private_key
        listenerForm.reality_public_key = generated.public_key
        listenerForm.short_id = generated.short_id
        generatedPrivateKey.value = true
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}

function confirmListenerToggle() {
  if (!listener.value) {
    return
  }
  const target = !listener.value.enabled
  const action = target ? t('listeners.enable') : t('listeners.disable')
  dialog.warning({
    title: t('listeners.toggleTitle', { action }),
    content: t('listeners.toggleBody'),
    positiveText: action,
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        listener.value = await setListenerEnabled(
          auth.csrfToken,
          listenerID.value,
          target,
        )
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}

function openNewUser() {
  editingUserID.value = ''
  Object.assign(userForm, { name: '', uuid: '', expiresAt: null })
  showUserEdit.value = true
}

function openUserEdit(user: User) {
  editingUserID.value = user.id
  Object.assign(userForm, {
    name: user.name,
    uuid: user.uuid,
    expiresAt: user.expires_at ? new Date(user.expires_at).getTime() : null,
  })
  showUserEdit.value = true
}

function userInput(): UserInput {
  return {
    name: userForm.name,
    uuid: userForm.uuid || undefined,
    expires_at:
      userForm.expiresAt === null
        ? null
        : new Date(userForm.expiresAt).toISOString(),
  }
}

async function saveUser() {
  saving.value = true
  try {
    if (editingUserID.value) {
      await updateUser(
        auth.csrfToken,
        listenerID.value,
        editingUserID.value,
        userInput(),
      )
      message.success(t('users.updated'))
    } else {
      await createUser(auth.csrfToken, listenerID.value, userInput())
      message.success(t('users.created'))
    }
    showUserEdit.value = false
    await load()
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}

function confirmUserToggle(user: User) {
  const target = !user.enabled
  const action = target ? t('listeners.enable') : t('listeners.disable')
  dialog.warning({
    title: t('users.toggleTitle', { action }),
    content: t('users.toggleBody'),
    positiveText: action,
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        await setUserEnabled(
          auth.csrfToken,
          listenerID.value,
          user.id,
          target,
        )
        await load()
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}

function confirmUserDelete(user: User) {
  dialog.error({
    title: t('users.deleteTitle'),
    content: t('users.deleteBody'),
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        await deleteUser(auth.csrfToken, listenerID.value, user.id)
        await load()
        message.success(t('users.deleted'))
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}

async function openShare(user: User) {
  try {
    share.value = await getShare(listenerID.value, user.id)
    showShare.value = true
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

function expired(user: User): boolean {
  return Boolean(
    user.expires_at && new Date(user.expires_at).getTime() <= Date.now(),
  )
}
</script>

<template>
  <AppShell>
    <main class="page-container">
      <NSpin :show="loading">
        <template v-if="listener">
          <PageHeader
            :title="listener.name"
            :description="`${listener.listen_address}:${listener.listen_port} · ${listener.server_name}`"
          >
            <template #actions>
              <NSpace>
                <NButton @click="router.push({ name: 'listeners' })">
                  {{ t('common.back') }}
                </NButton>
                <NButton @click="openListenerEdit">
                  {{ t('common.edit') }}
                </NButton>
                <NButton
                  :type="listener.enabled ? 'default' : 'primary'"
                  @click="confirmListenerToggle"
                >
                  {{
                    listener.enabled
                      ? t('listeners.disable')
                      : t('listeners.enable')
                  }}
                </NButton>
              </NSpace>
            </template>
          </PageHeader>

          <section class="two-column-grid">
            <NCard :title="t('listeners.basic')" :bordered="false">
              <dl class="detail-grid">
                <div>
                  <dt>{{ t('listeners.status') }}</dt>
                  <dd>
                    <NTag
                      :type="listener.enabled ? 'success' : 'default'"
                      :bordered="false"
                    >
                      {{
                        listener.enabled
                          ? t('common.enabled')
                          : t('common.disabled')
                      }}
                    </NTag>
                  </dd>
                </div>
                <div>
                  <dt>{{ t('listeners.listenAddress') }}</dt>
                  <dd class="mono">{{ listener.listen_address }}</dd>
                </div>
                <div>
                  <dt>{{ t('listeners.listenPort') }}</dt>
                  <dd>{{ listener.listen_port }}</dd>
                </div>
                <div>
                  <dt>{{ t('listeners.serverName') }}</dt>
                  <dd>{{ listener.server_name }}</dd>
                </div>
                <div>
                  <dt>{{ t('listeners.publicHost') }}</dt>
                  <dd>{{ listener.public_host_override || '—' }}</dd>
                </div>
                <div>
                  <dt>{{ t('listeners.publicPort') }}</dt>
                  <dd>{{ listener.public_port_override || '—' }}</dd>
                </div>
              </dl>
            </NCard>

            <NCard :title="t('listeners.reality')" :bordered="false">
              <dl class="detail-grid">
                <div>
                  <dt>{{ t('listeners.realityDest') }}</dt>
                  <dd>{{ listener.reality_dest }}</dd>
                </div>
                <div>
                  <dt>{{ t('listeners.shortID') }}</dt>
                  <dd class="mono">{{ listener.short_id }}</dd>
                </div>
                <div class="detail-span">
                  <dt>{{ t('listeners.publicKey') }}</dt>
                  <dd class="credential-row">
                    <code>{{ maskCredential(listener.reality_public_key) }}</code>
                    <CopyButton :value="listener.reality_public_key" />
                  </dd>
                </div>
                <div class="detail-span">
                  <dt>{{ t('listeners.privateKey') }}</dt>
                  <dd class="credential-row">
                    <code>••••••••••••••••</code>
                    <NText depth="3">{{ t('listeners.privateStored') }}</NText>
                  </dd>
                </div>
              </dl>
            </NCard>
          </section>

          <NCard :bordered="false" class="surface-card section-gap">
            <div class="card-heading">
              <div>
                <NText tag="h2" class="section-title">
                  {{ t('users.title') }}
                </NText>
                <NText depth="3">{{ listener.users.length }}</NText>
              </div>
              <NButton type="primary" @click="openNewUser">
                {{ t('users.add') }}
              </NButton>
            </div>

            <NEmpty
              v-if="!listener.users.length"
              :description="t('users.empty')"
            />
            <div v-else class="table-scroll">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>{{ t('users.name') }}</th>
                    <th>{{ t('users.uuid') }}</th>
                    <th>{{ t('users.expiresAt') }}</th>
                    <th>{{ t('users.status') }}</th>
                    <th class="actions-cell">{{ t('common.actions') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="user in listener.users" :key="user.id">
                    <td><strong>{{ user.name }}</strong></td>
                    <td>
                      <div class="credential-row">
                        <code>{{ maskCredential(user.uuid) }}</code>
                        <CopyButton :value="user.uuid" size="tiny" />
                      </div>
                    </td>
                    <td>
                      {{
                        user.expires_at
                          ? formatDateTime(user.expires_at, locale)
                          : t('common.never')
                      }}
                    </td>
                    <td>
                      <NTag
                        v-if="expired(user)"
                        type="error"
                        :bordered="false"
                        size="small"
                      >
                        {{ t('users.expired') }}
                      </NTag>
                      <NTag
                        v-else
                        :type="user.enabled ? 'success' : 'default'"
                        :bordered="false"
                        size="small"
                      >
                        {{
                          user.enabled
                            ? t('common.enabled')
                            : t('common.disabled')
                        }}
                      </NTag>
                    </td>
                    <td class="actions-cell">
                      <NSpace justify="end" :wrap="false">
                        <NButton
                          size="small"
                          secondary
                          :disabled="
                            !listener.enabled || !user.enabled || expired(user)
                          "
                          @click="openShare(user)"
                        >
                          {{ t('users.share') }}
                        </NButton>
                        <NButton size="small" @click="openUserEdit(user)">
                          {{ t('common.edit') }}
                        </NButton>
                        <NButton size="small" @click="confirmUserToggle(user)">
                          {{
                            user.enabled
                              ? t('listeners.disable')
                              : t('listeners.enable')
                          }}
                        </NButton>
                        <NButton
                          size="small"
                          type="error"
                          secondary
                          @click="confirmUserDelete(user)"
                        >
                          {{ t('common.delete') }}
                        </NButton>
                      </NSpace>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </NCard>
        </template>
      </NSpin>
    </main>

    <NModal v-model:show="showListenerEdit" :mask-closable="!saving">
      <NCard
        class="modal-card wide-modal"
        :title="t('listeners.edit')"
        :bordered="false"
        role="dialog"
      >
        <NTabs type="line" animated>
          <NTabPane name="basic" :tab="t('listeners.basic')">
            <NForm>
              <div class="form-grid">
                <NFormItem :label="t('listeners.name')" required>
                  <NInput v-model:value="listenerForm.name" maxlength="64" />
                </NFormItem>
                <NFormItem :label="t('listeners.listenAddress')" required>
                  <NInput v-model:value="listenerForm.listen_address" />
                </NFormItem>
                <NFormItem :label="t('listeners.listenPort')" required>
                  <NInputNumber
                    v-model:value="listenerForm.listen_port"
                    :min="1"
                    :max="65535"
                    class="full-width"
                  />
                </NFormItem>
                <NFormItem :label="t('listeners.serverName')" required>
                  <NInput v-model:value="listenerForm.server_name" />
                </NFormItem>
                <NFormItem :label="t('listeners.realityDest')" required>
                  <NInput v-model:value="listenerForm.reality_dest" />
                </NFormItem>
                <NFormItem :label="t('listeners.publicHost')">
                  <NInput v-model:value="listenerForm.public_host_override" />
                </NFormItem>
                <NFormItem :label="t('listeners.publicPort')">
                  <NInputNumber
                    v-model:value="listenerForm.public_port_override"
                    :min="1"
                    :max="65535"
                    clearable
                    class="full-width"
                  />
                </NFormItem>
                <NFormItem :label="t('listeners.udp')">
                  <NSwitch v-model:value="listenerForm.udp_enabled" />
                </NFormItem>
              </div>
            </NForm>
          </NTabPane>
          <NTabPane name="reality" :tab="t('listeners.reality')">
            <NAlert type="warning" :bordered="false">
              {{ t('listeners.generateKeysBody') }}
            </NAlert>
            <NForm class="section-gap">
              <NFormItem :label="t('listeners.publicKey')">
                <NInput v-model:value="listenerForm.reality_public_key" />
              </NFormItem>
              <NFormItem :label="t('listeners.privateKey')">
                <NInput
                  v-model:value="listenerForm.reality_private_key"
                  type="password"
                  show-password-on="click"
                  :placeholder="t('listeners.privateStored')"
                />
              </NFormItem>
              <NFormItem :label="t('listeners.shortID')">
                <NInput v-model:value="listenerForm.short_id" />
              </NFormItem>
              <NAlert
                v-if="generatedPrivateKey"
                type="info"
                :bordered="false"
                class="form-note"
              >
                {{ t('listeners.generatedPrivateHint') }}
              </NAlert>
              <NButton secondary @click="confirmGenerateKeys">
                {{ t('listeners.generateKeys') }}
              </NButton>
            </NForm>
          </NTabPane>
        </NTabs>
        <NSpace justify="end" class="modal-actions">
          <NButton :disabled="saving" @click="showListenerEdit = false">
            {{ t('common.cancel') }}
          </NButton>
          <NButton type="primary" :loading="saving" @click="saveListener">
            {{ t('common.save') }}
          </NButton>
        </NSpace>
      </NCard>
    </NModal>

    <NModal v-model:show="showUserEdit" :mask-closable="!saving">
      <NCard
        class="modal-card"
        :title="editingUserID ? t('users.edit') : t('users.add')"
        :bordered="false"
        role="dialog"
      >
        <NForm @submit.prevent="saveUser">
          <NFormItem :label="t('users.name')" required>
            <NInput v-model:value="userForm.name" maxlength="64" />
          </NFormItem>
          <NFormItem :label="t('users.uuid')">
            <NInput
              v-model:value="userForm.uuid"
              :placeholder="t('users.autoUUID')"
            />
          </NFormItem>
          <NFormItem :label="t('users.expiresAt')">
            <NDatePicker
              v-model:value="userForm.expiresAt"
              type="datetime"
              clearable
              class="full-width"
            />
          </NFormItem>
          <NText depth="3" class="form-note">
            {{ t('users.noExpiry') }}
          </NText>
          <NSpace justify="end" class="modal-actions">
            <NButton :disabled="saving" @click="showUserEdit = false">
              {{ t('common.cancel') }}
            </NButton>
            <NButton
              type="primary"
              attr-type="submit"
              :loading="saving"
              :disabled="!userForm.name"
            >
              {{ t('common.save') }}
            </NButton>
          </NSpace>
        </NForm>
      </NCard>
    </NModal>

    <NModal v-model:show="showShare">
      <NCard
        class="modal-card share-modal"
        :title="t('share.title')"
        :bordered="false"
        role="dialog"
      >
        <NAlert type="warning" :bordered="false">
          {{ t('share.warning') }}
        </NAlert>
        <NTabs v-if="share" type="segment" animated class="section-gap">
          <NTabPane name="uri" :tab="t('share.uri')">
            <div class="share-block">
              <code class="break-all">{{ share.uri }}</code>
              <CopyButton :value="share.uri" />
            </div>
          </NTabPane>
          <NTabPane name="qr" :tab="t('share.qr')">
            <div class="qr-wrap">
              <QrcodeVue
                :value="share.qr_content"
                :size="220"
                level="M"
                render-as="svg"
              />
            </div>
          </NTabPane>
          <NTabPane name="yaml" :tab="t('share.yaml')">
            <pre class="code-panel">{{ share.client_yaml }}</pre>
            <CopyButton :value="share.client_yaml" />
          </NTabPane>
        </NTabs>
        <NSpace justify="end" class="modal-actions">
          <NButton @click="showShare = false">{{ t('common.close') }}</NButton>
        </NSpace>
      </NCard>
    </NModal>
  </AppShell>
</template>
