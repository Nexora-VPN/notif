import { createApp } from 'vue'
import PrimeVue from 'primevue/config'
import ToastService from 'primevue/toastservice'
import ConfirmationService from 'primevue/confirmationservice'
import Tooltip from 'primevue/tooltip'
import Aura from '@primeuix/themes/aura'
import { definePreset } from '@primeuix/themes'
import 'primeicons/primeicons.css'
import './styles.css'
import App from './App.vue'
import { router } from './router'
import { i18n } from './i18n'

// Notif's colour: a signal amber, so its admin is told apart from the
// panel's and Shop's at a glance.
const Notif = definePreset(Aura, {
  semantic: {
    primary: {
      50: '{amber.50}',
      100: '{amber.100}',
      200: '{amber.200}',
      300: '{amber.300}',
      400: '{amber.400}',
      500: '{amber.500}',
      600: '{amber.600}',
      700: '{amber.700}',
      800: '{amber.800}',
      900: '{amber.900}',
      950: '{amber.950}',
    },
  },
})

// The admin follows the system's light or dark setting.
const dark = window.matchMedia('(prefers-color-scheme: dark)')
const applyDark = () => document.documentElement.classList.toggle('dark', dark.matches)
applyDark()
dark.addEventListener('change', applyDark)

createApp(App)
  .use(PrimeVue, {
    theme: { preset: Notif, options: { darkModeSelector: '.dark' } },
    license: import.meta.env.VITE_PRIMEUI_LICENSE,
  })
  .use(ToastService)
  .use(ConfirmationService)
  .directive('tooltip', Tooltip)
  .use(i18n)
  .use(router)
  .mount('#app')
