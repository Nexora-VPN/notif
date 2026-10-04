import { createRouter, createWebHistory } from 'vue-router'
import { api, whenSignedOut } from './api'
import Login from './views/Login.vue'
import Layout from './views/Layout.vue'
import Dashboard from './views/Dashboard.vue'
import Security from './views/Security.vue'
import Channels from './views/Channels.vue'
import Log from './views/Log.vue'
import Settings from './views/Settings.vue'
import Users from './views/Users.vue'
import Notices from './views/Notices.vue'
import Messages from './views/Messages.vue'
import Setup from './views/Setup.vue'

export const router = createRouter({
  // Relative to wherever Notif is served, the way its assets are.
  history: createWebHistory(new URL('.', document.baseURI).pathname),
  routes: [
    { path: '/login', name: 'login', component: Login, meta: { public: true } },
    {
      path: '/',
      component: Layout,
      children: [
        { path: '', name: 'dashboard', component: Dashboard },
        { path: 'channels', name: 'channels', component: Channels },
        { path: 'notices', name: 'notices', component: Notices },
        { path: 'messages', name: 'messages', component: Messages },
        { path: 'users', name: 'users', component: Users },
        { path: 'log', name: 'log', component: Log },
        { path: 'settings', name: 'settings', component: Settings },
        { path: 'security', name: 'security', component: Security },
        { path: 'setup', name: 'setup', component: Setup },
      ],
    },
  ],
})

let checked = false

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  if (checked) return true
  try {
    await api.me()
    checked = true
    return true
  } catch {
    return { name: 'login', query: to.fullPath === '/' ? {} : { next: to.fullPath } }
  }
})

whenSignedOut(() => {
  checked = false
  if (router.currentRoute.value.name !== 'login') void router.push({ name: 'login' })
})

export function signedIn() {
  checked = true
}
