<script setup lang="ts">
import {
  NButton,
  NCard,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSpace,
  NSwitch,
  NTag,
  NText,
  useDialog,
  useMessage,
} from 'naive-ui'
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import {
  createListener,
  deleteListener,
  setListenerEnabled,
  type Listener,
  type ListenerInput,
} from '@/api/management'
import AppShell from '@/components/AppShell.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useManagementStore } from '@/stores/management'
import { errorTranslationKey } from '@/utils/errors'

const { t } = useI18n()
const router = useRouter()
const dialog = useDialog()
const message = useMessage()
const auth = useAuthStore()
const management = useManagementStore()
const showCreate = ref(false)
const saving = ref(false)

const form = reactive<ListenerInput>(emptyListener())

function emptyListener(): ListenerInput {
  return {
    name: '',
    listen_address: '0.0.0.0',
    listen_port: 443,
    public_host_override: '',
    public_port_override: null,
    server_name: '',
    reality_dest: '',
    udp_enabled: true,
  }
}

onMounted(async () => {
  try {
    await management.refreshListeners()
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
})

function openCreate() {
  Object.assign(form, emptyListener())
  showCreate.value = true
}

async function submitCreate() {
  saving.value = true
  try {
    const created = await createListener(auth.csrfToken, { ...form })
    showCreate.value = false
    await management.refreshListeners()
    message.success(t('listeners.created'))
    await router.push({ name: 'listener-detail', params: { id: created.id } })
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}

function confirmToggle(listener: Listener) {
  const action = listener.enabled
    ? t('listeners.disable')
    : t('listeners.enable')
  dialog.warning({
    title: t('listeners.toggleTitle', { action }),
    content: t('listeners.toggleBody'),
    positiveText: action,
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        await setListenerEnabled(
          auth.csrfToken,
          listener.id,
          !listener.enabled,
        )
        await management.refreshListeners()
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}

function confirmDelete(listener: Listener) {
  dialog.error({
    title: t('listeners.deleteTitle'),
    content: t('listeners.deleteBody'),
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        await deleteListener(auth.csrfToken, listener.id)
        await management.refreshListeners()
        message.success(t('listeners.deleted'))
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}
</script>

<template>
  <AppShell>
    <main class="page-container">
      <PageHeader
        :title="t('listeners.title')"
        :description="t('listeners.description')"
      >
        <template #actions>
          <NButton type="primary" @click="openCreate">
            {{ t('listeners.create') }}
          </NButton>
        </template>
      </PageHeader>

      <NCard :bordered="false" class="surface-card">
        <NEmpty
          v-if="!management.listeners.length"
          :description="t('listeners.empty')"
        >
          <template #extra>
            <NButton type="primary" @click="router.push({ name: 'onboarding' })">
              {{ t('onboarding.create') }}
            </NButton>
          </template>
        </NEmpty>
        <div v-else class="table-scroll">
          <table class="data-table">
            <thead>
              <tr>
                <th>{{ t('listeners.name') }}</th>
                <th>{{ t('listeners.endpoint') }}</th>
                <th>{{ t('listeners.sni') }}</th>
                <th>{{ t('listeners.users') }}</th>
                <th>{{ t('listeners.status') }}</th>
                <th class="actions-cell">{{ t('common.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="listener in management.listeners" :key="listener.id">
                <td>
                  <NButton
                    text
                    type="primary"
                    @click="
                      router.push({
                        name: 'listener-detail',
                        params: { id: listener.id },
                      })
                    "
                  >
                    <strong>{{ listener.name }}</strong>
                  </NButton>
                </td>
                <td class="mono">
                  {{ listener.listen_address }}:{{ listener.listen_port }}
                </td>
                <td>{{ listener.server_name }}</td>
                <td>{{ listener.users.length }}</td>
                <td>
                  <NTag
                    :type="listener.enabled ? 'success' : 'default'"
                    :bordered="false"
                    size="small"
                  >
                    {{
                      listener.enabled
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
                      @click="
                        router.push({
                          name: 'listener-detail',
                          params: { id: listener.id },
                        })
                      "
                    >
                      {{ t('listeners.details') }}
                    </NButton>
                    <NButton size="small" @click="confirmToggle(listener)">
                      {{
                        listener.enabled
                          ? t('listeners.disable')
                          : t('listeners.enable')
                      }}
                    </NButton>
                    <NButton
                      size="small"
                      type="error"
                      secondary
                      @click="confirmDelete(listener)"
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
    </main>

    <NModal v-model:show="showCreate" :mask-closable="!saving">
      <NCard
        class="modal-card"
        :title="t('listeners.create')"
        :bordered="false"
        role="dialog"
      >
        <NForm @submit.prevent="submitCreate">
          <div class="form-grid">
            <NFormItem :label="t('listeners.name')" required>
              <NInput v-model:value="form.name" maxlength="64" />
            </NFormItem>
            <NFormItem :label="t('listeners.listenAddress')" required>
              <NInput v-model:value="form.listen_address" />
            </NFormItem>
            <NFormItem :label="t('listeners.listenPort')" required>
              <NInputNumber
                v-model:value="form.listen_port"
                :min="1"
                :max="65535"
                class="full-width"
              />
            </NFormItem>
            <NFormItem :label="t('listeners.serverName')" required>
              <NInput
                v-model:value="form.server_name"
                placeholder="www.example.com"
              />
            </NFormItem>
            <NFormItem :label="t('listeners.realityDest')" required>
              <NInput
                v-model:value="form.reality_dest"
                placeholder="www.example.com:443"
              />
            </NFormItem>
            <NFormItem :label="t('listeners.publicHost')">
              <NInput
                v-model:value="form.public_host_override"
                :placeholder="t('common.optional')"
              />
            </NFormItem>
            <NFormItem :label="t('listeners.publicPort')">
              <NInputNumber
                v-model:value="form.public_port_override"
                :min="1"
                :max="65535"
                clearable
                class="full-width"
              />
            </NFormItem>
            <NFormItem :label="t('listeners.udp')">
              <NSwitch v-model:value="form.udp_enabled" />
            </NFormItem>
          </div>
          <NText depth="3" class="form-note">
            {{ t('listeners.privateStored') }}
          </NText>
          <NSpace justify="end" class="modal-actions">
            <NButton :disabled="saving" @click="showCreate = false">
              {{ t('common.cancel') }}
            </NButton>
            <NButton
              type="primary"
              attr-type="submit"
              :loading="saving"
              :disabled="
                !form.name ||
                !form.server_name ||
                !form.reality_dest ||
                !form.listen_address
              "
            >
              {{ t('common.create') }}
            </NButton>
          </NSpace>
        </NForm>
      </NCard>
    </NModal>
  </AppShell>
</template>
