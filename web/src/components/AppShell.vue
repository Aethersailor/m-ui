<script setup lang="ts">
import {
  NButton,
  NDrawer,
  NDrawerContent,
  NLayout,
  NLayoutContent,
  NLayoutSider,
  NMenu,
  NTag,
  NText,
  type MenuOption,
} from 'naive-ui'
import { computed, h, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { useAuthStore } from '@/stores/auth'
import { useManagementStore } from '@/stores/management'
import { useThemeStore } from '@/stores/theme'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const management = useManagementStore()
const theme = useThemeStore()
const { locale, t } = useI18n()
const drawerOpen = ref(false)
const collapsed = ref(false)

const activeKey = computed(() => String(route.meta.section ?? 'dashboard'))
const panelTitle = computed(
  () => management.settings?.panel_title || t('product.name'),
)

const menuOptions = computed<MenuOption[]>(() => [
  menuLink('dashboard', 'dashboard', t('nav.dashboard')),
  menuLink('listeners', 'listeners', t('nav.listeners')),
  menuLink('config', 'config', t('nav.config')),
  menuLink('system', 'system', t('nav.system')),
])

function menuLink(key: string, routeName: string, label: string): MenuOption {
  return {
    key,
    label: () =>
      h(
        RouterLink,
        {
          to: { name: routeName },
          onClick: () => {
            drawerOpen.value = false
          },
        },
        { default: () => label },
      ),
  }
}

onMounted(async () => {
  theme.initialize()
  try {
    await management.loadShellSettings()
    if (management.settings) {
      locale.value = management.settings.ui_language
    }
  } catch {
    // Individual pages expose actionable request failures.
  }
})

async function signOut() {
  await auth.logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <NLayout has-sider class="app-layout">
    <NLayoutSider
      bordered
      collapse-mode="width"
      :collapsed-width="72"
      :width="248"
      :collapsed="collapsed"
      show-trigger="bar"
      class="desktop-sidebar"
      @collapse="collapsed = true"
      @expand="collapsed = false"
    >
      <div class="brand" :class="{ compact: collapsed }">
        <span class="brand-mark">m</span>
        <div v-if="!collapsed" class="brand-copy">
          <NText strong>{{ panelTitle }}</NText>
          <NText depth="3" class="brand-caption">Mihomo control plane</NText>
        </div>
      </div>
      <NMenu
        :value="activeKey"
        :options="menuOptions"
        :collapsed="collapsed"
        :collapsed-width="72"
        :collapsed-icon-size="20"
        class="main-menu"
      />
      <div v-if="!collapsed" class="sidebar-foot">
        <NTag size="small" :bordered="false" round>v0.1</NTag>
        <NText depth="3">{{ auth.admin?.username }}</NText>
      </div>
    </NLayoutSider>

    <NDrawer v-model:show="drawerOpen" placement="left" :width="280">
      <NDrawerContent :native-scrollbar="false" closable>
        <div class="brand drawer-brand">
          <span class="brand-mark">m</span>
          <div class="brand-copy">
            <NText strong>{{ panelTitle }}</NText>
            <NText depth="3" class="brand-caption">Mihomo control plane</NText>
          </div>
        </div>
        <NMenu :value="activeKey" :options="menuOptions" />
      </NDrawerContent>
    </NDrawer>

    <NLayout>
      <header class="topbar">
        <NButton
          quaternary
          class="mobile-only"
          :aria-label="t('nav.openMenu')"
          @click="drawerOpen = true"
        >
          ☰
        </NButton>
        <div class="topbar-spacer" />
        <NButton
          quaternary
          circle
          :aria-label="t('theme.toggle')"
          @click="theme.cycle"
        >
          {{ theme.dark ? '☀' : '◐' }}
        </NButton>
        <NButton text @click="signOut">{{ t('auth.signOut') }}</NButton>
      </header>
      <NLayoutContent class="workspace">
        <slot />
      </NLayoutContent>
    </NLayout>
  </NLayout>
</template>
