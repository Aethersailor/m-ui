import { createApp } from 'vue'

import { consumeSetupTokenFragment } from './setup-token'
import './styles.css'

consumeSetupTokenFragment()

const [{ default: App }, { i18n }, { router }, { pinia }] = await Promise.all([
  import('./App.vue'),
  import('./i18n'),
  import('./router'),
  import('./stores'),
])

createApp(App).use(pinia).use(router).use(i18n).mount('#app')
