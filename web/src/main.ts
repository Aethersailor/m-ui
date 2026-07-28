import { createApp } from 'vue'

import App from './App.vue'
import { i18n } from './i18n'
import { router } from './router'
import { pinia } from './stores'
import './styles.css'

createApp(App).use(pinia).use(router).use(i18n).mount('#app')
