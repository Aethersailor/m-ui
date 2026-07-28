<script setup lang="ts">
import { NConfigProvider, NGlobalStyle } from 'naive-ui'
</script>

<template>
  <NConfigProvider>
    <NGlobalStyle />
    <RouterView />
  </NConfigProvider>
</template>
